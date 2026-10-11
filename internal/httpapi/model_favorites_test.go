package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

func TestModelFavoritesHTTPClientRoundTrip(t *testing.T) {
	workspace := t.TempDir()
	configPath := filepath.Join(workspace, "config.json")
	config := map[string]any{"providers": map[string]any{
		"provider-a": map[string]any{"api": "responses", "baseURL": "https://provider.example/v1", "path": "/responses"},
	}}
	encodedConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, encodedConfig, 0644); err != nil {
		t.Fatal(err)
	}
	historyStore, err := history.Open(filepath.Join(t.TempDir(), "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(historyStore, nil, app.RunOptions{Workspace: workspace,
		ConfigPath: configPath, ModelFavoritesDir: t.TempDir()})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	httpServer := httptest.NewServer(handler)
	defer func() {
		httpServer.Close()
		_ = handler.Close()
		_ = historyStore.Close()
	}()
	client, err := apiclient.New(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	core, err := client.Core(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope := apiclient.Scope{Workspace: workspace}
	initial, err := client.ModelFavorites(context.Background(), scope, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if initial.ScopeID == "" || initial.Revision != 0 || initial.ModelIDs == nil || len(initial.ModelIDs) != 0 {
		t.Fatalf("initial API favorites = %+v", initial)
	}
	favorite := true
	saved, err := client.SetModelFavorite(context.Background(), api.SetModelFavoriteRequest{CoreID: core.CoreID,
		Workspace: workspace, ProviderID: "provider-a", ModelID: "model-one", Favorite: &favorite})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Favorite || saved.ModelID != "model-one" || saved.Revision != 1 ||
		!reflect.DeepEqual(saved.ModelIDs, []string{"model-one"}) {
		t.Fatalf("save API result = %+v", saved)
	}
	readBack, err := client.ModelFavorites(context.Background(), scope, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if readBack.ScopeID != saved.ScopeID || readBack.Revision != 1 || !reflect.DeepEqual(readBack.ModelIDs, []string{"model-one"}) {
		t.Fatalf("read API result = %+v", readBack)
	}

	falseValue := false
	removed, err := client.SetModelFavorite(context.Background(), api.SetModelFavoriteRequest{CoreID: core.CoreID,
		Workspace: workspace, ProviderID: "provider-a", ModelID: "model-one", Favorite: &falseValue})
	if err != nil {
		t.Fatal(err)
	}
	if removed.Favorite || removed.Revision != 2 || len(removed.ModelIDs) != 0 {
		t.Fatalf("remove API result = %+v", removed)
	}

	wrongCore, err := client.SetModelFavorite(context.Background(), api.SetModelFavoriteRequest{CoreID: "old-core",
		SessionID: "missing-session", ProviderID: "provider-a", ModelID: "model-one", Favorite: &favorite})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "core_changed" {
		t.Fatalf("wrong Core request error = %v", err)
	}
	if wrongCore.ModelID != "" {
		t.Fatalf("wrong Core unexpectedly returned a result: %+v", wrongCore)
	}
}
