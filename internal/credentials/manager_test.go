package credentials

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
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

	"hjh/internal/api"
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

func TestManagerRegistersAndReusesManagedAPIKey(t *testing.T) {
	stateDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "providers.yaml")
	target := Target{ConfigPath: configPath, ProviderID: "openrouter", Name: "shared", Kind: KindAPIKey, Profile: Profile{Env: "HJH_TEST_MANAGED_KEY"}}
	t.Setenv("HJH_TEST_MANAGED_KEY", "environment-key")
	manager := NewManager(Options{StateDir: stateDir})
	resolve := resolveCredentialTarget(target)

	states, err := manager.Inspect(context.Background(), func(context.Context) ([]Target, error) { return []Target{target}, nil })
	if err != nil || len(states) != 1 || states[0].APIKey == nil || states[0].APIKey.Source != "env" || states[0].APIKey.Registered {
		t.Fatalf("initial API key state = %#v, err=%v", states, err)
	}

	registered, err := manager.RegisterAPIKey(context.Background(), resolve, "mutation-one", "managed-key")
	if err != nil {
		t.Fatal(err)
	}
	if registered.Source != "registered" || !registered.Registered || registered.Revision != 1 || registered.MutationID != "mutation-one" {
		t.Fatalf("registered API key state = %#v", registered)
	}
	replayed, err := manager.RegisterAPIKey(context.Background(), resolve, "mutation-one", "managed-key")
	if err != nil || replayed.Revision != 1 {
		t.Fatalf("same mutation replay = %#v, err=%v", replayed, err)
	}
	updated, err := manager.RegisterAPIKey(context.Background(), resolve, "mutation-two", "new-managed-key")
	if err != nil || updated.Revision != 2 || updated.MutationID != "mutation-two" {
		t.Fatalf("second API key registration = %#v, err=%v", updated, err)
	}

	binding, err := manager.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := binding.Resolve(context.Background())
	if err != nil || resolved.Token != "new-managed-key" || resolved.Identity == "" {
		t.Fatalf("managed API key binding = %#v, err=%v", resolved, err)
	}
	other := target
	other.ConfigPath = filepath.Join(t.TempDir(), "other.yaml")
	otherBinding, err := manager.Capture(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	otherResolved, err := otherBinding.Resolve(context.Background())
	if err != nil || otherResolved.Token != "environment-key" {
		t.Fatalf("same profile in another config resolved %q, err=%v", otherResolved.Token, err)
	}

	missingFileTarget := target
	missingFileTarget.Name = "file-profile"
	missingFileTarget.Profile = Profile{File: filepath.Join(t.TempDir(), "not-installed")}
	states, err = manager.Inspect(context.Background(), func(context.Context) ([]Target, error) { return []Target{missingFileTarget}, nil })
	if err != nil || len(states) != 1 || states[0].APIKey == nil || states[0].APIKey.Source != "missing" {
		t.Fatalf("missing external file state = %#v, err=%v", states, err)
	}
}

func TestRunTicketCanceledReadKeepsSuccessorBehindPrevious(t *testing.T) {
	manager := NewManager(Options{})
	defer manager.Close()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	defer release()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- manager.runTicket(context.Background(), true, func(context.Context) error {
			close(firstStarted)
			<-releaseFirst
			return nil
		})
	}()
	<-firstStarted
	manager.mu.Lock()
	firstBarrier := manager.ticketTail
	manager.mu.Unlock()

	readCtx, cancelRead := context.WithCancel(context.Background())
	readDone := make(chan error, 1)
	go func() {
		readDone <- manager.runTicket(readCtx, false, func(context.Context) error {
			return errors.New("canceled read action ran")
		})
	}()
	secondBarrier := waitForTicketTailChange(t, manager, firstBarrier)
	cancelRead()
	select {
	case err := <-readDone:
		if err != context.Canceled {
			t.Fatalf("canceled read returned %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled read did not return promptly")
	}

	successorStarted := make(chan struct{})
	successorDone := make(chan error, 1)
	go func() {
		successorDone <- manager.runTicket(context.Background(), true, func(context.Context) error {
			close(successorStarted)
			return nil
		})
	}()
	waitForTicketTailChange(t, manager, secondBarrier)
	select {
	case <-secondBarrier:
		t.Fatal("canceled read released the successor before its predecessor finished")
	default:
	}
	select {
	case <-successorStarted:
		t.Fatal("successor ran before the accepted predecessor finished")
	default:
	}

	release()
	for name, done := range map[string]<-chan error{"first ticket": firstDone, "successor": successorDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s returned %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s did not finish after predecessor release", name)
		}
	}
	select {
	case <-successorStarted:
	default:
		t.Fatal("successor did not run after predecessor release")
	}
}

func waitForTicketTailChange(t *testing.T, manager *Manager, previous chan struct{}) chan struct{} {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		manager.mu.Lock()
		current := manager.ticketTail
		manager.mu.Unlock()
		if current != previous {
			return current
		}
		select {
		case <-deadline.C:
			t.Fatal("ticket was not accepted")
		case <-ticker.C:
		}
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
	defer manager.Close()
	profile := Profile{OAuthFile: filepath.Join(t.TempDir(), "shared-chatgpt.json")}
	target := Target{ConfigPath: filepath.Join(t.TempDir(), "providers.yaml"), ProviderID: "chatgpt", Name: "chatgpt-test", Kind: KindChatGPTOAuth, Profile: profile}
	first := startTestAttempt(t, manager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-first", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	params := mustLoginParams(t, first.AuthorizationURL)
	if params.Get("client_id") != chatGPTAuthorizeClientID || params.Get("redirect_uri") != first.RedirectURI || params.Get("agent_name_hint") != "hjh" {
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
	callback := first.RedirectURI + "?" + url.Values{
		"code":      {"test-code"},
		"state":     {params.Get("state")},
		"client_id": {"oaiapp_test-issued"},
	}.Encode()
	if _, err := manager.CompleteAttempt(context.Background(), first.AttemptID, callback); err != nil {
		t.Fatal(err)
	}
	completed := waitAttempt(t, manager, first.AttemptID, "completed")
	if !completed.AccountSaved || !completed.SelectionSaved {
		t.Fatalf("account save flags = %#v", completed)
	}
	accounts, err := manager.Accounts(context.Background(), resolveCredentialTarget(target))
	if err != nil || len(accounts.Accounts) != 1 || !accounts.Accounts[0].Connected || !accounts.Accounts[0].PlanUsageEnabled || accounts.Accounts[0].Label != "user@example.com" {
		t.Fatalf("unexpected account list after login: %#v err=%v", accounts, err)
	}
	resolved, err := manager.Resolve(context.Background(), KindChatGPTOAuth, "chatgpt-test", profile)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Token != "first-access" || resolved.Identity != accountIdentity("https://auth.openai.com", "oaiapp_test-issued", "subject-123") {
		t.Fatalf("unexpected resolved credentials: identity=%q token=%q", resolved.Identity, resolved.Token)
	}

	path, err := manager.oauthPath("chatgpt-test", profile)
	if err != nil {
		t.Fatal(err)
	}
	initialRecord, found, err := readOAuthRecord(path)
	if err != nil || !found {
		t.Fatalf("read initial OAuth record: found=%v err=%v", found, err)
	}
	reauthManager := NewManager(Options{StateDir: t.TempDir(), HTTPClient: client})
	defer reauthManager.Close()
	differentHostID, err := reauthManager.ensureHostID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if differentHostID == initialRecord.HostID {
		t.Fatal("test managers unexpectedly share a host identity")
	}
	reauth := startTestAttempt(t, reauthManager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-reauth-other-state-dir", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	reauthParams := mustLoginParams(t, reauth.AuthorizationURL)
	if reauthParams.Get("client_id") != initialRecord.ClientID || reauthParams.Get("ext_agent_host_id") != initialRecord.HostID {
		t.Fatalf("reauth did not preserve the connected record's client and host IDs: client=%q host=%q", reauthParams.Get("client_id"), reauthParams.Get("ext_agent_host_id"))
	}
	mu.Lock()
	nonce = reauthParams.Get("nonce")
	expectedRedirectURI = reauthParams.Get("redirect_uri")
	expectedChallenge = reauthParams.Get("code_challenge")
	mu.Unlock()
	reauthCallback := reauth.RedirectURI + "?" + url.Values{
		"code":      {"test-code"},
		"state":     {reauthParams.Get("state")},
		"client_id": {initialRecord.ClientID},
	}.Encode()
	if _, err := reauthManager.CompleteAttempt(context.Background(), reauth.AttemptID, reauthCallback); err != nil {
		t.Fatal(err)
	}
	reauthResult := waitAttempt(t, reauthManager, reauth.AttemptID, "completed")
	if !reauthResult.AccountSaved || !reauthResult.SelectionSaved {
		t.Fatalf("reauth did not save the account and selection: %#v", reauthResult)
	}
	reauthenticatedRecord, found, err := readOAuthRecord(path)
	if err != nil || !found || reauthenticatedRecord.ClientID != initialRecord.ClientID || reauthenticatedRecord.HostID != initialRecord.HostID {
		t.Fatalf("reauth did not preserve the OAuth record registration: %#v found=%v err=%v", reauthenticatedRecord, found, err)
	}

	returning := startTestAttempt(t, manager, target, api.AuthRequest{CoreID: "core", AttemptID: "attempt-returning", ProviderID: target.ProviderID, AuthProfile: target.Name, AccountID: "primary"})
	returningParams := mustLoginParams(t, returning.AuthorizationURL)
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
	returningCallback := returning.RedirectURI + "?" + url.Values{
		"code":      {"test-code"},
		"state":     {returningParams.Get("state")},
		"client_id": {"oaiapp_test-issued"},
	}.Encode()
	if _, err := manager.CompleteAttempt(context.Background(), returning.AttemptID, ""); err != nil {
		t.Fatal(err)
	}
	callbackResponse, err := http.Get(returningCallback)
	if err != nil {
		t.Fatalf("send automatic loopback callback: %v", err)
	}
	callbackResponse.Body.Close()
	returningResult := waitAttempt(t, manager, returning.AttemptID, "completed")
	if !returningResult.AccountSaved || !returningResult.SelectionSaved {
		t.Fatalf("returning account save flags = %#v", returningResult)
	}

	_, found, err = readOAuthRecord(path)
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
	defer secondManager.Close()
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
		if value.Token != "refreshed-access" || value.Identity != resolved.Identity {
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

func TestValidateAttemptCallbackRequiresRedirectAndState(t *testing.T) {
	job := &attemptJob{redirectURI: "http://127.0.0.1:12345/auth/callback", stateToken: "expected", clientID: "issued", registered: true}
	bad := job.redirectURI + "?" + url.Values{"state": {"other"}, "code": {"secret-code"}}.Encode()
	_, _, err := validateAttemptCallback(job, bad)
	if err == nil || strings.Contains(err.Error(), "secret-code") {
		t.Fatalf("callback mismatch error = %v", err)
	}
}

func resolveCredentialTarget(target Target) ResolveTarget {
	return func(context.Context) (Target, error) { return target, nil }
}

func startTestAttempt(t *testing.T, manager *Manager, target Target, request api.AuthRequest) api.AuthAttempt {
	t.Helper()
	attempt, err := manager.BeginAttempt(context.Background(), resolveCredentialTarget(target), request)
	if err != nil {
		t.Fatal(err)
	}
	return waitAttempt(t, manager, attempt.AttemptID, "ready")
}

func waitAttempt(t *testing.T, manager *Manager, attemptID, phase string) api.AuthAttempt {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		attempt, err := manager.Attempt(attemptID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt.Phase == phase {
			return attempt
		}
		if attempt.Terminal() && attempt.Phase != phase {
			t.Fatalf("attempt %s ended in %s (error=%v), want %s", attemptID, attempt.Phase, attempt.Error, phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
	attempt, _ := manager.Attempt(attemptID)
	t.Fatalf("attempt %s did not reach %s; current state %#v", attemptID, phase, attempt)
	return api.AuthAttempt{}
}
