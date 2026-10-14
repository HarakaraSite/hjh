package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"hjh/internal/contracts"
)

type latestRequestLocator struct {
	RecordID       string `json:"recordId"`
	RecordOrdinal  int64  `json:"recordOrdinal"`
	RequestOrdinal int    `json:"requestOrdinal"`
}

// AppendLatestRequest commits request metadata and the exact prepared provider
// body together. The body belongs to the execution's replaceable latest row;
// it is not stored in shared contents or on the semantic record.
func (s *Store) AppendLatestRequest(ctx context.Context, executionID string, input OccurrenceInput, externalRelations json.RawMessage) (Record, error) {
	if input.Kind != "model_request" {
		return Record{}, fmt.Errorf("history latest request: unexpected record kind %q", input.Kind)
	}
	wireBody := input.Content
	if wireBody == nil {
		// An empty prepared body is still a present byte sequence.
		wireBody = []byte{}
	}
	if externalRelations == nil {
		externalRelations = json.RawMessage(`[]`)
	}
	if !validJSON(externalRelations) {
		return Record{}, ErrInvalidJSON
	}
	var metadata struct {
		RequestOrdinal int `json:"requestOrdinal"`
	}
	if !validJSON(input.Payload) || json.Unmarshal(input.Payload, &metadata) != nil {
		return Record{}, ErrInvalidJSON
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("history begin latest request: %w", err)
	}
	defer tx.Rollback()

	// appendOccurrenceTx stores only the small logical metadata. In particular,
	// it does not hash, insert, or attach the wire body through contents.
	input.Content = nil
	record, err := appendOccurrenceTx(ctx, tx, executionID, input)
	if err != nil {
		return Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_latest_requests(execution_id,record_id,wire_body)
		VALUES(?,?,?) ON CONFLICT(execution_id) DO UPDATE SET
			record_id=excluded.record_id,wire_body=excluded.wire_body`, executionID, record.ID, wireBody); err != nil {
		return Record{}, fmt.Errorf("history replace latest request body: %w", err)
	}

	manifest := map[string]json.RawMessage{
		"schemaVersion":     contracts.JSON(1),
		"externalRelations": externalRelations,
	}
	var oldManifest string
	err = tx.QueryRowContext(ctx, `SELECT manifest_json FROM execution_contexts WHERE execution_id=?`, executionID).Scan(&oldManifest)
	if err == nil {
		var previous map[string]json.RawMessage
		if json.Unmarshal([]byte(oldManifest), &previous) == nil && previous != nil {
			manifest = previous
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("history read execution context manifest: %w", err)
	}
	delete(manifest, "latestRequest")
	manifest["schemaVersion"] = contracts.JSON(1)
	manifest["externalRelations"] = append(json.RawMessage(nil), externalRelations...)
	contextJSON, err := contracts.EncodeCanonicalJSON(contracts.JSON(manifest))
	if err != nil {
		return Record{}, fmt.Errorf("history encode execution context manifest: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_contexts(execution_id,manifest_json) VALUES(?,?)
		ON CONFLICT(execution_id) DO UPDATE SET manifest_json=excluded.manifest_json`, executionID, string(contextJSON)); err != nil {
		return Record{}, fmt.Errorf("history save execution context manifest: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("history commit latest request: %w", err)
	}
	record.Content = make([]byte, len(wireBody))
	copy(record.Content, wireBody)
	return record, nil
}
