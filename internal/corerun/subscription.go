package corerun

import (
	"context"
	"encoding/json"
	"sync"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

// Subscription is an independent, coalescing notification of display changes.
// Registration and the initial snapshot boundary use the same manager lock.
type Subscription struct {
	Notify  <-chan struct{}
	manager *Manager
	runID   string
	once    sync.Once
}

func (m *Manager) Subscribe(coreID, runID string) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.findLocked(coreID, runID)
	if err != nil {
		return nil, err
	}
	notify := make(chan struct{}, 1)
	s := &Subscription{Notify: notify, manager: m, runID: runID}
	r.subscribers[s] = notify
	return s, nil
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		s.manager.mu.Lock()
		defer s.manager.mu.Unlock()
		delete(s.manager.runs[s.runID].subscribers, s)
	})
}

func (s *Subscription) Snapshot() (api.RunState, error) {
	m := s.manager
	state, err := m.Get(m.core.CoreID, s.runID)
	if err != nil {
		return state, err
	}
	if state.Phase == "terminal" && state.ExecutionID != "" {
		// The retained result is available through Get even if evidence loading fails.
		err = m.restore(context.Background(), &state)
	}
	return state, err
}

func (m *Manager) restore(ctx context.Context, state *api.RunState) error {
	p := newProjection()
	var load func(string) error
	load = func(id string) error {
		inspection, err := m.service.Inspect(ctx, id, 0, -1)
		if err != nil {
			return err
		}
		for _, r := range inspection.Records {
			var o contracts.Observation
			// journal.observe stamps every Observation with its execution ID.
			// Other evidence payloads also have "kind" but are not progress facts.
			if json.Unmarshal(r.Payload, &o) == nil && o.Kind != "" && o.ExecutionID == r.ExecutionID {
				p.observe(state, o)
			}
		}
		e := inspection.Execution
		if v, ok := p.executions[id]; ok {
			v.Phase = "terminal"
			v.Outcome = e.Outcome
			p.executions[id] = v
		}
		for _, child := range inspection.Children {
			if err := load(child.ID); err != nil {
				return err
			}
		}
		return nil
	}
	if err := load(state.ExecutionID); err != nil {
		return err
	}
	if state.Result != nil && state.Result.AgentResult != nil && state.Result.AgentResult.FinalText != "" {
		step := 1
		for _, v := range p.texts {
			if v.ExecutionID == state.ExecutionID && v.ModelStep > step {
				step = v.ModelStep
			}
		}
		for _, v := range p.operations {
			if v.ExecutionID == state.ExecutionID && v.ModelStep > step {
				step = v.ModelStep
			}
		}
		p.setText(api.TextState{ExecutionID: state.ExecutionID, ModelStep: step, Text: state.Result.AgentResult.FinalText})
	}
	p.fill(state)
	return nil
}
