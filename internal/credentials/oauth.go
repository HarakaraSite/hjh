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
	"time"
)

const (
	chatGPTAuthorizeClientID = "dynamic_agent_client"
	chatGPTResource          = "https://api.openai.com/v1"
	chatGPTScope             = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	chatGPTPlanScope         = "chatgpt.tokens.use.direct"
	chatGPTCallbackPath      = "/auth/callback"
)

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

func (m *Manager) prepareOAuthAttempt(ctx context.Context, job *attemptJob) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	endpoints, err := m.openIDConfiguration(ctx)
	if err != nil {
		return err
	}
	hostID, err := m.ensureHostID(ctx)
	if err != nil {
		return err
	}
	if job.hostID != "" {
		hostID = job.hostID
	}
	if job.registered && job.registration.Issuer != "" && job.registration.Issuer != endpoints.Issuer {
		return errors.New("ChatGPT client registration issuer did not match OpenAI")
	}
	if job.registered && job.clientID == "" {
		return errors.New("ChatGPT client registration is incomplete")
	}
	state, err := randomURLToken(32)
	if err != nil {
		return err
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		return err
	}
	verifier, err := randomURLToken(32)
	if err != nil {
		return err
	}
	challenge := sha256.Sum256([]byte(verifier))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start ChatGPT callback listener: %w", err)
	}
	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return errors.New("start ChatGPT callback listener: unexpected address")
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", tcpAddr.Port, chatGPTCallbackPath)
	authorizeURL, err := url.Parse(endpoints.AuthorizationEndpoint)
	if err != nil {
		_ = listener.Close()
		return errors.New("invalid ChatGPT authorization endpoint")
	}
	requestClientID := chatGPTAuthorizeClientID
	if job.registered {
		requestClientID = job.clientID
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
	if !job.registered {
		params.Set("agent_name_hint", "hjh")
	}
	query := authorizeURL.Query()
	for name, values := range params {
		for _, value := range values {
			query.Add(name, value)
		}
	}
	authorizeURL.RawQuery = query.Encode()
	serveDone := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != chatGPTCallbackPath {
			http.NotFound(w, r)
			return
		}
		job.queueCallback(redirectURI + "?" + r.URL.RawQuery)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Sign-in response received. Return to hjh.\n")
	})}
	job.mu.Lock()
	job.endpoints = endpoints
	job.hostID = hostID
	job.stateToken = state
	job.nonce = nonce
	job.verifier = verifier
	job.redirectURI = redirectURI
	job.authorize = authorizeURL.String()
	job.listener = listener
	job.server = server
	job.serveDone = serveDone
	job.mu.Unlock()
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()
	return nil
}

func (m *Manager) completeOAuthAttempt(ctx context.Context, job *attemptJob, code, clientID string) (accountSaved, selectionSaved bool, err error) {
	if !job.registered {
		registration := oauthRegistration{Issuer: job.endpoints.Issuer, ClientID: clientID, HostID: job.hostID}
		if err := m.runTicket(ctx, true, func(operationCtx context.Context) error {
			return m.saveOAuthRegistration(operationCtx, job.accountPath, registration)
		}); err != nil {
			return false, false, err
		}
		job.clientID = clientID
		job.registered = true
		job.registration = registration
	}
	tokens, err := m.exchangeAuthorizationCode(ctx, job, code, clientID)
	if err != nil {
		return false, false, err
	}
	claims, err := m.verifyIDToken(ctx, job.endpoints.JWKSURI, job.endpoints.Issuer, tokens.IDToken, clientID, job.nonce)
	if err != nil {
		return false, false, err
	}
	if job.wasConnected && claims.Subject != job.oldSubject {
		return false, false, errors.New("ChatGPT account identity did not match the selected account")
	}
	if tokens.AccessToken == "" || tokens.IDToken == "" {
		return false, false, errors.New("ChatGPT token response was incomplete")
	}
	now := time.Now().UTC()
	record := oauthRecord{
		Issuer:    claims.Issuer,
		Subject:   claims.Subject,
		Email:     claims.Email,
		ClientID:  clientID,
		HostID:    job.hostID,
		IDToken:   tokens.IDToken,
		Access:    tokens.AccessToken,
		Refresh:   tokens.RefreshToken,
		TokenType: tokens.TokenType,
		ExpiresAt: now.Add(time.Duration(tokens.ExpiresIn) * time.Second),
		Scopes:    strings.Fields(tokens.Scope),
		SavedAt:   now,
	}
	job.setPhase("saving")
	metadataPath := oauthMetadataPath(job.basePath)
	err = m.runTicket(ctx, true, func(operationCtx context.Context) error {
		return withAccountMetadataLock(operationCtx, job.basePath, func() error {
			metadata, found, readErr := readAccountMetadata(metadataPath)
			if readErr != nil {
				return readErr
			}
			if !found {
				metadata = defaultAccountMetadata()
			}
			if !containsAccount(metadata.AccountIDs, job.state.AccountID) {
				return errors.New("ChatGPT account slot was removed during authorization")
			}
			if err := withFileLock(operationCtx, job.accountPath, func() error {
				current, currentFound, readErr := readOAuthRecord(job.accountPath)
				if readErr != nil {
					return readErr
				}
				if job.wasConnected {
					if !currentFound || current.ClientID != job.clientID || current.Subject != job.oldSubject {
						return errors.New("ChatGPT account changed during authorization")
					}
				} else if currentFound {
					return errors.New("ChatGPT account changed during authorization")
				}
				currentRegistration, registrationFound, readErr := readOAuthRegistration(oauthRegistrationPath(job.accountPath))
				if readErr != nil {
					return readErr
				}
				if registrationFound && (currentRegistration.Issuer != record.Issuer || currentRegistration.ClientID != record.ClientID || currentRegistration.HostID != record.HostID) {
					return errors.New("ChatGPT client registration changed during authorization")
				}
				if err := writeOAuthRecord(job.accountPath, record); err != nil {
					return err
				}
				accountSaved = true
				return nil
			}); err != nil {
				return err
			}
			if job.wasConnected {
				selectionSaved = true
				return nil
			}
			if metadata.SelectedAccountID != job.state.AccountID {
				metadata.SelectedAccountID = job.state.AccountID
				metadata.Revision++
				if err := writeAccountMetadata(metadataPath, metadata); err != nil {
					return err
				}
			}
			selectionSaved = true
			return nil
		})
	})
	return accountSaved, selectionSaved, err
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

func validateAttemptCallback(job *attemptJob, callbackURL string) (string, string, error) {
	callback, err := url.Parse(callbackURL)
	if err != nil {
		return "", "", callbackInputError{message: "could not parse ChatGPT callback URL"}
	}
	expected, _ := url.Parse(job.redirectURI)
	if callback.Scheme != expected.Scheme || callback.Host != expected.Host || callback.Path != expected.Path || callback.User != nil || callback.Fragment != "" {
		return "", "", callbackInputError{message: "ChatGPT callback URL did not match the pending redirect"}
	}
	query, err := url.ParseQuery(callback.RawQuery)
	if err != nil {
		return "", "", callbackInputError{message: "could not parse ChatGPT callback parameters"}
	}
	state := query.Get("state")
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(job.stateToken)) != 1 {
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
	if job.registered {
		if clientID != "" && clientID != job.clientID {
			return "", "", callbackInputError{message: "ChatGPT callback client did not match the selected profile"}
		}
		return code, job.clientID, nil
	}
	if clientID == "" || clientID == chatGPTAuthorizeClientID {
		return "", "", callbackInputError{message: "ChatGPT registration did not return an issued client ID"}
	}
	return code, clientID, nil
}

func (m *Manager) exchangeAuthorizationCode(ctx context.Context, job *attemptJob, code, clientID string) (tokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {job.verifier},
		"redirect_uri":  {job.redirectURI},
		"resource":      {chatGPTResource},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, job.endpoints.TokenEndpoint, strings.NewReader(form.Encode()))
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
	basePath, err := m.oauthPath(profileName, profile)
	if err != nil {
		return Resolved{}, err
	}
	metadataPath := oauthMetadataPath(basePath)
	metadata := defaultAccountMetadata()
	err = withAccountMetadataLock(ctx, basePath, func() error {
		loaded, found, readErr := readAccountMetadata(metadataPath)
		if readErr != nil {
			return readErr
		}
		if found {
			metadata = loaded
		}
		return nil
	})
	if err != nil {
		return Resolved{}, err
	}
	return m.resolveOAuthAccount(ctx, profileName, oauthAccountPath(basePath, metadata.SelectedAccountID))
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
		// Keep request cancellation recognizable outside the credential wrapper.
		// Other decode errors remain opaque; token response bytes are not exposed.
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("could not read ChatGPT refresh response: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("could not read ChatGPT refresh response: %w", context.DeadlineExceeded)
		}
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
