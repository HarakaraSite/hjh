package api

import "hjh/internal/contracts"

type NextRunOptions struct {
	Role        string `json:"role,omitempty"`
	ProviderID  string `json:"providerId,omitempty"`
	ModelID     string `json:"modelId,omitempty"`
	Effort      string `json:"effort,omitempty"`
	AuthProfile string `json:"authProfile,omitempty"`
	MaxSteps    *int   `json:"maxSteps,omitempty"`
	FromTurn    *int64 `json:"fromTurn,omitempty"`
}

type SubmitInputRequest struct {
	CoreID    string          `json:"coreId"`
	SessionID string          `json:"sessionId"`
	InputID   string          `json:"inputId"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text"`
	Next      *NextRunOptions `json:"next,omitempty"`
}

type InputReceipt = contracts.InputReceipt
type InputReceiptPage = contracts.InputReceiptPage

type InputAcceptance struct {
	Steering bool `json:"steering"`
	FollowUp bool `json:"followUp"`
}

type InputChainState struct {
	CoreID        string `json:"coreId"`
	SessionID     string `json:"sessionId"`
	AnchorRunID   string `json:"anchorRunId"`
	Phase         string `json:"phase"`
	ActiveRunID   string `json:"activeRunId,omitempty"`
	PendingWrites int    `json:"pendingWrites"`
	Revision      uint64 `json:"revision"`
}
