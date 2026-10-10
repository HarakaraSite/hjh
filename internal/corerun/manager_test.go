package corerun

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

type observedProvider struct {
	mu       sync.Mutex
	requests []contracts.ModelRequest
	entered  chan struct{}
	block    bool
}

func (p *observedProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}
func (p *observedProvider) Generate(ctx context.Context, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	for _, text := range []string{"途中", "途中の本文"} {
		if err := observe(contracts.Observation{Kind: "assistant_text", Phase: "delta", Result: contracts.JSON(map[string]any{"text": text})}); err != nil {
			return contracts.ModelResult{}, err
		}
	}
	if p.entered != nil {
		close(p.entered)
	}
	if p.block {
		<-ctx.Done()
		return contracts.ModelResult{}, ctx.Err()
	}
	return contracts.ModelResult{Text: "作業完了", FinishReason: "stop"}, nil
}

func productManager(t *testing.T, provider contracts.Provider, options app.RunOptions) (*Manager, *app.Service) {
	t.Helper()
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	options.Workspace = workspace
	service := app.NewService(store, provider, options)
	manager := New(service, workspace)
	t.Cleanup(func() { _ = manager.Close(); _ = service.Close(); _ = store.Close() })
	return manager, service
}

func awaitRun(t *testing.T, m *Manager, id string, predicate func(api.RunState) bool) api.RunState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := m.Get(m.Core().CoreID, id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(state) {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("run did not reach the expected product state")
	return api.RunState{}
}

func TestIndependentSubscriptionCancelAndSessionReservation(t *testing.T) {
	provider := &observedProvider{entered: make(chan struct{}), block: true}
	m, service := productManager(t, provider, app.RunOptions{})
	request := api.StartRunRequest{CoreID: m.Core().CoreID, RunID: uuid.NewString(), Task: "途中経過を確認してキャンセル"}
	_, created, err := m.Start(request)
	if err != nil || !created {
		t.Fatalf("start: %t %v", created, err)
	}
	slow, err := m.Subscribe(request.CoreID, request.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	// Nobody consumes slow.Notify: this must not block Generate or cancellation.
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("slow subscriber blocked execution")
	}
	state := awaitRun(t, m, request.RunID, func(s api.RunState) bool { return s.Phase == "running" })
	if state.SessionID == "" || state.ExecutionID == "" {
		t.Fatalf("admission missing IDs: %+v", state)
	}
	if len(state.Texts) != 1 || state.Texts[0].Text != "途中の本文" {
		t.Fatalf("cumulative text: %+v", state.Texts)
	}
	_, _, err = m.Start(api.StartRunRequest{CoreID: request.CoreID, RunID: uuid.NewString(), SessionID: state.SessionID, Task: "同じSessionの別依頼"})
	var conflict *api.Error
	if !errors.As(err, &conflict) || conflict.Code != "session_busy" || conflict.ActiveRunID != request.RunID {
		t.Fatalf("concurrent Session start: %v", err)
	}
	if _, created, err = m.Start(request); err != nil || created {
		t.Fatalf("same ID ran twice: %t %v", created, err)
	}
	if _, err = m.Cancel(request.CoreID, request.RunID); err != nil {
		t.Fatal(err)
	}
	terminal := awaitRun(t, m, request.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if terminal.Result == nil || terminal.Result.OK || terminal.Result.AgentResult.Outcome != "cancelled" {
		t.Fatalf("terminal: %+v", terminal.Result)
	}
	inspection, err := service.Inspect(context.Background(), terminal.ExecutionID, 0, -1)
	if err != nil || inspection.Execution.Lifecycle == "active" || inspection.Execution.Outcome != "cancelled" {
		t.Fatalf("terminal before saved completion: %+v %v", inspection.Execution, err)
	}
	late, err := m.Subscribe(request.CoreID, request.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()
	snapshot, err := late.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != "terminal" || len(snapshot.Texts) != 1 || snapshot.Texts[0].Text != "途中の本文" {
		t.Fatalf("late snapshot lost cancelled text: %+v", snapshot)
	}
	executions, err := service.Executions(context.Background(), state.SessionID)
	if err != nil || len(executions) != 1 {
		t.Fatalf("same-ID/Session request created another execution: %d %v", len(executions), err)
	}
}

func TestTerminalAdoptionResumeAndExplicitZeroOptions(t *testing.T) {
	provider := &observedProvider{}
	m, service := productManager(t, provider, app.RunOptions{MaxSteps: 3, FromTurn: 1})
	first := api.StartRunRequest{CoreID: m.Core().CoreID, RunID: uuid.NewString(), Task: "first"}
	if _, _, err := m.Start(first); err != nil {
		t.Fatal(err)
	}
	state := awaitRun(t, m, first.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if !state.Result.OK {
		t.Fatalf("first failed: %+v", state.Result)
	}
	saved, err := service.Session(context.Background(), state.SessionID)
	if err != nil || saved.TurnCount != 1 {
		t.Fatalf("terminal before adoption: %+v %v", saved, err)
	}
	maxSteps, fromTurn := 0, int64(0)
	second := api.StartRunRequest{CoreID: first.CoreID, RunID: uuid.NewString(), SessionID: state.SessionID, Task: "second", MaxSteps: &maxSteps, FromTurn: &fromTurn}
	if _, _, err := m.Start(second); err != nil {
		t.Fatal(err)
	}
	secondState := awaitRun(t, m, second.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if !secondState.Result.OK {
		t.Fatalf("second failed: %+v", secondState.Result)
	}
	executions, err := service.Executions(context.Background(), state.SessionID)
	if err != nil || len(executions) != 2 {
		t.Fatalf("executions: %d %v", len(executions), err)
	}
	limits := map[string]int64{}
	for _, e := range executions {
		limits[e.ID] = e.MaxSteps
	}
	if limits[state.ExecutionID] != 3 || limits[secondState.ExecutionID] != 0 {
		t.Fatalf("run maxSteps did not override Core default: %+v", limits)
	}
	provider.mu.Lock()
	var userTasks []string
	for _, message := range provider.requests[1].Messages {
		if message.Role == "user" {
			userTasks = append(userTasks, message.VisibleText())
		}
	}
	provider.mu.Unlock()
	if len(userTasks) != 2 || userTasks[0] != "first" || userTasks[1] != "second" {
		t.Fatalf("explicit fromTurn 0 lost adopted context: %+v", userTasks)
	}
	late, err := m.Subscribe(first.CoreID, second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Close()
	snapshot, err := late.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != "terminal" || len(snapshot.Texts) != 1 || snapshot.Texts[0].Text != "作業完了" {
		t.Fatalf("completed snapshot: %+v", snapshot)
	}
	for _, operation := range snapshot.Operations {
		if operation.ExecutionID != secondState.ExecutionID || operation.Phase == "" || operation.Time.IsZero() {
			t.Fatalf("non-observation evidence appeared as progress: %+v", operation)
		}
	}
}
