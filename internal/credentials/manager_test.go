package credentials

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerResolvesAPIKeyProfileWithoutProviderIdentity(t *testing.T) {
	state := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("HJH_TEST_API_KEY", "environment-secret")
	file := filepath.Join(home, "credentials", "api-key")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(" file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Options{StateDir: state})

	fromEnv, err := manager.Resolve(context.Background(), KindAPIKey, "shared-profile", Profile{Env: "HJH_TEST_API_KEY", File: "~/credentials/api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if fromEnv.Token != "environment-secret" {
		t.Fatalf("environment token = %q", fromEnv.Token)
	}
	fromFile, err := manager.Resolve(context.Background(), KindAPIKey, "shared-profile", Profile{File: "~/credentials/api-key"})
	if err != nil {
		t.Fatal(err)
	}
	if fromFile.Token != "file-secret" {
		t.Fatalf("file token = %q", fromFile.Token)
	}
	if fromEnv.Identity != fromFile.Identity {
		t.Fatalf("same profile had different identities: %q and %q", fromEnv.Identity, fromFile.Identity)
	}
}

func TestManagerResolvesUnauthenticatedProfileWithoutCredentialSource(t *testing.T) {
	resolved, err := NewManager(Options{}).Resolve(context.Background(), KindNone, "local-compatible", Profile{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "" {
		t.Fatalf("unauthenticated connection returned a token")
	}
	if resolved.Identity == "" {
		t.Fatal("unauthenticated connection did not receive a stable non-secret identity")
	}
}

func TestManagerCompletesManualChatGPTCallbackAndSerializesRefresh(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var nonce string
	var expectedRedirectURI string
	var expectedChallenge string
	var refreshCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(t, w, openIDConfiguration{
				Issuer:                "https://auth.openai.com",
				AuthorizationEndpoint: "http://openai.test/authorize",
				TokenEndpoint:         serverURL(r) + "/token",
				JWKSURI:               serverURL(r) + "/jwks",
			})
		case "/jwks":
			e := big.NewInt(int64(privateKey.PublicKey.E)).Bytes()
			writeJSON(t, w, jwksDocument{Keys: []jwk{{
				Kid: "test-signing-key",
				Kty: "RSA",
				Use: "sig",
				Alg: "RS256",
				N:   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
				E:   base64.RawURLEncoding.EncodeToString(e),
			}}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				mu.Lock()
				requestNonce := nonce
				redirectURI := expectedRedirectURI
				challenge := expectedChallenge
				mu.Unlock()
				verifierDigest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				gotChallenge := base64.RawURLEncoding.EncodeToString(verifierDigest[:])
				if r.Form.Get("client_id") != "oaiapp_test-issued" || r.Form.Get("code") != "test-code" || r.Form.Get("resource") != chatGPTResource || r.Form.Get("redirect_uri") != redirectURI || r.Form.Get("code_verifier") == "" || gotChallenge != challenge {
					t.Errorf("unexpected authorization code form")
					http.Error(w, "bad form", http.StatusBadRequest)
					return
				}
				idToken := signTestIDToken(t, privateKey, requestNonce, "oaiapp_test-issued")
				writeJSON(t, w, tokenResponse{
					AccessToken:  "first-access",
					RefreshToken: "first-refresh",
					IDToken:      idToken,
					TokenType:    "Bearer",
					ExpiresIn:    3600,
					Scope:        "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct",
				})
			case "refresh_token":
				if r.Form.Get("client_id") != "oaiapp_test-issued" || r.Form.Get("refresh_token") != "first-refresh" || r.Form.Get("resource") != chatGPTResource || r.Form.Has("scope") {
					t.Errorf("unexpected refresh form")
					http.Error(w, "bad form", http.StatusBadRequest)
					return
				}
				mu.Lock()
				refreshCalls++
				mu.Unlock()
				time.Sleep(60 * time.Millisecond)
				writeJSON(t, w, tokenResponse{AccessToken: "refreshed-access", RefreshToken: "rotated-refresh", TokenType: "Bearer", ExpiresIn: 3600})
			default:
				t.Errorf("unexpected grant_type %q", r.Form.Get("grant_type"))
				http.Error(w, "bad grant", http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	transport := discoveryRewriteTransport{base: http.DefaultTransport, local: server.URL}
	client := &http.Client{Transport: transport}
	stateDir := t.TempDir()
	manager := NewManager(Options{StateDir: stateDir, HTTPClient: client})
	profile := Profile{}
	login, err := manager.BeginLogin(context.Background(), "chatgpt-test", profile)
	if err != nil {
		t.Fatal(err)
	}
	parsedLogin, err := url.Parse(login.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	params := parsedLogin.Query()
	if params.Get("client_id") != chatGPTAuthorizeClientID || params.Get("redirect_uri") != login.RedirectURI || params.Get("agent_name_hint") != "hjh" {
		t.Fatalf("unexpected first-login authorization parameters")
	}
	if params.Get("id_token_hint") != "" || params.Get("ext_agent_host_id") == "" || params.Get("code_challenge_method") != "S256" || params.Get("resource") != chatGPTResource || !hasScope(strings.Fields(params.Get("scope")), chatGPTPlanScope) || !hasScope(strings.Fields(params.Get("scope")), "offline_access") {
		t.Fatalf("authorization request did not preserve the public-client contract")
	}
	mu.Lock()
	nonce = params.Get("nonce")
	expectedRedirectURI = params.Get("redirect_uri")
	expectedChallenge = params.Get("code_challenge")
	mu.Unlock()
	callback := login.RedirectURI + "?" + url.Values{
		"code":      {"test-code"},
		"state":     {params.Get("state")},
		"client_id": {"oaiapp_test-issued"},
	}.Encode()
	status, err := manager.CompleteLogin(context.Background(), "chatgpt-test", profile, callback)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Connected || !status.PlanUsageEnabled || status.Account != "user@example.com" {
		t.Fatalf("unexpected status after login: %#v", status)
	}
	resolved, err := manager.Resolve(context.Background(), KindChatGPTOAuth, "chatgpt-test", profile)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "first-access" || resolved.Identity != status.Identity {
		t.Fatalf("unexpected resolved credentials: identity=%q token=%q", resolved.Identity, resolved.Token)
	}

	returningLogin, err := manager.BeginLogin(context.Background(), "chatgpt-test", profile)
	if err != nil {
		t.Fatal(err)
	}
	returningURL, err := url.Parse(returningLogin.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	returningParams := returningURL.Query()
	if returningParams.Get("client_id") != "oaiapp_test-issued" || returningParams.Get("agent_name_hint") != "" || returningParams.Get("id_token_hint") != "" {
		t.Fatalf("returning authorization did not reuse the issued client without hints")
	}
	if returningParams.Get("ext_agent_host_id") != params.Get("ext_agent_host_id") {
		t.Fatalf("host identity changed between sign-ins")
	}
	mu.Lock()
	nonce = returningParams.Get("nonce")
	expectedRedirectURI = returningParams.Get("redirect_uri")
	expectedChallenge = returningParams.Get("code_challenge")
	mu.Unlock()
	returningCallback := returningLogin.RedirectURI + "?" + url.Values{
		"code":      {"test-code"},
		"state":     {returningParams.Get("state")},
		"client_id": {"oaiapp_test-issued"},
	}.Encode()
	callbackResponse, err := http.Get(returningCallback)
	if err != nil {
		t.Fatalf("send automatic loopback callback: %v", err)
	}
	callbackResponse.Body.Close()
	returningStatus, err := manager.CompleteLogin(context.Background(), "chatgpt-test", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	if returningStatus.Identity != status.Identity {
		t.Fatalf("returning account identity changed: %q != %q", returningStatus.Identity, status.Identity)
	}

	path, err := manager.oauthPath("chatgpt-test", profile)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err := readOAuthRecord(path)
	if err != nil || !found {
		t.Fatalf("credential record was not saved: found=%v err=%v", found, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential file mode = %04o, want 0600", info.Mode().Perm())
	}

	var record oauthRecord
	err = withFileLock(context.Background(), path, func() error {
		var readErr error
		record, _, readErr = readOAuthRecord(path)
		if readErr != nil {
			return readErr
		}
		record.ExpiresAt = time.Now().Add(-time.Minute)
		return writeOAuthRecord(path, record)
	})
	if err != nil {
		t.Fatal(err)
	}

	secondManager := NewManager(Options{StateDir: stateDir, HTTPClient: client})
	results := make(chan Resolved, 2)
	errors := make(chan error, 2)
	var callers sync.WaitGroup
	for _, current := range []*Manager{manager, secondManager} {
		callers.Add(1)
		go func(current *Manager) {
			defer callers.Done()
			value, resolveErr := current.Resolve(context.Background(), KindChatGPTOAuth, "chatgpt-test", profile)
			results <- value
			errors <- resolveErr
		}(current)
	}
	callers.Wait()
	close(results)
	close(errors)
	for resolveErr := range errors {
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
	}
	for value := range results {
		if value.Token != "refreshed-access" || value.Identity != status.Identity {
			t.Fatalf("unexpected token after refresh: %#v", value)
		}
	}
	mu.Lock()
	gotRefreshCalls := refreshCalls
	mu.Unlock()
	if gotRefreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want one serialized refresh", gotRefreshCalls)
	}
	refreshed, found, err := readOAuthRecord(path)
	if err != nil || !found || refreshed.Refresh != "rotated-refresh" {
		t.Fatalf("rotated refresh token was not saved: found=%v err=%v", found, err)
	}
}

type discoveryRewriteTransport struct {
	base  http.RoundTripper
	local string
}

func (transport discoveryRewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host == "auth.openai.com" && request.URL.Path == "/.well-known/openid-configuration" {
		copy := request.Clone(request.Context())
		local, _ := url.Parse(transport.local)
		copy.URL.Scheme = local.Scheme
		copy.URL.Host = local.Host
		copy.Host = local.Host
		return transport.base.RoundTrip(copy)
	}
	return transport.base.RoundTrip(request)
}

func signTestIDToken(t *testing.T, key *rsa.PrivateKey, nonce, clientID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test-signing-key"}`))
	claims, err := json.Marshal(map[string]any{
		"iss":   "https://auth.openai.com",
		"sub":   "subject-123",
		"aud":   clientID,
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Add(15 * time.Second).Unix(),
		"nonce": nonce,
		"email": "user@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	unsigned := header + "." + payload
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode test response: %v", err)
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}

func TestValidateCallbackRequiresPendingRedirectAndState(t *testing.T) {
	pending := &pendingLogin{redirectURI: "http://127.0.0.1:12345/auth/callback", state: "expected", clientID: "issued", existing: true}
	bad := pending.redirectURI + "?" + url.Values{"state": {"other"}, "code": {"secret-code"}}.Encode()
	_, _, err := validateCallback(pending, bad)
	if err == nil || strings.Contains(err.Error(), "secret-code") {
		t.Fatalf("callback mismatch error = %v", err)
	}
}
