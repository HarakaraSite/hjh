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

func TestResolveLimitSourceNamesTheGoverningTerm(t *testing.T) {
	selection := contracts.ModelSelection{Provider: "demo", ModelID: "model"}
	// The capacity window with the default ratio governs the limit.
	window := Resolve(Configuration{}, selection, Capacity{ContextTokens: int64Pointer(1000), Source: "models.dev"})
	if window.Limit == nil || *window.Limit != 800 || window.LimitSource != "models.dev" {
		t.Fatalf("window-governed limit source: %+v", window)
	}
	// A smaller configured input cap governs and keeps its own origin.
	capped := Resolve(Configuration{Defaults: Values{InputTokens: int64Pointer(100)}}, selection,
		Capacity{ContextTokens: int64Pointer(1000), Source: "models.dev"})
	if capped.Limit == nil || *capped.Limit != 100 || capped.LimitSource != "config/defaults" {
		t.Fatalf("input-governed limit source: %+v", capped)
	}
	// A hook policy override marks the limit as hook policy, never model capacity.
	hooked := Override(Resolve(Configuration{}, selection, Capacity{}), Values{InputTokens: int64Pointer(800000)})
	if hooked.Limit == nil || *hooked.Limit != 800000 || hooked.LimitSource != "hook" {
		t.Fatalf("hook-governed limit source: %+v", hooked)
	}
	// No known term means no limit and no origin to display.
	unknown := Resolve(Configuration{}, selection, Capacity{})
	if unknown.Limit != nil || unknown.LimitSource != "" {
		t.Fatalf("unknown limit source: %+v", unknown)
	}
}

func TestOverrideKeepsUnchangedBudgetOriginsAndTracksDerivedHookLimits(t *testing.T) {
	base := Resolve(Configuration{Defaults: Values{OutputReserve: int64Pointer(100)}},
		contracts.ModelSelection{}, Capacity{ContextTokens: int64Pointer(1000), Source: "models.dev"})
	// The full resolved object returned by select() can be passed to replace().
	echo := Override(base, Values{ContextTokens: base.ContextTokens, InputTokens: base.InputTokens,
		OutputReserve: base.OutputReserve, InputRatio: base.InputRatio, HistoryTokens: base.HistoryTokens})
	if echo.Sources != base.Sources || echo.LimitSource != "models.dev" || *echo.Limit != 800 {
		t.Fatalf("unchanged selection acquired hook provenance: %+v", echo)
	}
	for _, policy := range []Values{
		{InputRatio: float64Pointer(0.5)},
		{OutputReserve: int64Pointer(600)},
	} {
		changed := Override(base, policy)
		if changed.LimitSource != "hook" || *changed.Limit >= *base.Limit {
			t.Fatalf("derived hook limit lost its policy origin: %+v", changed)
		}
	}
}
