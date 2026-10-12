package app

import (
	"context"
	"encoding/json"
	"path/filepath"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/workspacecontext"
)

// prepareWorkspaceContext shares one read with instruction composition, source
// recording and run metadata. It never inserts these files into the VM loader.
func prepareWorkspaceContext(ctx context.Context, workspace string, snapshot *extensions.Snapshot) (workspacecontext.Snapshot, contracts.Orientation, error) {
	loaded, err := workspacecontext.Discover(ctx, workspace)
	if err != nil {
		return loaded, contracts.Orientation{}, err
	}
	reader := false
	for _, tool := range snapshot.Tools {
		reader = reader || tool.Name == "read_file"
	}
	extra := loaded.Instruction(reader)
	if extra != "" {
		snapshot.SystemInstruction += "\n\n" + extra
		snapshot.InstructionComponents = append(append([]json.RawMessage(nil), snapshot.InstructionComponents...),
			contracts.JSON(map[string]string{"identity": "instruction:workspace-context", "text": extra}))
	}
	return loaded, loaded.Orientation(snapshot.BaseInstruction, workspace, snapshot.Agent.Name, "actual", reader), nil
}

// Orientation reads current candidates for a frontend without preparing a run.
func (s *Service) Orientation(ctx context.Context, request RunRequest) (contracts.Orientation, error) {
	options := s.runOptions(request)
	workspace, role := options.Workspace, options.Role
	if options.SessionID != "" {
		session, err := s.store.Session(ctx, options.SessionID)
		if err != nil {
			return contracts.Orientation{}, err
		}
		workspace = session.WorkspaceRoot
		if role == "" {
			var choice struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(session.AgentChoice, &choice); err != nil {
				return contracts.Orientation{}, err
			}
			role = choice.Name
		}
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return contracts.Orientation{}, err
	}
	path, data, err := s.selectionConfig(workspace, options.ConfigPath)
	if err != nil {
		return contracts.Orientation{}, err
	}
	profile, err := extensions.ResolveInstructionProfile(data, path, role)
	if err != nil {
		return contracts.Orientation{}, err
	}
	loaded, err := workspacecontext.Discover(ctx, workspace)
	if err != nil {
		return contracts.Orientation{}, err
	}
	return loaded.Orientation(profile.Base, workspace, profile.Role, "preview", profile.ReadFile), nil
}
