package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"hjh/internal/contracts"
)

func TestRememberedSelectionSurvivesServiceRestartAndMatchesActualNewRun(t *testing.T) {
	store, workspace := delegationStore(t)
	configPath := writeSelectionConfig(t, workspace)
	var actual contracts.ModelSelection
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		actual = req.Model
		return contracts.ModelResult{Text: "selection confirmed"}, nil
	})
	options := RunOptions{Workspace: workspace, ConfigPath: configPath}
	service := NewService(store, provider, options)
	first, err := service.Run(t.Context(), RunRequest{Task: "original", ProviderID: "global", ModelID: "original-model", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	original := actual
	chosen, err := service.RememberSelection(t.Context(), RunRequest{SessionID: first.SessionID, ProviderID: "reviewer", ModelID: "chosen-model", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if chosen.Selection.Provider != "reviewer" || chosen.Selection.ModelID != "chosen-model" || chosen.Selection.Effort != "high" {
		t.Fatalf("remembered choice=%+v", chosen)
	}
	if sessions, err := service.Sessions(t.Context()); err != nil || len(sessions) != 1 {
		t.Fatalf("remembering a choice created a Session: %v, %v", sessions, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service = NewService(store, provider, options)
	defer service.Close()
	preview, err := service.PreviewSelection(t.Context(), RunRequest{})
	if err != nil || preview.Selection != chosen.Selection {
		t.Fatalf("new Session after restart=%+v, %v", preview, err)
	}
	changed, err := service.PreviewSelection(t.Context(), RunRequest{ModelID: "different-model"})
	if err != nil || changed.Selection.Effort != "auto" {
		t.Fatalf("model-only change inherited previous effort: %+v, %v", changed, err)
	}
	same, err := service.PreviewSelection(t.Context(), RunRequest{ModelID: chosen.Selection.ModelID})
	if err != nil || same.Selection.Effort != chosen.Selection.Effort {
		t.Fatalf("same model lost remembered effort: %+v, %v", same, err)
	}
	explicit, err := service.PreviewSelection(t.Context(), RunRequest{ModelID: "different-model", Effort: "medium"})
	if err != nil || explicit.Selection.Effort != "medium" {
		t.Fatalf("explicit effort lost on model change: %+v, %v", explicit, err)
	}
	resumed, err := service.PreviewSelection(t.Context(), RunRequest{SessionID: first.SessionID})
	if err != nil || resumed.Selection != original {
		t.Fatalf("saved Session selection changed=%+v, %v", resumed, err)
	}
	if _, err := service.Run(t.Context(), RunRequest{Task: "new"}); err != nil || actual != chosen.Selection {
		t.Fatalf("actual new run differs from remembered preview: %+v, %v", actual, err)
	}
	override, err := service.PreviewSelection(t.Context(), RunRequest{ProviderID: "writer"})
	if err != nil || override.Selection.Provider != "writer" || override.Selection.ModelID != "writer-default" || override.Selection.Effort != "writer-default-effort" {
		t.Fatalf("explicit provider did not use its own defaults: %+v, %v", override, err)
	}
	// Workspace, rather than the config file, owns this preference.
	otherWorkspace := t.TempDir()
	other, err := service.PreviewSelection(t.Context(), RunRequest{Workspace: otherWorkspace})
	if err != nil || other.Selection.ModelID != "writer-role-model" {
		t.Fatalf("choice leaked to another workspace: %+v, %v", other, err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, ".hjh", "selection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted contracts.ModelSelection
	if err := json.Unmarshal(data, &persisted); err != nil || persisted != chosen.Selection {
		t.Fatalf("preview/run override changed remembered choice: %+v, %v", persisted, err)
	}
}
