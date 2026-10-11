package contextbudget

import (
	"testing"

	"hjh/internal/contracts"
)

func TestResolvePrecedenceAndUnknownCapacity(t *testing.T) {
	defaults := Values{
		ContextTokens: int64Pointer(1000),
		InputTokens:   int64Pointer(900),
		OutputReserve: int64Pointer(100),
		HistoryTokens: int64Pointer(200),
		InputRatio:    float64Pointer(0.8),
	}
	provider := Values{InputTokens: int64Pointer(700)}
	model := Values{
		ContextTokens: int64Pointer(600),
		OutputReserve: int64Pointer(50),
		InputRatio:    float64Pointer(0.5),
	}
	config := Configuration{
		Defaults:  defaults,
		Providers: map[string]Values{"demo": provider},
		Models:    map[string]Values{"demo/model": model},
	}
	budget := Resolve(config, contracts.ModelSelection{Provider: "demo", ModelID: "model"}, Capacity{
		ContextTokens: int64Pointer(2000),
		InputTokens:   int64Pointer(1500),
		Source:        "models.dev",
	})
	if *budget.ContextTokens != 600 || *budget.InputTokens != 700 || *budget.OutputReserve != 50 ||
		*budget.InputRatio != 0.5 || *budget.HistoryTokens != 200 || *budget.Limit != 300 {
		t.Fatalf("unexpected resolved budget: %+v", budget)
	}
	if budget.Sources.ContextTokens != "config/models/demo/model" ||
		budget.Sources.InputTokens != "config/providers/demo" ||
		budget.Sources.OutputReserve != "config/models/demo/model" ||
		budget.Sources.InputRatio != "config/models/demo/model" ||
		budget.Sources.HistoryTokens != "config/defaults" {
		t.Fatalf("unexpected field sources: %+v", budget.Sources)
	}

	unknown := Resolve(Configuration{}, contracts.ModelSelection{}, Capacity{Source: "unknown"})
	if unknown.Limit != nil || unknown.ContextTokens != nil || unknown.InputTokens != nil ||
		unknown.Sources.ContextTokens != "unknown" || unknown.Sources.InputTokens != "unknown" ||
		unknown.InputRatio == nil || *unknown.InputRatio != 0.8 || unknown.Sources.InputRatio != "default" {
		t.Fatalf("unknown capacity invented a limit or lost the default ratio: %+v", unknown)
	}

	reserveOnly := Resolve(Configuration{Defaults: Values{OutputReserve: int64Pointer(42)}},
		contracts.ModelSelection{}, Capacity{Source: "unknown"})
	if reserveOnly.Limit != nil {
		t.Fatalf("output reserve without context capacity created a limit: %+v", reserveOnly)
	}
}

func TestResolveUsesCapacityOnlyWhenConfigurationDoesNotSetField(t *testing.T) {
	budget := Resolve(Configuration{}, contracts.ModelSelection{}, Capacity{
		ContextTokens: int64Pointer(1000),
		InputTokens:   int64Pointer(900),
		Source:        "catalog",
		ContextSource: "models.dev",
		InputSource:   "provider-catalog",
	})
	if budget.Limit == nil || *budget.Limit != 800 || budget.Sources.ContextTokens != "models.dev" ||
		budget.Sources.InputTokens != "provider-catalog" {
		t.Fatalf("capacity metadata was not used: %+v", budget)
	}
}

func int64Pointer(value int64) *int64 { return &value }

func float64Pointer(value float64) *float64 { return &value }
