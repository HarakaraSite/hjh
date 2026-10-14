package history

import (
	"context"
	"encoding/json"
	"fmt"
)

// AppendLatestBudget stores one scalar decision record and atomically replaces
// the execution's full latest Fact snapshot.
func (s *Store) AppendLatestBudget(ctx context.Context, executionID string, summary, fact json.RawMessage) (Record, error) {
	if !validJSON(summary) || !validJSON(fact) {
		return Record{}, ErrInvalidJSON
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("history begin latest budget: %w", err)
	}
	defer tx.Rollback()

	record, err := appendOccurrenceTx(ctx, tx, executionID, OccurrenceInput{
		Kind: "context_budget", Payload: summary,
	})
	if err != nil {
		return Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO execution_latest_budgets(execution_id, record_id, fact_json)
		VALUES(?, ?, ?)
		ON CONFLICT(execution_id) DO UPDATE SET
			record_id=excluded.record_id,
			fact_json=excluded.fact_json`, executionID, record.ID, string(fact)); err != nil {
		return Record{}, fmt.Errorf("history replace latest budget: %w", err)
	}
	publicRecord, err := presentOccurrenceRecord(record)
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("history commit latest budget: %w", err)
	}
	return publicRecord, nil
}
