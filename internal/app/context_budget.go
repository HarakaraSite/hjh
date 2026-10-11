package app

import (
	"context"
	"encoding/json"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

func budgetHistoryReader(store *history.Store, input executionInput) contextbudget.HistoryReader {
	if input.session == nil {
		return nil
	}
	cache := map[int64]*contextbudget.Turn{}
	return func(ctx context.Context, before int64) (*contextbudget.Turn, error) {
		if before <= 0 {
			before = input.execution.Turn
		}
		number, err := store.PreviousConversationTurn(ctx, input.rootSessionID, input.fromTurn, before)
		if err != nil || number == 0 {
			return nil, err
		}
		if turn := cache[number]; turn != nil {
			return turn, nil
		}
		stored, err := store.Conversation(ctx, input.rootSessionID, number, number)
		if err != nil {
			return nil, err
		}
		turn := &contextbudget.Turn{Number: number}
		for _, saved := range stored {
			var message contracts.Message
			if err := json.Unmarshal(saved.JSON, &message); err != nil {
				return nil, err
			}
			if saved.TurnNumber < input.session.PrivateStateFromTurn || input.previousModel.Provider != input.model.Provider || input.previousModel.ModelID != input.model.ModelID {
				message.ProviderState = nil
			}
			turn.Entries = append(turn.Entries, contextbudget.Entry{Message: message, Source: contextbudget.Source{
				Kind: "history", ExecutionID: saved.ExecutionID, Turn: saved.TurnNumber, MessageOrdinal: saved.Ordinal}})
		}
		cache[number] = turn
		return turn, nil
	}
}

// Notices are Core facts. The clients only render them; they do not decide
// what history to select. Each kind is emitted once per execution.
func contextBudgetNotice(j *journal, step int, fact contextbudget.Fact, seen map[string]bool) error {
	var notices []struct{ name, text string }
	if len(fact.OmittedCurrent) > 0 {
		notices = append(notices, struct{ name, text string }{"current_trimmed", "Context budget: older exchanges in this turn were omitted from model input. Full history remains saved."})
	}
	if fact.HistoryStatus == "partial" || fact.HistoryStatus == "omitted" {
		notices = append(notices, struct{ name, text string }{"history_" + fact.HistoryStatus, "Context budget: saved history was selected to fit the model input budget. Full history remains saved."})
	}
	for _, notice := range notices {
		if seen[notice.name] {
			continue
		}
		if err := j.observe(contracts.Observation{Kind: "context", Name: notice.name, Phase: "completed", Preview: notice.text, ModelStep: step}); err != nil {
			return err
		}
		seen[notice.name] = true
	}
	return nil
}
