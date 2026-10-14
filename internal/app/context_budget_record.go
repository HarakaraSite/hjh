package app

import (
	"encoding/json"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
)

type contextBudgetRecordPayload struct {
	ModelStep       int                            `json:"modelStep"`
	ModelSelection  contracts.ModelSelection       `json:"modelSelection"`
	MetadataError   string                         `json:"metadataError,omitempty"`
	Exceeded        bool                           `json:"exceeded"`
	Summary         contracts.ContextBudgetSummary `json:"summary"`
	DetailAvailable bool                           `json:"detailAvailable"`
}

func contextBudgetRecord(step int, selection contracts.ModelSelection, metadataError string,
	exceeded bool, fact contextbudget.Fact) (json.RawMessage, json.RawMessage) {
	summary := contracts.ContextBudgetSummary{
		Budget: contracts.ContextBudgetValues{
			ContextTokens: fact.Budget.ContextTokens,
			InputTokens:   fact.Budget.InputTokens,
			OutputReserve: fact.Budget.OutputReserve,
			InputRatio:    fact.Budget.InputRatio,
			HistoryTokens: fact.Budget.HistoryTokens,
			Limit:         fact.Budget.Limit,
			Sources: map[string]string{
				"contextTokens": fact.Budget.Sources.ContextTokens,
				"inputTokens":   fact.Budget.Sources.InputTokens,
				"outputReserve": fact.Budget.Sources.OutputReserve,
				"inputRatio":    fact.Budget.Sources.InputRatio,
				"historyTokens": fact.Budget.Sources.HistoryTokens,
			},
		},
		Estimator:                  fact.Estimator,
		WireBytes:                  fact.WireBytes,
		EstimatedTokens:            fact.EstimatedTokens,
		HistoryWireBytes:           fact.HistoryWireBytes,
		HistoryEstimatedTokens:     fact.HistoryEstimatedTokens,
		SelectedMessageCount:       len(fact.Sources),
		OmittedCurrentMessageCount: len(fact.OmittedCurrent),
		HistoryStatus:              fact.HistoryStatus,
		HistoryReason:              fact.History.Reason,
	}
	payload := contextBudgetRecordPayload{
		ModelStep: step, ModelSelection: selection, MetadataError: metadataError,
		Exceeded: exceeded, Summary: summary, DetailAvailable: false,
	}
	return contracts.JSON(payload), contracts.JSON(fact)
}
