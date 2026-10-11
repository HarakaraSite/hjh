package corerun

import (
	"hjh/internal/api"
)

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
