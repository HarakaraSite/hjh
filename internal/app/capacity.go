package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/providers"
)

// budget resolves captured configuration for this execution's own model.
// Descendants share the public metadata snapshot, never a parent's limit.
func (f *connectionFactory) budget(ctx context.Context, registry *providers.Registry,
	selection contracts.ModelSelection, injected bool, configPath string) (contextbudget.Budget, string) {
	definition, _ := registry.Definition(selection.Provider)
	var capacity contextbudget.Capacity
	for _, model := range definition.Models {
		if model.ID == selection.ModelID {
			capacity = contextbudget.Capacity{ContextTokens: model.ContextTokens, InputTokens: model.InputTokens,
				ContextSource: "config/models", InputSource: "config/models"}
			break
		}
	}
	var metadataError string
	if !injected && definition.Catalog.Path != "" {
		models, err := f.catalog(ctx, registry, definition, selection, configPath)
		if err != nil {
			metadataError = err.Error()
		}
		for _, model := range models {
			if model.ID != selection.ModelID {
				continue
			}
			if capacity.ContextTokens == nil {
				capacity.ContextTokens, capacity.ContextSource = model.ContextTokens, "catalog/"+definition.ID
			}
			if capacity.InputTokens == nil {
				capacity.InputTokens, capacity.InputSource = model.InputTokens, "catalog/"+definition.ID
			}
			break
		}
	}
	route, _ := url.Parse(definition.BaseURL)
	// In-process injected providers have no external capacity contract. Plain
	// HTTP local providers can opt into a named metadata source in configuration.
	if !injected && (definition.ModelsDevProviderID != "" || (route != nil && route.Scheme == "https")) {
		f.capacityOnce.Do(func() {
			// Metadata is optional; a stalled metadata service must not prevent the
			// user's model from running. Capture this failure as an unknown limit.
			loadCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			f.capacityMetadata, f.capacityError = providers.ReadCapacityMetadata(loadCtx, f.client)
			close(f.capacityDone)
		})
		select {
		case <-ctx.Done():
			metadataError = ctx.Err().Error()
		case <-f.capacityDone:
			if f.capacityError != nil {
				metadataError = strings.TrimSpace(metadataError + " " + f.capacityError.Error())
			}
			metadata := f.capacityMetadata.Capacity(definition, selection.ModelID)
			if capacity.ContextTokens == nil {
				capacity.ContextTokens = metadata.ContextTokens
				capacity.ContextSource = metadata.Source
			}
			if capacity.InputTokens == nil {
				capacity.InputTokens = metadata.InputTokens
				capacity.InputSource = metadata.Source
			}
		}
	}
	return contextbudget.Resolve(registry.Config.ContextBudget, selection, capacity), metadataError
}

type catalogFlight struct {
	done   chan struct{}
	models []providers.ModelInfo
	err    error
}

func (f *connectionFactory) catalog(ctx context.Context, registry *providers.Registry, definition providers.Definition,
	selection contracts.ModelSelection, configPath string) ([]providers.ModelInfo, error) {
	key := fmt.Sprint(configPath, "/", selection.Provider, "/", selection.AuthProfile, "/", digest(contracts.JSON(definition)))
	f.catalogMu.Lock()
	flight := f.catalogs[key]
	if flight != nil {
		f.catalogMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			return flight.models, flight.err
		}
	}
	flight = &catalogFlight{done: make(chan struct{})}
	f.catalogs[key] = flight
	f.catalogMu.Unlock()
	loadCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	target := credentials.Target{ConfigPath: configPath, ProviderID: selection.Provider, Name: selection.AuthProfile,
		Kind: definition.Auth.Kind, Profile: registry.Config.Credentials[selection.AuthProfile]}
	if selection.AuthProfile == f.overrideProfile {
		target.OverrideFile = f.overrideFile
	}
	binding, err := f.capture(loadCtx, target)
	if err == nil {
		resolved, resolveErr := binding.Resolve(loadCtx)
		err = resolveErr
		if err == nil {
			flight.models, err = loadModelCatalog(loadCtx, f.client, definition, resolved.Token)
		}
	}
	flight.err = err
	close(flight.done)
	return flight.models, flight.err
}
