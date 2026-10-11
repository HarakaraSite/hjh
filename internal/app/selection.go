package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/providers"
)

// SelectionPreview reports the effective next-run role and model selection.
// It is metadata only: producing it does not prepare a run or mutate history.
type SelectionPreview struct {
	Role      string                   `json:"role"`
	Selection contracts.ModelSelection `json:"selection"`
}

// PreviewSelection applies the same role and model resolution used by Run
// without capturing extension sources, creating a Session, or resolving a
// credential.
func (s *Service) PreviewSelection(ctx context.Context, request RunRequest) (SelectionPreview, error) {
	if err := ctx.Err(); err != nil {
		return SelectionPreview{}, err
	}
	options := s.runOptions(request)
	workspace := options.Workspace
	var active contracts.ModelSelection
	if options.SessionID != "" {
		session, err := s.store.Session(ctx, options.SessionID)
		if err != nil {
			return SelectionPreview{}, err
		}
		workspace = session.WorkspaceRoot
		if err = json.Unmarshal(session.ActiveModel, &active); err != nil {
			return SelectionPreview{}, err
		}
		if options.Role == "" {
			var choice struct {
				Name string `json:"name"`
			}
			if err = json.Unmarshal(session.AgentChoice, &choice); err != nil {
				return SelectionPreview{}, err
			}
			options.Role = choice.Name
		}
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return SelectionPreview{}, err
	}
	configPath, data, err := s.selectionConfig(workspace, options.ConfigPath)
	if err != nil {
		return SelectionPreview{}, err
	}
	role, err := extensions.ResolveRole(data, configPath, options.Role)
	if err != nil {
		return SelectionPreview{}, err
	}
	registry, err := providers.Parse(data)
	if err != nil {
		return SelectionPreview{}, err
	}
	selection, err := resolveModelSelection(registry, role, options.SessionID != "", active, options)
	if err != nil {
		return SelectionPreview{}, err
	}
	return SelectionPreview{Role: role, Selection: selection}, nil
}

func (s *Service) selectionConfig(workspace, configPath string) (string, []byte, error) {
	if configPath != "" {
		path, err := filepath.Abs(configPath)
		if err != nil {
			return "", nil, err
		}
		data, err := os.ReadFile(path)
		return path, data, err
	}
	path, err := filepath.Abs(filepath.Join(workspace, ".hjh", "extensions", "config.json"))
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		data, err = extensions.EmbeddedConfig()
	}
	return path, data, err
}

func resolveModelSelection(registry *providers.Registry, role string, resumed bool, active contracts.ModelSelection, options RunOptions) (contracts.ModelSelection, error) {
	base := active
	if !resumed {
		var err error
		base, err = registry.Resolve(contracts.ModelSelection{}, registry.Config.Model)
		if err != nil {
			return contracts.ModelSelection{}, err
		}
		base, err = registry.Resolve(base, registry.Config.Roles[role].Model)
		if err != nil {
			return contracts.ModelSelection{}, err
		}
	}
	return registry.Resolve(base, contracts.ModelSelection{
		Provider: options.ProviderID, ModelID: options.ModelID,
		AuthProfile: options.AuthProfile, Effort: options.Effort,
	})
}
