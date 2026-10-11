package providers

import (
	"encoding/json"
	"testing"
)

func TestCapacityUsesConfiguredRouteInsteadOfMatchingOnlyModelID(t *testing.T) {
	var metadata CapacityMetadata
	if err := json.Unmarshal([]byte(`{"route-a":{"api":"https://a.example/v1","models":{"shared-model":{"limit":{"context":1000,"input":800,"output":100}}}},"route-b":{"api":"https://b.example/v1","models":{"shared-model":{"limit":{"context":2000}}}},"explicit":{"models":{"shared-model":{"limit":{"context":3000}}}}}`), &metadata); err != nil {
		t.Fatal(err)
	}
	capacity := metadata.Capacity(Definition{BaseURL: "https://a.example/v1/"}, "shared-model")
	if capacity.ContextTokens == nil || *capacity.ContextTokens != 1000 || capacity.InputTokens == nil || *capacity.InputTokens != 800 || capacity.Source != "models.dev/route-a" {
		t.Fatalf("route capacity: %+v", capacity)
	}
	unknown := metadata.Capacity(Definition{BaseURL: "https://unlisted.example/v1"}, "shared-model")
	if unknown.ContextTokens != nil || unknown.InputTokens != nil {
		t.Fatal("other route's capacity was borrowed")
	}
	explicit := metadata.Capacity(Definition{BaseURL: "https://unlisted.example/v1", ModelsDevProviderID: "explicit"}, "shared-model")
	if explicit.ContextTokens == nil || *explicit.ContextTokens != 3000 {
		t.Fatalf("explicit mapping: %+v", explicit)
	}
}
