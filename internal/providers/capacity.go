package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"hjh/internal/contextbudget"
)

// CapacityMetadata is one captured models.dev response. It contains public
// model limits, never credentials, and is shared by an execution's descendants.
type CapacityMetadata map[string]struct {
	API    string `json:"api"`
	Models map[string]struct {
		Limit struct {
			Context *int64 `json:"context"`
			Input   *int64 `json:"input"`
		} `json:"limit"`
	} `json:"models"`
}

func ReadCapacityMetadata(ctx context.Context, client *http.Client) (CapacityMetadata, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://models.dev/api.json", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "hjh")
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("models.dev request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("models.dev HTTP %s", response.Status)
	}
	var metadata CapacityMetadata
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

// Capacity only matches the configured route or an explicit metadata provider.
// Identical model IDs served through other routes do not establish capacity.
func (m CapacityMetadata) Capacity(definition Definition, modelID string) contextbudget.Capacity {
	var result contextbudget.Capacity
	matches := 0
	for providerID, provider := range m {
		if definition.ModelsDevProviderID != "" {
			if providerID != definition.ModelsDevProviderID {
				continue
			}
		} else if provider.API == "" || normalizeMetadataAPI(provider.API) != normalizeMetadataAPI(definition.BaseURL) {
			continue
		}
		model, exists := provider.Models[modelID]
		if !exists {
			continue
		}
		matches++
		result = contextbudget.Capacity{ContextTokens: model.Limit.Context, InputTokens: model.Limit.Input,
			Source: "models.dev/" + providerID}
	}
	if matches != 1 {
		return contextbudget.Capacity{}
	}
	return result
}

func normalizeMetadataAPI(api string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(api)), "/")
}
