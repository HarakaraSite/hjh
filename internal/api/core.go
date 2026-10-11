package api

// CoreStatus reports Core-owned runtime metadata without loading conversations.
type CoreStatus struct {
	Core
	Phase      string          `json:"phase"`
	ActiveRuns []CoreActiveRun `json:"activeRuns"`
}

type CoreActiveRun struct {
	RunID       string `json:"runId"`
	SessionID   string `json:"sessionId"`
	ExecutionID string `json:"executionId,omitempty"`
	Phase       string `json:"phase"`
}
