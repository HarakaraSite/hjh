package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/providers"
)

func (s *Service) authTarget(ctx context.Context, workspace, providerID, profile string) (credentials.Target, error) {
	if err := ctx.Err(); err != nil {
		return credentials.Target{}, err
	}
	if workspace == "" {
		workspace = s.options.Workspace
	}
	path, data, err := s.selectionConfig(workspace, s.options.ConfigPath)
	if err != nil {
		return credentials.Target{}, err
	}
	registry, err := providers.Parse(data)
	if err != nil {
		return credentials.Target{}, err
	}
	if providerID == "" {
		providerID = registry.Config.Model.Provider
	}
	definition, err := registry.Definition(providerID)
	if err != nil {
		return credentials.Target{}, err
	}
	if profile == "" {
		profile = definition.Auth.Profile
	}
	return credentials.Target{ConfigPath: path, ProviderID: providerID, Name: profile, Kind: definition.Auth.Kind,
		Profile: registry.Config.Credentials[profile], OverrideFile: s.options.CredentialFile}, nil
}

func (s *Service) authResolver(workspace, providerID, profile string) credentials.ResolveTarget {
	return func(ctx context.Context) (credentials.Target, error) {
		return s.authTarget(ctx, workspace, providerID, profile)
	}
}

func (s *Service) AuthProfiles(ctx context.Context, request api.SelectionRequest) (api.AuthProfiles, error) {
	var result api.AuthProfiles
	var rows map[string]*api.AuthProfile
	states, err := s.connections.credentials.Inspect(ctx, func(operationCtx context.Context) ([]credentials.Target, error) {
		workspace := request.Workspace
		if workspace == "" {
			workspace = s.options.Workspace
		}
		preview, err := s.PreviewSelection(operationCtx, RunRequest{SessionID: request.SessionID, Workspace: workspace,
			Role: request.Role, ProviderID: request.ProviderID, ModelID: request.ModelID, Effort: request.Effort, AuthProfile: request.AuthProfile})
		if err != nil {
			return nil, err
		}
		path, data, err := s.selectionConfig(workspace, s.options.ConfigPath)
		if err != nil {
			return nil, err
		}
		registry, err := providers.Parse(data)
		if err != nil {
			return nil, err
		}
		definition, err := registry.Definition(preview.Selection.Provider)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(path))
		result.ScopeID = hex.EncodeToString(digest[:])
		result.Target = api.AuthTarget{ProviderID: definition.ID, AuthProfile: preview.Selection.AuthProfile, Kind: definition.Auth.Kind}
		rows = make(map[string]*api.AuthProfile)
		seen := make(map[string]bool)
		var targets []credentials.Target
		add := func(selection contracts.ModelSelection) error {
			d, err := registry.Definition(selection.Provider)
			if err != nil {
				return err
			}
			if d.Auth.Kind == credentials.KindNone {
				return nil
			}
			profile := selection.AuthProfile
			if profile == "" {
				profile = d.Auth.Profile
			}
			row := rows[profile]
			if row == nil {
				row = &api.AuthProfile{AuthProfile: profile}
				rows[profile] = row
			}
			providerSeen := false
			for _, p := range row.Providers {
				if p.ID == d.ID {
					providerSeen = true
				}
			}
			if !providerSeen {
				row.Providers = append(row.Providers, api.AuthProvider{ID: d.ID, DisplayName: d.DisplayName, Kind: d.Auth.Kind})
			}
			key := profile + "\x00" + d.Auth.Kind
			if !seen[key] {
				seen[key] = true
				row.Methods = append(row.Methods, d.Auth.Kind)
				targets = append(targets, credentials.Target{ConfigPath: path, ProviderID: d.ID, Name: profile, Kind: d.Auth.Kind,
					Profile: registry.Config.Credentials[profile], OverrideFile: s.options.CredentialFile})
			}
			return nil
		}
		for _, d := range registry.List() {
			if err = add(contracts.ModelSelection{Provider: d.ID, AuthProfile: d.Auth.Profile}); err != nil {
				return nil, err
			}
		}
		roles := make([]string, 0, len(registry.Config.Roles))
		for name := range registry.Config.Roles {
			roles = append(roles, name)
		}
		sort.Strings(roles)
		for _, name := range roles {
			role := registry.Config.Roles[name].Model
			selection, err := registry.Resolve(preview.Selection, role)
			if err != nil {
				return nil, err
			}
			if err = add(selection); err != nil {
				return nil, err
			}
		}
		if err = add(preview.Selection); err != nil {
			return nil, err
		}
		return targets, nil
	})
	if err != nil {
		return result, err
	}
	for _, state := range states {
		row := rows[state.Target.Name]
		if state.APIKey != nil {
			row.APIKey = state.APIKey
		}
		if state.ChatGPT != nil {
			row.ChatGPT = state.ChatGPT
		}
	}
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	result.Profiles = make([]api.AuthProfile, 0, len(names))
	for _, name := range names {
		row := rows[name]
		sort.Strings(row.Methods)
		sort.Slice(row.Providers, func(i, j int) bool { return row.Providers[i].ID < row.Providers[j].ID })
		result.Profiles = append(result.Profiles, *row)
	}
	return result, nil
}

func (s *Service) requestAuthResolver(request api.AuthRequest) credentials.ResolveTarget {
	return func(ctx context.Context) (credentials.Target, error) {
		workspace := request.Workspace
		if request.SessionID != "" {
			session, err := s.store.Session(ctx, request.SessionID)
			if err != nil {
				return credentials.Target{}, err
			}
			workspace = session.WorkspaceRoot
		}
		return s.authTarget(ctx, workspace, request.ProviderID, request.AuthProfile)
	}
}

func (s *Service) RegisterAPIKey(ctx context.Context, request api.AuthRequest) (api.APIKeyState, error) {
	return s.connections.credentials.RegisterAPIKey(ctx, s.requestAuthResolver(request), request.MutationID, request.Value)
}
func (s *Service) AuthAccounts(ctx context.Context, workspace, providerID, profile string) (api.AuthAccounts, error) {
	return s.connections.credentials.Accounts(ctx, s.authResolver(workspace, providerID, profile))
}
func (s *Service) SelectAuthAccount(ctx context.Context, request api.AuthRequest) (api.AuthAccounts, error) {
	return s.connections.credentials.SelectAccount(ctx, s.requestAuthResolver(request), request.AccountID)
}
func (s *Service) BeginAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	return s.connections.credentials.BeginAttempt(ctx, s.requestAuthResolver(request), request)
}
func (s *Service) AuthAttempt(ctx context.Context, coreID, attemptID string) (api.AuthAttempt, error) {
	if err := ctx.Err(); err != nil {
		return api.AuthAttempt{}, err
	}
	attempt, err := s.connections.credentials.Attempt(attemptID)
	if err == nil && attempt.CoreID != coreID {
		return api.AuthAttempt{}, &api.Error{Code: "core_changed", Message: "Core changed"}
	}
	return attempt, err
}
func (s *Service) CompleteAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	if _, err := s.AuthAttempt(ctx, request.CoreID, request.AttemptID); err != nil {
		return api.AuthAttempt{}, err
	}
	return s.connections.credentials.CompleteAttempt(ctx, request.AttemptID, request.CallbackURL)
}
func (s *Service) CancelAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	if _, err := s.AuthAttempt(ctx, request.CoreID, request.AttemptID); err != nil {
		return api.AuthAttempt{}, err
	}
	return s.connections.credentials.CancelAttempt(request.AttemptID)
}
func (s *Service) DeleteAuthAttempt(ctx context.Context, coreID, attemptID string) error {
	if _, err := s.AuthAttempt(ctx, coreID, attemptID); err != nil {
		return err
	}
	return s.connections.credentials.DeleteAttempt(attemptID)
}
func (s *Service) LoginStatus(ctx context.Context, workspace, providerID string) (credentials.Status, error) {
	return s.AuthStatus(ctx, workspace, providerID, "")
}
func (s *Service) AuthStatus(ctx context.Context, workspace, providerID, profile string) (credentials.Status, error) {
	accounts, err := s.AuthAccounts(ctx, workspace, providerID, profile)
	if err != nil {
		return credentials.Status{}, err
	}
	for _, account := range accounts.Accounts {
		if account.AccountID == accounts.SelectedAccountID {
			status := credentials.Status{Connected: account.Connected, Account: account.Label, Identity: accounts.OwnerID + ":" + account.AccountID, PlanUsageEnabled: account.PlanUsageEnabled}
			if account.ExpiresAt != nil {
				status.ExpiresAt = *account.ExpiresAt
			}
			return status, nil
		}
	}
	return credentials.Status{}, fmt.Errorf("selected authentication account is missing")
}
