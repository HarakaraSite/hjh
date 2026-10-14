package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

type heldSnapshotRetryProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (p *heldSnapshotRetryProvider) Prepare(contracts.ModelRequest) ([]byte, error) {
	return []byte("retry-wire-exact"), nil
}

func (p *heldSnapshotRetryProvider) Generate(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	select {
	case <-p.entered:
	default:
		close(p.entered)
	}
	select {
	case <-p.release:
		return contracts.ModelResult{Text: "finished", FinishReason: "stop"}, nil
	case <-ctx.Done():
		return contracts.ModelResult{}, ctx.Err()
	}
}

func TestSnapshotSaveRouteLeavesActiveRunAloneAndRefreshesTerminalStatus(t *testing.T) {
	workspace := t.TempDir()
	dbPath := filepath.Join(workspace, "history.sqlite3")
	store, err := history.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_snapshot_request
		BEFORE INSERT ON execution_latest_requests BEGIN SELECT RAISE(ABORT, 'snapshot fixture failure'); END`); err != nil {
		_ = db.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	provider := &heldSnapshotRetryProvider{entered: make(chan struct{}), release: make(chan struct{})}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	httpServer := httptest.NewServer(handler)
	client, err := apiclient.New(httpServer.URL)
	if err != nil {
		close(provider.release)
		httpServer.Close()
		_ = handler.Close()
		_ = db.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-provider.release:
		default:
			close(provider.release)
		}
		_ = client.Close()
		httpServer.Close()
		if err := handler.Close(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})

	core := manager.Core()
	started, created, err := manager.StartContext(context.Background(), api.StartRunRequest{
		CoreID: core.CoreID, RunID: "snapshot-retry-run", Task: "retry a saved snapshot", Workspace: workspace,
	})
	if err != nil || !created || started.RunID == "" {
		t.Fatalf("start run = %+v created=%t err=%v", started, created, err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not enter active request")
	}
	if started.ExecutionID == "" {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && started.ExecutionID == "" {
			started, err = manager.Get(core.CoreID, started.RunID)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if started.ExecutionID == "" {
		t.Fatal("active run has no execution locator")
	}

	active, err := client.SaveSnapshot(context.Background(), started.ExecutionID, core.CoreID)
	if err != nil || active.State != contracts.SnapshotSaveMemory || active.Frozen {
		t.Fatalf("active snapshot save = %+v err=%v", active, err)
	}
	var requestRows int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM execution_latest_requests WHERE execution_id=?", started.ExecutionID).Scan(&requestRows); err != nil {
		t.Fatal(err)
	}
	if requestRows != 0 {
		t.Fatalf("active save persisted %d request rows", requestRows)
	}

	close(provider.release)
	deadline := time.Now().Add(5 * time.Second)
	var terminal api.RunState
	for time.Now().Before(deadline) {
		terminal, err = manager.Get(core.CoreID, started.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if terminal.Phase == "terminal" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if terminal.Phase != "terminal" || terminal.Result == nil || !terminal.Result.OK || terminal.Result.SnapshotSave.State != contracts.SnapshotSaveFailed {
		t.Fatalf("terminal run after snapshot failure = %+v", terminal)
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER fail_snapshot_request"); err != nil {
		t.Fatal(err)
	}
	retried, err := client.SaveSnapshot(context.Background(), started.ExecutionID, core.CoreID)
	if err != nil || retried.State != contracts.SnapshotSaveSaved || !retried.Frozen {
		t.Fatalf("retry response = %+v err=%v", retried, err)
	}
	updated, err := manager.Get(core.CoreID, started.RunID)
	if err != nil || updated.Result == nil || !updated.Result.OK || updated.Result.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("updated terminal run = %+v err=%v", updated, err)
	}

	inspection, err := client.Inspect(context.Background(), started.ExecutionID, 0, -1)
	if err != nil || inspection.Execution.ID != started.ExecutionID {
		t.Fatalf("GET execution route inspection=%+v err=%v", inspection, err)
	}
	contextView, err := service.SessionContext(context.Background(), terminal.SessionID)
	if err != nil || contextView.LatestRequest == nil || contextView.LatestRequest.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("updated Session context=%+v err=%v", contextView, err)
	}
	record, err := client.Record(context.Background(), started.ExecutionID, contextView.LatestRequest.RecordOrdinal)
	if err != nil || string(record.Content) != "retry-wire-exact" {
		t.Fatalf("GET execution record=%+v err=%v", record, err)
	}
	var requestPayload struct {
		SnapshotSave contracts.SnapshotStatus `json:"snapshotSave"`
	}
	if err := json.Unmarshal(record.Payload, &requestPayload); err != nil || requestPayload.SnapshotSave.State != contracts.SnapshotSaveSaved {
		t.Fatalf("GET execution record status=%+v payload=%s err=%v", requestPayload.SnapshotSave, record.Payload, err)
	}
	beforeRestartRetry, err := store.Records(context.Background(), started.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	restartedService := app.NewService(store, nil, app.RunOptions{Workspace: workspace})
	restartedManager := corerun.New(restartedService, workspace)
	restartedHandler := New(restartedService, restartedManager)
	restartedServer := httptest.NewServer(restartedHandler)
	restartedClient, err := apiclient.New(restartedServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedClient.Close()
	defer restartedServer.Close()
	defer func() {
		if err := restartedHandler.Close(); err != nil {
			t.Error(err)
		}
	}()
	restartedStatus, err := restartedClient.SaveSnapshot(context.Background(), started.ExecutionID, restartedManager.Core().CoreID)
	if err != nil || restartedStatus.State != contracts.SnapshotSaveSaved || !restartedStatus.DetailAvailable || !restartedStatus.Frozen {
		t.Fatalf("post-restart save status=%+v err=%v", restartedStatus, err)
	}
	afterRestartRetry, err := store.Records(context.Background(), started.ExecutionID, 0, -1)
	if err != nil || len(afterRestartRetry) != len(beforeRestartRetry) {
		t.Fatalf("post-restart no-op changed record count: before=%d after=%d err=%v",
			len(beforeRestartRetry), len(afterRestartRetry), err)
	}
}
