package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/extensions"
	"hjh/internal/provider/protocol"
	"hjh/internal/providers"
)

// connectionFactory shares credential ownership, never a mutable selection.
type connectionFactory struct {
	credentials                   *credentials.Manager
	ownsCredentials               bool
	client                        *http.Client
	overrideProfile, overrideFile string
}

func newConnectionFactory(options RunOptions) *connectionFactory {
	manager := options.Credentials
	if manager == nil {
		manager = credentials.NewManager(credentials.Options{HTTPClient: options.HTTPClient})
	}
	return &connectionFactory{credentials: manager, client: options.HTTPClient, ownsCredentials: options.Credentials == nil}
}

func capturedRegistry(snapshot extensions.Snapshot, configPath string) (*providers.Registry, error) {
	for _, resource := range snapshot.Resources {
		if filepath.Clean(resource.SourceLocator) == filepath.Clean(configPath) {
			return providers.Parse(resource.Bytes)
		}
	}
	return nil, fmt.Errorf("configuration source was not captured: %s", configPath)
}

func (f *connectionFactory) provider(ctx context.Context, registry *providers.Registry, selection contracts.ModelSelection, sessionID string) (contracts.Provider, error) {
	definition, err := registry.Definition(selection.Provider)
	if err != nil {
		return nil, err
	}
	profile := registry.Config.Credentials[selection.AuthProfile]
	if f.overrideFile != "" && selection.AuthProfile == f.overrideProfile {
		profile.Env, profile.File = "", f.overrideFile
	}
	resolved, err := f.credentials.Resolve(ctx, definition.Auth.Kind, selection.AuthProfile, profile)
	if err != nil {
		return nil, err
	}
	// Hidden state belongs to this destination and account, not merely its editable ID.
	identityBytes, err := json.Marshal([]string{selection.Provider, definition.API, definition.BaseURL,
		definition.Path, selection.AuthProfile, resolved.Identity})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(identityBytes)
	identity := selection.Provider + ":" + hex.EncodeToString(digest[:])
	config := protocol.Config{ProviderID: selection.Provider, StateIdentity: identity,
		API: definition.API, BaseURL: definition.BaseURL, Path: definition.Path, Headers: definition.Headers,
		Options: definition.Adapter, APIKey: resolved.Token, HTTPClient: f.client,
		SessionID: sessionID, SessionHeader: definition.SessionHeader}
	if definition.Auth.Kind == credentials.KindChatGPTOAuth {
		config.ResolveToken = func(requestCtx context.Context) (string, error) {
			current, resolveErr := f.credentials.Resolve(requestCtx, definition.Auth.Kind, selection.AuthProfile, profile)
			if resolveErr != nil {
				return "", resolveErr
			}
			return current.Token, nil
		}
	}
	return protocol.New(config), nil
}

func (s *Service) connectionRegistry(workspace string) (*providers.Registry, error) {
	if workspace == "" {
		workspace = s.options.Workspace
	}
	path, err := prepareConfig(workspace, s.options.ConfigPath)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return providers.Parse(b)
}

func (s *Service) Providers(ctx context.Context, workspace string) ([]providers.Definition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, err := s.connectionRegistry(workspace)
	if err != nil {
		return nil, err
	}
	return r.List(), nil
}

func (s *Service) Models(ctx context.Context, workspace, providerID string) ([]providers.ModelInfo, error) {
	r, err := s.connectionRegistry(workspace)
	if err != nil {
		return nil, err
	}
	if providerID == "" {
		providerID = r.Config.Model.Provider
	}
	d, err := r.Definition(providerID)
	if err != nil {
		return nil, err
	}
	if d.Catalog.Path == "" {
		return d.Models, nil
	}
	selection, err := r.Resolve(contracts.ModelSelection{}, contracts.ModelSelection{Provider: providerID})
	if err != nil {
		return nil, err
	}
	resolved, err := s.connections.credentials.Resolve(ctx, d.Auth.Kind, selection.AuthProfile, r.Config.Credentials[selection.AuthProfile])
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.BaseURL, "/")+"/"+strings.TrimLeft(d.Catalog.Path, "/"), nil)
	if err != nil {
		return nil, err
	}
	for name, value := range d.Headers {
		request.Header.Set(name, value)
	}

	client := s.options.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	client = protocol.NewAuthenticatedHTTPClient(client, request.URL.String(), resolved.Token)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("model catalog request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("model catalog HTTP %s", response.Status)
	}
	var body struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			Slug       string `json:"slug"`
			Name       string `json:"display_name"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		return nil, err
	}
	var models []providers.ModelInfo
	if d.Catalog.Kind == "openai-account" {
		for _, model := range body.Models {
			if model.Visibility == "list" {
				models = append(models, providers.ModelInfo{ID: model.Slug, Name: model.Name})
			}
		}
	} else {
		for _, model := range body.Data {
			models = append(models, providers.ModelInfo{ID: model.ID, Name: model.Name})
		}
	}
	return models, nil
}

func (s *Service) authProfile(workspace, providerID string) (string, credentials.Profile, error) {
	r, err := s.connectionRegistry(workspace)
	if err != nil {
		return "", credentials.Profile{}, err
	}
	if providerID == "" {
		providerID = r.Config.Model.Provider
	}
	d, err := r.Definition(providerID)
	if err != nil {
		return "", credentials.Profile{}, err
	}
	if d.Auth.Kind != "chatgpt-oauth" {
		return "", credentials.Profile{}, fmt.Errorf("provider %s uses %s authentication", providerID, d.Auth.Kind)
	}
	return d.Auth.Profile, r.Config.Credentials[d.Auth.Profile], nil
}

func (s *Service) BeginLogin(ctx context.Context, workspace, providerID string) (credentials.Login, error) {
	name, profile, err := s.authProfile(workspace, providerID)
	if err != nil {
		return credentials.Login{}, err
	}
	return s.connections.credentials.BeginLogin(ctx, name, profile)
}
func (s *Service) CompleteLogin(ctx context.Context, workspace, providerID, callbackURL string) (credentials.Status, error) {
	name, profile, err := s.authProfile(workspace, providerID)
	if err != nil {
		return credentials.Status{}, err
	}
	return s.connections.credentials.CompleteLogin(ctx, name, profile, callbackURL)
}
func (s *Service) LoginStatus(ctx context.Context, workspace, providerID string) (credentials.Status, error) {
	name, profile, err := s.authProfile(workspace, providerID)
	if err != nil {
		return credentials.Status{}, err
	}
	return s.connections.credentials.Status(ctx, name, profile)
}

func (f *connectionFactory) close() error {
	if f.ownsCredentials {
		return f.credentials.Close()
	}
	return nil
}
