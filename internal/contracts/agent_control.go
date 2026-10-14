package contracts

import "context"

type AgentQuery struct {
	ExecutionID string `json:"executionId"`
	After       *int64 `json:"after,omitempty"`
	CallID      string `json:"-"`
}

// AgentSteerRequest carries one parent-authored instruction to an active child.
// CallID identifies the parent steer operation and is omitted from tool input.
type AgentSteerRequest struct {
	ExecutionID string `json:"executionId"`
	Text        string `json:"text"`
	CallID      string `json:"-"`
}

// ChildInstructionReceipt is the latest persisted state of one parent-to-child
// instruction. Its text is stored once as immutable Message content.
type ChildInstructionReceipt struct {
	InstructionID         string `json:"instructionId"`
	ExecutionID           string `json:"executionId"`
	ParentExecutionID     string `json:"parentExecutionId"`
	CallID                string `json:"steerCallId"`
	Sequence              uint64 `json:"sequence"`
	Status                string `json:"status"`
	ContentDigest         string `json:"contentDigest"`
	AcceptedAt            string `json:"acceptedAt"`
	UpdatedAt             string `json:"updatedAt"`
	AppliedSequence       uint64 `json:"appliedSequence,omitempty"`
	AppliedMessageOrdinal *int64 `json:"appliedMessageOrdinal,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

type AgentReport struct {
	ExecutionID    string `json:"executionId"`
	MessageOrdinal int64  `json:"messageOrdinal"`
	Text           string `json:"text"`
}

type AgentSpawnResult struct {
	ExecutionID string `json:"executionId"`
	State       string `json:"state"`
}

type AgentToolProgress struct {
	Name    string `json:"name"`
	CallID  string `json:"callId"`
	State   string `json:"state"`
	Outcome string `json:"outcome,omitempty"`
}

type AgentProgress struct {
	Phase     string             `json:"phase"`
	UpdatedAt string             `json:"updatedAt"`
	ModelStep int                `json:"modelStep,omitempty"`
	LastTool  *AgentToolProgress `json:"lastTool,omitempty"`
}

type AgentStatus struct {
	ExecutionID  string                    `json:"executionId"`
	State        string                    `json:"state"`
	Role         string                    `json:"role"`
	Model        ModelSelection            `json:"model"`
	Tools        []string                  `json:"tools"`
	Progress     AgentProgress             `json:"progress"`
	Reports      []AgentReport             `json:"reports"`
	NextCursor   int64                     `json:"nextCursor"`
	Instructions []ChildInstructionReceipt `json:"instructions"`
	SnapshotSave SnapshotStatus            `json:"snapshotSave"`
}

// AgentController owns children for one parent execution, independently of a VM call.
type AgentController interface {
	SpawnAgent(context.Context, AgentRunRequest) (AgentSpawnResult, error)
	StatusAgent(context.Context, AgentQuery) (AgentStatus, error)
	SteerAgent(context.Context, AgentSteerRequest) (ChildInstructionReceipt, error)
	CollectAgent(context.Context, AgentQuery) (AgentRunResult, error)
	CancelAgent(context.Context, AgentQuery) (AgentRunResult, error)
}
