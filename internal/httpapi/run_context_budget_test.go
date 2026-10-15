package httpapi

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

// The run state carries each model step's input measurement to clients over
// the existing run events, without an extra request or model call.
func TestRunStateCarriesContextBudgetOverRunEvents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, snapshotProvider{}, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	server := httptest.NewServer(handler)
	defer func() { server.Close(); _ = handler.Close(); _ = store.Close() }()
	client, err := apiclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	core, err := client.Core(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	updates := 0
	run := client.StartRun(api.StartRunRequest{CoreID: core.CoreID, RunID: "budget-run", Task: "measure input"},
		func(event apiclient.RunEvent) {
			if event.State.ContextBudget != nil {
				updates++
			}
		})
	state, err := run.Wait()
	if err != nil {
		t.Fatal(err)
	}
	budget := state.ContextBudget
	if budget == nil || budget.ExecutionID != state.ExecutionID || budget.ModelStep < 1 ||
		budget.Summary.EstimatedTokens <= 0 || budget.DetailAvailable {
		t.Fatalf("run state context budget: %+v", budget)
	}
	if budget.Summary.Budget.Limit != nil && budget.Summary.Budget.LimitSource == "" {
		t.Fatalf("limit without origin: %+v", budget.Summary.Budget)
	}
	if updates == 0 {
		t.Fatal("no run event carried the context budget")
	}
}
