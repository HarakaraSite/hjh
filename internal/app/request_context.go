package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
)

// requestContext owns one model request candidate. It never mutates loop
// messages or canonical history. A replacement is used only after its hook
// and all pending API calls finish successfully.
type requestContext struct {
	mu          sync.Mutex
	request     contracts.ModelRequest
	sources     []contextbudget.Source
	budget      contextbudget.Budget
	history     contextbudget.HistoryReader
	prepare     func(contracts.ModelRequest) ([]byte, error)
	projector   *contextbudget.Projector
	current     []contextbudget.Entry
	changed     bool
	memory      *contextMemoryScope
	generate    func(context.Context, contextModelInput) (contextModelResult, error)
	executionID string
	turn        int64
}

func newRequestContext(request contracts.ModelRequest, prefix, current []contextbudget.Entry,
	budget contextbudget.Budget, history contextbudget.HistoryReader,
	prepare func(contracts.ModelRequest) ([]byte, error), projector *contextbudget.Projector, executionID string, turn int64) *requestContext {
	owner := &requestContext{request: request, budget: budget, history: history, prepare: prepare,
		projector: projector, current: current, executionID: executionID, turn: turn}
	owner.request.Messages = make([]contracts.Message, 0, len(prefix)+len(current))
	for _, entries := range [][]contextbudget.Entry{prefix, current} {
		for _, entry := range entries {
			owner.request.Messages = append(owner.request.Messages, entry.Message)
			owner.sources = append(owner.sources, entry.Source)
		}
	}
	return owner
}

type contextCandidate struct {
	Messages    *[]contracts.Message  `json:"messages"`
	Instruction *string               `json:"instruction"`
	Scope       string                `json:"scope"`
	Budget      *contextbudget.Values `json:"budget"`
}

func (c *requestContext) Call(ctx context.Context, operation string, input json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch operation {
	case "model.generate":
		var arguments contextModelInput
		if err := json.Unmarshal(input, &arguments); err != nil {
			return nil, err
		}
		if c.generate == nil {
			return nil, errors.New("request model is unavailable")
		}
		result, err := c.generate(ctx, arguments)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case "memory.get", "memory.set":
		var arguments struct {
			Key   string          `json:"key"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(input, &arguments); err != nil {
			return nil, err
		}
		if c.memory == nil {
			return nil, errors.New("context memory is unavailable")
		}
		if operation == "memory.get" {
			return c.memory.get(arguments.Key), nil
		}
		if len(arguments.Value) == 0 {
			return nil, errors.New("context.memory.set requires value")
		}
		c.memory.set(arguments.Key, arguments.Value)
		return json.RawMessage("null"), nil
	case "get":
		return json.Marshal(struct {
			Instruction string                     `json:"instruction"`
			Tools       []contracts.ToolDefinition `json:"tools"`
			Messages    []contracts.Message        `json:"messages"`
			Sources     []contextbudget.Source     `json:"sources"`
			Budget      contextbudget.Budget       `json:"budget"`
		}{c.request.Instruction, c.request.Tools, c.request.Messages, c.sources, c.budget})
	case "history":
		var query struct {
			BeforeTurn int64 `json:"beforeTurn"`
		}
		if err := json.Unmarshal(input, &query); err != nil {
			return nil, fmt.Errorf("context.history: %w", err)
		}
		if c.history == nil {
			return json.RawMessage("null"), nil
		}
		turn, err := c.history(ctx, query.BeforeTurn)
		if err != nil {
			return nil, err
		}
		return json.Marshal(turn)
	case "replace", "measure", "select":
		var candidate contextCandidate
		if err := json.Unmarshal(input, &candidate); err != nil {
			return nil, fmt.Errorf("context.%s: %w", operation, err)
		}
		if operation != "replace" {
			return c.inspect(ctx, operation, candidate)
		}
		if candidate.Messages == nil {
			return nil, errors.New("context.replace requires messages")
		}
		request, budget := c.candidate(candidate)
		c.request, c.budget, c.changed = request, budget, true
		c.sources = nil
		for i := range request.Messages {
			c.sources = append(c.sources, contextbudget.Source{Kind: "hook", ExecutionID: c.executionID, Turn: c.turn, MessageOrdinal: int64(i)})
		}
		return json.RawMessage("null"), nil
	default:
		return nil, fmt.Errorf("unknown context operation %q", operation)
	}
}

func (c *requestContext) project(ctx context.Context, request contracts.ModelRequest, budget contextbudget.Budget) (contextbudget.Projection, error) {
	entries := make([]contextbudget.Entry, 0, len(request.Messages))
	for i, message := range request.Messages {
		entries = append(entries, contextbudget.Entry{Message: message, Source: contextbudget.Source{
			Kind: "hook", ExecutionID: c.executionID, Turn: c.turn, MessageOrdinal: int64(i)}})
	}
	request.Messages = nil
	// Explicit replacements are a complete projection. Treat them as prefix
	// entries so budgeting cannot silently trim or re-add canonical history.
	return contextbudget.New(budget, nil, entries).Project(ctx, request, nil, c.prepare)
}

func (c *requestContext) projection(ctx context.Context) (contextbudget.Projection, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.changed {
		return contextbudget.Projection{}, false, nil
	}
	projection, err := c.project(ctx, c.request, c.budget)
	projection.Fact.History.Reason = "project_request_hook"
	return projection, true, err
}
