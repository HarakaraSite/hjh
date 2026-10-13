package contracts

import "context"

// InputSelection fixes the next execution's selections at follow-up admission.
// Credentials and extension source bytes are never part of this value.
type InputSelection struct {
	Role      string         `json:"role"`
	Selection ModelSelection `json:"selection"`
	MaxSteps  int            `json:"maxSteps"`
	FromTurn  int64          `json:"fromTurn"`
}

// InputReceipt is the latest persisted state of one runtime input. Text is
// populated only for an explicit detail read and is not stored in result JSON.
type InputReceipt struct {
	SchemaVersion         int             `json:"schemaVersion"`
	CoreID                string          `json:"coreId"`
	SessionID             string          `json:"sessionId"`
	RunID                 string          `json:"runId"`
	ExecutionID           string          `json:"executionId"`
	InputID               string          `json:"inputId"`
	Kind                  string          `json:"kind"`
	Sequence              uint64          `json:"sequence"`
	Revision              uint64          `json:"revision"`
	Status                string          `json:"status"`
	ContentDigest         string          `json:"contentDigest"` // Steering: accepted-message checksum; follow-up: stored task reference.
	AcceptedAt            string          `json:"acceptedAt"`
	UpdatedAt             string          `json:"updatedAt"`
	Next                  *InputSelection `json:"next,omitempty"`
	StartedRunID          string          `json:"startedRunId,omitempty"`
	StartedExecutionID    string          `json:"startedExecutionId,omitempty"`
	AppliedMessageOrdinal *int64          `json:"appliedMessageOrdinal,omitempty"`
	AppliedSequence       uint64          `json:"appliedSequence,omitempty"`
	Reason                string          `json:"reason,omitempty"`
	Text                  string          `json:"text,omitempty"`
	ManagedByThisCore     bool            `json:"managedByThisCore"`
}

type InputReceiptPage struct {
	CoreID      string         `json:"coreId"`
	SessionID   string         `json:"sessionId"`
	Revision    uint64         `json:"revision"`
	ActiveRunID string         `json:"activeRunId,omitempty"`
	Receipts    []InputReceipt `json:"receipts"`
}

// SteeringInput is a locator passed from the Core input owner to the app.
// The app reads the receipt's accepted Message and saves an execution message
// independently when applying the instruction.
type SteeringInput struct {
	Receipt InputReceipt
}

type SteeringSource interface {
	// Pending closes admission atomically if final and no accepted/processing
	// input remains. Waiting for persistence happens outside the owner lock.
	Pending(context.Context, bool) ([]SteeringInput, error)
	Applied(SteeringInput, uint64)
	Close(string) error
}
