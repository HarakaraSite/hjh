package credentials

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/api"
)

func TestManagerCancellationStopsListenerAndAllowsFreshAttempt(t *testing.T) {
	manager, discovery := newLoginLifecycleManager(t)
	defer discovery.Close()
	defer manager.Close()
	target := Target{ConfigPath: filepath.Join(t.TempDir(), "providers.yaml"), ProviderID: "chatgpt", Name: "cancel-test", Kind: KindChatGPTOAuth}
	first := startTestAttempt(t, manager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-cancel-first", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	firstParams := mustLoginParams(t, first.AuthorizationURL)

	cancelled, err := manager.CancelAttempt(first.AttemptID)
	if err != nil || cancelled.Phase != "cancelled" {
		t.Fatalf("cancel attempt = %#v, err=%v", cancelled, err)
	}
	assertListenerClosed(t, first.RedirectURI)

	second := startTestAttempt(t, manager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-cancel-second", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	secondParams := mustLoginParams(t, second.AuthorizationURL)
	if secondParams.Get("state") == firstParams.Get("state") || secondParams.Get("nonce") == firstParams.Get("nonce") || secondParams.Get("code_challenge") == firstParams.Get("code_challenge") {
		t.Fatal("fresh login reused state, nonce, or PKCE challenge")
	}
	response, err := http.Get(strings.TrimRight(second.RedirectURI, "/") + "/not-callback")
	if err != nil {
		t.Fatalf("fresh loopback listener did not accept a connection: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("fresh listener response status = %d, want 404", response.StatusCode)
	}
	if _, err := manager.CancelAttempt(second.AttemptID); err != nil {
		t.Fatalf("cancel fresh attempt: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
	assertListenerClosed(t, second.RedirectURI)
}

func TestManagerCloseStopsListenerAndJoinsServe(t *testing.T) {
	manager, discovery := newLoginLifecycleManager(t)
	defer discovery.Close()
	target := Target{ConfigPath: filepath.Join(t.TempDir(), "providers.yaml"), ProviderID: "chatgpt", Name: "close-test", Kind: KindChatGPTOAuth}
	login := startTestAttempt(t, manager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-close", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
	closed, err := manager.Attempt(login.AttemptID)
	if err != nil || closed.Phase != "cancelled" {
		t.Fatalf("attempt after Close = %#v, err=%v", closed, err)
	}
	assertListenerClosed(t, login.RedirectURI)
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := manager.BeginAttempt(context.Background(), resolveCredentialTarget(target), api.AuthRequest{CoreID: "core", AttemptID: "attempt-after-close", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"}); err == nil {
		t.Fatal("closed manager began another login")
	}
}

func TestManagerRetriesExpiredInitialCodeWithPersistedIssuedClient(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var attempt int
	var nonce, redirectURI, challenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(t, w, openIDConfiguration{
				Issuer:                "https://auth.openai.com",
				AuthorizationEndpoint: "https://auth.openai.com/api/accounts/authorize",
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
			mu.Lock()
			attempt++
			currentAttempt := attempt
			currentNonce, currentRedirect, currentChallenge := nonce, redirectURI, challenge
			mu.Unlock()
			verifierDigest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			gotChallenge := base64.RawURLEncoding.EncodeToString(verifierDigest[:])
			if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("client_id") != "oaiapp_retry-issued" || r.Form.Get("resource") != chatGPTResource || r.Form.Get("redirect_uri") != currentRedirect || gotChallenge != currentChallenge {
				t.Errorf("unexpected authorization code form on attempt %d", currentAttempt)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if currentAttempt == 1 {
				if r.Form.Get("code") != "expired-code" {
					t.Errorf("first authorization code = %q", r.Form.Get("code"))
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			if currentAttempt != 2 || r.Form.Get("code") != "fresh-code" {
				t.Errorf("unexpected authorization code on attempt %d", currentAttempt)
				http.Error(w, "bad code", http.StatusBadRequest)
				return
			}
			idToken := signTestIDToken(t, privateKey, currentNonce, "oaiapp_retry-issued")
			writeJSON(t, w, tokenResponse{
				AccessToken:  "retry-access",
				RefreshToken: "retry-refresh",
				IDToken:      idToken,
				TokenType:    "Bearer",
				ExpiresIn:    3600,
				Scope:        "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &http.Client{Transport: discoveryRewriteTransport{base: http.DefaultTransport, local: server.URL}}
	manager := NewManager(Options{StateDir: t.TempDir(), HTTPClient: client})
	defer manager.Close()
	profile := Profile{}
	target := Target{ConfigPath: filepath.Join(t.TempDir(), "providers.yaml"), ProviderID: "chatgpt", Name: "retry-test", Kind: KindChatGPTOAuth, Profile: profile}
	first := startTestAttempt(t, manager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-retry-first", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	firstParams := mustLoginParams(t, first.AuthorizationURL)
	mu.Lock()
	nonce = firstParams.Get("nonce")
	redirectURI = firstParams.Get("redirect_uri")
	challenge = firstParams.Get("code_challenge")
	mu.Unlock()
	firstCallback := first.RedirectURI + "?" + url.Values{
		"code":      {"expired-code"},
		"state":     {firstParams.Get("state")},
		"client_id": {"oaiapp_retry-issued"},
	}.Encode()
	if _, err = manager.CompleteAttempt(context.Background(), first.AttemptID, firstCallback); err != nil {
		t.Fatal(err)
	}
	failed := waitAttempt(t, manager, first.AttemptID, "failed")
	if failed.Error == nil || failed.Error.Code != "authorization_code_invalid_grant" {
		t.Fatalf("expired authorization error = %#v", failed.Error)
	}

	oauthPath, err := manager.oauthPath("retry-test", profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := readOAuthRecord(oauthPath); err != nil || found {
		t.Fatalf("failed exchange created usable token record: found=%v err=%v", found, err)
	}
	registration, found, err := readOAuthRegistration(oauthRegistrationPath(oauthPath))
	if err != nil || !found || registration.ClientID != "oaiapp_retry-issued" || registration.Issuer != "https://auth.openai.com" || registration.HostID == "" {
		t.Fatalf("issued registration was not retained: %#v found=%v err=%v", registration, found, err)
	}
	accounts, err := manager.Accounts(context.Background(), resolveCredentialTarget(target))
	if err != nil || len(accounts.Accounts) != 1 || accounts.Accounts[0].Connected {
		t.Fatalf("unverified registration was treated as connected: %#v err=%v", accounts, err)
	}

	second := startTestAttempt(t, manager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-retry-second", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	secondParams := mustLoginParams(t, second.AuthorizationURL)
	if secondParams.Get("client_id") != "oaiapp_retry-issued" || secondParams.Get("agent_name_hint") != "" || secondParams.Get("state") == firstParams.Get("state") || secondParams.Get("nonce") == firstParams.Get("nonce") || secondParams.Get("code_challenge") == firstParams.Get("code_challenge") {
		t.Fatalf("fresh authorization did not reuse issued client with new proof values: %#v", secondParams)
	}
	mu.Lock()
	nonce = secondParams.Get("nonce")
	redirectURI = secondParams.Get("redirect_uri")
	challenge = secondParams.Get("code_challenge")
	mu.Unlock()
	secondCallback := second.RedirectURI + "?" + url.Values{
		"code":  {"fresh-code"},
		"state": {secondParams.Get("state")},
	}.Encode()
	if _, err = manager.CompleteAttempt(context.Background(), second.AttemptID, secondCallback); err != nil {
		t.Fatal(err)
	}
	completed := waitAttempt(t, manager, second.AttemptID, "completed")
	if !completed.AccountSaved || !completed.SelectionSaved {
		t.Fatalf("fresh authorization did not persist account and selection: %#v", completed)
	}
	accounts, err = manager.Accounts(context.Background(), resolveCredentialTarget(target))
	if err != nil || len(accounts.Accounts) != 1 || !accounts.Accounts[0].Connected || !accounts.Accounts[0].PlanUsageEnabled {
		t.Fatalf("fresh authorization did not connect the selected account: %#v err=%v", accounts, err)
	}
	mu.Lock()
	gotAttempts := attempt
	mu.Unlock()
	if gotAttempts != 2 {
		t.Fatalf("authorization code exchanges = %d, want 2", gotAttempts)
	}
}

func newLoginLifecycleManager(t *testing.T) (*Manager, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, openIDConfiguration{
			Issuer:                "https://auth.openai.com",
			AuthorizationEndpoint: "https://auth.openai.com/api/accounts/authorize",
			TokenEndpoint:         serverURL(r) + "/token",
			JWKSURI:               serverURL(r) + "/jwks",
		})
	}))
	client := &http.Client{Transport: discoveryRewriteTransport{base: http.DefaultTransport, local: server.URL}}
	return NewManager(Options{StateDir: t.TempDir(), HTTPClient: client}), server
}

func mustLoginParams(t *testing.T, authorizationURL string) url.Values {
	t.Helper()
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query()
}

func assertListenerClosed(t *testing.T, redirectURI string) {
	t.Helper()
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", parsed.Host, 30*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("loopback listener at %s remained open", parsed.Host)
}
