package corerun

import (
	"context"
	"sync"

	"hjh/internal/api"
	"hjh/internal/conversation"
)

type runStateSubscriber struct {
	notify chan struct{}
}

// Subscription joins lightweight run-state notifications with one independent
// conversation subscriber. It never holds the manager or execution-data lock
// while serializing a snapshot or writing an HTTP frame.
type Subscription struct {
	Notify             <-chan struct{}
	ConversationNotify <-chan struct{}

	manager      *Manager
	runID        string
	run          *managedRun
	state        *runStateSubscriber
	view         *conversation.View
	viewSub      *conversation.Subscription
	initialState api.RunState
	initial      api.ConversationSnapshot
	initialMu    sync.Mutex
	once         sync.Once
}

func (m *Manager) Subscribe(coreID, runID string) (*Subscription, error) {
	return m.SubscribeContext(context.Background(), coreID, runID)
}

func (m *Manager) SubscribeContext(ctx context.Context, coreID, runID string) (*Subscription, error) {
	m.mu.Lock()
	run, err := m.findLocked(coreID, runID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	state := &runStateSubscriber{notify: make(chan struct{}, 1)}
	run.subscribers[state] = state.notify
	run.viewConsumers++
	initialState := m.stateLocked(run)
	m.mu.Unlock()

	view, err := m.ensureRunView(ctx, runID, run)
	if err != nil {
		m.removeFailedConsumer(run, state)
		return nil, err
	}
	initial, viewSub, err := view.SubscribeSnapshot(ctx)
	if err != nil {
		m.removeFailedConsumer(run, state)
		return nil, err
	}
	if initialState.Phase == "terminal" {
		initialState, err = m.resolveTerminal(ctx, run, initialState)
		if err != nil {
			viewSub.Close()
			m.removeFailedConsumer(run, state)
			return nil, err
		}
	}
	return &Subscription{Notify: state.notify, ConversationNotify: viewSub.Notify,
		manager: m, runID: runID, run: run, state: state, view: view, viewSub: viewSub,
		initialState: initialState, initial: initial}, nil
}

func (m *Manager) removeFailedConsumer(run *managedRun, state *runStateSubscriber) {
	m.mu.Lock()
	delete(run.subscribers, state)
	run.viewConsumers--
	release := run.viewConsumers == 0
	m.mu.Unlock()
	if release {
		m.releaseViewIfUnused(run)
	}
}

func (s *Subscription) Snapshot() (api.RunSnapshot, error) {
	return s.SnapshotContext(context.Background())
}

func (s *Subscription) SnapshotContext(ctx context.Context) (api.RunSnapshot, error) {
	select {
	case <-ctx.Done():
		return api.RunSnapshot{}, ctx.Err()
	default:
	}
	s.initialMu.Lock()
	conversation := s.initial
	state := cloneRunState(s.initialState)
	s.initialMu.Unlock()
	return api.RunSnapshot{RunState: state, Conversation: conversation}, nil
}

// ReleaseInitialSnapshot drops the captured initial cut after its frame has
// been written. A long-lived SSE subscription then keeps only its live view.
func (s *Subscription) ReleaseInitialSnapshot() {
	s.initialMu.Lock()
	s.initial = api.ConversationSnapshot{}
	s.initialState = api.RunState{}
	s.initialMu.Unlock()
}

// FinalSnapshotContext resolves current terminal state and waits for the
// durable conversation prefix before taking the terminal snapshot.
func (s *Subscription) FinalSnapshotContext(ctx context.Context) (api.RunSnapshot, error) {
	state, err := s.manager.GetContext(ctx, s.manager.core.CoreID, s.runID)
	if err != nil {
		return api.RunSnapshot{}, err
	}
	if state.Phase == "terminal" {
		if err := s.manager.waitForFinalConversation(ctx, s.run, s.view); err != nil {
			return api.RunSnapshot{}, err
		}
	}
	snapshot, err := s.view.Snapshot(ctx)
	if err != nil {
		return api.RunSnapshot{}, err
	}
	return api.RunSnapshot{RunState: state, Conversation: snapshot}, nil
}

func (s *Subscription) UpdateContext(ctx context.Context) (api.RunUpdate, bool, error) {
	state, err := s.manager.GetContext(ctx, s.manager.core.CoreID, s.runID)
	if err != nil {
		return api.RunUpdate{}, false, err
	}
	var update *api.ConversationUpdate
	if value, ok := s.viewSub.TakeUpdate(); ok {
		update = &value
	}
	return api.RunUpdate{RunState: state, Conversation: update}, update != nil, nil
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		if s.viewSub != nil {
			s.viewSub.Close()
		}
		m := s.manager
		m.mu.Lock()
		delete(s.run.subscribers, s.state)
		s.run.viewConsumers--
		release := s.run.viewConsumers == 0
		m.mu.Unlock()
		if release {
			m.releaseViewIfUnused(s.run)
		}
		s.ReleaseInitialSnapshot()
		s.view = nil
		s.viewSub = nil
	})
}

func cloneRunState(state api.RunState) api.RunState {
	copy := state
	if state.ActualSelection != nil {
		selection := *state.ActualSelection
		copy.ActualSelection = &selection
	}
	if state.Result != nil {
		result := *state.Result
		copy.Result = &result
		if result.AgentResult != nil {
			agentResult := *result.AgentResult
			copy.Result.AgentResult = &agentResult
		}
		if result.Error != nil {
			apiError := *result.Error
			copy.Result.Error = &apiError
		}
	}
	return copy
}
