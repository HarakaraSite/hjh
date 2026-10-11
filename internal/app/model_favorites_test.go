package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hjh/internal/api"
)

func TestModelFavoritesUseConfigPathAndProviderAndSurviveServiceClose(t *testing.T) {
	favoritesDir := t.TempDir()
	workspaceA := t.TempDir()
	workspaceB := t.TempDir()
	writeFavoriteConfig(t, workspaceA)
	writeFavoriteConfig(t, workspaceB)

	service := NewService(nil, nil, RunOptions{Workspace: workspaceA, ModelFavoritesDir: favoritesDir})
	resolveA := func(context.Context) (string, error) { return workspaceA, nil }
	resolveB := func(context.Context) (string, error) { return workspaceB, nil }
	initial, err := service.ModelFavorites(context.Background(), resolveA, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 0 || initial.ModelIDs == nil || len(initial.ModelIDs) != 0 {
		t.Fatalf("initial favorites = %+v", initial)
	}
	set, err := service.SetModelFavorite(context.Background(), resolveA, "provider-a", "model-one", true)
	if err != nil {
		t.Fatal(err)
	}
	if set.Favorite != true || set.ModelID != "model-one" || set.Revision != 1 {
		t.Fatalf("set result = %+v", set)
	}

	otherProvider, err := service.ModelFavorites(context.Background(), resolveA, "provider-b")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherProvider.ModelIDs) != 0 || otherProvider.Revision != 1 {
		t.Fatalf("other provider favorites = %+v", otherProvider)
	}
	otherWorkspace, err := service.ModelFavorites(context.Background(), resolveB, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherWorkspace.ModelIDs) != 0 || otherWorkspace.Revision != 0 || otherWorkspace.ScopeID == initial.ScopeID {
		t.Fatalf("other config favorites = %+v", otherWorkspace)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := NewService(nil, nil, RunOptions{Workspace: workspaceA, ModelFavoritesDir: favoritesDir})
	persisted, err := reopened.ModelFavorites(context.Background(), resolveA, "provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Revision != 1 || !reflect.DeepEqual(persisted.ModelIDs, []string{"model-one"}) {
		t.Fatalf("favorites after service restart = %+v", persisted)
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestModelFavoritesRejectOperationsAfterCloseBegins(t *testing.T) {
	workspace := t.TempDir()
	writeFavoriteConfig(t, workspace)
	service := NewService(nil, nil, RunOptions{Workspace: workspace, ModelFavoritesDir: t.TempDir()})
	service.BeginModelFavoritesClose()
	_, err := service.ModelFavorites(context.Background(), func(context.Context) (string, error) {
		return workspace, nil
	}, "provider-a")
	var coded *api.Error
	if !errors.As(err, &coded) || coded.Code != "core_closed" {
		t.Fatalf("closed favorite request error = %v", err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeFavoriteConfig(t *testing.T, workspace string) {
	t.Helper()
	path := filepath.Join(workspace, ".hjh", "extensions", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"providers": map[string]any{
		"provider-a": map[string]any{"api": "responses", "baseURL": "https://provider.example/v1", "path": "/responses"},
		"provider-b": map[string]any{"api": "responses", "baseURL": "https://provider.example/v1", "path": "/responses"},
	}}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, encoded, 0644); err != nil {
		t.Fatal(err)
	}
}
