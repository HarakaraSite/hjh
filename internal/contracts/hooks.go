package contracts

import "encoding/json"

// HookRuntime describes the admitted Worker, never transport credentials.
type HookRuntime struct {
	WorkspaceRoot     string         `json:"workspaceRoot"`
	AgentName         string         `json:"agentName"`
	Role              string         `json:"role"`
	SessionID         string         `json:"sessionId"`
	ExecutionID       string         `json:"executionId"`
	ParentExecutionID string         `json:"parentExecutionId,omitempty"`
	SpawnCallID       string         `json:"spawnCallId,omitempty"`
	TurnNumber        int64          `json:"turnNumber"`
	Model             ModelSelection `json:"model"`
	StartedAt         string         `json:"startedAt"`
	TimeZone          string         `json:"timeZone"`
}

// HookContext describes instructions and saved conversation locators without
// copying the whole canonical transcript into every lifecycle invocation.
type HookContext struct {
	SystemInstruction     string            `json:"systemInstruction"`
	InstructionComponents []json.RawMessage `json:"instructionComponents"`
	Tools                 []ToolDefinition  `json:"tools"`
	History               HookHistory       `json:"history"`
}

type HookHistory struct {
	SessionID         string   `json:"sessionId"`
	FromTurn          int64    `json:"fromTurn"`
	NextTurn          int64    `json:"nextTurn"`
	MessageCount      int64    `json:"messageCount"`
	ExecutionLocators []string `json:"executionLocators"`
}

type HookSettlement struct {
	Outcome          string `json:"outcome"`
	Adoption         string `json:"adoption"`
	TerminalRecordID string `json:"terminalRecordId"`
	AdoptionError    string `json:"adoptionError,omitempty"`
}

type LifecycleHookInput struct {
	Runtime    HookRuntime     `json:"runtime"`
	Context    HookContext     `json:"context"`
	Task       *string         `json:"task,omitempty"`
	Outcome    json.RawMessage `json:"outcome,omitempty"`
	Settlement *HookSettlement `json:"settlement,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

type HookContextAddition struct {
	Context []string `json:"context"`
}
