// Package contracts contains values shared by execution, provider, runtime, and history.
// It owns neither a VM nor a database connection.
package contracts

import (
	"context"
	"encoding/json"
	"time"
)

type TextContent struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type ToolCall struct {
	Kind      string          `json:"kind"`
	CallID    string          `json:"callId"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	Kind     string          `json:"kind"`
	CallID   string          `json:"callId"`
	Name     string          `json:"name"`
	Text     string          `json:"text"`
	Outcome  string          `json:"outcome"`
	Failure  json.RawMessage `json:"failure,omitempty"`
	Terminal string          `json:"terminal,omitempty"`
}

type Reasoning struct {
	Field string `json:"field"`
	Text  string `json:"text"`
}

type ProviderState struct {
	Provider         string            `json:"provider"`
	Model            string            `json:"model,omitempty"`
	Reasoning        *Reasoning        `json:"reasoning,omitempty"`
	ReasoningDetails []json.RawMessage `json:"reasoningDetails,omitempty"`
	ReplayItems      []json.RawMessage `json:"replayItems,omitempty"`
}

type Message struct {
	Role          string          `json:"role"`
	Content       json.RawMessage `json:"content"`
	Text          *string         `json:"text,omitempty"`
	ProviderState *ProviderState  `json:"providerState,omitempty"`
	Steering      *bool           `json:"steering,omitempty"`
	ExecutionFact *ExecutionFact  `json:"-"`
}

func User(text string) Message {
	return Message{Role: "user", Content: JSON(TextContent{Kind: "text", Text: text})}
}
func Assistant(text string, state *ProviderState) Message {
	return Message{Role: "assistant", Content: JSON(TextContent{Kind: "text", Text: text}), ProviderState: state}
}
func AssistantCalls(calls []ToolCall, text string, state *ProviderState) Message {
	for i := range calls {
		calls[i].Kind = "tool_call"
	}
	m := Message{Role: "assistant", Content: JSON(calls), ProviderState: state}
	if text != "" {
		m.Text = &text
	}
	return m
}
func ToolResults(results []ToolResult) Message {
	for i := range results {
		results[i].Kind = "tool_result"
	}
	return Message{Role: "tool", Content: JSON(results)}
}
func (m Message) VisibleText() string {
	if m.Text != nil {
		return *m.Text
	}
	var c TextContent
	if json.Unmarshal(m.Content, &c) == nil {
		return c.Text
	}
	return ""
}

// JSON is for already serializable contract values. Storage APIs return encoding errors.
func JSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type ModelSelection struct {
	Provider    string `json:"provider"`
	API         string `json:"api"`
	AuthProfile string `json:"authProfile"`
	ModelID     string `json:"modelId"`
	Effort      string `json:"effort"`
}

func OpenRouterModel(id string) ModelSelection {
	return ModelSelection{Provider: "openrouter-chat", API: "openrouter-chat-completions", AuthProfile: "openrouter-api-key", ModelID: id, Effort: "auto"}
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type ModelRequest struct {
	Model       ModelSelection
	Instruction string
	Messages    []Message
	Tools       []ToolDefinition
	WireBody    []byte `json:"-"`
}
type ModelResult struct {
	Text              string
	FinishReason      string
	Calls             []ToolCall
	ProviderState     *ProviderState
	Usage             json.RawMessage
	PhysicalRequestID string
	Thinking          []ThinkingItem
}

// Observation is a neutral execution fact. Its sink decides storage and display.
type Observation struct {
	ExecutionID       string          `json:"executionId,omitempty"`
	ParentExecutionID string          `json:"parentExecutionId,omitempty"`
	ID                string          `json:"id"`
	Parent            string          `json:"parent,omitempty"`
	Kind              string          `json:"kind"`
	Name              string          `json:"name"`
	Phase             string          `json:"phase"`
	Preview           string          `json:"preview,omitempty"`
	Time              time.Time       `json:"time"`
	Input             json.RawMessage `json:"input,omitempty"`
	Result            json.RawMessage `json:"result,omitempty"`
	Error             string          `json:"error,omitempty"`
	ModelStep         int             `json:"modelStep,omitempty"`
	// Text and Thinking are typed Go-side values. The provider must not wrap
	// cumulative body text in Result JSON and make the history path parse it.
	Text              string         `json:"-"`
	Thinking          []ThinkingItem `json:"-"`
	PhysicalRequestID string         `json:"-"`
}
type Observer func(Observation) error

// RequestIdentity links provider output to the model request which produced it.
type RequestIdentity struct {
	ExecutionID       string `json:"executionId"`
	Lane              string `json:"lane"`
	RequestOrdinal    int    `json:"requestOrdinal"`
	ModelStep         int    `json:"modelStep"`
	PhysicalRequestID string `json:"physicalRequestId,omitempty"`
}

// ConversationMetadata is stored in a semantic payload and is the durable
// source for live and replay identity and ordering.
type ConversationMetadata struct {
	RootExecutionID string `json:"rootExecutionId"`
	Sequence        uint64 `json:"sequence"`
	EntityKind      string `json:"entityKind"`
	LocalKey        string `json:"localKey"`
	FirstSequence   uint64 `json:"firstSequence"`
	ItemIndex       int    `json:"itemIndex,omitempty"`
}

// ExecutionLocator points to a saved source or a latest-state row without
// copying private tool or provider detail into a conversation snapshot.
type ExecutionLocator struct {
	ExecutionID    string `json:"executionId"`
	RecordID       string `json:"recordId,omitempty"`
	Ordinal        int64  `json:"ordinal,omitempty"`
	MessageOrdinal int64  `json:"messageOrdinal,omitempty"`
	ItemIndex      int    `json:"itemIndex,omitempty"`
	Lane           string `json:"lane,omitempty"`
	ModelStep      int    `json:"modelStep,omitempty"`
}

// ThinkingItem is provider-returned, readable thinking text. Opaque replay
// state is intentionally represented only by ProviderState.
type ThinkingItem struct {
	Key  string `json:"key"`
	Text string `json:"text"`
	Kind string `json:"kind,omitempty"`
}

// ExecutionFact is the typed, lightweight fact shared by storage, Core, and
// consumers. Payloads and private details stay in the persistence closure.
type ExecutionFact struct {
	RootExecutionID      string               `json:"rootExecutionId"`
	ExecutionID          string               `json:"executionId"`
	ParentExecutionID    string               `json:"parentExecutionId,omitempty"`
	RootExecutionOrdinal int64                `json:"rootExecutionOrdinal,omitempty"`
	Kind                 string               `json:"kind"`
	Phase                string               `json:"phase,omitempty"`
	Role                 string               `json:"role,omitempty"`
	Text                 string               `json:"text,omitempty"`
	Name                 string               `json:"name,omitempty"`
	Preview              string               `json:"preview,omitempty"`
	CallID               string               `json:"callId,omitempty"`
	ParentCallID         string               `json:"parentCallId,omitempty"`
	Outcome              string               `json:"outcome,omitempty"`
	Error                string               `json:"error,omitempty"`
	Request              RequestIdentity      `json:"request"`
	Thinking             []ThinkingItem       `json:"thinking,omitempty"`
	ThinkingKind         string               `json:"thinkingKind,omitempty"`
	Conversation         ConversationMetadata `json:"conversation"`
	Locator              ExecutionLocator     `json:"locator"`
	Recall               *RecallMarker        `json:"recall,omitempty"`
}

// ExecutionCommit contains facts published only after the corresponding
// storage transaction succeeds. SequenceCount advances the shared root prefix.
type ExecutionCommit struct {
	Facts           []ExecutionFact
	SequenceCount   uint64
	RootExecutionID string
	ThroughSequence uint64
}

type ExecutionDataCommit func(sequence uint64) (ExecutionCommit, error)

// ExecutionDataOwner is the synchronous root/child write gate. It assigns a
// candidate sequence, calls commit while holding the gate, and exposes returned
// facts only after the persistence transaction has succeeded.

// AgentRunner is injected into a Worker; it owns no VM values.
type AgentRunRequest struct {
	Role   string `json:"role,omitempty"`
	Task   string `json:"task"`
	CallID string `json:"-"`
}
type AgentRunResult struct {
	ExecutionID string `json:"executionId"`
	Outcome     string `json:"outcome"`
	FinalText   string `json:"finalText,omitempty"`
	Error       string `json:"error,omitempty"`
}
type AgentRunner interface {
	RunAgent(context.Context, AgentRunRequest) (AgentRunResult, error)
}

// ExecutionError bypasses recoverable tool failures when common execution
// cannot persist its evidence. JavaScript cannot clear the Go-owned error.
type ExecutionError struct{ Err error }

func (e *ExecutionError) Error() string { return e.Err.Error() }
func (e *ExecutionError) Unwrap() error { return e.Err }

type Provider interface {
	Prepare(ModelRequest) ([]byte, error)
	Generate(context.Context, ModelRequest, Observer) (ModelResult, error)
}
type ToolExecutor interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

type callParentKey struct{}

type modelToolCallKey struct{}

// WithModelToolCall identifies the call whose start and result are owned by
// the agent loop. Runtime calls inside it remain distinct child calls.
func WithModelToolCall(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, modelToolCallKey{}, id)
}

func ModelToolCall(ctx context.Context) string {
	id, _ := ctx.Value(modelToolCallKey{}).(string)
	return id
}

func WithCallParent(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, callParentKey{}, id)
}
func CallParent(ctx context.Context) string { id, _ := ctx.Value(callParentKey{}).(string); return id }
