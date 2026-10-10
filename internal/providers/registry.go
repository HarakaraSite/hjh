// Package providers reads externally supplied provider definitions and resolves
// partial model selections against those definitions.
package providers

import (
	"encoding/json"
	"fmt"
	"sort"

	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/provider/protocol"
)

// Config is the provider-related portion of the extension config. It is kept
// public so app can pass the same captured configuration to its resolver and
// provider factory.
type Config struct {
	Providers   map[string]Definition          `json:"providers"`
	Credentials map[string]credentials.Profile `json:"credentials"`
	Model       contracts.ModelSelection       `json:"model"`
	Roles       map[string]RoleModel           `json:"roles"`
}

// RoleModel contains the model defaults contributed by one role. Every field
// is optional so it can be resolved against the parent or configured default.
type RoleModel struct {
	Model contracts.ModelSelection `json:"model"`
}

// Definition describes one connection using an API and authentication
// implementation already supported by the Go runtime.
type Definition struct {
	ID            string            `json:"id"`
	DisplayName   string            `json:"displayName"`
	API           string            `json:"api"`
	BaseURL       string            `json:"baseURL"`
	Path          string            `json:"path"`
	Auth          Auth              `json:"auth"`
	DefaultModel  string            `json:"defaultModel"`
	DefaultEffort string            `json:"defaultEffort"`
	Models        []ModelInfo       `json:"models,omitempty"`
	Catalog       Catalog           `json:"catalog,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	SessionHeader string            `json:"sessionHeader,omitempty"`
	Adapter       protocol.Options  `json:"adapter,omitempty"`
}

// Auth selects a Go authentication method and names a credentials profile.
// The profile stores references only, never a credential value.
type Auth struct {
	Kind    string `json:"kind"`
	Profile string `json:"profile,omitempty"`
}

// ModelInfo is a display candidate. It does not limit model IDs accepted by
// Resolve.
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Catalog identifies a model catalog source. An account-specific catalog is
// loaded by the higher-level operation using that account's credentials.
type Catalog struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}

// Registry contains definitions from one captured configuration.
type Registry struct {
	Config Config
}

// Parse decodes provider definitions from the captured extension config bytes.
func Parse(data []byte) (*Registry, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse provider config: %w", err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]Definition{}
	}
	for id, definition := range cfg.Providers {
		definition.ID = id
		cfg.Providers[id] = definition
	}
	return &Registry{Config: cfg}, nil
}

// Definition returns the connection definition identified by id.
func (r *Registry) Definition(id string) (Definition, error) {
	definition, ok := r.Config.Providers[id]
	if !ok {
		return Definition{}, fmt.Errorf("provider %q is not defined", id)
	}
	definition.ID = id
	return definition, nil
}

// List returns definitions in stable ID order for CLI and TUI selection.
func (r *Registry) List() []Definition {
	ids := make([]string, 0, len(r.Config.Providers))
	for id := range r.Config.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	definitions := make([]Definition, 0, len(ids))
	for _, id := range ids {
		definition := r.Config.Providers[id]
		definition.ID = id
		definitions = append(definitions, definition)
	}
	return definitions
}

// Resolve applies a partial override to base. API and defaults always come
// from the selected connection definition. Model lists are informational and
// are not an allowlist for explicit IDs.
func (r *Registry) Resolve(base, override contracts.ModelSelection) (contracts.ModelSelection, error) {
	providerID := override.Provider
	if providerID == "" {
		providerID = base.Provider
	}
	definition, err := r.Definition(providerID)
	if err != nil {
		return contracts.ModelSelection{}, err
	}

	sameProvider := base.Provider == providerID
	authProfile := override.AuthProfile
	if authProfile == "" && sameProvider {
		authProfile = base.AuthProfile
	}
	if authProfile == "" {
		authProfile = definition.Auth.Profile
	}

	modelID := override.ModelID
	if modelID == "" && sameProvider {
		modelID = base.ModelID
	}
	if modelID == "" {
		modelID = definition.DefaultModel
	}

	effort := override.Effort
	if effort == "" && sameProvider {
		effort = base.Effort
	}
	if effort == "" {
		effort = definition.DefaultEffort
	}

	return contracts.ModelSelection{
		Provider:    providerID,
		API:         definition.API,
		AuthProfile: authProfile,
		ModelID:     modelID,
		Effort:      effort,
	}, nil
}
