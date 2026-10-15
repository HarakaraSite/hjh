// Package contextbudget resolves model input budgets and builds request-only
// projections without changing the saved conversation.
package contextbudget

import (
	"math"

	"hjh/internal/contracts"
)

type Values struct {
	ContextTokens *int64   `json:"contextTokens,omitempty"`
	InputTokens   *int64   `json:"inputTokens,omitempty"`
	OutputReserve *int64   `json:"outputReserve,omitempty"`
	InputRatio    *float64 `json:"inputRatio,omitempty"`
	HistoryTokens *int64   `json:"historyTokens,omitempty"`
}

type Configuration struct {
	Defaults  Values            `json:"defaults"`
	Providers map[string]Values `json:"providers,omitempty"`
	Models    map[string]Values `json:"models,omitempty"`
}

// Capacity contains catalog metadata for the selected route. Source describes
// the origin of the non-nil capacity fields.
type Capacity struct {
	ContextTokens *int64 `json:"contextTokens,omitempty"`
	InputTokens   *int64 `json:"inputTokens,omitempty"`
	Source        string `json:"source"`
	ContextSource string `json:"contextSource,omitempty"`
	InputSource   string `json:"inputSource,omitempty"`
}

type FieldSources struct {
	ContextTokens string `json:"contextTokens"`
	InputTokens   string `json:"inputTokens"`
	OutputReserve string `json:"outputReserve"`
	InputRatio    string `json:"inputRatio"`
	HistoryTokens string `json:"historyTokens"`
}

// Budget contains effective limits and the source of each effective value.
// Limit is absent when none of the known C/I/R terms establishes an input cap.
type Budget struct {
	ContextTokens *int64       `json:"contextTokens,omitempty"`
	InputTokens   *int64       `json:"inputTokens,omitempty"`
	OutputReserve *int64       `json:"outputReserve,omitempty"`
	InputRatio    *float64     `json:"inputRatio"`
	HistoryTokens *int64       `json:"historyTokens,omitempty"`
	Limit         *int64       `json:"limit,omitempty"`
	LimitSource   string       `json:"limitSource,omitempty"`
	Sources       FieldSources `json:"sources"`
}

// Resolve applies model > provider > defaults > capacity metadata precedence.
// An output reservation is configuration only; adapter-advertised output
// capacities do not describe the reservation used for this request.
func Resolve(config Configuration, selection contracts.ModelSelection, capacity Capacity) Budget {
	values := Values{}
	sources := FieldSources{}
	mergeValues(&values, &sources, config.Defaults, "config/defaults")
	if provider, ok := config.Providers[selection.Provider]; ok {
		mergeValues(&values, &sources, provider, "config/providers/"+selection.Provider)
	}
	modelKey := selection.Provider + "/" + selection.ModelID
	if model, ok := config.Models[modelKey]; ok {
		mergeValues(&values, &sources, model, "config/models/"+modelKey)
	}

	budget := Budget{
		ContextTokens: cloneInt64(values.ContextTokens),
		InputTokens:   cloneInt64(values.InputTokens),
		OutputReserve: cloneInt64(values.OutputReserve),
		HistoryTokens: cloneInt64(values.HistoryTokens),
		Sources:       sources,
	}
	setUnknownSources(&budget.Sources)
	if values.InputRatio == nil {
		ratio := 0.8
		budget.InputRatio = &ratio
		budget.Sources.InputRatio = "default"
	} else {
		budget.InputRatio = cloneFloat64(values.InputRatio)
	}

	if values.ContextTokens == nil && capacity.ContextTokens != nil {
		budget.ContextTokens = cloneInt64(capacity.ContextTokens)
		budget.Sources.ContextTokens = capacitySource(capacity.ContextSource, capacity.Source)
	}
	if values.InputTokens == nil && capacity.InputTokens != nil {
		budget.InputTokens = cloneInt64(capacity.InputTokens)
		budget.Sources.InputTokens = capacitySource(capacity.InputSource, capacity.Source)
	}

	return resolveLimit(budget)
}

// Override applies a request-local policy using the same terms and formula as
// configuration resolution. It does not mutate the configured budget.
func Override(base Budget, policy Values) Budget {
	values := Values{ContextTokens: base.ContextTokens, InputTokens: base.InputTokens,
		OutputReserve: base.OutputReserve, InputRatio: base.InputRatio, HistoryTokens: base.HistoryTokens}
	sources := base.Sources
	mergeValues(&values, &sources, policy, "hook")
	// select() returns resolved budget values which callers can pass directly
	// to replace(). Echoing an unchanged term does not make it hook policy.
	if policy.ContextTokens != nil && base.ContextTokens != nil && *policy.ContextTokens == *base.ContextTokens {
		sources.ContextTokens = base.Sources.ContextTokens
	}
	if policy.InputTokens != nil && base.InputTokens != nil && *policy.InputTokens == *base.InputTokens {
		sources.InputTokens = base.Sources.InputTokens
	}
	if policy.OutputReserve != nil && base.OutputReserve != nil && *policy.OutputReserve == *base.OutputReserve {
		sources.OutputReserve = base.Sources.OutputReserve
	}
	if policy.InputRatio != nil && base.InputRatio != nil && *policy.InputRatio == *base.InputRatio {
		sources.InputRatio = base.Sources.InputRatio
	}
	if policy.HistoryTokens != nil && base.HistoryTokens != nil && *policy.HistoryTokens == *base.HistoryTokens {
		sources.HistoryTokens = base.Sources.HistoryTokens
	}
	return resolveLimit(cloneBudget(Budget{ContextTokens: values.ContextTokens, InputTokens: values.InputTokens,
		OutputReserve: values.OutputReserve, InputRatio: values.InputRatio, HistoryTokens: values.HistoryTokens, Sources: sources}))
}

type limitTerm struct {
	value  int64
	source string
}

func resolveLimit(budget Budget) Budget {
	budget.Limit = nil
	budget.LimitSource = ""
	var limits []limitTerm
	if budget.ContextTokens != nil {
		limits = append(limits, limitTerm{int64(math.Floor(float64(*budget.ContextTokens) * (*budget.InputRatio))), termSource(budget.Sources.ContextTokens, budget.Sources.InputRatio)})
	}
	if budget.InputTokens != nil {
		limits = append(limits, limitTerm{*budget.InputTokens, budget.Sources.InputTokens})
	}
	if budget.ContextTokens != nil && budget.OutputReserve != nil {
		limits = append(limits, limitTerm{*budget.ContextTokens - *budget.OutputReserve, termSource(budget.Sources.ContextTokens, budget.Sources.OutputReserve)})
	}
	if len(limits) != 0 {
		minimum := limits[0]
		for _, limit := range limits[1:] {
			if limit.value < minimum.value {
				minimum = limit
			}
		}
		budget.Limit = &minimum.value
		budget.LimitSource = minimum.source
	}
	return budget
}

// A derived limit is hook policy when either operand comes from the hook.
func termSource(capacity, policy string) string {
	if policy == "hook" {
		return "hook"
	}
	return capacity
}

func mergeValues(target *Values, sources *FieldSources, source Values, origin string) {
	if source.ContextTokens != nil {
		target.ContextTokens = source.ContextTokens
		sources.ContextTokens = origin
	}
	if source.InputTokens != nil {
		target.InputTokens = source.InputTokens
		sources.InputTokens = origin
	}
	if source.OutputReserve != nil {
		target.OutputReserve = source.OutputReserve
		sources.OutputReserve = origin
	}
	if source.InputRatio != nil {
		target.InputRatio = source.InputRatio
		sources.InputRatio = origin
	}
	if source.HistoryTokens != nil {
		target.HistoryTokens = source.HistoryTokens
		sources.HistoryTokens = origin
	}
}

func setUnknownSources(sources *FieldSources) {
	if sources.ContextTokens == "" {
		sources.ContextTokens = "unknown"
	}
	if sources.InputTokens == "" {
		sources.InputTokens = "unknown"
	}
	if sources.OutputReserve == "" {
		sources.OutputReserve = "unknown"
	}
	if sources.InputRatio == "" {
		sources.InputRatio = "unknown"
	}
	if sources.HistoryTokens == "" {
		sources.HistoryTokens = "unknown"
	}
}

func capacitySource(fieldSource, fallback string) string {
	if fieldSource != "" && fieldSource != "unknown" {
		return fieldSource
	}
	source := fallback
	if source == "" || source == "unknown" {
		return "unknown"
	}
	return source
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
