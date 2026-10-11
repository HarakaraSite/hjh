package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"hjh/internal/api"
)

const authControlTimeout = 5 * time.Second

func (m *model) authControlContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(m.ctx), authControlTimeout)
}

func authAttemptCancellable(attempt api.AuthAttempt) bool {
	return !attempt.Terminal() && attempt.Phase != "exchanging" && attempt.Phase != "saving"
}

func channelClosed(channel <-chan struct{}) bool {
	if channel == nil {
		return false
	}
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func (m *model) authContextCurrent(state *authUIState) bool {
	return state != nil && m.authUI == state && state.coreID == m.core.CoreID
}

func (m *model) beginAuthAttempt(accountID string) {
	state := m.authUI
	if state == nil {
		return
	}
	request := api.AuthRequest{
		CoreID: state.coreID, SessionID: state.scope.SessionID, Workspace: state.scope.Workspace,
		ProviderID: state.providerID, AuthProfile: state.selectedProfile, AttemptID: uuid.NewString(), AccountID: accountID,
	}
	if state.beginPending || state.attemptJobID != 0 {
		return
	}
	ctx, cancel := m.authControlContext()
	job := m.newJob(func() {})
	job.closeBeforeBackend = true
	state.attemptJobID = job.id
	state.beginPending = true
	state.cancelRequested = false
	state.cancelIntent = make(chan struct{})
	state.attemptID = request.AttemptID
	state.attemptCoreID = state.coreID
	state.attemptScope = state.scope
	state.attemptProvider = state.providerID
	state.attemptProfile = state.selectedProfile
	state.attemptOwnerID = state.ownerID
	state.attempt = &api.AuthAttempt{
		CoreID: state.coreID, AttemptID: request.AttemptID, ProviderID: state.providerID,
		AuthProfile: state.selectedProfile, OwnerID: state.ownerID, AccountID: accountID, Phase: "starting",
	}
	state.screen = authOAuthScreen
	generation, coreID, providerID, profile, ownerID := state.generation, state.coreID, state.providerID, state.selectedProfile, state.ownerID
	cancelIntent := state.cancelIntent
	state.status = "Starting ChatGPT authorization in Core."
	m.status = state.status
	m.refreshViewport(false)
	m.launch(job, func() tea.Msg {
		defer cancel()
		attempt, err := m.backend.BeginAuthAttempt(ctx, request)
		if err != nil {
			// A lost response can still mean Core accepted the attempt. Confirm the client ID
			// before reporting failure, without beginning a second attempt.
			var readErr error
			attempt, readErr = m.backend.AuthAttempt(ctx, coreID, request.AttemptID)
			if readErr == nil {
				err = nil
			}
		}
		if err == nil {
			if attempt.CoreID == "" {
				attempt.CoreID = coreID
			}
			if attempt.AttemptID == "" {
				attempt.AttemptID = request.AttemptID
			}
			if attempt.ProviderID == "" {
				attempt.ProviderID = providerID
			}
			if attempt.AuthProfile == "" {
				attempt.AuthProfile = profile
			}
			if attempt.OwnerID == "" {
				attempt.OwnerID = ownerID
			}
		}
		ack := make(chan struct{})
		m.sendMessage(authAttemptStartedMsg{
			jobID: job.id, generation: generation, coreID: coreID, providerID: providerID,
			profile: profile, ownerID: ownerID, attempt: attempt, err: err,
			cancelIntent: cancelIntent, ack: ack,
		})
		if err != nil {
			return nil
		}
		select {
		case <-ack:
			return nil
		case <-cancelIntent:
			if !authAttemptCancellable(attempt) {
				return nil
			}
			cancelCtx, cancelCall := m.authControlContext()
			defer cancelCall()
			cancelled, cancelErr := m.backend.CancelAuthAttempt(cancelCtx, api.AuthRequest{CoreID: attempt.CoreID, AttemptID: attempt.AttemptID})
			return authAttemptCancelledMsg{
				jobID: job.id, generation: generation, coreID: attempt.CoreID,
				attemptID: attempt.AttemptID, attempt: cancelled, err: cancelErr,
			}
		}
	})
}

func (m *model) acceptAuthAttemptStarted(msg authAttemptStartedMsg) tea.Cmd {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.coreID != msg.coreID || state.providerID != msg.providerID ||
		state.selectedProfile != msg.profile || state.attemptJobID != msg.jobID || !state.beginPending || !m.authContextCurrent(state) {
		if msg.cancelIntent != nil {
			select {
			case <-msg.cancelIntent:
			default:
				close(msg.cancelIntent)
			}
		}
		return nil
	}
	if msg.err != nil {
		state.attemptJobID = 0
		state.beginPending = false
		state.cancelIntent = nil
		state.cancelRequested = false
		state.status = "Could not confirm the authorization attempt. Reopen /login to check its Core status."
		m.status = "authorization attempt result unknown"
		state.screen = authAccountsScreen
		m.refreshViewport(false)
		m.applyPendingAuthRouteRebase(state)
		if msg.ack != nil {
			close(msg.ack)
		}
		return nil
	}
	m.adoptAuthAttempt(state, msg.attempt)
	if state.cancelRequested {
		if !authAttemptCancellable(msg.attempt) {
			state.attemptJobID = 0
			state.beginPending = false
			state.cancelIntent = nil
			state.cancelRequested = false
			state.status = attemptStatus(msg.attempt)
			if state.open {
				state.screen = authAccountsScreen
				m.status = state.status
				m.refreshViewport(false)
			}
			if msg.ack != nil {
				close(msg.ack)
			}
			return m.scheduleAuthAttemptPoll(state)
		}
		state.status = "Cancelling authorization in Core."
		if state.open {
			state.screen = authAccountsScreen
			m.status = state.status
			m.refreshViewport(false)
		}
		return nil
	}
	state.attemptJobID = 0
	state.beginPending = false
	state.cancelIntent = nil
	state.screen = authOAuthScreen
	state.status = attemptStatus(msg.attempt)
	m.status = state.status
	m.refreshViewport(false)
	if msg.ack != nil {
		close(msg.ack)
	}
	return m.scheduleAuthAttemptPoll(state)
}

func (m *model) adoptAuthAttempt(state *authUIState, attempt api.AuthAttempt) {
	if state == nil {
		return
	}
	state.attempt = &attempt
	state.attemptID = attempt.AttemptID
	state.attemptCoreID = attempt.CoreID
	if state.attemptCoreID == "" {
		state.attemptCoreID = state.coreID
	}
	state.attemptScope = state.scope
	state.attemptProvider = attempt.ProviderID
	state.attemptProfile = attempt.AuthProfile
	state.attemptOwnerID = attempt.OwnerID
	state.callbackSubmitted = false
}

func attemptStatus(attempt api.AuthAttempt) string {
	switch attempt.Phase {
	case "starting":
		return "Preparing ChatGPT authorization in Core."
	case "ready":
		return "Open the authorization URL. Enter opens the masked callback field."
	case "waiting_callback":
		return "Waiting for the local callback or a callback URL pasted here."
	case "exchanging", "saving":
		return "Finishing authorization and saving the account in Core."
	case "completed":
		return "ChatGPT account saved in Core."
	case "cancelled":
		return "Authorization was cancelled."
	case "failed":
		return "Authorization did not complete. You can start another attempt."
	default:
		return "Checking ChatGPT authorization in Core."
	}
}

func (m *model) readAuthAttempt(state *authUIState) tea.Cmd {
	if state == nil || state.attemptID == "" || state.pollJobID != 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	state.pollJobID = job.id
	generation, coreID, attemptID := state.generation, state.attemptCoreID, state.attemptID
	m.launch(job, func() tea.Msg {
		attempt, err := m.backend.AuthAttempt(ctx, coreID, attemptID)
		return authAttemptReadMsg{jobID: job.id, generation: generation, coreID: coreID, attemptID: attemptID, attempt: attempt, err: err}
	})
	return nil
}

func (m *model) scheduleAuthAttemptPoll(state *authUIState) tea.Cmd {
	if state == nil || state.attemptID == "" || (state.attempt != nil && state.attempt.Terminal()) {
		return nil
	}
	generation, coreID, attemptID := state.generation, state.attemptCoreID, state.attemptID
	return tea.Tick(750*time.Millisecond, func(time.Time) tea.Msg {
		return authPollTickMsg{generation: generation, coreID: coreID, attemptID: attemptID}
	})
}

func (m *model) acceptAuthAttemptRead(msg authAttemptReadMsg) tea.Cmd {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.attemptCoreID != msg.coreID || state.attemptID != msg.attemptID ||
		state.pollJobID != msg.jobID || !m.authContextCurrent(state) {
		return nil
	}
	state.pollJobID = 0
	if msg.err != nil {
		state.status = "Could not confirm authorization status. Reopen /login to check Core."
		m.status = "authorization status unavailable"
		m.refreshViewport(false)
		return tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
			return authPollTickMsg{generation: msg.generation, coreID: msg.coreID, attemptID: msg.attemptID}
		})
	}
	state.attempt = &msg.attempt
	state.status = attemptStatus(msg.attempt)
	if state.open && state.screen == authOAuthScreen {
		m.status = state.status
	}
	m.refreshViewport(false)
	if msg.attempt.Terminal() {
		return m.finishAuthAttempt(state, msg.attempt)
	}
	return m.scheduleAuthAttemptPoll(state)
}

func (m *model) finishAuthAttempt(state *authUIState, attempt api.AuthAttempt) tea.Cmd {
	if state == nil {
		return nil
	}
	if attempt.AccountSaved && !attempt.SelectionSaved {
		state.status = "Account saved in Core, but selection was not completed. Select the saved account and press Enter to use it."
		m.status = "ChatGPT account saved; selection incomplete"
		state.selectionRecoveryAccountID = attempt.AccountID
		if state.open {
			state.screen = authAccountsScreen
		}
	} else if attempt.Phase == "completed" {
		state.status = "ChatGPT account saved in Core."
		m.status = "ChatGPT account connected"
		if state.open {
			state.screen = authAccountsScreen
		}
	} else if attempt.Phase == "cancelled" {
		state.status = "Authorization was cancelled."
		m.status = state.status
		if state.open {
			state.screen = authAccountsScreen
		}
	} else {
		state.status = "Authorization did not complete. Start another attempt to retry."
		m.status = "ChatGPT authorization failed"
		if state.open {
			state.screen = authAccountsScreen
		}
	}
	state.attempt = &attempt
	m.applyPendingAuthRouteRebase(state)
	if state.open {
		m.refreshViewport(false)
	}
	return m.deleteAuthAttempt(state)
}

func (m *model) deleteAuthAttempt(state *authUIState) tea.Cmd {
	if state == nil || state.attemptID == "" || state.deleteJobID != 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	state.deleteJobID = job.id
	generation, coreID, attemptID := state.generation, state.attemptCoreID, state.attemptID
	m.launch(job, func() tea.Msg {
		err := m.backend.DeleteAuthAttempt(ctx, coreID, attemptID)
		return authAttemptDeletedMsg{jobID: job.id, generation: generation, coreID: coreID, attemptID: attemptID, err: err}
	})
	return nil
}

func (m *model) acceptAuthAttemptDeleted(msg authAttemptDeletedMsg) {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.attemptCoreID != msg.coreID || state.attemptID != msg.attemptID ||
		state.deleteJobID != msg.jobID {
		return
	}
	state.deleteJobID = 0
	if msg.err == nil {
		state.attemptID = ""
		if state.open && state.screen == authAccountsScreen {
			m.loadAuthAccounts(state)
		}
	} else {
		state.status = "Authorization result confirmed; Core retained its attempt summary."
		if state.open {
			m.refreshViewport(false)
		}
	}
	m.applyPendingAuthRouteRebase(state)
}

func (m *model) cancelAuthAttempt(state *authUIState) {
	if state == nil {
		return
	}
	if state.beginPending && state.cancelIntent != nil {
		if !state.cancelRequested {
			state.cancelRequested = true
			close(state.cancelIntent)
		}
		return
	}
	if state.attemptID == "" || state.attempt == nil || !authAttemptCancellable(*state.attempt) || state.attemptJobID != 0 {
		return
	}
	ctx, cancel := m.authControlContext()
	job := m.newJob(func() {})
	job.closeBeforeBackend = true
	state.attemptJobID = job.id
	generation, coreID, attemptID := state.generation, state.attemptCoreID, state.attemptID
	request := api.AuthRequest{CoreID: coreID, AttemptID: attemptID}
	m.launch(job, func() tea.Msg {
		defer cancel()
		attempt, err := m.backend.CancelAuthAttempt(ctx, request)
		return authAttemptCancelledMsg{jobID: job.id, generation: generation, coreID: coreID, attemptID: attemptID, attempt: attempt, err: err}
	})
}

func (m *model) acceptAuthAttemptCancelled(msg authAttemptCancelledMsg) tea.Cmd {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.attemptCoreID != msg.coreID || state.attemptID != msg.attemptID ||
		state.attemptJobID != msg.jobID || !m.authContextCurrent(state) {
		return nil
	}
	state.attemptJobID = 0
	state.beginPending = false
	state.cancelIntent = nil
	state.cancelRequested = false
	if msg.err != nil {
		state.status = "Cancellation result is unknown; Core will retain the attempt for later confirmation."
		m.status = "authorization cancellation result unknown"
		return nil
	}
	state.attempt = &msg.attempt
	state.status = attemptStatus(msg.attempt)
	if msg.attempt.Terminal() {
		return m.finishAuthAttempt(state, msg.attempt)
	}
	return m.readAuthAttempt(state)
}

func (m *model) submitAuthCallback() {
	state := m.authUI
	if state == nil || state.attemptID == "" || strings.TrimSpace(state.secret) == "" {
		if state != nil {
			state.status = "Paste the callback URL before continuing."
			m.refreshViewport(false)
		}
		return
	}
	callback := strings.TrimSpace(state.secret)
	state.secret = ""
	state.cursor = 0
	request := api.AuthRequest{CoreID: state.attemptCoreID, AttemptID: state.attemptID, CallbackURL: callback}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	state.attemptJobID = job.id
	state.callbackSubmitted = true
	generation, coreID, attemptID := state.generation, state.attemptCoreID, state.attemptID
	state.status = "Sending callback to Core."
	m.status = "Completing ChatGPT authorization"
	m.refreshViewport(false)
	m.launch(job, func() tea.Msg {
		attempt, err := m.backend.CompleteAuthAttempt(ctx, request)
		return authAttemptCompletedMsg{jobID: job.id, generation: generation, coreID: coreID, attemptID: attemptID, attempt: attempt, err: err}
	})
}

func (m *model) acceptAuthAttemptCompleted(msg authAttemptCompletedMsg) tea.Cmd {
	state := m.authUI
	if state == nil || state.generation != msg.generation || state.attemptCoreID != msg.coreID || state.attemptID != msg.attemptID ||
		state.attemptJobID != msg.jobID || !m.authContextCurrent(state) {
		return nil
	}
	state.attemptJobID = 0
	if msg.err != nil {
		state.status = "Core could not accept this callback. Check the URL and retry from the authorization screen."
		state.callbackSubmitted = false
		state.screen = authOAuthScreen
		m.status = "callback was not accepted"
		m.refreshViewport(false)
		return m.readAuthAttempt(state)
	}
	state.attempt = &msg.attempt
	state.status = attemptStatus(msg.attempt)
	state.screen = authOAuthScreen
	m.status = state.status
	m.refreshViewport(false)
	if msg.attempt.Terminal() {
		return m.finishAuthAttempt(state, msg.attempt)
	}
	return m.scheduleAuthAttemptPoll(state)
}
