package corerun

import (
	"sort"

	"hjh/internal/api"
)

// Status includes preparing runs even before their Session has been saved.
func (m *Manager) Status() api.CoreStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := api.CoreStatus{Core: m.core, Phase: "running", ActiveRuns: []api.CoreActiveRun{}}
	if m.closing {
		status.Phase = "stopping"
	}
	for _, run := range m.runs {
		if run.state.Phase == "terminal" {
			continue
		}
		status.ActiveRuns = append(status.ActiveRuns, api.CoreActiveRun{RunID: run.state.RunID,
			SessionID: run.sessionReservation, ExecutionID: run.state.ExecutionID, Phase: run.state.Phase})
	}
	sort.Slice(status.ActiveRuns, func(i, j int) bool { return status.ActiveRuns[i].RunID < status.ActiveRuns[j].RunID })
	return status
}
