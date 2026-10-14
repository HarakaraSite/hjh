package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

func snapshotTestDB(t *testing.T, workspace string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type snapshotExecutionProvider struct {
	started chan contracts.ModelRequest
	release <-chan struct{}
}

func (p snapshotExecutionProvider) Prepare(contracts.ModelRequest) ([]byte, error) {
	return []byte("prepared-wire-exact"), nil
}

func (p snapshotExecutionProvider) Generate(ctx context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	if p.started != nil {
		p.started <- request
	}
	select {
	case <-ctx.Done():
		return contracts.ModelResult{}, ctx.Err()
	case <-p.release:
		return contracts.ModelResult{Text: "completed"}, nil
	}
}

func TestRunReadsLiveSnapshotsAndSavesAfterProviderReturns(t *testing.T) {
	store, workspace := delegationStore(t)
	db := snapshotTestDB(t, workspace)
	started := make(chan contracts.ModelRequest, 1)
	release := make(chan struct{})
	service := NewService(store, snapshotExecutionProvider{started: started, release: release}, RunOptions{Workspace: workspace})
	defer service.Close()
	admitted := make(chan contracts.ExecutionAdmission, 1)
	type runCompletion struct {
		result RunResult
		err    error
	}
	done := make(chan runCompletion, 1)
	go func() {
		result, err := service.Run(context.Background(), RunRequest{Task: "snapshot lifecycle", Workspace: workspace,
			OnAdmission: func(admission contracts.ExecutionAdmission) { admitted <- admission }})
		done <- runCompletion{result: result, err: err}
	}()
	admission := <-admitted
	request := <-started
	if string(request.WireBody) != "prepared-wire-exact" {
		t.Fatalf("provider received wire body %q", request.WireBody)
	}
	activeStatus, err := service.SaveSnapshot(context.Background(), admission.ExecutionID)
	if err != nil || activeStatus.State != contracts.SnapshotSaveMemory || activeStatus.Frozen {
		t.Fatalf("active explicit save status = %+v, error = %v", activeStatus, err)
	}
	var requestRows, budgetRows int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", admission.ExecutionID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_budgets WHERE execution_id=?", admission.ExecutionID).Scan(&budgetRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 || budgetRows != 0 {
		t.Fatalf("provider wait persisted snapshots early: request=%d budget=%d", requestRows, budgetRows)
	}
	// The Session ID is not known until admission has created it. Read it from
	// the execution locator, then verify matching record-ID live composition.
	execution, err := store.Execution(context.Background(), admission.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	live, err := service.SessionContext(context.Background(), execution.SessionCorrelation)
	if err != nil {
		t.Fatal(err)
	}
	if live.LatestRequest == nil || live.LatestRequest.SnapshotSave.State != contracts.SnapshotSaveMemory || !live.LatestRequest.SnapshotSave.DetailAvailable ||
		live.LatestBudget == nil || live.LatestBudget.SnapshotSave.State != contracts.SnapshotSaveMemory || !live.LatestBudget.DetailAvailable {
		t.Fatalf("live context status = %+v", live)
	}
	requestRecord, err := service.Record(context.Background(), admission.ExecutionID, live.LatestRequest.RecordOrdinal)
	if err != nil || string(requestRecord.Content) != string(request.WireBody) || requestRecord.SnapshotSave.State != contracts.SnapshotSaveMemory {
		t.Fatalf("live request record=%+v err=%v", requestRecord, err)
	}
	budgetRecord, err := service.Record(context.Background(), admission.ExecutionID, live.LatestBudget.RecordOrdinal)
	if err != nil {
		t.Fatalf("live budget record: %v", err)
	}
	var budgetPayload struct {
		DetailAvailable bool            `json:"detailAvailable"`
		Fact            json.RawMessage `json:"fact"`
	}
	if err := json.Unmarshal(budgetRecord.Payload, &budgetPayload); err != nil || !budgetPayload.DetailAvailable || len(budgetPayload.Fact) == 0 {
		t.Fatalf("live budget record payload=%s err=%v", budgetRecord.Payload, err)
	}
	captured, ok := service.snapshotOwner.capture(admission.ExecutionID)
	if !ok || captured.Request == nil || captured.Budget == nil {
		t.Fatalf("pre-save snapshot capture = %+v, found=%t", captured, ok)
	}
	staleRequestRecords, err := store.Records(context.Background(), admission.ExecutionID,
		live.LatestRequest.RecordOrdinal, live.LatestRequest.RecordOrdinal)
	if err != nil || len(staleRequestRecords) != 1 || staleRequestRecords[0].Content != nil {
		t.Fatalf("pre-save request metadata records=%+v err=%v", staleRequestRecords, err)
	}
	staleBudgetRecords, err := store.Records(context.Background(), admission.ExecutionID,
		live.LatestBudget.RecordOrdinal, live.LatestBudget.RecordOrdinal)
	if err != nil || len(staleBudgetRecords) != 1 {
		t.Fatalf("pre-save budget metadata records=%+v err=%v", staleBudgetRecords, err)
	}
	var staleBudgetPayload struct {
		Fact json.RawMessage `json:"fact"`
	}
	if err := json.Unmarshal(staleBudgetRecords[0].Payload, &staleBudgetPayload); err != nil || len(staleBudgetPayload.Fact) != 0 {
		t.Fatalf("pre-save budget payload=%s err=%v", staleBudgetRecords[0].Payload, err)
	}
	close(release)
	completion := <-done
	if completion.err != nil || !completion.result.Result.OK || completion.result.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("completed run=%+v err=%v", completion.result, completion.err)
	}
	requestRows, budgetRows = 0, 0
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", admission.ExecutionID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_budgets WHERE execution_id=?", admission.ExecutionID).Scan(&budgetRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 1 || budgetRows != 1 {
		t.Fatalf("terminal snapshot rows: request=%d budget=%d", requestRows, budgetRows)
	}
	saved, err := service.SessionContext(context.Background(), completion.result.SessionID)
	if err != nil || saved.LatestRequest.SnapshotSave.State != contracts.SnapshotSaveSaved || saved.LatestBudget.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("saved context=%+v err=%v", saved, err)
	}
	if err := service.mergeSnapshotRecord(&staleRequestRecords[0], captured, true); err != nil ||
		string(staleRequestRecords[0].Content) != string(captured.Request.WireBody) ||
		staleRequestRecords[0].SnapshotSave.State != contracts.SnapshotSaveMemory {
		t.Fatalf("pre-save request capture lost across save: record=%+v err=%v", staleRequestRecords[0], err)
	}
	if err := service.mergeSnapshotRecord(&staleBudgetRecords[0], captured, true); err != nil {
		t.Fatalf("merge pre-save budget capture: %v", err)
	}
	var capturedBudgetPayload struct {
		Fact         json.RawMessage          `json:"fact"`
		SnapshotSave contracts.SnapshotStatus `json:"snapshotSave"`
	}
	if err := json.Unmarshal(staleBudgetRecords[0].Payload, &capturedBudgetPayload); err != nil {
		t.Fatal(err)
	}
	expectedFact, err := json.Marshal(captured.Budget.Fact)
	if err != nil || string(capturedBudgetPayload.Fact) != string(expectedFact) ||
		capturedBudgetPayload.SnapshotSave.State != contracts.SnapshotSaveMemory {
		t.Fatalf("pre-save budget capture lost across save: payload=%s expectedFact=%s err=%v",
			staleBudgetRecords[0].Payload, expectedFact, err)
	}
}

func TestSnapshotOnlySaveFailurePreservesRunAndLiveDetails(t *testing.T) {
	store, workspace := delegationStore(t)
	db := snapshotTestDB(t, workspace)
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_snapshot_request
		BEFORE INSERT ON execution_latest_requests BEGIN SELECT RAISE(ABORT, 'snapshot fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, snapshotExecutionProvider{release: closedSignal()}, RunOptions{Workspace: workspace})
	defer service.Close()
	result, err := service.Run(context.Background(), RunRequest{Task: "snapshot failure does not change run", Workspace: workspace})
	if err != nil || !result.Result.OK || result.Result.Outcome != "final" || result.SnapshotSave.State != contracts.SnapshotSaveFailed || !result.SnapshotSave.DetailAvailable {
		t.Fatalf("snapshot failure changed run: result=%+v err=%v", result, err)
	}
	execution, err := store.Execution(context.Background(), result.ExecutionID)
	if err != nil || execution.Outcome != string(history.OutcomeCompleted) || execution.Adoption != "canonical" {
		t.Fatalf("execution outcome/adoption=%+v err=%v", execution, err)
	}
	state, err := service.SessionContext(context.Background(), result.SessionID)
	if err != nil || state.LatestRequest == nil || state.LatestRequest.SnapshotSave.State != contracts.SnapshotSaveFailed ||
		state.LatestBudget == nil || state.LatestBudget.SnapshotSave.State != contracts.SnapshotSaveFailed || !state.LatestBudget.DetailAvailable {
		t.Fatalf("failed snapshot context=%+v err=%v", state, err)
	}
	restarted := NewService(store, nil, RunOptions{Workspace: workspace})
	unavailable, err := restarted.SaveSnapshot(context.Background(), result.ExecutionID)
	if err != nil || unavailable.State != contracts.SnapshotSaveUnavailable || unavailable.DetailAvailable || !unavailable.Frozen {
		t.Fatalf("ownerless failed snapshot status=%+v err=%v", unavailable, err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := service.Record(context.Background(), result.ExecutionID, state.LatestRequest.RecordOrdinal)
	if err != nil || string(request.Content) != "prepared-wire-exact" || request.SnapshotSave.State != contracts.SnapshotSaveFailed {
		t.Fatalf("failed snapshot request=%+v err=%v", request, err)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER fail_snapshot_request"); err != nil {
		t.Fatal(err)
	}
	retried, err := service.SaveSnapshot(context.Background(), result.ExecutionID)
	if err != nil || retried.State != contracts.SnapshotSaveSaved || !retried.Frozen || !retried.DetailAvailable {
		t.Fatalf("explicit retry status = %+v, error = %v", retried, err)
	}
	state, err = service.SessionContext(context.Background(), result.SessionID)
	if err != nil || state.LatestRequest == nil || state.LatestRequest.SnapshotSave.State != contracts.SnapshotSaveSaved ||
		state.LatestBudget == nil || state.LatestBudget.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("retried snapshot context=%+v err=%v", state, err)
	}
	request, err = service.Record(context.Background(), result.ExecutionID, state.LatestRequest.RecordOrdinal)
	if err != nil || string(request.Content) != "prepared-wire-exact" || request.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("retried request=%+v err=%v", request, err)
	}
	beforeRestart, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	restarted = NewService(store, nil, RunOptions{Workspace: workspace})
	persisted, err := restarted.SaveSnapshot(context.Background(), result.ExecutionID)
	if err != nil || persisted.State != contracts.SnapshotSaveSaved || !persisted.DetailAvailable || !persisted.Frozen {
		t.Fatalf("ownerless saved snapshot status=%+v err=%v", persisted, err)
	}
	afterRestart, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil || len(afterRestart) != len(beforeRestart) {
		t.Fatalf("ownerless saved snapshot retry changed records: before=%d after=%d err=%v", len(beforeRestart), len(afterRestart), err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestServiceCloseRetriesFailedSnapshotsBeforeReturning(t *testing.T) {
	store, workspace := delegationStore(t)
	db := snapshotTestDB(t, workspace)
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_snapshot_request
		BEFORE INSERT ON execution_latest_requests BEGIN SELECT RAISE(ABORT, 'snapshot fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, snapshotExecutionProvider{release: closedSignal()}, RunOptions{Workspace: workspace})
	result, err := service.Run(context.Background(), RunRequest{Task: "snapshot close retry", Workspace: workspace})
	if err != nil || result.SnapshotSave.State != contracts.SnapshotSaveFailed {
		t.Fatalf("setup run=%+v err=%v", result, err)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER fail_snapshot_request"); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Service.Close retry: %v", err)
	}
	if status := service.SnapshotStatus(result.ExecutionID); status.State != contracts.SnapshotSaveSaved {
		t.Fatalf("close retry status = %+v", status)
	}
	var rows int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", result.ExecutionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("close retry saved %d request rows, want 1", rows)
	}
}

func TestServiceCloseReportsFailedExecutionIDAndCause(t *testing.T) {
	store, workspace := delegationStore(t)
	db := snapshotTestDB(t, workspace)
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_snapshot_request
		BEFORE INSERT ON execution_latest_requests BEGIN SELECT RAISE(ABORT, 'snapshot fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, snapshotExecutionProvider{release: closedSignal()}, RunOptions{Workspace: workspace})
	result, err := service.Run(context.Background(), RunRequest{Task: "snapshot close failure", Workspace: workspace})
	if err != nil || result.SnapshotSave.State != contracts.SnapshotSaveFailed {
		t.Fatalf("setup run=%+v err=%v", result, err)
	}
	closeErr := service.Close()
	if closeErr == nil || !strings.Contains(closeErr.Error(), result.ExecutionID) || !strings.Contains(closeErr.Error(), "snapshot fixture failure") {
		t.Fatalf("Service.Close error = %v, want execution ID and persistence cause", closeErr)
	}
}

func TestDeleteSessionPurgesFailedSnapshotMemory(t *testing.T) {
	store, workspace := delegationStore(t)
	db := snapshotTestDB(t, workspace)
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_snapshot_request
		BEFORE INSERT ON execution_latest_requests BEGIN SELECT RAISE(ABORT, 'snapshot fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, snapshotExecutionProvider{release: closedSignal()}, RunOptions{Workspace: workspace})
	defer service.Close()
	result, err := service.Run(context.Background(), RunRequest{Task: "snapshot delete", Workspace: workspace})
	if err != nil || result.SnapshotSave.State != contracts.SnapshotSaveFailed {
		t.Fatalf("setup run=%+v err=%v", result, err)
	}
	if err := service.DeleteSession(context.Background(), result.SessionID); err != nil {
		t.Fatal(err)
	}
	if status := service.SnapshotStatus(result.ExecutionID); status.State != contracts.SnapshotSaveUnavailable || status.DetailAvailable {
		t.Fatalf("deleted Session retained snapshot: %+v", status)
	}
}

func TestTerminalRecordFailureStillSavesLatestSnapshots(t *testing.T) {
	store, workspace := delegationStore(t)
	db := snapshotTestDB(t, workspace)
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_terminal_record
		BEFORE INSERT ON semantic_records WHEN NEW.kind='host_decision'
		BEGIN SELECT RAISE(ABORT, 'terminal fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, snapshotExecutionProvider{release: closedSignal()}, RunOptions{Workspace: workspace})
	defer service.Close()
	result, err := service.Run(context.Background(), RunRequest{Task: "terminal failure still snapshots", Workspace: workspace})
	if err == nil || result.ExecutionID == "" || result.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("terminal failure result=%+v err=%v", result, err)
	}
	var requestRows, budgetRows int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", result.ExecutionID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_budgets WHERE execution_id=?", result.ExecutionID).Scan(&budgetRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 1 || budgetRows != 1 {
		t.Fatalf("terminal failure skipped snapshot save: request=%d budget=%d", requestRows, budgetRows)
	}
	execution, err := store.Execution(context.Background(), result.ExecutionID)
	if err != nil || execution.Lifecycle != "active" {
		t.Fatalf("terminal record failure changed stored lifecycle: execution=%+v err=%v", execution, err)
	}
}

func closedSignal() <-chan struct{} {
	closed := make(chan struct{})
	close(closed)
	return closed
}

func TestSnapshotOwnerCapturesLatestRequestAndBudgetIndependently(t *testing.T) {
	owner := NewSnapshotOwner()
	fact := contextbudget.Fact{
		Estimator: "estimator", Sources: []contextbudget.Source{{Kind: "current", ExecutionID: "child-1", MessageOrdinal: 3}},
		History: contextbudget.HistoryFact{Status: contextbudget.HistoryPartial, Turns: []contextbudget.HistoryTurnFact{{
			Number: 2, SelectedSources: []contextbudget.Source{{Kind: "prefix", ExecutionID: "root-1", MessageOrdinal: 1}},
		}}},
	}
	if err := owner.recordRequest("session-1", "root-1", "child-1", "request-1", []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := owner.recordBudget("session-1", "root-1", "child-1", "budget-1", fact); err != nil {
		t.Fatal(err)
	}
	if err := owner.recordRequest("session-1", "root-1", "child-1", "request-2", []byte("latest wire")); err != nil {
		t.Fatal(err)
	}
	if err := owner.recordBudget("session-1", "root-1", "child-1", "budget-2", fact); err != nil {
		t.Fatal(err)
	}
	if err := owner.recordRequest("session-1", "root-1", "root-1", "root-request", []byte("root wire")); err != nil {
		t.Fatal(err)
	}
	if err := owner.recordBudget("session-1", "root-1", "root-1", "root-budget", fact); err != nil {
		t.Fatal(err)
	}

	captured, ok := owner.capture("child-1")
	if !ok || captured.SessionID != "session-1" || captured.RootExecutionID != "root-1" ||
		captured.Request == nil || captured.Request.RecordID != "request-2" || string(captured.Request.WireBody) != "latest wire" ||
		captured.Budget == nil || captured.Budget.RecordID != "budget-2" || captured.Budget.Fact.History.Turns[0].Number != 2 {
		t.Fatalf("capture = %+v, found=%t", captured, ok)
	}
	if captured.Status.State != contracts.SnapshotSaveMemory || !captured.Status.DetailAvailable || captured.Status.Frozen {
		t.Fatalf("capture status = %+v", captured.Status)
	}
	rootCapture, ok := owner.capture("root-1")
	if !ok || rootCapture.Request == nil || string(rootCapture.Request.WireBody) != "root wire" ||
		rootCapture.Budget == nil || rootCapture.Budget.RecordID != "root-budget" {
		t.Fatalf("root capture = %+v, found=%t", rootCapture, ok)
	}

	captured.Request.WireBody[0] = 'X'
	captured.Budget.Fact.Sources[0].ExecutionID = "changed"
	captured.Budget.Fact.History.Turns[0].SelectedSources[0].ExecutionID = "changed"
	again, _ := owner.capture("child-1")
	if string(again.Request.WireBody) != "latest wire" || again.Budget.Fact.Sources[0].ExecutionID != "child-1" ||
		again.Budget.Fact.History.Turns[0].SelectedSources[0].ExecutionID != "root-1" {
		t.Fatalf("capture mutation changed owner: %+v", again)
	}
	frozen, ok := owner.freeze("child-1")
	if !ok || !frozen.Status.Frozen || frozen.Status.State != contracts.SnapshotSaveMemory {
		t.Fatalf("frozen capture = %+v, found=%t", frozen, ok)
	}
	if err := owner.recordBudget("session-1", "root-1", "child-1", "budget-3", fact); err == nil {
		t.Fatal("frozen execution accepted another budget snapshot")
	}
}

func TestServiceOwnsSnapshotOwnerAndExposesUnavailableStatus(t *testing.T) {
	store, err := history.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(store, nil, RunOptions{})
	if service.snapshotOwner == nil {
		t.Fatal("Service did not create a snapshot owner")
	}
	if options := service.runOptions(RunRequest{}); options.SnapshotOwner != service.snapshotOwner {
		t.Fatal("Service RunOptions do not share its snapshot owner")
	}
	if status := service.SnapshotStatus("missing-execution"); status.State != contracts.SnapshotSaveUnavailable || status.DetailAvailable || status.Frozen {
		t.Fatalf("missing execution snapshot status = %+v", status)
	}
}

type blockingSnapshotPersister struct {
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
	owner     *SnapshotOwner
	entered   chan struct{}
	release   chan struct{}
	err       error
}

func (p *blockingSnapshotPersister) SaveLatestSnapshots(_ context.Context, _ string,
	request *history.LatestRequestSnapshot, budget *history.LatestBudgetSnapshot) error {
	if p.owner != nil {
		_ = p.owner.status("execution-1") // persistence runs without the owner mutex held
	}
	p.mu.Lock()
	p.calls++
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	err := p.err
	p.mu.Unlock()
	if request == nil || request.RecordID != "request-1" || string(request.WireBody) != "wire" ||
		budget == nil || budget.RecordID != "budget-1" || len(budget.Fact) == 0 {
		return errors.New("snapshot persistence did not receive the captured details")
	}
	if p.entered != nil {
		p.entered <- struct{}{}
	}
	if p.release != nil {
		<-p.release
	}
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	return err
}

func TestSnapshotOwnerFailedSaveRetainsDetailsAndSuccessfulSaveReleasesThem(t *testing.T) {
	owner := NewSnapshotOwner()
	if err := owner.recordRequest("session-1", "root-1", "execution-1", "request-1", []byte("wire")); err != nil {
		t.Fatal(err)
	}
	if err := owner.recordBudget("session-1", "root-1", "execution-1", "budget-1", contextbudget.Fact{
		Estimator: "estimator", Sources: []contextbudget.Source{{Kind: "current", ExecutionID: "execution-1"}},
	}); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("database write failed")
	persister := &blockingSnapshotPersister{owner: owner, entered: make(chan struct{}, 2), release: make(chan struct{}), err: writeErr}
	firstResult := make(chan error, 1)
	go func() {
		_, err := owner.save(context.Background(), persister, "execution-1")
		firstResult <- err
	}()
	<-persister.entered

	secondResult := make(chan error, 1)
	go func() {
		_, err := owner.save(context.Background(), persister, "execution-1")
		secondResult <- err
	}()
	deadline := time.After(time.Second)
	for {
		persister.mu.Lock()
		calls := persister.calls
		persister.mu.Unlock()
		if calls > 1 {
			t.Fatalf("concurrent save reached persistence %d times", calls)
		}
		select {
		case err := <-secondResult:
			t.Fatalf("second save completed before first persistence: %v", err)
		case <-deadline:
			goto releaseFirst
		default:
			time.Sleep(time.Millisecond)
		}
	}

releaseFirst:
	persister.release <- struct{}{}
	if err := <-firstResult; !errors.Is(err, writeErr) {
		t.Fatalf("first save error = %v", err)
	}
	if err := <-secondResult; !errors.Is(err, writeErr) {
		t.Fatalf("joined save error = %v", err)
	}
	failed, _ := owner.capture("execution-1")
	if failed.Status.State != contracts.SnapshotSaveFailed || !failed.Status.DetailAvailable || !failed.Status.Frozen ||
		string(failed.Request.WireBody) != "wire" || failed.Budget.Fact.Estimator != "estimator" {
		t.Fatalf("failed save did not retain details: %+v", failed)
	}
	if err := owner.recordRequest("session-1", "root-1", "execution-1", "request-2", []byte("later")); err == nil {
		t.Fatal("frozen execution accepted another request snapshot")
	}

	persister.mu.Lock()
	persister.err = nil
	persister.entered = nil
	persister.release = nil
	persister.mu.Unlock()
	status, err := owner.save(context.Background(), persister, "execution-1")
	if err != nil || status.State != contracts.SnapshotSaveSaved || !status.DetailAvailable || !status.Frozen {
		t.Fatalf("retry status = %+v, error = %v", status, err)
	}
	saved, _ := owner.capture("execution-1")
	if saved.Request == nil || saved.Request.RecordID != "request-1" || len(saved.Request.WireBody) != 0 ||
		saved.Budget == nil || saved.Budget.RecordID != "budget-1" || saved.Budget.Fact.Estimator != "" {
		t.Fatalf("successful save retained large details: %+v", saved)
	}
	persister.mu.Lock()
	calls, maxActive := persister.calls, persister.maxActive
	persister.mu.Unlock()
	if calls != 2 || maxActive != 1 {
		t.Fatalf("persistence calls=%d max concurrent=%d, want 2 and 1", calls, maxActive)
	}
}

func TestSnapshotOwnerBudgetCanExistWithoutRequest(t *testing.T) {
	owner := NewSnapshotOwner()
	if err := owner.recordBudget("session-1", "root-1", "execution-1", "budget-1", contextbudget.Fact{Estimator: "budget-only"}); err != nil {
		t.Fatal(err)
	}
	captured, ok := owner.capture("execution-1")
	if !ok || captured.Request != nil || captured.Budget == nil || captured.Budget.Fact.Estimator != "budget-only" {
		t.Fatalf("budget-only capture = %+v, found=%t", captured, ok)
	}
}
