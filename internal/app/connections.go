package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

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
	bindingMu                     sync.Mutex
	bindings                      map[string]*bindingFlight
}

type bindingFlight struct {
	done    chan struct{}
	binding *credentials.Binding
	err     error
}

func newConnectionFactory(options RunOptions) *connectionFactory {
	manager := options.Credentials
	if manager == nil {
		manager = credentials.NewManager(credentials.Options{HTTPClient: options.HTTPClient, StateDir: options.CredentialStateDir})
	}
	return &connectionFactory{credentials: manager, client: options.HTTPClient, ownsCredentials: options.Credentials == nil,
		bindings: make(map[string]*bindingFlight)}
}

// capture joins the first preparation for one owner without holding a factory
// mutex over credential reads or refresh. Different owners prepare independently.
func (f *connectionFactory) capture(ctx context.Context, target credentials.Target) (*credentials.Binding, error) {
	key, err := f.credentials.BindingKey(target)
	if err != nil {
		return nil, err
	}
	f.bindingMu.Lock()
	flight := f.bindings[key]
	if flight != nil {
		f.bindingMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			return flight.binding, flight.err
		}
	}
	flight = &bindingFlight{done: make(chan struct{})}
	f.bindings[key] = flight
	f.bindingMu.Unlock()
	flight.binding, flight.err = f.credentials.Capture(ctx, target)
	f.bindingMu.Lock()
	if flight.err != nil {
		delete(f.bindings, key)
	}
	close(flight.done)
	f.bindingMu.Unlock()
	return flight.binding, flight.err
}

func capturedRegistry(snapshot extensions.Snapshot, configPath string) (*providers.Registry, error) {
	for _, resource := range snapshot.Resources {
		if filepath.Clean(resource.SourceLocator) == filepath.Clean(configPath) {
			return providers.Parse(resource.Bytes)
		}
	}
	return nil, fmt.Errorf("configuration source was not captured: %s", configPath)
}

func (f *connectionFactory) provider(ctx context.Context, registry *providers.Registry, selection contracts.ModelSelection, sessionID, configPath string) (contracts.Provider, error) {
	definition, err := registry.Definition(selection.Provider)
	if err != nil {
		return nil, err
	}
	target := credentials.Target{ConfigPath: configPath, ProviderID: selection.Provider, Name: selection.AuthProfile,
		Kind: definition.Auth.Kind, Profile: registry.Config.Credentials[selection.AuthProfile]}
	if selection.AuthProfile == f.overrideProfile {
		target.OverrideFile = f.overrideFile
	}
	binding, err := f.capture(ctx, target)
	if err != nil {
		return nil, err
	}
	resolved, err := binding.Resolve(ctx)
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
			current, resolveErr := binding.Resolve(requestCtx)
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
	_, b, err := s.selectionConfig(workspace, s.options.ConfigPath)
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

func (s *Service) Models(ctx context.Context, workspace, providerID, authProfile string) ([]providers.ModelInfo, error) {
	result, err := s.ModelsWithAuth(ctx, workspace, providerID, authProfile)
	return result.Models, err
}

type ModelCatalog struct {
	Models                     []providers.ModelInfo
	AuthOwnerID, AuthAccountID string
}

func (s *Service) ModelsWithAuth(ctx context.Context, workspace, providerID, authProfile string) (result ModelCatalog, err error) {
	if workspace == "" {
		workspace = s.options.Workspace
	}
	configPath, data, err := s.selectionConfig(workspace, s.options.ConfigPath)
	if err != nil {
		return result, err
	}
	r, err := providers.Parse(data)
	if err != nil {
		return result, err
	}
	if providerID == "" {
		providerID = r.Config.Model.Provider
	}
	d, err := r.Definition(providerID)
	if err != nil {
		return result, err
	}
	if d.Catalog.Path == "" {
		result.Models = d.Models
		return result, nil
	}
	selection, err := r.Resolve(contracts.ModelSelection{}, contracts.ModelSelection{Provider: providerID, AuthProfile: authProfile})
	if err != nil {
		return result, err
	}
	binding, err := s.connections.credentials.Capture(ctx, credentials.Target{ConfigPath: configPath,
		ProviderID: providerID, Name: selection.AuthProfile, Kind: d.Auth.Kind,
		Profile: r.Config.Credentials[selection.AuthProfile], OverrideFile: s.options.CredentialFile})
	if err != nil {
		return result, err
	}
	resolved, err := binding.Resolve(ctx)
	if err != nil {
		return result, err
	}
	result.AuthOwnerID, result.AuthAccountID = binding.OwnerID, binding.AccountID
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.BaseURL, "/")+"/"+strings.TrimLeft(d.Catalog.Path, "/"), nil)
	if err != nil {
		return result, err
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
		return result, fmt.Errorf("model catalog request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("model catalog HTTP %s", response.Status)
	}
	var body struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			Slug                     string `json:"slug"`
			Name                     string `json:"display_name"`
			Visibility               string `json:"visibility"`
			SupportedReasoningLevels *[]struct {
				Description string `json:"description"`
				Effort      string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		return result, err
	}
	var models []providers.ModelInfo
	if d.Catalog.Kind == "openai-account" {
		for _, model := range body.Models {
			if model.Visibility == "list" {
				info := providers.ModelInfo{ID: model.Slug, Name: model.Name}
				if model.SupportedReasoningLevels != nil {
					info.EffortSource = "catalog"
					info.Efforts = make([]string, 0, len(*model.SupportedReasoningLevels))
					for _, level := range *model.SupportedReasoningLevels {
						info.Efforts = append(info.Efforts, level.Effort)
					}
				}
				models = append(models, info)
			}
		}
	} else {
		for _, model := range body.Data {
			models = append(models, providers.ModelInfo{ID: model.ID, Name: model.Name})
		}
	}
	result.Models = models
	return result, nil
}

func (f *connectionFactory) close() error {
	if f.ownsCredentials {
		return f.credentials.Close()
	}
	return nil
}
