package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// AppendBudgetSummary appends one context_budget record without persisting its
// detailed Fact. The caller can later save the Fact against the returned ID.
func (s *Store) AppendBudgetSummary(ctx context.Context, executionID string, summary json.RawMessage) (Record, error) {
	if !validJSON(summary) {
		return Record{}, ErrInvalidJSON
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("history begin budget summary: %w", err)
	}
	defer tx.Rollback()
	record, err := appendBudgetSummaryTx(ctx, tx, executionID, summary)
	if err != nil {
		return Record{}, err
	}
	publicRecord, err := presentOccurrenceRecord(record)
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("history commit budget summary: %w", err)
	}
	return publicRecord, nil
}

func appendBudgetSummaryTx(ctx context.Context, tx *sql.Tx, executionID string, summary json.RawMessage) (Record, error) {
	return appendOccurrenceTx(ctx, tx, executionID, OccurrenceInput{
		Kind: "context_budget", Payload: summary,
	})
}
