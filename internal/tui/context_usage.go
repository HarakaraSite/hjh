package tui

import (
	"fmt"
	"strconv"

	"hjh/internal/contracts"
)

// contextUsageLabel renders the footer fragment for the latest root model
// call: estimated input tokens against Core's effective input budget. It is a
// pure display formatter; Core owns the measurement and budget resolution.
func contextUsageLabel(budget contracts.ContextBudgetStatus) string {
	limit := budget.Summary.Budget.Limit
	if limit != nil && *limit > 0 {
		percent := budget.Summary.EstimatedTokens * 100 / *limit
		mark := ""
		if budget.Summary.Budget.LimitSource == "hook" {
			mark = "*"
		}
		return fmt.Sprintf("ctx %d%%%s", percent, mark)
	}
	return "ctx " + contextTokenLabel(budget.Summary.EstimatedTokens) + " tok"
}

// contextTokenLabel formats token counts without a usable limit: 1000 and
// above truncates to one decimal k, below 1000 stays an integer.
func contextTokenLabel(tokens int64) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%d.%dk", tokens/1000, tokens%1000/100)
	}
	return strconv.FormatInt(tokens, 10)
}

func (m *model) footerContextUsage() string {
	if !m.contextBudgetSet {
		return ""
	}
	return contextUsageLabel(m.contextBudget)
}

func (m *model) setContextBudget(budget contracts.ContextBudgetStatus) {
	m.contextBudget = budget
	m.contextBudgetSet = true
}

func (m *model) clearContextBudget() {
	m.contextBudget = contracts.ContextBudgetStatus{}
	m.contextBudgetSet = false
}
