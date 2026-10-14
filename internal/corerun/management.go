package corerun

import (
	"context"
	"errors"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

// SaveSnapshot retries only a frozen failed snapshot. The Session write
// reservation keeps deletion and Core shutdown ordered around the DB update.
func (m *Manager) SaveSnapshot(ctx context.Context, coreID, executionID string) (contracts.SnapshotStatus, error) {
	m.mu.Lock()
	if err := m.coreError(coreID); err != nil {
		m.mu.Unlock()
		return contracts.SnapshotStatus{}, err
	}
	if m.closing {
		m.mu.Unlock()
		return contracts.SnapshotStatus{}, &api.Error{Code: "core_closing", Message: "Core is shutting down"}
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()

	sessionID, err := m.service.SnapshotSessionID(ctx, executionID)
	if err != nil {
		return contracts.SnapshotStatus{}, err
	}
	m.mu.Lock()
	if m.deletingSessions[sessionID] {
		m.mu.Unlock()
		return contracts.SnapshotStatus{}, sessionBusy("", "Session deletion is in progress; retry after it completes")
	}
	if sessionID != "" {
		m.sessionWrites[sessionID]++
	}
	m.mu.Unlock()
	if sessionID != "" {
		defer m.finishSessionWrite(sessionID)
	}
	return m.service.SaveSnapshot(ctx, executionID)
}

// ActiveSessionRun returns the lightweight nonterminal run state reserved for
// a persisted Session. It never resolves terminal output or reads history.
func (m *Manager) ActiveSessionRun(coreID, sessionID string) (*api.RunState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.coreError(coreID); err != nil {
		return nil, err
	}
	runID := m.sessions[sessionID]
	if runID == "" {
		return nil, nil
	}
	r := m.runs[runID]
	if r == nil || r.state.Phase == "terminal" || r.state.SessionID != sessionID {
		return nil, nil
	}
	state := m.stateLocked(r)
	return &state, nil
}

// DeleteSession reserves an idle Session against new runs and input replays
// while history removes it. An active run owns this reservation through its
// follow-up saves, so deletion never cancels or races that work.
func (m *Manager) DeleteSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return &api.Error{Code: "core_closing", Message: "Core is shutting down"}
	}
	if m.deletingSessions[sessionID] {
		m.mu.Unlock()
		return sessionBusy("", "Session deletion is already in progress; retry after it completes")
	}
	if runID := m.sessions[sessionID]; runID != "" {
		m.mu.Unlock()
		return sessionBusy(runID, "Session is running. Stop the active run and its follow-up chain before deleting it")
	}
	if m.recallBySession[sessionID] != nil {
		m.mu.Unlock()
		return sessionBusy("", "A Session recall save is in progress; retry after it completes")
	}
	if m.sessionWrites[sessionID] != 0 {
		m.mu.Unlock()
		return sessionBusy("", "A Session input save is in progress; retry after it completes")
	}
	m.deletingSessions[sessionID] = true
	m.wg.Add(1)
	// Invalidate Session view restorations that captured the old state before
	// deletion acquired the reservation.
	m.sessionVersions[sessionID]++
	m.mu.Unlock()
	defer m.wg.Done()

	err := m.service.DeleteSession(ctx, sessionID)

	var runsToRelease []*managedRun
	m.mu.Lock()
	delete(m.deletingSessions, sessionID)
	if err == nil || errors.Is(err, history.ErrNotFound) {
		delete(m.sessions, sessionID)
		delete(m.sessionWrites, sessionID)
		delete(m.inputSequences, sessionID)
		delete(m.recallPending, sessionID)
		delete(m.recallRevision, sessionID)
		delete(m.recallBySession, sessionID)
		if flight := m.sessionViews[sessionID]; flight != nil {
			flight.invalidated = true
			delete(m.sessionViews, sessionID)
		}
		for runID, run := range m.runs {
			if run.sessionReservation != sessionID {
				continue
			}
			delete(m.runs, runID)
			run.terminal = nil
			run.state = api.RunState{CoreID: m.core.CoreID, RunID: runID, Phase: "terminal"}
			for _, notify := range run.subscribers {
				select {
				case notify <- struct{}{}:
				default:
				}
			}
			run.subscribers = map[*runStateSubscriber]chan struct{}{}
			runsToRelease = append(runsToRelease, run)
		}
		m.sessionVersions[sessionID]++
	}
	m.mu.Unlock()
	for _, run := range runsToRelease {
		m.releaseView(run)
	}
	return err
}

func (m *Manager) finishSessionWrite(sessionID string) {
	m.mu.Lock()
	if m.sessionWrites[sessionID] <= 1 {
		delete(m.sessionWrites, sessionID)
	} else {
		m.sessionWrites[sessionID]--
	}
	m.mu.Unlock()
}

// BeginShutdown closes run admission and cancels active owners without
// joining them. The caller can acknowledge shutdown before waiting in Close.
func (m *Manager) BeginShutdown(coreID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.coreError(coreID); err != nil {
		return err
	}
	m.beginShutdownLocked()
	return nil
}

func (m *Manager) beginShutdown() {
	m.mu.Lock()
	m.beginShutdownLocked()
	m.mu.Unlock()
}

func (m *Manager) beginShutdownLocked() {
	m.closing = true
	chains := map[*inputChain]bool{}
	for _, r := range m.runs {
		if !chains[r.chain] {
			r.chain.mu.Lock()
			if r.chain.phase != "settled" {
				r.chain.cancelled = true
				r.chain.cancelReason = "core_stopped"
				r.chain.phase = "cancelling"
				r.chain.steeringOpen = false
				r.chain.followUpOpen = false
				r.chain.signalLocked()
			}
			r.chain.mu.Unlock()
			chains[r.chain] = true
		}
		if r.state.Phase != "terminal" {
			r.cancel()
			if r.state.Phase != "cancelling" || r.state.InputAcceptance != (api.InputAcceptance{}) {
				r.state.Phase = "cancelling"
				r.state.InputAcceptance = api.InputAcceptance{}
				m.changedLocked(r)
			}
		}
	}
	// Publish each chain's stop reason before cancelling the shared context;
	// app finalization may immediately close pending steering inputs.
	m.cancel()
}
