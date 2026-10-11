package app

import (
	"context"
	"errors"

	"hjh/internal/api"
	"hjh/internal/modelfavorites"
	"hjh/internal/providers"
)

// ModelFavorites resolves the same config scope used by selection operations,
// then reads the saved model IDs for one explicitly selected provider.
func (s *Service) ModelFavorites(ctx context.Context, resolveWorkspace func(context.Context) (string, error), providerID string) (api.ModelFavorites, error) {
	result, err := modelfavorites.Execute(s.favorites, ctx, func(operationCtx context.Context) (api.ModelFavorites, error) {
		configPath, resolveErr := s.modelFavoritesConfig(operationCtx, resolveWorkspace, providerID)
		if resolveErr != nil {
			return api.ModelFavorites{}, resolveErr
		}
		stored, readErr := s.favorites.Get(operationCtx, configPath, providerID)
		if readErr != nil {
			return api.ModelFavorites{}, readErr
		}
		return api.ModelFavorites{ScopeID: stored.ScopeID, ProviderID: stored.ProviderID,
			Revision: stored.Revision, ModelIDs: stored.ModelIDs}, nil
	})
	if errors.Is(err, modelfavorites.ErrClosed) {
		return api.ModelFavorites{}, &api.Error{Code: "core_closed", Message: "Core is closing"}
	}
	return result, err
}

// SetModelFavorite applies the requested value after scope resolution. Once
// the write starts it completes independently of request cancellation.
func (s *Service) SetModelFavorite(ctx context.Context, resolveWorkspace func(context.Context) (string, error), providerID, modelID string, favorite bool) (api.ModelFavoriteResult, error) {
	result, err := modelfavorites.Execute(s.favorites, ctx, func(operationCtx context.Context) (api.ModelFavoriteResult, error) {
		if modelID == "" {
			return api.ModelFavoriteResult{}, &api.Error{Code: "invalid_request", Message: "modelId is required"}
		}
		configPath, resolveErr := s.modelFavoritesConfig(operationCtx, resolveWorkspace, providerID)
		if resolveErr != nil {
			return api.ModelFavoriteResult{}, resolveErr
		}
		if err := operationCtx.Err(); err != nil {
			return api.ModelFavoriteResult{}, err
		}
		stored, saveErr := s.favorites.Set(configPath, providerID, modelID, favorite)
		if saveErr != nil {
			return api.ModelFavoriteResult{}, saveErr
		}
		return api.ModelFavoriteResult{ModelFavorites: api.ModelFavorites{ScopeID: stored.ScopeID,
			ProviderID: stored.ProviderID, Revision: stored.Revision, ModelIDs: stored.ModelIDs},
			ModelID: modelID, Favorite: favorite}, nil
	})
	if errors.Is(err, modelfavorites.ErrClosed) {
		return api.ModelFavoriteResult{}, &api.Error{Code: "core_closed", Message: "Core is closing"}
	}
	return result, err
}

func (s *Service) modelFavoritesConfig(ctx context.Context, resolveWorkspace func(context.Context) (string, error), providerID string) (string, error) {
	if providerID == "" {
		return "", &api.Error{Code: "invalid_request", Message: "providerId is required"}
	}
	if resolveWorkspace == nil {
		return "", &api.Error{Code: "invalid_request", Message: "workspace resolver is required"}
	}
	workspace, err := resolveWorkspace(ctx)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if workspace == "" {
		workspace = s.options.Workspace
	}
	configPath, data, err := s.selectionConfig(workspace, s.options.ConfigPath)
	if err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	registry, err := providers.Parse(data)
	if err != nil {
		return "", err
	}
	if _, err = registry.Definition(providerID); err != nil {
		return "", err
	}
	return configPath, nil
}
