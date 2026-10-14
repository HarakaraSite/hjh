package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

// childRuns owns async children until their parent execution settles.
// The same wait group covers admission and execution, so closing a parent
// cannot race a late admission into an untracked child.
type childRuns struct {
	ctx        context.Context
	delegation *delegation
	mu         sync.Mutex
	closed     bool
	all        map[*childRun]struct{}
	byID       map[string]*childRun
	wg         sync.WaitGroup
}

type childRun struct {
	mu           sync.Mutex
	cancel       context.CancelFunc
	done         chan struct{}
	status       contracts.AgentStatus
	result       contracts.AgentRunResult
	err          error
	instructions *childInstructionOwner
	activeTools  map[string]struct{}
}

func newChildRuns(ctx context.Context, d *delegation) *childRuns {
	return &childRuns{ctx: ctx, delegation: d, all: map[*childRun]struct{}{}, byID: map[string]*childRun{}}
}

func (s *childRuns) SpawnAgent(ctx context.Context, request contracts.AgentRunRequest) (contracts.AgentSpawnResult, error) {
	if err := ctx.Err(); err != nil {
		return contracts.AgentSpawnResult{}, err
	}
	childCtx, cancel := context.WithCancel(s.ctx)
	r := &childRun{cancel: cancel, done: make(chan struct{}), activeTools: map[string]struct{}{}}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return contracts.AgentSpawnResult{}, errors.New("parent execution has finished")
	}
	s.all[r] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()
	stopCaller := context.AfterFunc(ctx, cancel)
	prepared, err := s.delegation.prepareChild(childCtx, request)
	stopCaller()
	if err != nil {
		cancel()
		s.mu.Lock()
		delete(s.all, r)
		s.mu.Unlock()
		close(r.done)
		s.wg.Done()
		return contracts.AgentSpawnResult{}, err
	}
	id := prepared.input.execution.ID
	instructions := newChildInstructionOwner(id, s.delegation.input.execution.ID)
	prepared.input.childInstructions = instructions
	r.instructions = instructions
	r.status = contracts.AgentStatus{ExecutionID: id, State: "starting", Role: prepared.input.snapshot.Agent.Name,
		Model: prepared.input.model, Tools: append([]string{}, prepared.input.snapshot.Agent.Tools...),
		Progress:     contracts.AgentProgress{Phase: "starting", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		Instructions: []contracts.ChildInstructionReceipt{}}
	prepared.input.childObserve = r.observe
	s.mu.Lock()
	s.byID[id] = r
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer close(r.done)
		defer cancel()
		result, runErr := s.delegation.runChild(childCtx, request, prepared)
		r.mu.Lock()
		r.result, r.err = result, runErr
		r.status.State = result.Outcome
		r.status.Progress.Phase = "settled"
		r.status.Progress.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		r.mu.Unlock()
	}()
	return contracts.AgentSpawnResult{ExecutionID: id, State: "starting"}, nil
}

func (s *childRuns) lookup(ctx context.Context, id string) (*childRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	r := s.byID[id]
	s.mu.Unlock()
	if r == nil {
		return nil, fmt.Errorf("child execution %q was not started by this parent", id)
	}
	return r, nil
}

func (s *childRuns) StatusAgent(ctx context.Context, q contracts.AgentQuery) (contracts.AgentStatus, error) {
	r, err := s.lookup(ctx, q.ExecutionID)
	if err != nil {
		return contracts.AgentStatus{}, err
	}
	if fatal := s.delegation.control.failure(); fatal != nil {
		return contracts.AgentStatus{}, &contracts.ExecutionError{Err: fatal}
	}
	r.mu.Lock()
	status := r.status
	status.Tools = append([]string{}, status.Tools...)
	if status.Progress.LastTool != nil {
		tool := *status.Progress.LastTool
		status.Progress.LastTool = &tool
	}
	r.mu.Unlock()
	status.SnapshotSave = s.delegation.input.snapshotOwner.status(q.ExecutionID)
	after := int64(-1)
	if q.After != nil {
		after = *q.After
	}
	status.Reports, status.NextCursor, err = s.delegation.store.AssistantReports(ctx, q.ExecutionID, after)
	if err != nil {
		return contracts.AgentStatus{}, err
	}
	status.Instructions, err = s.delegation.store.ChildInstructionReceipts(ctx, q.ExecutionID)
	if err != nil {
		return contracts.AgentStatus{}, err
	}
	return status, nil
}

func (s *childRuns) SteerAgent(ctx context.Context, request contracts.AgentSteerRequest) (contracts.ChildInstructionReceipt, error) {
	r, err := s.lookup(ctx, request.ExecutionID)
	if err != nil {
		return contracts.ChildInstructionReceipt{}, err
	}
	return r.instructions.Accept(ctx, request)
}

func (s *childRuns) CollectAgent(ctx context.Context, q contracts.AgentQuery) (contracts.AgentRunResult, error) {
	r, err := s.lookup(ctx, q.ExecutionID)
	if err != nil {
		return contracts.AgentRunResult{}, err
	}
	select {
	case <-ctx.Done():
		return contracts.AgentRunResult{}, ctx.Err()
	case <-r.done:
	}
	if fatal := s.delegation.control.failure(); fatal != nil {
		return contracts.AgentRunResult{}, &contracts.ExecutionError{Err: fatal}
	}
	r.mu.Lock()
	result, runErr := r.result, r.err
	r.mu.Unlock()
	if result.ExecutionID == "" {
		return result, runErr
	}
	result.SnapshotSave = s.delegation.input.snapshotOwner.status(q.ExecutionID)
	// The child has already settled durably. Caller cancellation must not
	// turn this evidence read into a root-wide storage failure.
	settled, err := s.delegation.store.Execution(context.WithoutCancel(ctx), q.ExecutionID)
	if err != nil {
		return result, s.delegation.control.storage(err)
	}
	s.delegation.journal.link(q.CallID, history.Relation{Relation: "child_terminal", TargetRecordID: settled.TerminalRecordID, Mandatory: true})
	return result, nil
}

func (s *childRuns) CancelAgent(ctx context.Context, q contracts.AgentQuery) (contracts.AgentRunResult, error) {
	r, err := s.lookup(ctx, q.ExecutionID)
	if err != nil {
		return contracts.AgentRunResult{}, err
	}
	r.cancel()
	return s.CollectAgent(ctx, q)
}

func (s *childRuns) Close() {
	s.mu.Lock()
	s.closed = true
	for r := range s.all {
		r.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *childRuns) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (r *childRun) observe(o contracts.Observation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status.State != "starting" && r.status.State != "running" {
		return nil
	}
	r.status.State = "running"
	p := &r.status.Progress
	p.UpdatedAt = o.Time.UTC().Format(time.RFC3339Nano)
	if o.ModelStep > 0 {
		p.ModelStep = o.ModelStep
	}
	switch o.Kind {
	case "model_request", "assistant_text":
		p.Phase = "model"
	case "model_result":
		p.Phase = "between_steps"
	case "model_tool", "tool":
		state := "completed"
		outcome := ""
		if o.Phase == "started" || o.Phase == "start" {
			state = "running"
			r.activeTools[o.ID] = struct{}{}
			p.Phase = "tool"
		} else {
			delete(r.activeTools, o.ID)
			outcome = o.Phase
			p.Phase = "between_steps"
			if len(r.activeTools) != 0 {
				p.Phase = "tool"
			}
		}
		p.LastTool = &contracts.AgentToolProgress{Name: o.Name, CallID: o.ID, State: state, Outcome: outcome}
	}
	return nil
}
