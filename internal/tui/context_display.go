package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"hjh/internal/contracts"
)

type contextSourceDisplay struct {
	Kind           string `json:"kind"`
	ExecutionID    string `json:"executionId"`
	MessageOrdinal int64  `json:"messageOrdinal"`
}

func formatCoreBudget(status contracts.ContextBudgetStatus) string {
	var fact struct {
		Budget struct {
			Limit         *int64            `json:"limit"`
			ContextTokens *int64            `json:"contextTokens"`
			InputTokens   *int64            `json:"inputTokens"`
			OutputReserve *int64            `json:"outputReserve"`
			InputRatio    *float64          `json:"inputRatio"`
			HistoryTokens *int64            `json:"historyTokens"`
			Sources       map[string]string `json:"sources"`
		} `json:"budget"`
		EstimatedTokens       int64                  `json:"estimatedTokens"`
		WireBytes             int64                  `json:"wireBytes"`
		OmittedCurrent        []int                  `json:"omittedCurrent"`
		OmittedCurrentSources []contextSourceDisplay `json:"omittedCurrentSources"`
		Sources               []contextSourceDisplay `json:"sources"`
		HistoryStatus         string                 `json:"historyStatus"`
		History               struct {
			Reason string `json:"reason"`
			Turns  []struct {
				Number          int64                  `json:"number"`
				Status          string                 `json:"status"`
				Reason          string                 `json:"reason"`
				SelectedSources []contextSourceDisplay `json:"selectedSources"`
				OmittedSources  []contextSourceDisplay `json:"omittedSources"`
			} `json:"turns"`
		} `json:"history"`
	}
	if json.Unmarshal(status.Fact, &fact) != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\nCore context budget · execution %s · step %d\nEstimated input · %d tokens · %d wire bytes · limit %s", status.ExecutionID, status.ModelStep, fact.EstimatedTokens, fact.WireBytes, formatBudgetValue(fact.Budget.Limit))
	for _, term := range []struct {
		name, key string
		value     *int64
	}{
		{"Context C", "contextTokens", fact.Budget.ContextTokens}, {"Input I", "inputTokens", fact.Budget.InputTokens},
		{"Output reserve R", "outputReserve", fact.Budget.OutputReserve}, {"History H", "historyTokens", fact.Budget.HistoryTokens},
	} {
		fmt.Fprintf(&b, "\n%s · %s · %s", term.name, formatBudgetValue(term.value), fact.Budget.Sources[term.key])
	}
	ratio := "unknown"
	if fact.Budget.InputRatio != nil {
		ratio = strconv.FormatFloat(*fact.Budget.InputRatio, 'g', -1, 64)
	}
	fmt.Fprintf(&b, "\nInput ratio q · %s · %s\nHistory · %s · omitted current messages %d", ratio, fact.Budget.Sources["inputRatio"], fact.HistoryStatus, len(fact.OmittedCurrent))
	if fact.History.Reason != "" {
		fmt.Fprintf(&b, " · %s", fact.History.Reason)
	}
	var current []contextSourceDisplay
	for _, source := range fact.Sources {
		if source.Kind == "current" {
			current = append(current, source)
		}
	}
	fmt.Fprintf(&b, "\nCurrent · kept messages %s · omitted messages %s", formatSourceOrdinals(current), formatSourceOrdinals(fact.OmittedCurrentSources))
	for _, turn := range fact.History.Turns {
		execution := ""
		if len(turn.SelectedSources) > 0 {
			execution = turn.SelectedSources[0].ExecutionID
		} else if len(turn.OmittedSources) > 0 {
			execution = turn.OmittedSources[0].ExecutionID
		}
		fmt.Fprintf(&b, "\nTurn %d · %s · %s\n  kept messages %s · omitted messages %s", turn.Number, turn.Status, execution, formatSourceOrdinals(turn.SelectedSources), formatSourceOrdinals(turn.OmittedSources))
		if turn.Reason != "" {
			fmt.Fprintf(&b, " · %s", turn.Reason)
		}
	}
	if status.Exceeded {
		b.WriteString("\nProtected input exceeds budget; provider request was not sent.")
	}
	if status.MetadataError != "" {
		b.WriteString("\nCapacity metadata · " + status.MetadataError)
	}
	fmt.Fprintf(&b, "\nSaved budget/wire · execution %s · record %d · /inspect %s", status.ExecutionID, status.RecordOrdinal, status.ExecutionID)
	return b.String()
}

func formatBudgetValue(value *int64) string {
	if value == nil {
		return "unknown"
	}
	return strconv.FormatInt(*value, 10)
}

func formatSourceOrdinals(sources []contextSourceDisplay) string {
	if len(sources) == 0 {
		return "none"
	}
	values := make([]int64, 0, len(sources))
	for _, source := range sources {
		values = append(values, source.MessageOrdinal)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	var ranges []string
	for i := 0; i < len(values); i++ {
		start, end := values[i], values[i]
		for i+1 < len(values) && values[i+1] <= end+1 {
			i++
			end = values[i]
		}
		value := strconv.FormatInt(start, 10)
		if end != start {
			value += "-" + strconv.FormatInt(end, 10)
		}
		ranges = append(ranges, value)
	}
	return strings.Join(ranges, ",")
}
