package corerun

import (
	"encoding/json"
	"fmt"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

// projection stores display state only; raw evidence remains in history.
type projection struct {
	texts          map[string]api.TextState
	textOrder      []string
	operations     map[string]api.OperationState
	operationOrder []string
	executions     map[string]api.ExecutionState
	executionOrder []string
}

func newProjection() *projection {
	return &projection{texts: map[string]api.TextState{}, operations: map[string]api.OperationState{}, executions: map[string]api.ExecutionState{}}
}

func (p *projection) observe(state *api.RunState, o contracts.Observation) {
	if o.Kind == "execution" && o.Name == "run" && (o.Phase == "started" || o.Phase == "start") {
		var input struct {
			SessionID string                   `json:"sessionId"`
			Model     contracts.ModelSelection `json:"model"`
			Role      string                   `json:"role"`
		}
		_ = json.Unmarshal(o.Input, &input)
		selection := input.Model
		if _, exists := p.executions[o.ExecutionID]; !exists {
			p.executionOrder = append(p.executionOrder, o.ExecutionID)
		}
		p.executions[o.ExecutionID] = api.ExecutionState{ExecutionID: o.ExecutionID, ParentExecutionID: o.ParentExecutionID, Role: input.Role, Selection: &selection, Phase: "running"}
		if o.ParentExecutionID == "" {
			state.ExecutionID = o.ExecutionID
			if input.SessionID != "" {
				state.SessionID = input.SessionID
			}
			state.ActualRole = input.Role
			state.ActualSelection = &selection
			if state.Phase == "preparing" {
				state.Phase = "running"
			}
		}
	}
	if o.Kind == "assistant_text" {
		var result struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(o.Result, &result) == nil {
			p.setText(api.TextState{ExecutionID: o.ExecutionID, ParentExecutionID: o.ParentExecutionID, ModelStep: o.ModelStep, Text: result.Text})
		}
		return
	}
	if o.Kind == "model_result" {
		return
	}
	if o.Kind == "agent" && o.Phase != "start" && o.Phase != "started" {
		var result struct {
			ExecutionID string `json:"executionId"`
			Outcome     string `json:"outcome"`
		}
		if json.Unmarshal(o.Result, &result) == nil && result.ExecutionID != "" {
			if child, ok := p.executions[result.ExecutionID]; ok {
				child.Phase = "terminal"
				child.Outcome = result.Outcome
				p.executions[result.ExecutionID] = child
			}
		}
	}
	id := o.ID
	if id == "" {
		id = fmt.Sprintf("%s/%s/%d", o.Kind, o.Name, o.ModelStep)
	}
	key := o.ExecutionID + "/" + id
	if _, exists := p.operations[key]; !exists {
		p.operationOrder = append(p.operationOrder, key)
	}
	p.operations[key] = api.OperationState{ID: id, ExecutionID: o.ExecutionID, ParentExecutionID: o.ParentExecutionID, Parent: o.Parent,
		Kind: o.Kind, Name: o.Name, Phase: o.Phase, Time: o.Time, ModelStep: o.ModelStep, Error: o.Error}
}

func (p *projection) setText(v api.TextState) {
	key := fmt.Sprintf("%s/%d", v.ExecutionID, v.ModelStep)
	if _, exists := p.texts[key]; !exists {
		p.textOrder = append(p.textOrder, key)
	}
	p.texts[key] = v
}
func (p *projection) fill(s *api.RunState) {
	for _, key := range p.textOrder {
		s.Texts = append(s.Texts, p.texts[key])
	}
	for _, key := range p.operationOrder {
		s.Operations = append(s.Operations, p.operations[key])
	}
	for _, key := range p.executionOrder {
		v := p.executions[key]
		if v.Selection != nil {
			selection := *v.Selection
			v.Selection = &selection
		}
		s.Executions = append(s.Executions, v)
	}
}
