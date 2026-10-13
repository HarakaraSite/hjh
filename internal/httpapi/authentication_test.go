package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

// The public registration operation must supply the actual catalog and run,
// preserve external key files, and remain usable after a Core restart.
func TestAuthenticationRegistrationCatalogRunAndRestart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	configDir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(configDir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	external := filepath.Join(workspace, "external-key")
	if err := os.WriteFile(external, []byte("test-external-key"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HJH_I12_TEST_KEY", "test-env-key")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-registered-key" {
			t.Error("catalog/run did not use registered key")
		}
		if r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"model","name":"Model"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"model\":\"model\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"AUTH_REGISTERED_OK\"}]}]}}\n\n"))
	}))
	defer upstream.Close()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	provider := func(profile string) any {
		return map[string]any{"api": "responses", "baseURL": upstream.URL, "path": "/responses", "defaultModel": "model", "auth": map[string]string{"kind": "api-key", "profile": profile}, "catalog": map[string]string{"kind": "openai", "path": "/models"}}
	}
	config["providers"] = map[string]any{"a": provider("shared"), "b": provider("shared")}
	config["credentials"] = map[string]any{"shared": map[string]string{"env": "HJH_I12_TEST_KEY", "file": external}, "role-key": map[string]string{}, "unused": map[string]string{}}
	config["model"] = map[string]string{"provider": "a", "modelId": "model"}
	config["roles"].(map[string]any)["reviewer"].(map[string]any)["model"] = map[string]string{"authProfile": "role-key"}
	data, _ = json.Marshal(config)
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	start := func() (*apiclient.Client, *app.Service, func()) {
		service := app.NewService(store, nil, app.RunOptions{Workspace: workspace, ConfigPath: configPath})
		handler := New(service, corerun.New(service, workspace))
		server := httptest.NewServer(handler)
		client, err := apiclient.New(server.URL)
		if err != nil {
			t.Fatal(err)
		}
		return client, service, func() { client.Close(); server.Close(); handler.Close() }
	}
	client, service, closeCore := start()
	profiles, err := client.AuthProfiles(t.Context(), api.SelectionRequest{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles.Profiles) != 2 || profiles.Target.AuthProfile != "shared" {
		t.Fatalf("profiles/target=%+v", profiles)
	}
	var shared api.AuthProfile
	for _, p := range profiles.Profiles {
		if p.AuthProfile == "shared" {
			shared = p
		}
	}
	if len(shared.Providers) != 2 || shared.APIKey.Source != "env" || shared.APIKey.Registered {
		t.Fatalf("initial shared profile=%+v", shared)
	}
	core, err := client.Core(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	request := api.AuthRequest{CoreID: core.CoreID, Workspace: workspace, ProviderID: "a", AuthProfile: "shared", MutationID: "registration", Value: "test-registered-key"}
	saved, err := client.RegisterAPIKey(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Registered || saved.Source != "registered" || saved.Revision != 1 || saved.MutationID != "registration" {
		t.Fatalf("saved=%+v", saved)
	}
	saved, err = client.RegisterAPIKey(t.Context(), request)
	if err != nil || saved.Revision != 1 {
		t.Fatalf("registration replay=%+v err=%v", saved, err)
	}
	catalog, err := client.ModelsWithAuth(t.Context(), apiclient.Scope{Workspace: workspace}, "b", "shared")
	if err != nil || len(catalog.Models) != 1 || catalog.AuthOwnerID == "" {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	result, err := service.Run(t.Context(), app.RunRequest{Task: "reply", Workspace: workspace})
	if err != nil || !result.Result.OK || result.Result.FinalText != "AUTH_REGISTERED_OK" {
		t.Fatalf("registered run=%+v err=%v", result, err)
	}
	if current, err := os.ReadFile(external); err != nil || string(current) != "test-external-key" {
		t.Fatal("external credential file changed")
	}
	closeCore()
	reopened, _, closeReopened := start()
	defer closeReopened()
	profiles, err = reopened.AuthProfiles(context.Background(), api.SelectionRequest{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(profiles)
	if strings.Contains(string(encoded), "test-registered-key") {
		t.Fatal("secret leaked through profile metadata")
	}
	for _, p := range profiles.Profiles {
		if p.AuthProfile == "shared" && (!p.APIKey.Registered || p.APIKey.Revision != 1) {
			t.Fatalf("registration not restored: %+v", p)
		}
	}
	if _, err = reopened.ModelsWithAuth(t.Context(), apiclient.Scope{Workspace: workspace}, "a", "shared"); err != nil {
		t.Fatal(err)
	}
}
