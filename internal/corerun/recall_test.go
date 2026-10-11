package corerun

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
)

const recallTestConfig = `{"providers":{"test":{"api":"responses","baseURL":"http://unused","auth":{"kind":"none"},"defaultModel":"test/model"}},"model":{"provider":"test"},"defaultRole":"writer","roles":{"writer":{"tools":[]}}}`

func startStoppedRecallSource(t *testing.T, manager *Manager, entered <-chan string, coreID string) (string, string) {
	t.Helper()
	request := api.StartRunRequest{CoreID: coreID, RunID: uuid.NewString(), Role: "writer", Task: "source"}
	if _, created, err := manager.Start(request); err != nil || !created {
		t.Fatalf("start source: created=%t err=%v", created, err)
	}
	awaitProviderTask(t, entered, "source")
	state := awaitRun(t, manager, request.RunID, func(state api.RunState) bool { return state.Phase == "running" })
	if _, err := manager.Cancel(coreID, request.RunID); err != nil {
		t.Fatal(err)
	}
	terminal := awaitRun(t, manager, request.RunID, func(state api.RunState) bool { return state.Phase == "terminal" })
	if terminal.ExecutionID == "" || state.SessionID == "" {
		t.Fatalf("stopped source missing persisted IDs: state=%+v terminal=%+v", state, terminal)
	}
	return state.SessionID, terminal.ExecutionID
}

func TestRecallPreAdmissionFailureRetainsClaimAndAdmissionConsumesIt(t *testing.T) {
	sourceGate := make(chan struct{})
	targetGate := make(chan struct{})
	provider := &taskGateProvider{entered: make(chan string, 8), gates: map[string]<-chan struct{}{
		"source": sourceGate, "target": targetGate,
	}}
	manager, _, _ := productManagerWithConfig(t, provider, app.RunOptions{}, recallTestConfig)
	coreID := manager.Core().CoreID
	sessionID, sourceID := startStoppedRecallSource(t, manager, provider.entered, coreID)

	prepared, err := manager.Recall(context.Background(), sessionID, api.RecallOperationRequest{
		CoreID: coreID, OperationID: "recall-prepare-once", Action: "prepare", Selector: sourceID,
	})
	if err != nil || prepared.Receipt.Result != "prepared" || prepared.Receipt.SourceExecutionID != sourceID {
		t.Fatalf("prepare recall: %+v, %v", prepared, err)
	}

	failedID := uuid.NewString()
	if _, created, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: failedID, SessionID: sessionID,
		Role: "missing-role", Task: "must fail before admission"}); err != nil || !created {
		t.Fatalf("start pre-admission failure: created=%t err=%v", created, err)
	}
	failed := awaitRun(t, manager, failedID, func(state api.RunState) bool { return state.Phase == "terminal" })
	if failed.ExecutionID != "" {
		t.Fatalf("pre-admission failure saved execution %q", failed.ExecutionID)
	}
	view, err := manager.SessionContext(context.Background(), coreID, sessionID)
	if err != nil || view.PendingRecall == nil || view.PendingRecall.ClaimedByRunID != "" {
		t.Fatalf("pre-admission failure did not release claim: %+v, %v", view, err)
	}

	targetRunID := uuid.NewString()
	if _, created, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: targetRunID, SessionID: sessionID,
		Role: "writer", Task: "target"}); err != nil || !created {
		t.Fatalf("start target: created=%t err=%v", created, err)
	}
	awaitProviderTask(t, provider.entered, "target")
	target := awaitRun(t, manager, targetRunID, func(state api.RunState) bool { return state.Phase == "running" })
	view, err = manager.SessionContext(context.Background(), coreID, sessionID)
	if err != nil || view.PendingRecall != nil || view.LatestRecallAdmission == nil || view.LatestRecallAdmission.TargetExecutionID != target.ExecutionID {
		t.Fatalf("committed target did not consume recall at admission: state=%+v context=%+v err=%v", target, view, err)
	}

	// Durable replay is answered while the target owns the Session and cannot
	// restore or replace its already-claimed reference.
	replayed, err := manager.Recall(context.Background(), sessionID, api.RecallOperationRequest{
		CoreID: coreID, OperationID: "recall-prepare-once", Action: "prepare", Selector: sourceID,
	})
	if err != nil || replayed.Receipt.CompletedAt != prepared.Receipt.CompletedAt {
		t.Fatalf("same-ID replay while busy = %+v, %v", replayed, err)
	}
	_, err = manager.Recall(context.Background(), sessionID, api.RecallOperationRequest{
		CoreID: coreID, OperationID: "recall-prepare-once", Action: "prepare", Selector: "latest",
	})
	var coded *api.Error
	if !errors.As(err, &coded) || coded.Code != "command_id_conflict" {
		t.Fatalf("same ID with changed selector error = %v", err)
	}

	if _, err := manager.Cancel(coreID, targetRunID); err != nil {
		t.Fatal(err)
	}
	awaitRun(t, manager, targetRunID, func(state api.RunState) bool { return state.Phase == "terminal" })
	view, err = manager.SessionContext(context.Background(), coreID, sessionID)
	if err != nil || view.PendingRecall != nil || view.LatestRecallAdmission == nil || view.LatestRecallAdmission.TargetExecutionID != target.ExecutionID {
		t.Fatalf("post-admission cancellation restored recall: %+v, %v", view, err)
	}

	// Model a POST whose first durable lookup happened before the operation was
	// saved, then whose Manager registration was delayed until after consumption.
	signature, err := recallSignature(sessionID, api.RecallOperationRequest{
		CoreID: coreID, OperationID: "recall-prepare-once", Action: "prepare", Selector: sourceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	late := &recallOperation{sessionID: sessionID, request: api.RecallOperationRequest{
		CoreID: coreID, OperationID: "recall-prepare-once", Action: "prepare", Selector: sourceID,
	}, signature: signature, generation: prepared.Receipt.Generation, done: make(chan struct{})}
	manager.mu.Lock()
	manager.recallOperations[late.request.OperationID] = late
	manager.recallBySession[sessionID] = late
	manager.wg.Add(1)
	manager.mu.Unlock()
	go manager.persistRecallOperation(late)
	<-late.done
	if late.err != nil || late.result.Receipt.CompletedAt != prepared.Receipt.CompletedAt {
		t.Fatalf("late same-ID worker did not return durable receipt: %+v, %v", late.result, late.err)
	}
	view, err = manager.SessionContext(context.Background(), coreID, sessionID)
	if err != nil || view.PendingRecall != nil {
		t.Fatalf("late same-ID worker restored consumed recall: %+v, %v", view, err)
	}
}

func TestRecallOperationFromPreviousCoreDoesNotRestorePendingState(t *testing.T) {
	sourceGate := make(chan struct{})
	provider := &taskGateProvider{entered: make(chan string, 4), gates: map[string]<-chan struct{}{"source": sourceGate}}
	manager, service, workspace := productManagerWithConfig(t, provider, app.RunOptions{}, recallTestConfig)
	coreID := manager.Core().CoreID
	sessionID, sourceID := startStoppedRecallSource(t, manager, provider.entered, coreID)
	request := api.RecallOperationRequest{CoreID: coreID, OperationID: "durable-prepare", Action: "prepare", Selector: sourceID}
	prepared, err := manager.Recall(context.Background(), sessionID, request)
	if err != nil || prepared.Receipt.Result != "prepared" {
		t.Fatalf("prepare recall: %+v, %v", prepared, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := New(service, workspace)
	defer restarted.Close()
	replayed, err := restarted.RecallOperation(context.Background(), coreID, sessionID, request.OperationID)
	if err != nil || replayed.ManagedByThisCore || replayed.Receipt.CompletedAt != prepared.Receipt.CompletedAt {
		t.Fatalf("historical receipt = %+v, %v", replayed, err)
	}
	view, err := restarted.SessionContext(context.Background(), restarted.Core().CoreID, sessionID)
	if err != nil || view.PendingRecall != nil || view.RecallRevision != 0 {
		t.Fatalf("new Core restored old pending recall: %+v, %v", view, err)
	}
}

func TestRecallOperationGETJoinsRegistrationAfterDurableRead(t *testing.T) {
	const coreID, sessionID, operationID = "current-core", "session", "operation"
	manager := &Manager{core: api.Core{CoreID: coreID}, recallOperations: map[string]*recallOperation{}}
	durableReceipt := contracts.RecallReceipt{CoreID: coreID, SessionID: sessionID, OperationID: operationID,
		Action: "prepare", Result: "prepared", CompletedAt: "durable-write-finished"}
	registeredResult := api.RecallOperationResult{Receipt: contracts.RecallReceipt{CoreID: coreID, SessionID: sessionID,
		OperationID: operationID, Action: "prepare", Result: "prepared", CompletedAt: "pending-publication-finished"},
		ManagedByThisCore: true}
	op := &recallOperation{sessionID: sessionID, request: api.RecallOperationRequest{CoreID: coreID, OperationID: operationID,
		Action: "prepare"}, done: make(chan struct{})}
	// The durable read has returned, then the POST's local registration becomes
	// visible before the GET applies the result of that read.
	manager.recallOperations[operationID] = op

	type response struct {
		result api.RecallOperationResult
		err    error
	}
	got := make(chan response, 1)
	go func() {
		result, err := manager.recallOperationResultAfterRead(context.Background(), coreID, sessionID, operationID,
			durableReceipt, nil)
		got <- response{result: result, err: err}
	}()
	select {
	case result := <-got:
		t.Fatalf("GET returned durable receipt before local publication completed: %+v, %v", result.result, result.err)
	case <-time.After(25 * time.Millisecond):
	}

	manager.completeRecallOperation(op, registeredResult, nil)
	select {
	case result := <-got:
		if result.err != nil || result.result.Receipt.CompletedAt != "pending-publication-finished" || !result.result.ManagedByThisCore {
			t.Fatalf("GET result = %+v, %v; want joined local operation", result.result, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GET did not finish after local recall publication")
	}
}

func TestManagerCloseJoinsRegisteredRecallPersistence(t *testing.T) {
	sourceGate := make(chan struct{})
	provider := &taskGateProvider{entered: make(chan string, 4), gates: map[string]<-chan struct{}{"source": sourceGate}}
	manager, _, configPath := productManagerWithConfig(t, provider, app.RunOptions{}, recallTestConfig)
	coreID := manager.Core().CoreID
	sessionID, sourceID := startStoppedRecallSource(t, manager, provider.entered, coreID)

	fileURL := &url.URL{Scheme: "file", Path: filepath.Join(filepath.Dir(filepath.Dir(configPath)), "history.sqlite3")}
	locker, err := sql.Open("sqlite", fileURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	tx, err := locker.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(context.Background(), "UPDATE sessions SET state_revision=state_revision WHERE session_id=?", sessionID); err != nil {
		t.Fatal(err)
	}

	request := api.RecallOperationRequest{CoreID: coreID, OperationID: "close-join-recall", Action: "prepare", Selector: sourceID}
	type recallResponse struct {
		result api.RecallOperationResult
		err    error
	}
	response := make(chan recallResponse, 1)
	go func() {
		result, recallErr := manager.Recall(context.Background(), sessionID, request)
		response <- recallResponse{result: result, err: recallErr}
	}()

	deadline := time.Now().Add(3 * time.Second)
	registered := false
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		registered = manager.recallOperations[request.OperationID] != nil
		manager.mu.Unlock()
		if registered {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !registered {
		t.Fatal("recall operation was not registered")
	}

	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Core.Close returned before registered receipt persistence completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Core.Close did not finish after recall persistence could proceed")
	}
	result := <-response
	if result.err != nil || result.result.Receipt.Result != "prepared" {
		t.Fatalf("registered recall result = %+v, %v", result.result, result.err)
	}
}

func TestRecallIsNotClaimedByFollowUpRun(t *testing.T) {
	sourceGate := make(chan struct{})
	targetGate := make(chan struct{})
	nextGate := make(chan struct{})
	provider := &taskGateProvider{entered: make(chan string, 8), gates: map[string]<-chan struct{}{
		"source": sourceGate, "target": targetGate, "next": nextGate,
	}}
	manager, _, _ := productManagerWithConfig(t, provider, app.RunOptions{}, recallTestConfig)
	coreID := manager.Core().CoreID
	sessionID, sourceID := startStoppedRecallSource(t, manager, provider.entered, coreID)
	if _, err := manager.Recall(context.Background(), sessionID, api.RecallOperationRequest{
		CoreID: coreID, OperationID: "followup-prepare", Action: "prepare", Selector: sourceID,
	}); err != nil {
		t.Fatal(err)
	}

	targetRunID := uuid.NewString()
	if _, created, err := manager.Start(api.StartRunRequest{CoreID: coreID, RunID: targetRunID, SessionID: sessionID,
		Role: "writer", Task: "target"}); err != nil || !created {
		t.Fatalf("start target: created=%t err=%v", created, err)
	}
	awaitProviderTask(t, provider.entered, "target")
	target := awaitRun(t, manager, targetRunID, func(state api.RunState) bool { return state.Phase == "running" })
	followUp, created, err := manager.SubmitInput(context.Background(), targetRunID, api.SubmitInputRequest{
		CoreID: coreID, SessionID: sessionID, InputID: uuid.NewString(), Kind: "follow-up", Text: "next",
		Next: &api.NextRunOptions{Role: "writer"},
	})
	if err != nil || !created || followUp.Status != "queued" {
		t.Fatalf("queue follow-up: receipt=%+v created=%t err=%v", followUp, created, err)
	}
	close(targetGate)
	awaitProviderTask(t, provider.entered, "next")
	finishedTarget := awaitRun(t, manager, targetRunID, func(state api.RunState) bool { return state.Phase == "terminal" })
	if finishedTarget.NextRunID == "" {
		t.Fatalf("target did not register its follow-up: %+v", finishedTarget)
	}
	next := awaitRun(t, manager, finishedTarget.NextRunID, func(state api.RunState) bool { return state.Phase == "running" })
	view, err := manager.SessionContext(context.Background(), coreID, sessionID)
	if err != nil || view.PendingRecall != nil || view.LatestRecallAdmission == nil ||
		view.LatestRecallAdmission.TargetExecutionID != target.ExecutionID || next.ExecutionID == target.ExecutionID {
		t.Fatalf("follow-up claimed or reinserted recall: target=%+v next=%+v context=%+v err=%v", target, next, view, err)
	}
	close(nextGate)
	awaitRun(t, manager, finishedTarget.NextRunID, func(state api.RunState) bool { return state.Phase == "terminal" })
}
