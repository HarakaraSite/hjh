package contracts

import "encoding/json"

// RecallReference holds only the prepared source identity, never its body.
type RecallReference struct {
	CoreID            string `json:"coreId"`
	OperationID       string `json:"operationId"`
	SourceExecutionID string `json:"sourceExecutionId"`
	Generation        uint64 `json:"generation"`
	RecordID          string `json:"recordId,omitempty"`
}

type ExecutionAdmission struct {
	ExecutionID      string
	RecallRecordID   string
	RecallGeneration uint64
}

type RecallMarker struct {
	SourceExecutionID string `json:"sourceExecutionId"`
	RelationRecordID  string `json:"relationRecordId"`
	OperationID       string `json:"operationId"`
	Generation        uint64 `json:"generation"`
}

type RecallOperationRequest struct {
	CoreID      string `json:"coreId"`
	OperationID string `json:"operationId"`
	Action      string `json:"action"`
	Selector    string `json:"selector,omitempty"`
}

type RecallReceipt struct {
	SchemaVersion     int    `json:"schemaVersion"`
	CoreID            string `json:"coreId"`
	SessionID         string `json:"sessionId"`
	OperationID       string `json:"operationId"`
	Action            string `json:"action"`
	Selector          string `json:"selector,omitempty"`
	SourceExecutionID string `json:"sourceExecutionId,omitempty"`
	Generation        uint64 `json:"generation"`
	CompletedAt       string `json:"completedAt"`
	Result            string `json:"result"`
}

type RecallOperationResult struct {
	Receipt           RecallReceipt `json:"receipt"`
	ManagedByThisCore bool          `json:"managedByThisCore"`
}

type PendingRecall struct {
	Generation        uint64 `json:"generation"`
	OperationID       string `json:"operationId"`
	SourceExecutionID string `json:"sourceExecutionId"`
	SourceOutcome     string `json:"sourceOutcome"`
	PreparedAt        string `json:"preparedAt"`
	ClaimedByRunID    string `json:"claimedByRunId,omitempty"`
}

type CanonicalContext struct {
	StateRevision int64          `json:"stateRevision"`
	TurnCount     int64          `json:"turnCount"`
	MessageCount  int64          `json:"messageCount"`
	ActiveModel   ModelSelection `json:"activeModel"`
	AgentName     string         `json:"agentName"`
}

type ContextExecution struct {
	ExecutionID string `json:"executionId"`
	Lifecycle   string `json:"lifecycle"`
	Outcome     string `json:"outcome"`
}

type ContextRequest struct {
	ExecutionID       string         `json:"executionId"`
	RecordOrdinal     int64          `json:"recordOrdinal"`
	RequestOrdinal    int            `json:"requestOrdinal"`
	ModelStep         int            `json:"modelStep"`
	Selection         ModelSelection `json:"selection"`
	ItemCount         int            `json:"itemCount"`
	MessageCount      int            `json:"messageCount"`
	ToolContractCount int            `json:"toolContractCount"`
	InstructionCount  int            `json:"instructionCount"`
	WireBytes         int64          `json:"wireBytes"`
}

type RecallAdmission struct {
	SourceExecutionID       string `json:"sourceExecutionId"`
	TargetExecutionID       string `json:"targetExecutionId"`
	RecordID                string `json:"recordId"`
	CreatedAt               string `json:"createdAt"`
	ReferenceMessageOrdinal *int64 `json:"referenceMessageOrdinal,omitempty"`
	RequestRecordOrdinal    *int64 `json:"requestRecordOrdinal,omitempty"`
}

type SessionContext struct {
	CoreID                string               `json:"coreId"`
	SessionID             string               `json:"sessionId"`
	Canonical             CanonicalContext     `json:"canonical"`
	ActiveRunID           string               `json:"activeRunId,omitempty"`
	LatestRootExecution   *ContextExecution    `json:"latestRootExecution,omitempty"`
	LatestRequest         *ContextRequest      `json:"latestRequest,omitempty"`
	LatestBudget          *ContextBudgetStatus `json:"latestBudget,omitempty"`
	RecallRevision        uint64               `json:"recallRevision"`
	PendingRecall         *PendingRecall       `json:"pendingRecall,omitempty"`
	LatestRecallAdmission *RecallAdmission     `json:"latestRecallAdmission,omitempty"`
}

// ContextBudgetStatus describes Core's latest input selection, including a
// selection that exceeded budget before a provider request was sent.
type ContextBudgetStatus struct {
	ExecutionID     string               `json:"executionId"`
	RecordOrdinal   int64                `json:"recordOrdinal"`
	ModelStep       int                  `json:"modelStep"`
	Selection       ModelSelection       `json:"modelSelection"`
	Summary         ContextBudgetSummary `json:"summary"`
	DetailAvailable bool                 `json:"detailAvailable"`
	Fact            json.RawMessage      `json:"fact"`
	MetadataError   string               `json:"metadataError,omitempty"`
	Exceeded        bool                 `json:"exceeded"`
}

// ContextBudgetSummary is the per-call scalar history retained for every
// budget decision. Selection lists and per-turn history details belong only
// to the latest execution budget snapshot.
type ContextBudgetSummary struct {
	Budget                     ContextBudgetValues `json:"budget"`
	Estimator                  string              `json:"estimator"`
	WireBytes                  int64               `json:"wireBytes"`
	EstimatedTokens            int64               `json:"estimatedTokens"`
	HistoryWireBytes           *int64              `json:"historyWireBytes,omitempty"`
	HistoryEstimatedTokens     *int64              `json:"historyEstimatedTokens,omitempty"`
	SelectedMessageCount       int                 `json:"selectedMessageCount"`
	OmittedCurrentMessageCount int                 `json:"omittedCurrentMessageCount"`
	HistoryStatus              string              `json:"historyStatus"`
	HistoryReason              string              `json:"historyReason,omitempty"`
}

// ContextBudgetValues retains the scalar effective budget and field origins.
type ContextBudgetValues struct {
	ContextTokens *int64            `json:"contextTokens,omitempty"`
	InputTokens   *int64            `json:"inputTokens,omitempty"`
	OutputReserve *int64            `json:"outputReserve,omitempty"`
	InputRatio    *float64          `json:"inputRatio"`
	HistoryTokens *int64            `json:"historyTokens,omitempty"`
	Limit         *int64            `json:"limit,omitempty"`
	Sources       map[string]string `json:"sources"`
}
