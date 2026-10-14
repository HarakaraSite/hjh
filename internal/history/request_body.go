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

// AppendLatestRequestMetadata appends a model_request record and its recall
// relations without persisting the prepared provider body. The caller can
// later save that body against the returned record ID.
func (s *Store) AppendLatestRequestMetadata(ctx context.Context, executionID string, input OccurrenceInput, externalRelations json.RawMessage) (Record, error) {
	if input.Kind != "model_request" {
		return Record{}, fmt.Errorf("history latest request metadata: unexpected record kind %q", input.Kind)
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
		return Record{}, fmt.Errorf("history begin latest request metadata: %w", err)
	}
	defer tx.Rollback()
	record, err := appendLatestRequestMetadataTx(ctx, tx, executionID, input, externalRelations)
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("history commit latest request metadata: %w", err)
	}
	return record, nil
}

func appendLatestRequestMetadataTx(ctx context.Context, tx *sql.Tx, executionID string, input OccurrenceInput, externalRelations json.RawMessage) (Record, error) {
	// Metadata records do not own or hash the request body through contents.
	input.Content = nil
	record, err := appendOccurrenceTx(ctx, tx, executionID, input)
	if err != nil {
		return Record{}, err
	}
	if err := saveRequestExternalRelationsTx(ctx, tx, executionID, externalRelations); err != nil {
		return Record{}, err
	}
	return record, nil
}

func saveRequestExternalRelationsTx(ctx context.Context, tx *sql.Tx, executionID string, externalRelations json.RawMessage) error {
	manifest := map[string]json.RawMessage{
		"schemaVersion":     contracts.JSON(1),
		"externalRelations": externalRelations,
	}
	var oldManifest string
	err := tx.QueryRowContext(ctx, `SELECT manifest_json FROM execution_contexts WHERE execution_id=?`, executionID).Scan(&oldManifest)
	if err == nil {
		var previous map[string]json.RawMessage
		if json.Unmarshal([]byte(oldManifest), &previous) == nil && previous != nil {
			manifest = previous
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("history read execution context manifest: %w", err)
	}
	delete(manifest, "latestRequest")
	manifest["schemaVersion"] = contracts.JSON(1)
	manifest["externalRelations"] = append(json.RawMessage(nil), externalRelations...)
	contextJSON, err := contracts.EncodeCanonicalJSON(contracts.JSON(manifest))
	if err != nil {
		return fmt.Errorf("history encode execution context manifest: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_contexts(execution_id,manifest_json) VALUES(?,?)
		ON CONFLICT(execution_id) DO UPDATE SET manifest_json=excluded.manifest_json`, executionID, string(contextJSON)); err != nil {
		return fmt.Errorf("history save execution context manifest: %w", err)
	}
	return nil
}
