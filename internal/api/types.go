// Package api defines the JSON contract shared by independent product clients.
// It owns no execution, database, credential or terminal implementation.
package api

import (
	"encoding/json"
	"time"

	"hjh/internal/contracts"
)

type Core struct {
	CoreID     string `json:"coreId"`
	APIVersion string `json:"apiVersion"`
	Workspace  string `json:"workspace"`
}

type StartRunRequest struct {
	CoreID      string `json:"coreId"`
	RunID       string `json:"runId"`
	Task        string `json:"task"`
	SessionID   string `json:"sessionId,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Role        string `json:"role,omitempty"`
	ProviderID  string `json:"providerId,omitempty"`
	ModelID     string `json:"modelId,omitempty"`
	Effort      string `json:"effort,omitempty"`
	AuthProfile string `json:"authProfile,omitempty"`
	MaxSteps    *int   `json:"maxSteps,omitempty"`
	FromTurn    *int64 `json:"fromTurn,omitempty"`
}

type RunState struct {
	CoreID          string                    `json:"coreId"`
	RunID           string                    `json:"runId"`
	Seq             uint64                    `json:"seq"`
	Phase           string                    `json:"phase"`
	SessionID       string                    `json:"sessionId,omitempty"`
	ExecutionID     string                    `json:"executionId,omitempty"`
	ActualSelection *contracts.ModelSelection `json:"actualSelection,omitempty"`
	ActualRole      string                    `json:"actualRole,omitempty"`
	Texts           []TextState               `json:"texts"`
	Operations      []OperationState          `json:"operations"`
	Executions      []ExecutionState          `json:"executions"`
	Result          *RunResult                `json:"result,omitempty"`
}

type TextState struct {
	ExecutionID       string `json:"executionId"`
	ParentExecutionID string `json:"parentExecutionId,omitempty"`
	ModelStep         int    `json:"modelStep"`
	Text              string `json:"text"`
}

type OperationState struct {
	ID                string    `json:"id"`
	ExecutionID       string    `json:"executionId"`
	ParentExecutionID string    `json:"parentExecutionId,omitempty"`
	Parent            string    `json:"parent,omitempty"`
	Kind              string    `json:"kind"`
	Name              string    `json:"name"`
	Phase             string    `json:"phase"`
	Time              time.Time `json:"time"`
	ModelStep         int       `json:"modelStep,omitempty"`
	Error             string    `json:"error,omitempty"`
}

type ExecutionState struct {
	ExecutionID       string                    `json:"executionId"`
	ParentExecutionID string                    `json:"parentExecutionId,omitempty"`
	Role              string                    `json:"role,omitempty"`
	Selection         *contracts.ModelSelection `json:"selection,omitempty"`
	Phase             string                    `json:"phase,omitempty"`
	Outcome           string                    `json:"outcome,omitempty"`
}

type RunResult struct {
	OK          bool         `json:"ok"`
	AgentResult *AgentResult `json:"agentResult,omitempty"`
	Error       *Error       `json:"error,omitempty"`
}

type AgentResult struct {
	OK         bool   `json:"ok"`
	Task       string `json:"task"`
	Outcome    string `json:"outcome"`
	StopReason string `json:"stopReason"`
	FinalText  string `json:"finalText,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Error struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	ActiveRunID string `json:"activeRunId,omitempty"`
}

func (e *Error) Error() string { return e.Message }

type ErrorResponse struct {
	Error *Error `json:"error"`
}

type Session struct {
	ID                   string          `json:"id"`
	WorkspaceRoot        string          `json:"workspaceRoot"`
	AgentChoice          json.RawMessage `json:"agentChoice"`
	Agent                string          `json:"agent"`
	CreatedAt            string          `json:"createdAt"`
	UpdatedAt            string          `json:"updatedAt"`
	Title                *string         `json:"title"`
	StateRevision        int64           `json:"stateRevision"`
	NextTurn             int64           `json:"nextTurn"`
	ActiveModel          json.RawMessage `json:"activeModel"`
	MessageCount         int64           `json:"messageCount"`
	ModelChangeCount     int64           `json:"modelChangeCount"`
	TurnCount            int64           `json:"turnCount"`
	PrivateStateFromTurn int64           `json:"privateStateFromTurn"`
}

type Execution struct {
	ID                 string          `json:"id"`
	TaskID             string          `json:"taskId"`
	Task               string          `json:"task"`
	SessionID          string          `json:"sessionId"`
	SessionCorrelation string          `json:"sessionCorrelation"`
	ParentExecutionID  string          `json:"parentExecutionId"`
	SpawnCallID        string          `json:"spawnCallId"`
	Turn               int64           `json:"turn"`
	BaseRevision       int64           `json:"baseRevision"`
	BaseMessageCount   int64           `json:"baseMessageCount"`
	CreatedAt          string          `json:"createdAt"`
	SettledAt          *string         `json:"settledAt"`
	AgentName          string          `json:"agentName"`
	Model              json.RawMessage `json:"model"`
	Build              json.RawMessage `json:"build"`
	ConfigurationID    string          `json:"configurationId"`
	MaxSteps           int64           `json:"maxSteps"`
	Lifecycle          string          `json:"lifecycle"`
	Outcome            string          `json:"outcome"`
	Adoption           string          `json:"adoption"`
	TerminalRecordID   string          `json:"terminalRecordId"`
}

type Message struct {
	ExecutionID string          `json:"executionId"`
	Ordinal     int64           `json:"ordinal"`
	TurnNumber  int64           `json:"turnNumber"`
	JSON        json.RawMessage `json:"json"`
}
type Relation struct {
	Relation       string `json:"relation"`
	TargetRecordID string `json:"targetRecordId"`
	Mandatory      bool   `json:"mandatory"`
	Resolved       bool   `json:"resolved"`
}
type Record struct {
	ID          string          `json:"id"`
	ExecutionID string          `json:"executionId"`
	Ordinal     int64           `json:"ordinal"`
	Kind        string          `json:"kind"`
	ObservedAt  string          `json:"observedAt"`
	Payload     json.RawMessage `json:"payload"`
	Content     []byte          `json:"content"`
	Relations   []Relation      `json:"relations"`
}
type Inspection struct {
	Execution     Execution       `json:"execution"`
	Children      []Execution     `json:"children"`
	Messages      []Message       `json:"messages"`
	Records       []Record        `json:"records"`
	Configuration json.RawMessage `json:"configuration"`
	Context       json.RawMessage `json:"context"`
}
type RoleInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}
type RoleCatalog struct {
	DefaultRole string     `json:"defaultRole"`
	Roles       []RoleInfo `json:"roles"`
}
type ProviderAuth struct {
	Kind    string `json:"kind"`
	Profile string `json:"profile,omitempty"`
}
type ProviderDefinition struct {
	ID            string       `json:"id"`
	DisplayName   string       `json:"displayName"`
	API           string       `json:"api"`
	BaseURL       string       `json:"baseURL"`
	Path          string       `json:"path"`
	Auth          ProviderAuth `json:"auth"`
	DefaultModel  string       `json:"defaultModel"`
	DefaultEffort string       `json:"defaultEffort"`
	Models        []ModelInfo  `json:"models,omitempty"`
}
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type Login struct {
	AuthorizationURL string `json:"authorizationUrl"`
	RedirectURI      string `json:"redirectUri"`
	Workspace        string `json:"workspace"`
	ProviderID       string `json:"providerId"`
	AuthProfile      string `json:"authProfile"`
}
type Status struct {
	Connected        bool      `json:"connected"`
	Identity         string    `json:"identity"`
	Account          string    `json:"account"`
	ExpiresAt        time.Time `json:"expiresAt"`
	PlanUsageEnabled bool      `json:"planUsageEnabled"`
}
