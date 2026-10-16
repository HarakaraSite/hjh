package corerun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/extensions"
)

func TestCoreWaitsForLifecycleCleanupBeforeTerminalAndShutdown(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(entered)
		<-release
		_, _ = w.Write([]byte("cleanup finished"))
	}))
	defer server.Close()
	defer unblock()
	m, service := productManager(t, &observedProvider{}, app.RunOptions{})
	dir := filepath.Join(m.Core().Workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	source := `import * as fs from "node:fs";
import * as path from "node:path";
export async function afterTurn(){await (await fetch(` + string(contracts.JSON(server.URL)) + `)).text();}
export async function runtimeStop({runtime},api){await fs.promises.writeFile(path.resolve(process.cwd(),'stop-'+runtime.executionId),'STOP_FINISHED');}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	state, _, err := m.Start(api.StartRunRequest{CoreID: m.Core().CoreID, RunID: uuid.NewString(), Task: "wait for afterTurn"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("afterTurn did not start")
	}
	state = awaitRun(t, m, state.RunID, func(s api.RunState) bool { return s.ExecutionID != "" })
	inspection, err := service.Inspect(context.Background(), state.ExecutionID, 0, -1)
	if err != nil || inspection.Execution.Lifecycle != "settled" || inspection.Execution.Adoption != "canonical" || state.Phase == "terminal" {
		t.Fatalf("turn must be saved/adopted while run waits for hook: %+v state=%+v err=%v", inspection.Execution, state, err)
	}
	if _, _, err := m.Start(api.StartRunRequest{CoreID: m.Core().CoreID, RunID: uuid.NewString(), SessionID: state.SessionID, Task: "next turn"}); err == nil {
		t.Fatal("next turn started before cleanup finished")
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case <-closed:
		t.Fatal("Core stopped before its hook finished")
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Core failed to finish shutdown after hook completed")
	}
	if body, err := os.ReadFile(filepath.Join(m.Core().Workspace, "stop-"+state.ExecutionID)); err != nil || string(body) != "STOP_FINISHED" {
		t.Fatalf("runtimeStop was not awaited: %s %v", body, err)
	}
}
