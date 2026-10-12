// Package api defines the JSON contract shared by independent product clients.
// It owns no execution, database, credential or terminal implementation.
package api

import (
	"encoding/json"
	"sync"
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
	ActualContext   *contracts.Orientation    `json:"actualContext,omitempty"`
	NextRunID       string                    `json:"nextRunId,omitempty"`
	InputRevision   uint64                    `json:"inputRevision"`
	InputAcceptance InputAcceptance           `json:"inputAcceptance"`
	Result          *RunResult                `json:"result,omitempty"`
}

// RunSnapshot and RunUpdate use the same wire key for different conversation
// shapes. The SSE event name determines which shape the client decodes.
type RunSnapshot struct {
	RunState
	Conversation ConversationSnapshot `json:"conversation"`
}

type RunUpdate struct {
	RunState
	Conversation *ConversationUpdate `json:"conversation,omitempty"`
}

type ConversationSnapshot struct {
	ScopeID         string                        `json:"scopeId"`
	ViewID          string                        `json:"viewId"`
	Revision        uint64                        `json:"revision"`
	ThroughSequence *uint64                       `json:"throughSequence"`
	RootCuts        []ConversationCut             `json:"rootCuts,omitempty"`
	Order           []string                      `json:"order"`
	Entities        map[string]ConversationEntity `json:"entities"`
	serialized      *serializedJSON
}

// ShareConversationSnapshot enables immutable consumers of one view revision
// to reuse its JSON encoding. Callers must not mutate the returned maps/slices.
func ShareConversationSnapshot(snapshot ConversationSnapshot) ConversationSnapshot {
	if snapshot.serialized == nil {
		snapshot.serialized = &serializedJSON{}
	}
	return snapshot
}

func (snapshot ConversationSnapshot) MarshalJSON() ([]byte, error) {
	type wire ConversationSnapshot
	if snapshot.serialized == nil {
		return json.Marshal(wire(snapshot))
	}
	return snapshot.serialized.marshal(func() ([]byte, error) { return json.Marshal(wire(snapshot)) })
}

type ConversationCut struct {
	RootExecutionID      string                      `json:"rootExecutionId"`
	RootExecutionOrdinal int64                       `json:"rootExecutionOrdinal"`
	ThroughSequence      *uint64                     `json:"throughSequence"`
	Executions           []ConversationExecutionHead `json:"executions"`
}

// ConversationExecutionHead describes the immutable upper bounds included in
// a fixed conversation cut. Ordinals are nullable in meaning through their
// sentinel values: semanticOrdinal=0 means no semantic records, and
// messageOrdinal=-1 means no messages.
type ConversationExecutionHead struct {
	ExecutionID       string `json:"executionId"`
	ParentExecutionID string `json:"parentExecutionId,omitempty"`
	SemanticOrdinal   int64  `json:"semanticOrdinal"`
	MessageOrdinal    int64  `json:"messageOrdinal"`
}

type ConversationPosition struct {
	RootExecutionOrdinal int64  `json:"rootExecutionOrdinal"`
	FirstSequence        uint64 `json:"firstSequence"`
	ItemIndex            int    `json:"itemIndex"`
}

type ConversationEntity struct {
	ID                string                     `json:"id"`
	Kind              string                     `json:"kind"`
	ExecutionID       string                     `json:"executionId"`
	ParentExecutionID string                     `json:"parentExecutionId,omitempty"`
	ParentEntityID    string                     `json:"parentEntityId,omitempty"`
	Position          ConversationPosition       `json:"position"`
	Text              string                     `json:"text,omitempty"`
	Status            string                     `json:"status"`
	Name              string                     `json:"name,omitempty"`
	Outcome           string                     `json:"outcome,omitempty"`
	Error             string                     `json:"error,omitempty"`
	ThinkingKind      string                     `json:"thinkingKind,omitempty"`
	Detail            *ConversationDetailLocator `json:"detail,omitempty"`
	Preview           string                     `json:"preview,omitempty"`
}

type ConversationDetailLocator struct {
	ExecutionID string `json:"executionId"`
	RecordID    string `json:"recordId"`
	Ordinal     int64  `json:"ordinal"`
	CallID      string `json:"callId,omitempty"`
}

type ConversationUpdate struct {
	ScopeID         string               `json:"scopeId"`
	ViewID          string               `json:"viewId"`
	BaseRevision    uint64               `json:"baseRevision"`
	Revision        uint64               `json:"revision"`
	ThroughSequence *uint64              `json:"throughSequence"`
	Upserts         []ConversationEntity `json:"upserts"`
	serialized      *serializedJSON
}

// ShareConversationUpdate enables immutable consumers of one update frame to
// reuse its JSON encoding. Callers must not mutate the returned upserts.
func ShareConversationUpdate(update ConversationUpdate) ConversationUpdate {
	if update.serialized == nil {
		update.serialized = &serializedJSON{}
	}
	return update
}

func (update ConversationUpdate) MarshalJSON() ([]byte, error) {
	type wire ConversationUpdate
	if update.serialized == nil {
		return json.Marshal(wire(update))
	}
	return update.serialized.marshal(func() ([]byte, error) { return json.Marshal(wire(update)) })
}

type serializedJSON struct {
	once sync.Once
	data []byte
	err  error
}

func (cache *serializedJSON) marshal(build func() ([]byte, error)) ([]byte, error) {
	cache.once.Do(func() { cache.data, cache.err = build() })
	return cache.data, cache.err
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
	ActiveRunID          string          `json:"activeRunId,omitempty"`
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
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Efforts      []string `json:"efforts,omitempty"`
	EffortSource string   `json:"effortSource,omitempty"`
}

// SelectionRequest contains next-run inputs without starting or reserving a run.
type SelectionRequest struct {
	SessionID   string `json:"sessionId,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	Role        string `json:"role,omitempty"`
	ProviderID  string `json:"providerId,omitempty"`
	ModelID     string `json:"modelId,omitempty"`
	Effort      string `json:"effort,omitempty"`
	AuthProfile string `json:"authProfile,omitempty"`
}

type SelectionPreview struct {
	Role      string                   `json:"role"`
	Selection contracts.ModelSelection `json:"selection"`
}
type Status struct {
	Connected        bool      `json:"connected"`
	Identity         string    `json:"identity"`
	Account          string    `json:"account"`
	ExpiresAt        time.Time `json:"expiresAt"`
	PlanUsageEnabled bool      `json:"planUsageEnabled"`
}
