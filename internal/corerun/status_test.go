package corerun

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
)

func TestCoreStatusShowsActiveRunAndShutdown(t *testing.T) {
	provider := &observedProvider{entered: make(chan struct{}), block: true}
	manager, _ := productManager(t, provider, app.RunOptions{})
	request := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: uuid.NewString(), Task: "status during an active task"}
	if _, _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not enter provider")
	}
	status := manager.Status()
	if status.CoreID != request.CoreID || status.Phase != "running" || len(status.ActiveRuns) != 1 {
		t.Fatalf("Core status = %+v", status)
	}
	active := status.ActiveRuns[0]
	if active.RunID != request.RunID || active.SessionID == "" || active.ExecutionID == "" || active.Phase != "running" {
		t.Fatalf("active run = %+v", active)
	}
	if err := manager.BeginShutdown(request.CoreID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	status = manager.Status()
	if status.Phase != "stopping" || len(status.ActiveRuns) != 0 {
		t.Fatalf("joined Core status = %+v", status)
	}
}
