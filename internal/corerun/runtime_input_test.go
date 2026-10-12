package corerun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

type taskGateProvider struct {
	entered chan string
	gates   map[string]<-chan struct{}
}

type waitingContext struct {
	context.Context
	waiting chan struct{}
}

func (c waitingContext) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func (p *taskGateProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}

func (p *taskGateProvider) Generate(ctx context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	task := lastUserTask(request)
	p.entered <- task
	gate := p.gates[task]
	select {
	case <-ctx.Done():
		return contracts.ModelResult{}, ctx.Err()
	case <-gate:
		return contracts.ModelResult{Text: "done: " + task, FinishReason: "stop"}, nil
	}
}

func lastUserTask(request contracts.ModelRequest) string {
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == "user" {
			return request.Messages[i].VisibleText()
		}
	}
	return ""
}

func awaitProviderTask(t *testing.T, entered <-chan string, want string) {
	t.Helper()
	select {
	case got := <-entered:
		if got != want {
			t.Fatalf("provider task = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("provider did not receive task %q", want)
	}
}

func productManagerWithConfig(t *testing.T, provider contracts.Provider, options app.RunOptions, config string) (*Manager, *app.Service, string) {
	t.Helper()
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, "extensions", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	options.Workspace = workspace
	options.ConfigPath = configPath
	service := app.NewService(store, provider, options)
	manager := New(service, workspace)
	t.Cleanup(func() { _ = manager.Close(); _ = service.Close(); _ = store.Close() })
	return manager, service, configPath
}

func TestFollowUpReplayKeepsSelectionAfterConfigEdit(t *testing.T) {
	provider := &observedProvider{entered: make(chan struct{}), block: true}
	configWithReviewer := `{"providers":{"test":{"api":"responses","baseURL":"http://unused","auth":{"kind":"none"},"defaultModel":"test/model"}},"model":{"provider":"test"},"defaultRole":"writer","roles":{"writer":{"tools":[]},"reviewer":{"tools":[]}}}`
	manager, _, configPath := productManagerWithConfig(t, provider, app.RunOptions{}, configWithReviewer)
	request := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: uuid.NewString(), Role: "writer", Task: "parent"}
	if _, created, err := manager.Start(request); err != nil || !created {
		t.Fatalf("start parent: created=%t err=%v", created, err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("parent did not enter provider generation")
	}
	state := awaitRun(t, manager, request.RunID, func(s api.RunState) bool { return s.Phase == "running" })
	input := api.SubmitInputRequest{CoreID: request.CoreID, SessionID: state.SessionID, InputID: uuid.NewString(),
		Kind: "follow-up", Text: "reserved task", Next: &api.NextRunOptions{Role: "reviewer"}}
	first, created, err := manager.SubmitInput(context.Background(), request.RunID, input)
	if err != nil || !created || first.Status != "queued" || first.Next == nil || first.Next.Role != "reviewer" {
		t.Fatalf("admit follow-up: receipt=%+v created=%t err=%v", first, created, err)
	}

	configWithoutReviewer := `{"providers":{"test":{"api":"responses","baseURL":"http://unused","auth":{"kind":"none"},"defaultModel":"test/model"}},"model":{"provider":"test"},"defaultRole":"writer","roles":{"writer":{"tools":[]}}}`
	if err := os.WriteFile(configPath, []byte(configWithoutReviewer), 0644); err != nil {
		t.Fatal(err)
	}
	replay, created, err := manager.SubmitInput(context.Background(), request.RunID, input)
	if err != nil || created || replay.Revision != first.Revision || replay.Sequence != first.Sequence || replay.Next == nil || replay.Next.Role != "reviewer" {
		t.Fatalf("same-ID replay after source edit: receipt=%+v created=%t err=%v", replay, created, err)
	}

	if _, err := manager.CancelInputChain(request.CoreID, request.RunID); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, manager, request.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	receipt, err := manager.Input(context.Background(), request.CoreID, request.RunID, input.InputID, false)
	if err != nil || receipt.Status != "not_started" || receipt.Reason != "cancelled" {
		t.Fatalf("cancelled follow-up receipt: %+v err=%v", receipt, err)
	}
	awaitInputChain(t, manager, request.CoreID, request.RunID, "settled")
}

func TestInputChainSettlesAfterAllRunWorkersFinish(t *testing.T) {
	chain := newInputChain(nil, "root", "session", "workspace")
	chain.mu.Lock()
	chain.inFlightRuns++ // successor registration precedes its worker launch
	chain.activeRunID = "successor"
	chain.mu.Unlock()
	manager := &Manager{}
	manager.inputRunWorkerFinished(chain)
	chain.mu.Lock()
	phase, activeRun, inFlight := chain.phase, chain.activeRunID, chain.inFlightRuns
	chain.mu.Unlock()
	if phase == "settled" || activeRun != "successor" || inFlight != 1 {
		t.Fatalf("chain settled with successor worker active: phase=%q active=%q inFlight=%d", phase, activeRun, inFlight)
	}
	manager.inputRunWorkerFinished(chain)
	chain.mu.Lock()
	phase, activeRun, inFlight = chain.phase, chain.activeRunID, chain.inFlightRuns
	chain.mu.Unlock()
	if phase != "settled" || activeRun != "" || inFlight != 0 {
		t.Fatalf("chain did not settle after worker completion: phase=%q active=%q inFlight=%d", phase, activeRun, inFlight)
	}
}

func TestFinalSteeringPendingDoesNotClaimAfterChainCancel(t *testing.T) {
	chain := newInputChain(nil, "root", "session", "workspace")
	chain.sequence = 1
	chain.cancelled = true
	chain.cancelReason = "cancelled"
	chain.steering["root"] = []contracts.SteeringInput{{Receipt: contracts.InputReceipt{InputID: "accepted", Sequence: 1, Status: "accepted"}}}
	pending, err := chain.steeringSource("root").Pending(context.Background(), true)
	if !errors.Is(err, context.Canceled) || len(pending) != 0 {
		t.Fatalf("final pending after owner cancellation: pending=%+v err=%v", pending, err)
	}
	chain.mu.Lock()
	status := chain.steering["root"][0].Receipt.Status
	steeringOpen := chain.steeringOpen
	chain.mu.Unlock()
	if status != "accepted" || steeringOpen {
		t.Fatalf("cancelled final pending claimed input: status=%q steeringOpen=%t", status, steeringOpen)
	}
}

func TestExactCancelBeforeFollowUpExecutionSavesCancelledReason(t *testing.T) {
	manager, service := productManager(t, &observedProvider{}, app.RunOptions{})
	coreID := manager.Core().CoreID
	anchorID, nextID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	receipt, err := service.SaveRuntimeInput(context.Background(), contracts.InputReceipt{
		CoreID: coreID, SessionID: sessionID, RunID: anchorID, InputID: uuid.NewString(),
		Kind: "follow-up", Sequence: 1, Status: "starting", StartedRunID: nextID,
	}, "accepted-follow-up", []byte("next task"))
	if err != nil {
		t.Fatal(err)
	}
	chain := newInputChain(manager, anchorID, sessionID, manager.Core().Workspace)
	chain.activeRunID = nextID
	ctx, cancel := context.WithCancel(context.Background())
	run := &managedRun{state: api.RunState{CoreID: coreID, RunID: nextID, SessionID: sessionID, Phase: "preparing"},
		chain: chain, cancel: cancel}
	manager.runs[nextID] = run
	manager.sessions[sessionID] = nextID
	if _, err := manager.Cancel(coreID, nextID); err != nil {
		t.Fatal(err)
	}
	if err := manager.finishInputRun(run, runInput{chain: chain, followUpReceipt: &receipt}, app.RunResult{}, ctx.Err()); err != nil {
		t.Fatal(err)
	}
	stored, err := service.RuntimeInput(context.Background(), coreID, receipt.InputID, false)
	if err != nil || stored.Status != "not_started" || stored.Reason != "cancelled" {
		t.Fatalf("exact-cancelled follow-up receipt = %+v, error = %v", stored, err)
	}
}

func TestCancelMarksEveryAcceptedSteeringUnappliedAndSettles(t *testing.T) {
	provider := &observedProvider{entered: make(chan struct{}), block: true}
	manager, _ := productManager(t, provider, app.RunOptions{})
	coreID, runID := manager.Core().CoreID, uuid.NewString()
	if _, _, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: runID, Task: "held root"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	state := awaitRun(t, manager, runID, func(s api.RunState) bool { return s.Phase == "running" })
	var inputs []string
	for i := 0; i < 8; i++ {
		id := uuid.NewString()
		_, created, err := manager.SubmitInput(context.Background(), runID, api.SubmitInputRequest{
			CoreID: coreID, SessionID: state.SessionID, InputID: id, Kind: "steering", Text: "accepted detail"})
		if err != nil || !created {
			t.Fatalf("admit steering %d: created=%t err=%v", i, created, err)
		}
		inputs = append(inputs, id)
	}
	if _, err := manager.CancelInputChain(coreID, runID); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, manager, runID, func(s api.RunState) bool { return s.Phase == "terminal" })
	awaitInputChain(t, manager, coreID, runID, "settled")
	for _, id := range inputs {
		receipt, err := manager.Input(context.Background(), coreID, runID, id, false)
		if err != nil || receipt.Status != "unapplied" || receipt.Reason != "cancelled" {
			t.Fatalf("steering %s after cancel: %+v err=%v", id, receipt, err)
		}
	}
}

func TestCoreStopSavesCoreStoppedForPendingSteeringAndFollowUp(t *testing.T) {
	provider := &observedProvider{entered: make(chan struct{}), block: true}
	manager, _ := productManager(t, provider, app.RunOptions{})
	coreID, runID := manager.Core().CoreID, uuid.NewString()
	if _, _, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: runID, Task: "held root"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	state := awaitRun(t, manager, runID, func(s api.RunState) bool { return s.Phase == "running" })
	for _, kind := range []string{"steering", "steering", "follow-up"} {
		if _, _, err := manager.SubmitInput(context.Background(), runID, api.SubmitInputRequest{
			CoreID: coreID, SessionID: state.SessionID, InputID: uuid.NewString(), Kind: kind, Text: "pending task"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := manager.Inputs(context.Background(), state.SessionID, coreID, 0, false)
	if err != nil || len(page.Receipts) != 3 {
		t.Fatalf("saved inputs after Core stop: %+v err=%v", page, err)
	}
	for _, receipt := range page.Receipts {
		wantStatus := "unapplied"
		if receipt.Kind == "follow-up" {
			wantStatus = "not_started"
		}
		if receipt.Status != wantStatus || receipt.Reason != "core_stopped" {
			t.Fatalf("input after Core stop: %+v", receipt)
		}
	}
}

func TestFinalPendingWaitsForRegisteredInputBeforeSealing(t *testing.T) {
	manager := &Manager{runs: map[string]*managedRun{}}
	chain := newInputChain(manager, "root", "session", "workspace")
	chain.steeringOpen = true
	chain.sequence = 1
	write := &inputWrite{candidate: contracts.InputReceipt{InputID: "processing", Sequence: 1}, done: make(chan struct{})}
	chain.writes[write.candidate.InputID] = write
	chain.pendingWrites = 1
	source := chain.steeringSource("root")
	type pendingResult struct {
		inputs []contracts.SteeringInput
		err    error
	}
	result := make(chan pendingResult, 1)
	waiting := make(chan struct{}, 1)
	go func() {
		inputs, err := source.Pending(waitingContext{Context: context.Background(), waiting: waiting}, true)
		result <- pendingResult{inputs: inputs, err: err}
	}()
	select {
	case <-waiting:
	case got := <-result:
		t.Fatalf("final check returned before registered input persisted: %+v", got)
	case <-time.After(5 * time.Second):
		t.Fatal("final check did not wait for the registered input")
	}

	chain.mu.Lock()
	delete(chain.writes, write.candidate.InputID)
	chain.pendingWrites = 0
	accepted := contracts.SteeringInput{Receipt: contracts.InputReceipt{InputID: "processing", Sequence: 1, Status: "accepted"}}
	chain.steering["root"] = []contracts.SteeringInput{accepted}
	chain.signalLocked()
	close(write.done)
	chain.mu.Unlock()
	got := <-result
	if got.err != nil || len(got.inputs) != 1 || got.inputs[0].Receipt.InputID != accepted.Receipt.InputID {
		t.Fatalf("final check did not return newly durable input: %+v", got)
	}
	chain.mu.Lock()
	stillOpen := chain.steeringOpen
	delete(chain.steering, "root")
	chain.mu.Unlock()
	if !stillOpen {
		t.Fatal("final check sealed admission while a newly durable input was pending")
	}
	inputs, err := source.Pending(context.Background(), true)
	if err != nil || len(inputs) != 0 {
		t.Fatalf("empty final check: inputs=%+v err=%v", inputs, err)
	}
	chain.mu.Lock()
	sealed := !chain.steeringOpen
	chain.mu.Unlock()
	if !sealed {
		t.Fatal("final check did not seal empty steering admission")
	}
}

func TestAnchorCancelStopsHandedOffRunButLeavesIndependentRun(t *testing.T) {
	rootGate, firstGate, independentGate := make(chan struct{}), make(chan struct{}), make(chan struct{})
	provider := &taskGateProvider{entered: make(chan string, 8), gates: map[string]<-chan struct{}{
		"root task": rootGate, "first follow-up": firstGate, "independent task": independentGate,
	}}
	manager, _ := productManager(t, provider, app.RunOptions{})
	root := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: uuid.NewString(), Task: "root task"}
	if _, created, err := manager.Start(root); err != nil || !created {
		t.Fatalf("start root: created=%t err=%v", created, err)
	}
	awaitProviderTask(t, provider.entered, "root task")
	rootState := awaitRun(t, manager, root.RunID, func(s api.RunState) bool { return s.Phase == "running" })
	first, firstCreated, err := manager.SubmitInput(context.Background(), root.RunID, api.SubmitInputRequest{CoreID: root.CoreID,
		SessionID: rootState.SessionID, InputID: uuid.NewString(), Kind: "follow-up", Text: "first follow-up"})
	if err != nil || !firstCreated || first.Status != "queued" {
		t.Fatalf("queue first follow-up: receipt=%+v created=%t err=%v", first, firstCreated, err)
	}
	second, secondCreated, err := manager.SubmitInput(context.Background(), root.RunID, api.SubmitInputRequest{CoreID: root.CoreID,
		SessionID: rootState.SessionID, InputID: uuid.NewString(), Kind: "follow-up", Text: "second follow-up"})
	if err != nil || !secondCreated || second.Status != "queued" || second.Sequence <= first.Sequence {
		t.Fatalf("queue second follow-up out of order: first=%+v second=%+v created=%t err=%v", first, second, secondCreated, err)
	}
	close(rootGate)
	awaitProviderTask(t, provider.entered, "first follow-up")
	rootTerminal := awaitRun(t, manager, root.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if rootTerminal.NextRunID == "" || !rootTerminal.Result.OK {
		t.Fatalf("root terminal did not retain successful handoff: %+v", rootTerminal)
	}
	childID := rootTerminal.NextRunID
	childState := awaitRun(t, manager, childID, func(s api.RunState) bool { return s.Phase == "running" })
	startedAt, err := time.Parse(time.RFC3339Nano, childState.StartedAt)
	rootStartedAt, rootTimeErr := time.Parse(time.RFC3339Nano, rootTerminal.StartedAt)
	if err != nil || rootTimeErr != nil || !startedAt.After(rootStartedAt) {
		t.Fatalf("follow-up did not get its own Core start time: root=%q next=%q", rootTerminal.StartedAt, childState.StartedAt)
	}

	independent := api.StartRunRequest{CoreID: root.CoreID, RunID: uuid.NewString(), Task: "independent task"}
	if _, created, err := manager.Start(independent); err != nil || !created {
		t.Fatalf("start independent run: created=%t err=%v", created, err)
	}
	awaitProviderTask(t, provider.entered, "independent task")
	independentState := awaitRun(t, manager, independent.RunID, func(s api.RunState) bool { return s.Phase == "running" })

	chainState, err := manager.CancelInputChain(root.CoreID, root.RunID)
	if err != nil || chainState.Phase != "cancelling" || chainState.ActiveRunID != childID {
		t.Fatalf("anchor cancel did not target handed-off child: state=%+v err=%v", chainState, err)
	}
	childTerminal := awaitRun(t, manager, childID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if childTerminal.Result == nil || childTerminal.Result.OK {
		t.Fatalf("handed-off child did not cancel: %+v", childTerminal.Result)
	}
	secondAfterCancel, err := manager.Input(context.Background(), root.CoreID, root.RunID, second.InputID, false)
	if err != nil || secondAfterCancel.Status != "not_started" || secondAfterCancel.Reason != "cancelled" {
		t.Fatalf("queued successor after anchor cancel: %+v err=%v", secondAfterCancel, err)
	}
	awaitInputChain(t, manager, root.CoreID, root.RunID, "settled")

	stillRunning, err := manager.Get(root.CoreID, independent.RunID)
	if err != nil || stillRunning.Phase != "running" || stillRunning.SessionID != independentState.SessionID {
		t.Fatalf("anchor cancel affected independent run: state=%+v err=%v", stillRunning, err)
	}
	close(independentGate)
	if terminal := awaitRun(t, manager, independent.RunID, func(s api.RunState) bool { return s.Phase == "terminal" }); !terminal.Result.OK {
		t.Fatalf("independent run did not complete: %+v", terminal.Result)
	}
}

func TestMultipleFollowUpsStartInFIFOOrderOnSameSession(t *testing.T) {
	rootGate, firstGate, secondGate := make(chan struct{}), make(chan struct{}), make(chan struct{})
	provider := &taskGateProvider{entered: make(chan string, 8), gates: map[string]<-chan struct{}{
		"root FIFO": rootGate, "first FIFO": firstGate, "second FIFO": secondGate,
	}}
	manager, service := productManager(t, provider, app.RunOptions{})
	root := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: uuid.NewString(), Task: "root FIFO"}
	if _, created, err := manager.Start(root); err != nil || !created {
		t.Fatalf("start root: created=%t err=%v", created, err)
	}
	awaitProviderTask(t, provider.entered, "root FIFO")
	rootState := awaitRun(t, manager, root.RunID, func(s api.RunState) bool { return s.Phase == "running" })

	first, firstCreated, err := manager.SubmitInput(context.Background(), root.RunID, api.SubmitInputRequest{CoreID: root.CoreID,
		SessionID: rootState.SessionID, InputID: uuid.NewString(), Kind: "follow-up", Text: "first FIFO"})
	if err != nil || !firstCreated || first.Status != "queued" {
		t.Fatalf("queue first follow-up: receipt=%+v created=%t err=%v", first, firstCreated, err)
	}
	second, secondCreated, err := manager.SubmitInput(context.Background(), root.RunID, api.SubmitInputRequest{CoreID: root.CoreID,
		SessionID: rootState.SessionID, InputID: uuid.NewString(), Kind: "follow-up", Text: "second FIFO"})
	if err != nil || !secondCreated || second.Status != "queued" || second.Sequence <= first.Sequence {
		t.Fatalf("queue second follow-up out of order: first=%+v second=%+v created=%t err=%v", first, second, secondCreated, err)
	}

	close(rootGate)
	awaitProviderTask(t, provider.entered, "first FIFO")
	rootTerminal := awaitRun(t, manager, root.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if !rootTerminal.Result.OK || rootTerminal.NextRunID == "" || rootTerminal.SessionID != rootState.SessionID {
		t.Fatalf("root did not complete with first handoff in the same Session: %+v", rootTerminal)
	}
	firstRunID := rootTerminal.NextRunID
	firstRun := awaitRun(t, manager, firstRunID, func(s api.RunState) bool { return s.Phase == "running" })
	if firstRun.SessionID != rootState.SessionID {
		t.Fatalf("first follow-up changed Session: root=%q first=%q", rootState.SessionID, firstRun.SessionID)
	}

	close(firstGate)
	awaitProviderTask(t, provider.entered, "second FIFO")
	firstTerminal := awaitRun(t, manager, firstRunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if !firstTerminal.Result.OK || firstTerminal.NextRunID == "" || firstTerminal.SessionID != rootState.SessionID {
		t.Fatalf("first follow-up did not complete with second handoff in the same Session: %+v", firstTerminal)
	}
	secondRunID := firstTerminal.NextRunID
	secondRun := awaitRun(t, manager, secondRunID, func(s api.RunState) bool { return s.Phase == "running" })
	if secondRun.SessionID != rootState.SessionID {
		t.Fatalf("second follow-up changed Session: root=%q second=%q", rootState.SessionID, secondRun.SessionID)
	}

	close(secondGate)
	secondTerminal := awaitRun(t, manager, secondRunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	if !secondTerminal.Result.OK || secondTerminal.NextRunID != "" || secondTerminal.SessionID != rootState.SessionID {
		t.Fatalf("second follow-up did not complete as the final same-Session turn: %+v", secondTerminal)
	}

	for _, check := range []struct {
		inputID string
		runID   string
	}{{first.InputID, firstRunID}, {second.InputID, secondRunID}} {
		receipt, err := manager.Input(context.Background(), root.CoreID, root.RunID, check.inputID, false)
		if err != nil || receipt.Status != "started" || receipt.StartedRunID != check.runID || receipt.StartedExecutionID == "" {
			t.Fatalf("follow-up receipt did not record successful handoff: %+v err=%v want run %q", receipt, err, check.runID)
		}
	}
	awaitInputChain(t, manager, root.CoreID, root.RunID, "settled")

	executions, err := service.Executions(context.Background(), rootState.SessionID)
	if err != nil || len(executions) != 3 {
		t.Fatalf("same-Session execution history: got %d executions, err=%v", len(executions), err)
	}
	wantTasks := []string{"root FIFO", "first FIFO", "second FIFO"}
	for index, execution := range executions {
		if execution.SessionID != rootState.SessionID || execution.Task != wantTasks[index] || (index > 0 && execution.Turn <= executions[index-1].Turn) {
			t.Fatalf("follow-up execution order/session mismatch at %d: execution=%+v all=%+v", index, execution, executions)
		}
	}
}

func TestSessionInputSequenceContinuesAcrossNormalRunChains(t *testing.T) {
	firstGate, secondGate := make(chan struct{}), make(chan struct{})
	provider := &taskGateProvider{entered: make(chan string, 4), gates: map[string]<-chan struct{}{
		"first session run": firstGate, "second session run": secondGate,
	}}
	manager, _ := productManager(t, provider, app.RunOptions{})
	first := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: uuid.NewString(), Task: "first session run"}
	if _, _, err := manager.Start(first); err != nil {
		t.Fatal(err)
	}
	awaitProviderTask(t, provider.entered, "first session run")
	firstState := awaitRun(t, manager, first.RunID, func(s api.RunState) bool { return s.Phase == "running" })
	firstReceipt, _, err := manager.SubmitInput(context.Background(), first.RunID, api.SubmitInputRequest{CoreID: first.CoreID,
		SessionID: firstState.SessionID, InputID: uuid.NewString(), Kind: "steering", Text: "first steering"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CancelInputChain(first.CoreID, first.RunID); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, manager, first.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	awaitInputChain(t, manager, first.CoreID, first.RunID, "settled")

	second := api.StartRunRequest{CoreID: first.CoreID, RunID: uuid.NewString(), SessionID: firstState.SessionID, Task: "second session run"}
	if _, created, err := manager.Start(second); err != nil || !created {
		t.Fatalf("start second Session run: created=%t err=%v", created, err)
	}
	awaitProviderTask(t, provider.entered, "second session run")
	secondState := awaitRun(t, manager, second.RunID, func(s api.RunState) bool { return s.Phase == "running" })
	secondReceipt, _, err := manager.SubmitInput(context.Background(), second.RunID, api.SubmitInputRequest{CoreID: second.CoreID,
		SessionID: secondState.SessionID, InputID: uuid.NewString(), Kind: "steering", Text: "second steering"})
	if err != nil || secondReceipt.Sequence <= firstReceipt.Sequence {
		t.Fatalf("Session input sequence reset across normal run: first=%d second=%d err=%v", firstReceipt.Sequence, secondReceipt.Sequence, err)
	}
	if _, err := manager.CancelInputChain(second.CoreID, second.RunID); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, manager, second.RunID, func(s api.RunState) bool { return s.Phase == "terminal" })
	awaitInputChain(t, manager, second.CoreID, second.RunID, "settled")
}

func awaitInputChain(t *testing.T, manager *Manager, coreID, runID, phase string) api.InputChainState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err := manager.InputChain(coreID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase == phase {
			return state
		}
		time.Sleep(time.Millisecond)
	}
	state, err := manager.InputChain(coreID, runID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("input chain did not reach %q: %+v", phase, state)
	return api.InputChainState{}
}
