package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
)

type contextSelectionMeasurement struct {
	contextbudget.Measurement
	Omitted       bool   `json:"omitted"`
	HistoryStatus string `json:"historyStatus"`
}

func (c *requestContext) candidate(options contextCandidate) (contracts.ModelRequest, contextbudget.Budget) {
	request, budget := c.request, c.budget
	if options.Messages != nil {
		request.Messages = *options.Messages
	}
	if options.Instruction != nil {
		request.Instruction = *options.Instruction
	}
	if options.Budget != nil {
		budget = contextbudget.Override(budget, *options.Budget)
	}
	request.WireBody = nil
	return request, budget
}

func (c *requestContext) inspect(ctx context.Context, operation string, options contextCandidate) (json.RawMessage, error) {
	if options.Scope != "" && options.Scope != "current" && options.Scope != "request" {
		return nil, fmt.Errorf("context.%s scope must be request or current", operation)
	}
	request, budget := c.candidate(options)
	var preview contextbudget.Preview
	var err error
	coverage := "explicit"
	if options.Messages == nil && !c.changed && options.Scope != "current" {
		coverage = "request-through-first-overflow"
		request.Messages = nil
		preview, err = c.projector.Preview(ctx, budget, request, c.current, c.prepare)
	} else if operation == "select" {
		current := c.hookEntries(request.Messages)
		request.Messages = nil
		preview, err = contextbudget.New(budget, nil, nil).Preview(ctx, budget, request, current, c.prepare)
	} else {
		projection, projectErr := c.project(ctx, request, budget)
		preview = contextbudget.Preview{Projection: projection}
		preview.Candidate = preview.Selected()
		err = projectErr
	}
	if options.Messages == nil && !c.changed && options.Scope == "current" {
		coverage = "current"
	}
	var exceeded *contextbudget.ExceededError
	if err != nil && !errors.As(err, &exceeded) {
		return nil, err
	}
	selected := contextSelectionMeasurement{preview.Selected(), preview.Omitted(), preview.Projection.Fact.HistoryStatus}
	if operation == "measure" {
		return json.Marshal(struct {
			contextbudget.Measurement
			Estimator string                      `json:"estimator"`
			Budget    contextbudget.Budget        `json:"budget"`
			Coverage  string                      `json:"coverage"`
			Selection contextSelectionMeasurement `json:"selection"`
		}{preview.Candidate, contextbudget.Estimator, budget, coverage, selected})
	}
	return json.Marshal(struct {
		contextbudget.Measurement
		Instruction   string                    `json:"instruction"`
		Messages      []contracts.Message       `json:"messages"`
		Sources       []contextbudget.Source    `json:"sources"`
		Budget        contextbudget.Budget      `json:"budget"`
		Omitted       bool                      `json:"omitted"`
		HistoryStatus string                    `json:"historyStatus"`
		Candidate     contextbudget.Measurement `json:"candidate"`
		Coverage      string                    `json:"coverage"`
	}{selected.Measurement, preview.Projection.Request.Instruction, preview.Projection.Request.Messages,
		preview.Projection.Sources, budget, selected.Omitted, selected.HistoryStatus, preview.Candidate, coverage})
}

func (c *requestContext) hookEntries(messages []contracts.Message) []contextbudget.Entry {
	entries := make([]contextbudget.Entry, 0, len(messages))
	for i, message := range messages {
		entries = append(entries, contextbudget.Entry{Message: message, Source: contextbudget.Source{
			Kind: "hook", ExecutionID: c.executionID, Turn: c.turn, MessageOrdinal: int64(i)}})
	}
	return entries
}
