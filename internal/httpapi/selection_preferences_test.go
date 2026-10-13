package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

func TestRememberSelectionHTTPPersistsWithoutRunAndSurvivesCoreRestart(t *testing.T) {
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, "config.json")
	config := `{"defaultRole":"default","model":{"provider":"local","modelId":"original","effort":"auto"},"providers":{"local":{"api":"responses","auth":{"kind":"none"},"defaultModel":"original"}},"roles":{"default":{"tools":[]}}}`
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := func() (*apiclient.Client, string, func()) {
		service := app.NewService(store, nil, app.RunOptions{Workspace: workspace, ConfigPath: configPath})
		handler := New(service, corerun.New(service, workspace))
		server := httptest.NewServer(handler)
		client, err := apiclient.New(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		core, err := client.Core(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return client, core.CoreID, func() { client.Close(); server.Close(); handler.Close(); service.Close() }
	}
	client, coreID, closeCore := start()
	saved, err := client.RememberSelection(t.Context(), api.RememberSelectionRequest{CoreID: coreID, SelectionRequest: api.SelectionRequest{Workspace: workspace, ModelID: "chosen-model", Effort: "high"}})
	closeCore()
	if err != nil || saved.Selection.ModelID != "chosen-model" || saved.Selection.Effort != "high" {
		t.Fatalf("save through public API=%+v, %v", saved, err)
	}
	client, _, closeCore = start()
	defer closeCore()
	preview, err := client.Selection(t.Context(), api.SelectionRequest{Workspace: workspace})
	if err != nil || preview != saved {
		t.Fatalf("new Core preview=%+v, %v", preview, err)
	}
	sessions, err := store.ListSessions(t.Context())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("selection save started a Session: %+v, %v", sessions, err)
	}
}
