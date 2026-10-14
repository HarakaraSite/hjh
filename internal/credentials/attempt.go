package credentials

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"

	"hjh/internal/api"
)

type attemptJob struct {
	mu sync.Mutex

	state              api.AuthAttempt
	ownerID            string
	slotKey            string
	target             Target
	basePath           string
	accountPath        string
	requestedAccountID string

	wasConnected bool
	oldSubject   string
	clientID     string
	registered   bool
	registration oauthRegistration

	endpoints   openIDConfiguration
	hostID      string
	stateToken  string
	nonce       string
	verifier    string
	authorize   string
	redirectURI string

	ctx             context.Context
	cancel          context.CancelFunc
	callbacks       chan string
	callbackPending bool
	done            chan struct{}
	listener        net.Listener
	server          *http.Server
	serveDone       chan struct{}
}

func (job *attemptJob) snapshot() api.AuthAttempt {
	job.mu.Lock()
	defer job.mu.Unlock()
	state := job.state
	if state.Error != nil {
		copy := *state.Error
		state.Error = &copy
	}
	return state
}

func (job *attemptJob) setReady() {
	job.mu.Lock()
	job.state.Phase = "ready"
	job.state.AuthorizationURL = job.authorize
	job.state.RedirectURI = job.redirectURI
	job.state.Error = nil
	job.mu.Unlock()
}

func (job *attemptJob) queueCallback(callbackURL string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	switch job.state.Phase {
	case "starting", "ready", "waiting_callback":
	default:
		return
	}
	if job.callbackPending {
		return
	}
	job.callbackPending = true
	job.callbacks <- callbackURL
}

func (job *attemptJob) setRetryableError(err error) {
	job.mu.Lock()
	job.callbackPending = false
	job.state.Phase = "ready"
	job.state.Error = &api.Error{Code: "callback_input_invalid", Message: err.Error()}
	job.mu.Unlock()
}

func (job *attemptJob) setPhase(phase string) {
	job.mu.Lock()
	job.state.Phase = phase
	job.state.Error = nil
	job.mu.Unlock()
}

func authAttemptError(err error) *api.Error {
	if err == nil {
		return nil
	}
	var invalidGrant *AuthorizationCodeInvalidGrantError
	if errors.As(err, &invalidGrant) {
		return &api.Error{Code: "authorization_code_invalid_grant", Message: err.Error()}
	}
	return &api.Error{Code: "auth_attempt_failed", Message: err.Error()}
}

func (job *attemptJob) stopListener() {
	job.mu.Lock()
	server, listener, serveDone := job.server, job.listener, job.serveDone
	job.server, job.listener, job.serveDone = nil, nil, nil
	job.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	if listener != nil {
		_ = listener.Close()
	}
	if serveDone != nil {
		<-serveDone
	}
}

func (job *attemptJob) clearSecrets() {
	job.mu.Lock()
	job.stateToken = ""
	job.nonce = ""
	job.verifier = ""
	job.authorize = ""
	job.redirectURI = ""
	job.clientID = ""
	job.oldSubject = ""
	job.registration = oauthRegistration{}
	job.endpoints = openIDConfiguration{}
	job.state.AuthorizationURL = ""
	job.state.RedirectURI = ""
	job.callbackPending = false
	select {
	case <-job.callbacks:
	default:
	}
	job.mu.Unlock()
}

func (m *Manager) BeginAttempt(ctx context.Context, resolve ResolveTarget, request api.AuthRequest) (api.AuthAttempt, error) {
	var result api.AuthAttempt
	err := m.runTicket(ctx, true, func(operationCtx context.Context) error {
		target, err := resolve(operationCtx)
		if err != nil {
			return err
		}
		target, err = normalizeTarget(target)
		if err != nil {
			return err
		}
		if target.Kind != KindChatGPTOAuth {
			return fmt.Errorf("provider %s uses %s authentication", target.ProviderID, target.Kind)
		}
		if request.AttemptID == "" {
			return errors.New("authentication attempt ID is required")
		}
		basePath, err := m.oauthPath(target.Name, target.Profile)
		if err != nil {
			return err
		}
		ownerID := oauthOwnerID(basePath)
		scopeID := credentialScopeID(target.ConfigPath)
		if existing, exists := m.getAttempt(request.AttemptID); exists {
			if !existing.matches(request, target, ownerID, scopeID) {
				return errors.New("authentication attempt ID is already used for another target")
			}
			result = existing.snapshot()
			return nil
		}
		if request.AccountID != "" {
			if existing, exists := m.getSlotAttempt(ownerID, request.AccountID); exists {
				result = existing.snapshot()
				return nil
			}
		}

		metadataPath := oauthMetadataPath(basePath)
		var metadata accountMetadata
		var accountID string
		var accountPath string
		var record oauthRecord
		var recordFound bool
		var registration oauthRegistration
		var registrationFound bool
		err = withAccountMetadataLock(operationCtx, basePath, func() error {
			var found bool
			metadata, found, err = readAccountMetadata(metadataPath)
			if err != nil {
				return err
			}
			if !found {
				metadata = defaultAccountMetadata()
			}
			accountID = request.AccountID
			if accountID == "" {
				accountID, err = newAccountID()
				if err != nil {
					return err
				}
				metadata.AccountIDs = append(metadata.AccountIDs, accountID)
				metadata.Revision++
				if err := writeAccountMetadata(metadataPath, metadata); err != nil {
					return err
				}
			} else if !containsAccount(metadata.AccountIDs, accountID) {
				return fmt.Errorf("ChatGPT account %q is not registered for this credential owner", accountID)
			}
			accountPath = oauthAccountPath(basePath, accountID)
			return withFileLock(operationCtx, accountPath, func() error {
				var readErr error
				record, recordFound, readErr = readOAuthRecord(accountPath)
				if readErr != nil {
					return readErr
				}
				registration, registrationFound, readErr = readOAuthRegistration(oauthRegistrationPath(accountPath))
				return readErr
			})
		})
		if err != nil {
			return err
		}
		if existing, exists := m.getSlotAttempt(ownerID, accountID); exists {
			result = existing.snapshot()
			return nil
		}
		slotKey := ownerID + "\x00" + accountID
		jobCtx, cancel := context.WithCancel(m.lifeCtx)
		job := &attemptJob{
			state: api.AuthAttempt{CoreID: request.CoreID, AttemptID: request.AttemptID, ScopeID: scopeID,
				ProviderID: target.ProviderID, AuthProfile: target.Name, OwnerID: ownerID, AccountID: accountID, Phase: "starting"},
			ownerID: ownerID, slotKey: slotKey, target: target, basePath: basePath, accountPath: accountPath,
			requestedAccountID: request.AccountID, wasConnected: recordFound, hostID: record.HostID,
			oldSubject: record.Subject, clientID: record.ClientID, registered: recordFound || registrationFound,
			registration: registration, ctx: jobCtx, cancel: cancel, callbacks: make(chan string, 1), done: make(chan struct{}),
		}
		if registrationFound && !recordFound {
			job.clientID = registration.ClientID
			job.hostID = registration.HostID
		}
		m.mu.Lock()
		if current := m.attempts[request.AttemptID]; current != nil {
			m.mu.Unlock()
			cancel()
			if !current.matches(request, target, ownerID, scopeID) {
				return errors.New("authentication attempt ID is already used for another target")
			}
			result = current.snapshot()
			return nil
		}
		if currentID := m.slotAttempts[slotKey]; currentID != "" {
			if current := m.attempts[currentID]; current != nil && !current.snapshot().Terminal() {
				m.mu.Unlock()
				cancel()
				result = current.snapshot()
				return nil
			}
		}
		m.attempts[request.AttemptID] = job
		m.slotAttempts[slotKey] = request.AttemptID
		m.jobs.Add(1)
		m.mu.Unlock()
		go m.runAttempt(job)
		result = job.snapshot()
		return nil
	})
	return result, err
}

func (job *attemptJob) matches(request api.AuthRequest, target Target, ownerID, scopeID string) bool {
	state := job.snapshot()
	return state.CoreID == request.CoreID && state.ScopeID == scopeID && state.ProviderID == target.ProviderID &&
		state.AuthProfile == target.Name && state.OwnerID == ownerID && job.requestedAccountID == request.AccountID
}

func (m *Manager) getAttempt(attemptID string) (*attemptJob, bool) {
	m.mu.Lock()
	job := m.attempts[attemptID]
	m.mu.Unlock()
	return job, job != nil
}

func (m *Manager) getSlotAttempt(ownerID, accountID string) (*attemptJob, bool) {
	m.mu.Lock()
	jobID := m.slotAttempts[ownerID+"\x00"+accountID]
	job := m.attempts[jobID]
	m.mu.Unlock()
	if job == nil || job.snapshot().Terminal() {
		return nil, false
	}
	return job, true
}

func (m *Manager) Attempt(attemptID string) (api.AuthAttempt, error) {
	job, exists := m.getAttempt(attemptID)
	if !exists {
		return api.AuthAttempt{}, errors.New("authentication attempt was not found")
	}
	return job.snapshot(), nil
}

func (m *Manager) CompleteAttempt(ctx context.Context, attemptID, callbackURL string) (api.AuthAttempt, error) {
	if err := ctx.Err(); err != nil {
		return api.AuthAttempt{}, err
	}
	job, exists := m.getAttempt(attemptID)
	if !exists {
		return api.AuthAttempt{}, errors.New("authentication attempt was not found")
	}
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.state.Terminal() || job.state.Phase == "exchanging" || job.state.Phase == "saving" {
		return copyAuthAttempt(job.state), nil
	}
	if callbackURL == "" {
		if job.state.Phase == "ready" {
			job.state.Phase = "waiting_callback"
		}
		return copyAuthAttempt(job.state), nil
	}
	if job.callbackPending {
		return copyAuthAttempt(job.state), nil
	}
	if job.state.Phase != "starting" && job.state.Phase != "ready" && job.state.Phase != "waiting_callback" {
		return copyAuthAttempt(job.state), nil
	}
	job.callbackPending = true
	job.callbacks <- callbackURL
	return copyAuthAttempt(job.state), nil
}

func copyAuthAttempt(state api.AuthAttempt) api.AuthAttempt {
	if state.Error != nil {
		copy := *state.Error
		state.Error = &copy
	}
	return state
}

func (m *Manager) CancelAttempt(attemptID string) (api.AuthAttempt, error) {
	job, exists := m.getAttempt(attemptID)
	if !exists {
		return api.AuthAttempt{}, errors.New("authentication attempt was not found")
	}
	job.mu.Lock()
	switch job.state.Phase {
	case "starting", "ready", "waiting_callback":
		job.cancel()
		job.mu.Unlock()
		<-job.done
		return job.snapshot(), nil
	default:
		state := copyAuthAttempt(job.state)
		job.mu.Unlock()
		return state, nil
	}
}

func (m *Manager) DeleteAttempt(attemptID string) error {
	m.mu.Lock()
	job := m.attempts[attemptID]
	if job == nil {
		m.mu.Unlock()
		return nil
	}
	state := job.snapshot()
	if !state.Terminal() {
		m.mu.Unlock()
		return errors.New("authentication attempt is still active")
	}
	select {
	case <-job.done:
	default:
		m.mu.Unlock()
		return errors.New("authentication attempt cleanup is still in progress")
	}
	delete(m.attempts, attemptID)
	if m.slotAttempts[job.slotKey] == attemptID {
		delete(m.slotAttempts, job.slotKey)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) runAttempt(job *attemptJob) {
	phase := "failed"
	var finalErr error
	var accountSaved, selectionSaved bool
	defer func() {
		job.stopListener()
		job.clearSecrets()
		job.mu.Lock()
		job.state.Phase = phase
		job.state.AuthorizationURL = ""
		job.state.RedirectURI = ""
		job.state.Error = authAttemptError(finalErr)
		job.state.AccountSaved = accountSaved
		job.state.SelectionSaved = selectionSaved
		job.mu.Unlock()
		m.mu.Lock()
		if m.slotAttempts[job.slotKey] == job.state.AttemptID {
			delete(m.slotAttempts, job.slotKey)
		}
		m.mu.Unlock()
		close(job.done)
		m.jobs.Done()
	}()

	if err := m.prepareOAuthAttempt(job.ctx, job); err != nil {
		if job.ctx.Err() != nil {
			phase = "cancelled"
		} else {
			finalErr = err
		}
		return
	}
	if job.ctx.Err() != nil {
		phase = "cancelled"
		return
	}
	job.setReady()
	for {
		select {
		case <-job.ctx.Done():
			phase = "cancelled"
			return
		case callbackURL := <-job.callbacks:
			job.mu.Lock()
			job.callbackPending = false
			job.mu.Unlock()
			code, clientID, err := validateAttemptCallback(job, callbackURL)
			if err != nil {
				var retryable callbackInputError
				if errors.As(err, &retryable) {
					job.setRetryableError(err)
					continue
				}
				finalErr = err
				return
			}
			job.mu.Lock()
			if job.ctx.Err() != nil || job.state.Terminal() {
				job.mu.Unlock()
				phase = "cancelled"
				return
			}
			job.state.Phase = "exchanging"
			job.state.Error = nil
			job.mu.Unlock()
			accountSaved, selectionSaved, err = m.completeOAuthAttempt(job.ctx, job, code, clientID)
			if err != nil {
				if job.ctx.Err() != nil {
					phase = "cancelled"
				} else {
					finalErr = err
				}
				return
			}
			phase = "completed"
			return
		}
	}
}

func newAccountID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create ChatGPT account ID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	text := hex.EncodeToString(value[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}

var _ net.Listener
