package credentials

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	chatGPTAuthorizeClientID = "dynamic_agent_client"
	chatGPTResource          = "https://api.openai.com/v1"
	chatGPTScope             = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	chatGPTPlanScope         = "chatgpt.tokens.use.direct"
	chatGPTCallbackPath      = "/auth/callback"
)

type pendingLogin struct {
	profileName string
	path        string
	existing    bool
	registered  bool
	clientID    string
	subject     string
	hostID      string
	state       string
	nonce       string
	verifier    string
	redirectURI string
	authorize   string
	endpoints   openIDConfiguration
	callback    chan string
	listener    net.Listener
	server      *http.Server
	ctx         context.Context
	cancel      context.CancelFunc
	watchStop   chan struct{}
	watcherDone chan struct{}
	serveDone   chan struct{}
	retired     chan struct{}

	busy        bool
	mu          sync.Mutex
	shutdown    sync.Once
	retire      sync.Once
	shutdownErr error
}

// AuthorizationCodeInvalidGrantError reports an authorization code that must
// be discarded. The issued client registration remains available for a fresh
// authorization attempt.
type AuthorizationCodeInvalidGrantError struct{}

func (*AuthorizationCodeInvalidGrantError) Error() string {
	return "ChatGPT authorization code expired; start a fresh sign-in"
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

// BeginLogin creates a Sign in with ChatGPT authorization request. It starts a
// loopback callback listener when possible; callers may instead pass the URL
// copied from a browser on another machine to CompleteLogin.
func (m *Manager) BeginLogin(ctx context.Context, profileName string, profile Profile) (Login, error) {
	if err := ctx.Err(); err != nil {
		return Login{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Login{}, errors.New("credential manager is closed")
	}
	if pending := m.pending[profileName]; pending != nil {
		login := Login{AuthorizationURL: pending.authorize, RedirectURI: pending.redirectURI}
		m.mu.Unlock()
		return login, nil
	}
	m.mu.Unlock()

	path, err := m.oauthPath(profileName, profile)
	if err != nil {
		return Login{}, err
	}
	var existing oauthRecord
	var registration oauthRegistration
	found := false
	registrationFound := false
	err = withFileLock(ctx, path, func() error {
		var readErr error
		existing, found, readErr = readOAuthRecord(path)
		if readErr != nil {
			return readErr
		}
		registration, registrationFound, readErr = readOAuthRegistration(oauthRegistrationPath(path))
		return readErr
	})
	if err != nil {
		return Login{}, err
	}
	hostID, err := m.ensureHostID(ctx)
	if err != nil {
		return Login{}, err
	}
	endpoints, err := m.openIDConfiguration(ctx)
	if err != nil {
		return Login{}, err
	}
	registered := found || registrationFound
	issuedClientID := ""
	if found {
		issuedClientID = existing.ClientID
		if existing.HostID != "" {
			hostID = existing.HostID
		}
	} else if registrationFound {
		if registration.Issuer != endpoints.Issuer {
			return Login{}, errors.New("ChatGPT client registration issuer did not match OpenAI")
		}
		issuedClientID = registration.ClientID
		hostID = registration.HostID
	}
	state, err := randomURLToken(32)
	if err != nil {
		return Login{}, err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return Login{}, err
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return Login{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	loginURL, err := url.Parse(endpoints.AuthorizationEndpoint)
	if err != nil {
		return Login{}, errors.New("invalid ChatGPT authorization endpoint")
	}
	pendingCtx, cancelPending := context.WithCancel(ctx)
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		cancelPending()
		return Login{}, err
	}
	if m.closed {
		m.mu.Unlock()
		cancelPending()
		return Login{}, errors.New("credential manager is closed")
	}
	if previous := m.pending[profileName]; previous != nil {
		login := Login{AuthorizationURL: previous.authorize, RedirectURI: previous.redirectURI}
		m.mu.Unlock()
		cancelPending()
		return login, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.mu.Unlock()
		cancelPending()
		return Login{}, fmt.Errorf("start ChatGPT callback listener: %w", err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		m.mu.Unlock()
		cancelPending()
		return Login{}, errors.New("start ChatGPT callback listener: unexpected address")
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", tcpAddr.Port, chatGPTCallbackPath)
	requestClientID := chatGPTAuthorizeClientID
	if registered {
		requestClientID = issuedClientID
	}
	params := url.Values{
		"client_id":             {requestClientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {chatGPTScope},
		"resource":              {chatGPTResource},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge_method": {"S256"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"ext_agent_host_id":     {hostID},
	}
	if !registered {
		params.Set("agent_name_hint", "hjh")
	}
	query := loginURL.Query()
	for name, values := range params {
		for _, value := range values {
			query.Add(name, value)
		}
	}
	loginURL.RawQuery = query.Encode()
	pending := &pendingLogin{
		profileName: profileName,
		path:        path,
		existing:    found,
		subject:     existing.Subject,
		registered:  registered,
		clientID:    issuedClientID,
		hostID:      hostID,
		state:       state,
		nonce:       nonce,
		verifier:    verifier,
		redirectURI: redirectURI,
		authorize:   loginURL.String(),
		endpoints:   endpoints,
		callback:    make(chan string, 1),
		listener:    listener,
		ctx:         pendingCtx,
		cancel:      cancelPending,
		watchStop:   make(chan struct{}),
		watcherDone: make(chan struct{}),
		serveDone:   make(chan struct{}),
		retired:     make(chan struct{}),
	}
	pending.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != chatGPTCallbackPath {
			http.NotFound(w, r)
			return
		}
		select {
		case pending.callback <- pending.redirectURI + "?" + r.URL.RawQuery:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, "Sign-in response received. Return to hjh.\n")
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	})}
	go func() {
		defer close(pending.serveDone)
		_ = pending.server.Serve(listener)
	}()
	m.pending[profileName] = pending
	m.live[pending] = struct{}{}
	go m.watchPendingContext(pending)
	m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		_ = m.retirePending(profileName, pending)
		<-pending.watcherDone
		return Login{}, err
	}
	return Login{AuthorizationURL: pending.authorize, RedirectURI: pending.redirectURI}, nil
}

// CompleteLogin exchanges a validated authorization callback and stores the
// resulting account. When callbackURL is empty it waits for the loopback
// listener started by BeginLogin.
func (m *Manager) CompleteLogin(ctx context.Context, profileName string, profile Profile, callbackURL string) (Status, error) {
	_ = profile // The pending operation keeps the exact profile path it began with.
	m.mu.Lock()
	pending := m.pending[profileName]
	if pending == nil {
		m.mu.Unlock()
		return Status{}, fmt.Errorf("no ChatGPT login is pending for profile %q", profileName)
	}
	pending.mu.Lock()
	if pending.busy {
		pending.mu.Unlock()
		m.mu.Unlock()
		return Status{}, errors.New("ChatGPT login completion is already in progress")
	}
	pending.busy = true
	pending.mu.Unlock()
	m.mu.Unlock()

	completionCtx, cancelCompletion := context.WithCancel(ctx)
	stopBeginContext := context.AfterFunc(pending.ctx, cancelCompletion)
	if pending.ctx.Err() != nil {
		cancelCompletion()
	}
	defer func() {
		stopBeginContext()
		cancelCompletion()
	}()
	ctx = completionCtx

	keepPending := false
	defer func() {
		m.mu.Lock()
		if keepPending {
			if m.pending[profileName] == pending {
				pending.mu.Lock()
				pending.busy = false
				pending.mu.Unlock()
			}
		}
		m.mu.Unlock()
		if !keepPending {
			_ = m.retirePending(profileName, pending)
			<-pending.watcherDone
		}
	}()

	if callbackURL == "" {
		select {
		case callbackURL = <-pending.callback:
		case <-ctx.Done():
			return Status{}, ctx.Err()
		}
	}
	code, clientID, err := validateCallback(pending, callbackURL)
	if err != nil {
		var retryable callbackInputError
		keepPending = errors.As(err, &retryable)
		return Status{}, err
	}
	if !pending.registered {
		registration := oauthRegistration{
			Issuer:   pending.endpoints.Issuer,
			ClientID: clientID,
			HostID:   pending.hostID,
		}
		if err := m.saveOAuthRegistration(ctx, pending.path, registration); err != nil {
			return Status{}, err
		}
		pending.clientID = clientID
		pending.registered = true
	}
	tokens, err := m.exchangeAuthorizationCode(ctx, pending, code, clientID)
	if err != nil {
		return Status{}, err
	}
	claims, err := m.verifyIDToken(ctx, pending.endpoints.JWKSURI, pending.endpoints.Issuer, tokens.IDToken, clientID, pending.nonce)
	if err != nil {
		return Status{}, err
	}
	if pending.existing && claims.Subject != pending.subject {
		return Status{}, errors.New("ChatGPT account identity did not match the selected profile")
	}
	if tokens.AccessToken == "" || tokens.IDToken == "" {
		return Status{}, errors.New("ChatGPT token response was incomplete")
	}
	now := time.Now().UTC()
	record := oauthRecord{
		Issuer:    claims.Issuer,
		Subject:   claims.Subject,
		Email:     claims.Email,
		ClientID:  clientID,
		HostID:    pending.hostID,
		IDToken:   tokens.IDToken,
		Access:    tokens.AccessToken,
		Refresh:   tokens.RefreshToken,
		TokenType: tokens.TokenType,
		ExpiresAt: now.Add(time.Duration(tokens.ExpiresIn) * time.Second),
		Scopes:    strings.Fields(tokens.Scope),
		SavedAt:   now,
	}
	err = withFileLock(ctx, pending.path, func() error {
		current, currentFound, readErr := readOAuthRecord(pending.path)
		if readErr != nil {
			return readErr
		}
		if pending.existing {
			if !currentFound || current.ClientID != pending.clientID || current.Subject != pending.subject {
				return errors.New("ChatGPT credential profile changed during login")
			}
		} else if currentFound {
			return errors.New("ChatGPT credential profile changed during login")
		}
		currentRegistration, registrationFound, readErr := readOAuthRegistration(oauthRegistrationPath(pending.path))
		if readErr != nil {
			return readErr
		}
		if registrationFound && (currentRegistration.Issuer != record.Issuer || currentRegistration.ClientID != record.ClientID || currentRegistration.HostID != record.HostID) {
			return errors.New("ChatGPT client registration changed during login")
		}
		return writeOAuthRecord(pending.path, record)
	})
	if err != nil {
		return Status{}, err
	}
	return statusFor(record), nil
}

func (m *Manager) saveOAuthRegistration(ctx context.Context, path string, registration oauthRegistration) error {
	return withFileLock(ctx, path, func() error {
		current, found, err := readOAuthRegistration(oauthRegistrationPath(path))
		if err != nil {
			return err
		}
		if found && current != registration {
			return errors.New("ChatGPT client registration changed during login")
		}
		return writeOAuthRegistration(oauthRegistrationPath(path), registration)
	})
}

type callbackInputError struct{ message string }

func (e callbackInputError) Error() string { return e.message }

func validateCallback(pending *pendingLogin, callbackURL string) (string, string, error) {
	callback, err := url.Parse(callbackURL)
	if err != nil {
		return "", "", callbackInputError{message: "could not parse ChatGPT callback URL"}
	}
	expected, _ := url.Parse(pending.redirectURI)
	if callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != expected.Path || callback.User != nil || callback.Fragment != "" {
		return "", "", callbackInputError{message: "ChatGPT callback URL did not match the pending redirect"}
	}
	query, err := url.ParseQuery(callback.RawQuery)
	if err != nil {
		return "", "", callbackInputError{message: "could not parse ChatGPT callback parameters"}
	}
	state := query.Get("state")
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(pending.state)) != 1 {
		return "", "", callbackInputError{message: "ChatGPT callback state did not match"}
	}
	if query.Get("error") != "" {
		if query.Get("error") == "access_denied" {
			return "", "", errors.New("ChatGPT sign-in was declined")
		}
		return "", "", errors.New("ChatGPT authorization did not complete")
	}
	code := query.Get("code")
	if code == "" {
		return "", "", callbackInputError{message: "ChatGPT callback did not include an authorization code"}
	}
	clientID := query.Get("client_id")
	if pending.registered {
		if clientID != "" && clientID != pending.clientID {
			return "", "", callbackInputError{message: "ChatGPT callback client did not match the selected profile"}
		}
		return code, pending.clientID, nil
	}
	if clientID == "" || clientID == chatGPTAuthorizeClientID {
		return "", "", callbackInputError{message: "ChatGPT registration did not return an issued client ID"}
	}
	return code, clientID, nil
}

func (m *Manager) exchangeAuthorizationCode(ctx context.Context, pending *pendingLogin, code, clientID string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {pending.verifier},
		"redirect_uri":  {pending.redirectURI},
		"resource":      {chatGPTResource},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, pending.endpoints.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, errors.New("could not prepare ChatGPT token exchange")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("ChatGPT token exchange failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var oauthError struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(response.Body).Decode(&oauthError)
		if oauthError.Error == "invalid_grant" {
			return tokenResponse{}, &AuthorizationCodeInvalidGrantError{}
		}
		return tokenResponse{}, fmt.Errorf("ChatGPT token exchange failed with HTTP %d", response.StatusCode)
	}
	var tokens tokenResponse
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		return tokenResponse{}, errors.New("could not read ChatGPT token response")
	}
	return tokens, nil
}

func (m *Manager) resolveChatGPT(ctx context.Context, profileName string, profile Profile) (Resolved, error) {
	path, err := m.oauthPath(profileName, profile)
	if err != nil {
		return Resolved{}, err
	}
	var resolved Resolved
	err = withFileLock(ctx, path, func() error {
		record, found, readErr := readOAuthRecord(path)
		if readErr != nil {
			return readErr
		}
		if !found {
			return fmt.Errorf("ChatGPT login is required for credential profile %q", profileName)
		}
		if !hasScope(record.Scopes, chatGPTPlanScope) {
			return errors.New("ChatGPT plan usage is not enabled for this account")
		}
		if !record.ExpiresAt.After(time.Now().Add(30 * time.Second)) {
			if record.Refresh == "" {
				return errors.New("ChatGPT access token expired and no refresh token is available")
			}
			if err := m.refreshRecord(ctx, path, &record); err != nil {
				return err
			}
		}
		if !hasScope(record.Scopes, chatGPTPlanScope) {
			return errors.New("ChatGPT plan usage is not enabled for this account")
		}
		resolved = Resolved{Token: record.Access, Identity: accountIdentity(record.Issuer, record.ClientID, record.Subject)}
		return nil
	})
	if err != nil {
		return Resolved{}, err
	}
	return resolved, nil
}

func (m *Manager) refreshRecord(ctx context.Context, path string, record *oauthRecord) error {
	endpoints, err := m.openIDConfiguration(ctx)
	if err != nil {
		return err
	}
	if endpoints.Issuer != record.Issuer {
		return errors.New("ChatGPT credential issuer did not match OpenAI")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {record.ClientID},
		"refresh_token": {record.Refresh},
		"resource":      {chatGPTResource},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoints.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("could not prepare ChatGPT token refresh")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("ChatGPT token refresh failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("ChatGPT token refresh failed with HTTP %d", response.StatusCode)
	}
	var tokens tokenResponse
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		return errors.New("could not read ChatGPT refresh response")
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return errors.New("ChatGPT refresh response did not include replacement credentials")
	}
	now := time.Now().UTC()
	record.Access = tokens.AccessToken
	record.Refresh = tokens.RefreshToken
	record.TokenType = tokens.TokenType
	record.ExpiresAt = now.Add(time.Duration(tokens.ExpiresIn) * time.Second)
	if tokens.Scope != "" {
		record.Scopes = strings.Fields(tokens.Scope)
	}
	record.SavedAt = now
	if err := writeOAuthRecord(path, *record); err != nil {
		return err
	}
	return nil
}

func randomURLToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create ChatGPT authorization request: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (m *Manager) watchPendingContext(pending *pendingLogin) {
	defer close(pending.watcherDone)
	select {
	case <-pending.ctx.Done():
		_ = m.retirePending(pending.profileName, pending)
	case <-pending.watchStop:
	}
}

func (m *Manager) retirePending(profileName string, pending *pendingLogin) error {
	pending.retire.Do(func() {
		m.mu.Lock()
		if m.pending[profileName] == pending {
			delete(m.pending, profileName)
		}
		m.mu.Unlock()

		pending.shutdownErr = pending.stop()

		m.mu.Lock()
		delete(m.live, pending)
		m.mu.Unlock()
	})
	return pending.shutdownErr
}

func (pending *pendingLogin) stop() error {
	pending.shutdown.Do(func() {
		close(pending.watchStop)
		if pending.cancel != nil {
			pending.cancel()
		}
		var closeErrors []error
		if pending.server != nil {
			if err := pending.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				closeErrors = append(closeErrors, err)
			}
		}
		if pending.listener != nil {
			if err := pending.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErrors = append(closeErrors, err)
			}
		}
		if pending.serveDone != nil {
			<-pending.serveDone
		}
		pending.shutdownErr = errors.Join(closeErrors...)
		close(pending.retired)
	})
	return pending.shutdownErr
}

func statusFor(record oauthRecord) Status {
	return Status{
		Connected:        true,
		Identity:         accountIdentity(record.Issuer, record.ClientID, record.Subject),
		Account:          record.Email,
		ExpiresAt:        record.ExpiresAt,
		PlanUsageEnabled: hasScope(record.Scopes, chatGPTPlanScope),
	}
}

func hasScope(scopes []string, wanted string) bool {
	for _, scope := range scopes {
		if scope == wanted {
			return true
		}
	}
	return false
}

func accountIdentity(issuer, clientID, subject string) string {
	digest := sha256.Sum256([]byte(issuer + "\x00" + clientID + "\x00" + subject))
	return "chatgpt:" + base64.RawURLEncoding.EncodeToString(digest[:])
}
