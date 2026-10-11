package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

func TestPreviewSelectionMatchesNewAndResumedRunResolution(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	configPath := writeSelectionConfig(t, workspace)
	responseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]any{"content": "selection fixture"}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer responseServer.Close()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(store, chatFixtureProvider(responseServer.URL, "unused", "selection-fixture", protocol.Options{}), RunOptions{
		Workspace: workspace, ConfigPath: configPath, Role: "reviewer",
	})
	defer service.Close()

	globalPreview, err := service.PreviewSelection(ctx, RunRequest{Workspace: workspace, Role: "inherit"})
	if err != nil {
		t.Fatal(err)
	}
	wantGlobal := contracts.ModelSelection{Provider: "global", API: "responses", AuthProfile: "global-profile",
		ModelID: "global-model", Effort: "global-effort"}
	if globalPreview.Role != "inherit" || globalPreview.Selection != wantGlobal {
		t.Fatalf("global selection preview = %+v, want %+v", globalPreview, wantGlobal)
	}
	roleModelPreview, err := service.PreviewSelection(ctx, RunRequest{Workspace: workspace, Role: "reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	wantRoleModel := contracts.ModelSelection{Provider: "reviewer", API: "responses", AuthProfile: "reviewer-profile",
		ModelID: "reviewer-role-model", Effort: "reviewer-effort"}
	if roleModelPreview.Selection != wantRoleModel {
		t.Fatalf("role model preview = %+v, want %+v", roleModelPreview.Selection, wantRoleModel)
	}

	newRequest := RunRequest{Task: "first", Workspace: workspace, ModelID: "freeform/new-model"}
	preview, err := service.PreviewSelection(ctx, newRequest)
	if err != nil {
		t.Fatal(err)
	}
	wantNew := contracts.ModelSelection{Provider: "reviewer", API: "responses", AuthProfile: "reviewer-profile",
		ModelID: "freeform/new-model", Effort: "reviewer-effort"}
	if preview.Role != "reviewer" || preview.Selection != wantNew {
		t.Fatalf("new preview = %+v, want role reviewer / %+v", preview, wantNew)
	}
	first, err := service.Run(ctx, newRequest)
	if err != nil {
		t.Fatal(err)
	}
	firstSession, err := service.Session(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var firstActive contracts.ModelSelection
	if err = json.Unmarshal(firstSession.ActiveModel, &firstActive); err != nil {
		t.Fatal(err)
	}
	if firstActive != preview.Selection {
		t.Fatalf("new run saved selection %+v, preview was %+v", firstActive, preview.Selection)
	}

	resumeRequest := RunRequest{Task: "second", SessionID: first.SessionID, Role: "writer", ModelID: "freeform/resumed-model"}
	savedRolePreview, err := service.PreviewSelection(ctx, RunRequest{SessionID: first.SessionID, ModelID: "freeform/resumed-model"})
	if err != nil {
		t.Fatal(err)
	}
	if savedRolePreview.Role != "reviewer" || savedRolePreview.Selection.Provider != "reviewer" {
		t.Fatalf("resumed saved-role preview = %+v", savedRolePreview)
	}
	resumePreview, err := service.PreviewSelection(ctx, resumeRequest)
	if err != nil {
		t.Fatal(err)
	}
	wantResume := contracts.ModelSelection{Provider: "reviewer", API: "responses", AuthProfile: "reviewer-profile",
		ModelID: "freeform/resumed-model", Effort: "reviewer-effort"}
	if resumePreview.Role != "writer" || resumePreview.Selection != wantResume {
		t.Fatalf("resumed preview = %+v, want role writer / %+v", resumePreview, wantResume)
	}
	second, err := service.Run(ctx, resumeRequest)
	if err != nil {
		t.Fatal(err)
	}
	executions, err := service.Executions(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 2 || executions[1].ID != second.ExecutionID || executions[1].AgentName != "writer" {
		t.Fatalf("resumed execution role/identity = %+v", executions)
	}
	var resumedModel contracts.ModelSelection
	if err = json.Unmarshal(executions[1].Model, &resumedModel); err != nil {
		t.Fatal(err)
	}
	if resumedModel != resumePreview.Selection {
		t.Fatalf("resumed run used selection %+v, preview was %+v", resumedModel, resumePreview.Selection)
	}
}

func TestPreviewSelectionReadsEmbeddedMetadataWithoutPreparingWorkspace(t *testing.T) {
	workspace := t.TempDir()
	extensionsDir := filepath.Join(workspace, ".hjh", "extensions")
	if err := os.MkdirAll(extensionsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-module.js", filepath.Join(extensionsDir, "index.js")); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(store, nil, RunOptions{Workspace: workspace})
	defer service.Close()

	preview, err := service.PreviewSelection(context.Background(), RunRequest{Workspace: workspace})
	if err != nil {
		t.Fatalf("metadata-only preview failed on uncaptured module: %v", err)
	}
	if preview.Role != "default" || preview.Selection.Provider != "openrouter-responses" {
		t.Fatalf("embedded preview = %+v", preview)
	}
	providers, err := service.Providers(context.Background(), workspace)
	if err != nil || len(providers) == 0 {
		t.Fatalf("uninitialized provider catalog = %v, %v", providers, err)
	}
	models, err := service.Models(context.Background(), workspace, preview.Selection.Provider, preview.Selection.AuthProfile)
	if err != nil || len(models) == 0 {
		t.Fatalf("uninitialized static model catalog = %v, %v", models, err)
	}
	if _, err := os.Stat(filepath.Join(extensionsDir, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("preview installed the editable config: stat err=%v", err)
	}
	entries, err := os.ReadDir(extensionsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "index.js" {
		t.Fatalf("preview prepared extension files: %v", entries)
	}
	sessions, err := service.Sessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("preview created Session rows %v: %v", sessions, err)
	}
}

func TestModelsUseRequestedProfileAndExposeConfirmedEfforts(t *testing.T) {
	t.Setenv("HJH_I08_DEFAULT_TOKEN", "default-token")
	t.Setenv("HJH_I08_SELECTED_TOKEN", "selected-token")
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, "providers.json")
	wantToken := "selected-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("catalog authorization = %q, want selected profile", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"slug":"gpt-6.1-sol","display_name":"GPT 6.1 Sol","visibility":"list","supported_reasoning_levels":[{"description":"Low","effort":"low"},{"description":"High","effort":"high"}]},{"slug":"private-model","display_name":"Private","visibility":"private","supported_reasoning_levels":[{"description":"Low","effort":"low"}]}]}`)
	}))
	defer server.Close()
	config := fmt.Sprintf(`{
"defaultRole":"default",
"credentials":{"default":{"env":"HJH_I08_DEFAULT_TOKEN"},"selected":{"env":"HJH_I08_SELECTED_TOKEN"}},
"providers":{
  "static":{"api":"responses","baseURL":"%s","auth":{"kind":"api-key","profile":"default"},"defaultModel":"static-model","models":[{"id":"static-model","name":"Static","efforts":["minimal","high"]}]},
  "account":{"api":"responses","baseURL":"%s","auth":{"kind":"api-key","profile":"default"},"defaultModel":"gpt-6.1-sol","catalog":{"kind":"openai-account","path":"/models"}}
},"roles":{"default":{"tools":[]}}}`, server.URL, server.URL)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, nil, RunOptions{Workspace: workspace, ConfigPath: configPath})
	defer service.Close()

	dynamic, err := service.Models(context.Background(), workspace, "account", "selected")
	if err != nil {
		t.Fatal(err)
	}
	if len(dynamic) != 1 || dynamic[0].ID != "gpt-6.1-sol" || dynamic[0].EffortSource != "catalog" || !reflect.DeepEqual(dynamic[0].Efforts, []string{"low", "high"}) {
		t.Fatalf("dynamic model catalog = %+v", dynamic)
	}
	static, err := service.Models(context.Background(), workspace, "static", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(static) != 1 || static[0].EffortSource != "config" || !reflect.DeepEqual(static[0].Efforts, []string{"minimal", "high"}) {
		t.Fatalf("static model catalog = %+v", static)
	}

	keyFile := filepath.Join(workspace, "catalog-key")
	if err := os.WriteFile(keyFile, []byte("override-token"), 0600); err != nil {
		t.Fatal(err)
	}
	overridden := NewService(nil, nil, RunOptions{Workspace: workspace, ConfigPath: configPath, CredentialFile: keyFile})
	defer overridden.Close()
	wantToken = "override-token"
	if models, err := overridden.Models(context.Background(), workspace, "account", "selected"); err != nil || len(models) != 1 {
		t.Fatalf("catalog with run credential-file = %+v, %v", models, err)
	}
}

func writeSelectionConfig(t *testing.T, workspace string) string {
	t.Helper()
	path := filepath.Join(workspace, ".hjh", "extensions", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	config := `{
"defaultRole":"writer",
"model":{"provider":"global","modelId":"global-model","effort":"global-effort","authProfile":"global-profile"},
"providers":{
  "global":{"api":"responses","auth":{"kind":"api-key","profile":"global-profile"},"defaultModel":"global-default","defaultEffort":"global-default-effort"},
  "writer":{"api":"chat-completions","auth":{"kind":"api-key","profile":"writer-profile"},"defaultModel":"writer-default","defaultEffort":"writer-default-effort"},
  "reviewer":{"api":"responses","auth":{"kind":"api-key","profile":"reviewer-profile"},"defaultModel":"reviewer-default","defaultEffort":"reviewer-default-effort"}
},"roles":{
  "writer":{"model":{"provider":"writer","modelId":"writer-role-model","effort":"writer-effort","authProfile":"writer-profile"},"tools":[]},
  "reviewer":{"model":{"provider":"reviewer","modelId":"reviewer-role-model","effort":"reviewer-effort","authProfile":"reviewer-profile"},"tools":[]},
  "inherit":{"tools":[]}
}}`
	if err := os.WriteFile(path, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
