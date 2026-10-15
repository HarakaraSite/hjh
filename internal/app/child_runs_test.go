package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

func childScopeTest(t *testing.T, provider contracts.Provider) (*childRuns, *history.Store, string) {
	t.Helper()
	store, workspace := delegationStore(t)
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	snapshot, err := extensions.Capture(configPath)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := capturedRegistry(snapshot, configPath)
	if err != nil {
		t.Fatal(err)
	}
	model, err := registry.Resolve(contracts.ModelSelection{}, registry.Config.Model)
	if err != nil {
		t.Fatal(err)
	}
	model.ModelID = "test/child"
	encoded, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveConfiguration(context.Background(), snapshot.ConfigurationID, encoded); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(context.Background(), history.SessionInput{WorkspaceRoot: workspace, AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: contracts.JSON(model)})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.BeginExecution(context.Background(), history.BeginExecutionInput{Task: "parent", SessionID: session.ID, AgentName: "default", Model: contracts.JSON(model), Build: json.RawMessage(`{}`), ConfigurationID: snapshot.ConfigurationID})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	control := &executionControl{cancel: cancel}
	progress := &progressSink{}
	owner := executiondata.NewOwner()
	j := newJournal(context.Background(), store, parent.ID, parent.ID, "parent", owner, progress.observe)
	j.control = control
	if err = j.observe(contracts.Observation{ID: parent.ID, Kind: "execution", Name: "run", Phase: "started"}); err != nil {
		t.Fatal(err)
	}
	factory := newConnectionFactory(RunOptions{})
	snapshotOwner := NewSnapshotOwner()
	d := &delegation{store: store, provider: provider, input: executionInput{execution: parent, snapshot: snapshot, workspace: workspace, configPath: configPath, rootSessionID: session.ID, rootExecutionID: parent.ID, model: model, connections: factory, injectedProvider: true, executionDataOwner: owner, snapshotOwner: snapshotOwner}, journal: j, control: control, progress: progress}
	scope := newChildRuns(ctx, d)
	t.Cleanup(func() { scope.Close(); cancel(); _ = factory.close() })
	return scope, store, parent.ID
}

func TestChildRunsParallelCollectAndIndividualCancel(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		task := req.Messages[0].VisibleText()
		if err := observe(contracts.Observation{ID: task + "-request", Kind: "model_request", Phase: "start", ModelStep: 1, Time: time.Now().UTC()}); err != nil {
			return contracts.ModelResult{}, err
		}
		started <- task
		if task == "A" {
			select {
			case <-release:
			case <-ctx.Done():
				return contracts.ModelResult{}, ctx.Err()
			}
		} else {
			<-ctx.Done()
			return contracts.ModelResult{}, ctx.Err()
		}
		return contracts.ModelResult{Text: "A completed"}, nil
	})
	scope, store, parentID := childScopeTest(t, provider)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, err := scope.SpawnAgent(ctx, contracts.AgentRunRequest{Task: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := scope.SpawnAgent(ctx, contracts.AgentRunRequest{Task: "B"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	status, err := scope.StatusAgent(ctx, contracts.AgentQuery{ExecutionID: a.ExecutionID})
	if err != nil || status.State != "running" || status.Progress.Phase != "model" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	stopped, err := scope.CancelAgent(ctx, contracts.AgentQuery{ExecutionID: b.ExecutionID})
	if err != nil || stopped.Outcome != "cancelled" {
		t.Fatalf("cancel=%+v err=%v", stopped, err)
	}
	close(release)
	result, err := scope.CollectAgent(ctx, contracts.AgentQuery{ExecutionID: a.ExecutionID})
	if err != nil || result.Outcome != "completed" || result.FinalText != "A completed" {
		t.Fatalf("collect=%+v err=%v", result, err)
	}
	again, err := scope.CollectAgent(ctx, contracts.AgentQuery{ExecutionID: a.ExecutionID})
	if err != nil || again != result {
		t.Fatalf("repeat=%+v err=%v", again, err)
	}
	children, err := store.Children(ctx, parentID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children=%+v err=%v", children, err)
	}
	for _, child := range children {
		if child.Lifecycle != "settled" || child.Adoption != "non_canonical" || child.TerminalRecordID == "" {
			t.Fatalf("unsettled child=%+v", child)
		}
	}
}

func TestChildRunsCloseJoinsBeforeParentTerminal(t *testing.T) {
	started := make(chan struct{})
	provider := delegationProvider(func(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		close(started)
		<-ctx.Done()
		return contracts.ModelResult{}, ctx.Err()
	})
	scope, store, parentID := childScopeTest(t, provider)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spawn, err := scope.SpawnAgent(ctx, contracts.AgentRunRequest{Task: "long child"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	scope.Close()
	child, err := store.Execution(ctx, spawn.ExecutionID)
	if err != nil || child.Lifecycle != "settled" || child.Outcome != "cancelled" || child.TerminalRecordID == "" {
		t.Fatalf("child before parent terminal=%+v err=%v", child, err)
	}
	if err = scope.delegation.journal.end(history.OutcomeCompleted, map[string]any{"ok": true}); err != nil {
		t.Fatal(err)
	}
	parent, err := store.Execution(ctx, parentID)
	if err != nil || parent.Lifecycle != "settled" {
		t.Fatalf("parent=%+v err=%v", parent, err)
	}
	if _, err = scope.SpawnAgent(ctx, contracts.AgentRunRequest{Task: "late"}); err == nil {
		t.Fatal("spawn after parent close accepted")
	}
}

func TestChildRunsDoNotNotifyParentContextBudget(t *testing.T) {
	provider := delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		return contracts.ModelResult{Text: "completed"}, nil
	})
	scope, store, _ := childScopeTest(t, provider)
	notified := 0
	scope.delegation.input.onContextBudget = func(contracts.ContextBudgetStatus) { notified++ }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spawn, err := scope.SpawnAgent(ctx, contracts.AgentRunRequest{Task: "quick child"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scope.CollectAgent(ctx, contracts.AgentQuery{ExecutionID: spawn.ExecutionID}); err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(ctx, spawn.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	measured := 0
	for _, record := range records {
		if record.Kind == "context_budget" {
			measured++
		}
	}
	if measured == 0 {
		t.Fatal("child projection did not run")
	}
	if notified != 0 {
		t.Fatalf("child execution notified %d context budget facts", notified)
	}
}

func TestChildRunsCollectCancellationDoesNotBecomeStorageFailure(t *testing.T) {
	provider := delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		return contracts.ModelResult{Text: "completed"}, nil
	})
	scope, _, _ := childScopeTest(t, provider)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spawn, err := scope.SpawnAgent(ctx, contracts.AgentRunRequest{Task: "quick child"})
	if err != nil {
		t.Fatal(err)
	}
	query := contracts.AgentQuery{ExecutionID: spawn.ExecutionID}
	if _, err = scope.CollectAgent(ctx, query); err != nil {
		t.Fatal(err)
	}
	// Race a normal caller cancellation against reads of an already durable
	// child. The operation may return its result or a cancellation, but neither
	// is a failed history write and neither may cancel the parent execution.
	var wg sync.WaitGroup
	for range 32 {
		callCtx, stop := context.WithCancel(ctx)
		wg.Add(1)
		go func() { defer wg.Done(); runtime.Gosched(); stop() }()
		_, _ = scope.CollectAgent(callCtx, query)
		stop()
	}
	wg.Wait()
	if err = scope.delegation.control.failure(); err != nil {
		t.Fatalf("caller cancellation became a storage failure: %v", err)
	}
	if _, err = scope.CollectAgent(ctx, query); err != nil {
		t.Fatalf("parent cannot collect after another caller cancelled: %v", err)
	}
}
