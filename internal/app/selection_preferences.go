package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"hjh/internal/contracts"
)

// The workspace owns its remembered next-session selection. This is display
// configuration, separate from the active model stored with each Session.
func readRememberedSelection(workspace string) (contracts.ModelSelection, error) {
	path := filepath.Join(workspace, ".hjh", "selection.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return contracts.ModelSelection{}, nil
	}
	if err != nil {
		return contracts.ModelSelection{}, fmt.Errorf("read remembered model selection: %w", err)
	}
	var selection contracts.ModelSelection
	if err := json.Unmarshal(data, &selection); err != nil {
		return contracts.ModelSelection{}, fmt.Errorf("decode remembered model selection: %w", err)
	}
	return selection, nil
}

// RememberSelection resolves and saves an explicit human selection without
// starting a run or changing the current Session's active model.
func (s *Service) RememberSelection(ctx context.Context, request RunRequest) (SelectionPreview, error) {
	s.selectionMu.Lock()
	defer s.selectionMu.Unlock()
	preview, err := s.PreviewSelection(ctx, request)
	if err != nil {
		return SelectionPreview{}, err
	}
	workspace := s.runOptions(request).Workspace
	if request.SessionID != "" {
		session, err := s.store.Session(ctx, request.SessionID)
		if err != nil {
			return SelectionPreview{}, err
		}
		workspace = session.WorkspaceRoot
	}
	if err := ctx.Err(); err != nil {
		return SelectionPreview{}, err
	}
	dir := filepath.Join(workspace, ".hjh")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return SelectionPreview{}, fmt.Errorf("create model selection directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "selection-*.tmp")
	if err != nil {
		return SelectionPreview{}, err
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(preview.Selection); err != nil {
		_ = file.Close()
		return SelectionPreview{}, err
	}
	if err := file.Close(); err != nil {
		return SelectionPreview{}, err
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, "selection.json")); err != nil {
		return SelectionPreview{}, fmt.Errorf("save remembered model selection: %w", err)
	}
	return preview, nil
}
