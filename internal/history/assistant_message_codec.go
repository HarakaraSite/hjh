package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"hjh/internal/contracts"
)

// compactAssistantMessage uses an explicit prior result record from the
// journal. Without that handoff, independent Store writes retain inline state.
// Visible content/text stay inline for AssistantReports' SQL and cursor.
func compactAssistantMessage(message json.RawMessage, resultRecordID string) (json.RawMessage, bool, error) {
	if resultRecordID == "" {
		return message, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(message, &fields); err != nil {
		return nil, false, fmt.Errorf("history decode assistant Message: %w", err)
	}
	if string(fields["role"]) != `"assistant"` {
		return message, false, nil
	}
	if _, hasState := fields["providerState"]; !hasState {
		return message, false, nil
	}
	delete(fields, "providerState")
	fields["providerStateRecordId"] = contracts.JSON(resultRecordID)
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, false, fmt.Errorf("history encode assistant Message reference: %w", err)
	}
	canonical, err := contracts.EncodeCanonicalJSON(encoded)
	if err != nil {
		return nil, false, fmt.Errorf("history canonicalize assistant Message: %w", err)
	}
	return json.RawMessage(canonical), true, nil
}

// expandStoredAssistantMessage resolves only provider continuation state. Its
// owner is the same execution's immutable result, not a thinking projection.
// Callers close SQL rows before invoking this codec.
func (s *Store) expandStoredAssistantMessage(ctx context.Context, executionID string, message json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(message, &fields); err != nil || fields == nil {
		return message, nil
	}
	rawRef, referenced := fields["providerStateRecordId"]
	if !referenced || string(fields["role"]) != `"assistant"` {
		return message, nil
	}
	var recordID string
	if err := json.Unmarshal(rawRef, &recordID); err != nil {
		return nil, fmt.Errorf("history decode assistant Message result record: %w", err)
	}
	var state sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT json_extract(body.content_bytes,'$.providerState')
  FROM semantic_records owner JOIN contents body ON body.content_digest=owner.content_digest
  WHERE owner.execution_id=? AND owner.record_id=? AND owner.kind='model_result'`,
		executionID, recordID).Scan(&state); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read assistant Message provider state: %w", err)
	}
	delete(fields, "providerStateRecordId")
	if state.Valid {
		fields["providerState"] = json.RawMessage(state.String)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("history expand assistant Message: %w", err)
	}
	canonical, err := contracts.EncodeCanonicalJSON(encoded)
	if err != nil {
		return nil, fmt.Errorf("history canonicalize expanded assistant Message: %w", err)
	}
	return json.RawMessage(canonical), nil
}
