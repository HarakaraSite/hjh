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

type executionRequestContext struct {
	SchemaVersion     int                  `json:"schemaVersion"`
	LatestRequest     latestRequestLocator `json:"latestRequest"`
	ExternalRelations json.RawMessage      `json:"externalRelations"`
}

// AppendLatestRequest commits the newest prepared provider body, its semantic
// record, and its inspection locator together. Previous request records retain
// their metadata with no body reference. Any body that loses its last owner is
// removed in the same transaction.
func (s *Store) AppendLatestRequest(ctx context.Context, executionID string, input OccurrenceInput, externalRelations json.RawMessage) (Record, error) {
	if input.Kind != "model_request" {
		return Record{}, fmt.Errorf("history latest request: unexpected record kind %q", input.Kind)
	}
	if input.Content == nil {
		// A provider may prepare an empty body. It still has an exact byte
		// sequence to retain, so distinguish it from no content reference.
		input.Content = []byte{}
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

	var oldManifest string
	err = tx.QueryRowContext(ctx, `SELECT manifest_json FROM execution_contexts WHERE execution_id=?`, executionID).Scan(&oldManifest)
	var oldLocator latestRequestLocator
	if err == nil {
		var previous executionRequestContext
		if json.Unmarshal([]byte(oldManifest), &previous) == nil && previous.SchemaVersion == 1 {
			oldLocator = previous.LatestRequest
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, fmt.Errorf("history read previous request locator: %w", err)
	}

	record, err := appendOccurrenceTx(ctx, tx, executionID, input)
	if err != nil {
		return Record{}, err
	}

	var oldDigest sql.NullString
	if oldLocator.RecordID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT content_digest FROM semantic_records
			WHERE record_id=? AND execution_id=? AND kind='model_request'`, oldLocator.RecordID, executionID).Scan(&oldDigest); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Record{}, fmt.Errorf("history previous request locator %q is missing: %w", oldLocator.RecordID, ErrNotFound)
			}
			return Record{}, fmt.Errorf("history read previous request body: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE semantic_records SET content_digest=NULL
			WHERE record_id=? AND execution_id=? AND kind='model_request'`, oldLocator.RecordID, executionID); err != nil {
			return Record{}, fmt.Errorf("history release previous request body: %w", err)
		}
	}

	contextJSON, err := contracts.EncodeCanonicalJSON(contracts.JSON(executionRequestContext{
		SchemaVersion:     1,
		LatestRequest:     latestRequestLocator{RecordID: record.ID, RecordOrdinal: record.Ordinal, RequestOrdinal: metadata.RequestOrdinal},
		ExternalRelations: externalRelations,
	}))
	if err != nil {
		return Record{}, fmt.Errorf("history encode latest request locator: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_contexts(execution_id,manifest_json) VALUES(?,?)
		ON CONFLICT(execution_id) DO UPDATE SET manifest_json=excluded.manifest_json`, executionID, string(contextJSON)); err != nil {
		return Record{}, fmt.Errorf("history save latest request locator: %w", err)
	}
	if oldDigest.Valid {
		if err := deleteContentIfUnreferenced(ctx, tx, oldDigest.String); err != nil {
			return Record{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("history commit latest request: %w", err)
	}
	return record, nil
}

func deleteContentIfUnreferenced(ctx context.Context, tx *sql.Tx, contentDigest string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM contents WHERE content_digest=? AND NOT EXISTS (
		SELECT 1 FROM configurations WHERE snapshot_content_digest=?
	) AND NOT EXISTS (
		SELECT 1 FROM executions WHERE task_content_digest=?
	) AND NOT EXISTS (
		SELECT 1 FROM messages WHERE content_digest=?
	) AND NOT EXISTS (
		SELECT 1 FROM semantic_records WHERE content_digest=?
	) AND NOT EXISTS (
		SELECT 1 FROM diagnostics WHERE content_digest=?
	) AND NOT EXISTS (
		SELECT 1 FROM follow_up_receipts WHERE text_content_digest=?
	) AND NOT EXISTS (
		SELECT 1 FROM command_receipts WHERE json_extract(result_json,'$.contentDigest')=?
			AND (operation='follow-up' OR (operation='steering' AND json_type(result_json,'$.acceptedMessage') IS NULL))
	)`, contentDigest, contentDigest, contentDigest, contentDigest, contentDigest, contentDigest, contentDigest, contentDigest)
	if err != nil {
		return fmt.Errorf("history reclaim unreferenced request body: %w", err)
	}
	return nil
}
