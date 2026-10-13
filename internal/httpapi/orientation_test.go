package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

func TestOrientationHTTPBeforeSessionAndAttachActualContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("HTTP_WORKSPACE_INSTRUCTION"), 0644); err != nil {
		t.Fatal(err)
	}
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
	preview, err := client.Orientation(context.Background(), api.OrientationRequest{})
	if err != nil || preview.Mode != "preview" || len(preview.Instructions) == 0 || preview.Base.BuiltinVersion == "" {
		t.Fatalf("orientation HTTP: %+v %v", preview, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".hjh", "extensions")); !os.IsNotExist(err) {
		t.Fatalf("query installed extensions: %v", err)
	}
	sessions, _ := store.ListSessions(context.Background())
	if len(sessions) != 0 {
		t.Fatal("query created session")
	}
	core, _ := client.Core(context.Background())
	basePath := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hjh", "instructions", "base.md")
	if err := os.MkdirAll(filepath.Dir(basePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, []byte("HTTP_BASE_ONE"), 0644); err != nil {
		t.Fatal(err)
	}
	externalPreview, err := client.Orientation(context.Background(), api.OrientationRequest{})
	if err != nil || externalPreview.Base.Path != basePath || externalPreview.Base.Digest != extensions.Digest([]byte("HTTP_BASE_ONE")) || externalPreview.Base.BuiltinVersion != "" {
		t.Fatalf("external base preview HTTP: %+v %v", externalPreview.Base, err)
	}
	run := client.StartRun(api.StartRunRequest{CoreID: core.CoreID, RunID: "context-run", Task: "confirm instruction"}, nil)
	state, err := run.Wait()
	if err != nil || state.ActualContext == nil || state.ActualContext.Mode != "actual" {
		t.Fatalf("actual context: %+v %v", state, err)
	}
	if state.ActualContext.Base != externalPreview.Base {
		t.Fatalf("run actual base differs from preview: %+v", state.ActualContext.Base)
	}
	if err := os.WriteFile(basePath, []byte("HTTP_BASE_TWO"), 0644); err != nil {
		t.Fatal(err)
	}
	attached := client.AttachRun(core.CoreID, state.RunID, nil)
	reattached, err := attached.Wait()
	if err != nil || reattached.ActualContext == nil || len(reattached.ActualContext.Instructions) == 0 {
		t.Fatalf("reconnect context: %+v %v", reattached, err)
	}
	if reattached.ActualContext.Base != externalPreview.Base {
		t.Fatal("attach re-read base.md instead of returning the captured actual source")
	}
	updatedPreview, err := client.Orientation(context.Background(), api.OrientationRequest{})
	if err != nil || updatedPreview.Base.Digest != extensions.Digest([]byte("HTTP_BASE_TWO")) {
		t.Fatalf("new preview did not read edited base: %+v %v", updatedPreview.Base, err)
	}
	before, _ := json.Marshal(state.ActualContext)
	state.ActualContext.Instructions[0].Path = "changed-client-copy"
	current, err := manager.Get(core.CoreID, state.RunID)
	after, _ := json.Marshal(current.ActualContext)
	if err != nil || string(before) != string(after) {
		t.Fatal("client metadata mutated manager state")
	}
}
