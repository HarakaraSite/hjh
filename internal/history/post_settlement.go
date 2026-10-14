package history

import (
	"context"
	"encoding/json"
	"fmt"
)

// AppendPostSettlementObservation stores an observation after execution
// settlement while preserving the saved terminal record and execution outcome.
func (s *Store) AppendPostSettlementObservation(ctx context.Context, executionID string, input OccurrenceInput) (Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("history begin post-settlement observation: %w", err)
	}
	defer tx.Rollback()
	record, err := appendPostSettlementOccurrenceTx(ctx, tx, executionID, input)
	if err != nil {
		return Record{}, err
	}
	publicRecord, err := presentOccurrenceRecord(record)
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("history commit post-settlement observation: %w", err)
	}
	return publicRecord, nil
}

func addPostSettlementFields(payload json.RawMessage, terminalRecordID string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("history post-settlement observation requires an object payload: %w", ErrInvalidJSON)
	}
	object["postSettlement"] = json.RawMessage(`true`)
	terminalID, err := json.Marshal(terminalRecordID)
	if err != nil {
		return nil, fmt.Errorf("history encode terminal record ID: %w", err)
	}
	object["terminalRecordId"] = terminalID
	return json.Marshal(object)
}
