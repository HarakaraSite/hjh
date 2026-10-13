package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// DeleteSession removes the Session and its execution subtree in one
// transaction, including its configurations and bodies that no remaining
// record references.
// The caller owns stopping live runs before this offline operation.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history begin Session deletion: %w", err)
	}
	defer tx.Rollback()
	var found string
	if err := tx.QueryRowContext(ctx, "SELECT session_id FROM sessions WHERE session_id=?", sessionID).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	const owned = `WITH RECURSIVE owned(execution_id) AS (
		SELECT execution_id FROM executions WHERE canonical_session_id=? OR session_correlation=?
		UNION
		SELECT child.execution_id FROM executions child JOIN owned parent ON child.parent_execution_id=parent.execution_id
	) `
	// Collect before cascades remove the references. Only data belonging to
	// this deletion is considered; unrelated orphaned data is left alone.
	rows, err := tx.QueryContext(ctx, owned+`, owned_configurations AS (
		SELECT DISTINCT configuration_id FROM executions
		WHERE execution_id IN (SELECT execution_id FROM owned) AND configuration_id IS NOT NULL
	), owned_commands AS (
		SELECT operation,result_json FROM command_receipts
		WHERE json_extract(target_json,'$.sessionId')=? OR execution_id IN (SELECT execution_id FROM owned)
	), owned_follow_ups AS (
		SELECT text_content_digest FROM follow_up_receipts WHERE session_correlation=?
			OR parent_execution_id IN (SELECT execution_id FROM owned)
			OR started_execution_id IN (SELECT execution_id FROM owned)
	)
	SELECT 'configuration',configuration_id FROM owned_configurations
	UNION SELECT 'content',snapshot_content_digest FROM configurations
		WHERE configuration_id IN (SELECT configuration_id FROM owned_configurations)
	UNION SELECT 'content',task_content_digest FROM executions WHERE execution_id IN (SELECT execution_id FROM owned)
	UNION SELECT 'content',content_digest FROM messages WHERE execution_id IN (SELECT execution_id FROM owned)
	UNION SELECT 'content',content_digest FROM semantic_records
		WHERE execution_id IN (SELECT execution_id FROM owned) AND content_digest IS NOT NULL
	UNION SELECT 'content',content_digest FROM diagnostics WHERE execution_id IN (SELECT execution_id FROM owned)
	UNION SELECT 'content',text_content_digest FROM owned_follow_ups
	UNION SELECT 'content',json_extract(result_json,'$.contentDigest') FROM owned_commands
		WHERE (operation='follow-up' OR (operation='steering' AND json_type(result_json,'$.acceptedMessage') IS NULL))
			AND json_extract(result_json,'$.contentDigest') IS NOT NULL`, sessionID, sessionID, sessionID, sessionID)
	if err != nil {
		return fmt.Errorf("history collect Session deletion data: %w", err)
	}
	var configurationIDs, contentDigests []string
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("history scan Session deletion data: %w", err)
		}
		if kind == "configuration" {
			configurationIDs = append(configurationIDs, id)
		} else {
			contentDigests = append(contentDigests, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("history collect Session deletion data: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("history close Session deletion data: %w", err)
	}
	statements := []struct {
		query string
		args  []any
	}{
		{"DELETE FROM core_session_cursors WHERE session_id=?", []any{sessionID}},
		{owned + `DELETE FROM data_session_read_cursors WHERE session_correlation=?
			OR latest_execution_id IN (SELECT execution_id FROM owned)`, []any{sessionID, sessionID, sessionID}},
		{owned + `DELETE FROM command_receipts WHERE json_extract(target_json,'$.sessionId')=?
			OR execution_id IN (SELECT execution_id FROM owned)`, []any{sessionID, sessionID, sessionID}},
		{owned + `DELETE FROM follow_up_receipts WHERE session_correlation=?
			OR parent_execution_id IN (SELECT execution_id FROM owned)
			OR started_execution_id IN (SELECT execution_id FROM owned)`, []any{sessionID, sessionID, sessionID}},
		{"DELETE FROM conversation_messages WHERE session_id=?", []any{sessionID}},
		{"DELETE FROM session_turns WHERE session_id=?", []any{sessionID}},
		{owned + `DELETE FROM recall_relations WHERE source_execution_id IN (SELECT execution_id FROM owned)
			OR target_execution_id IN (SELECT execution_id FROM owned)`, []any{sessionID, sessionID}},
		{owned + "DELETE FROM executions WHERE execution_id IN (SELECT execution_id FROM owned)", []any{sessionID, sessionID}},
		{"DELETE FROM sessions WHERE session_id=?", []any{sessionID}},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("history delete Session data: %w", err)
		}
	}
	configurationJSON, err := json.Marshal(configurationIDs)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM configurations
		WHERE configuration_id IN (SELECT value FROM json_each(?))
			AND configuration_id NOT IN (SELECT configuration_id FROM executions WHERE configuration_id IS NOT NULL)`, string(configurationJSON)); err != nil {
		return fmt.Errorf("history delete Session configurations: %w", err)
	}
	contentJSON, err := json.Marshal(contentDigests)
	if err != nil {
		return err
	}
	// Include both relational references and legacy input receipts whose body
	// reference lives in JSON. New steering receipts own their acceptedMessage;
	// their contentDigest is a checksum, not a contents reference.
	if _, err := tx.ExecContext(ctx, `DELETE FROM contents
		WHERE content_digest IN (SELECT value FROM json_each(?)) AND content_digest NOT IN (
			SELECT snapshot_content_digest FROM configurations
			UNION SELECT task_content_digest FROM executions
			UNION SELECT content_digest FROM messages
			UNION SELECT content_digest FROM semantic_records WHERE content_digest IS NOT NULL
			UNION SELECT content_digest FROM diagnostics
			UNION SELECT text_content_digest FROM follow_up_receipts
			UNION SELECT json_extract(result_json,'$.contentDigest') FROM command_receipts
				WHERE (operation='follow-up' OR (operation='steering' AND json_type(result_json,'$.acceptedMessage') IS NULL))
					AND json_extract(result_json,'$.contentDigest') IS NOT NULL
		)`, string(contentJSON)); err != nil {
		return fmt.Errorf("history delete Session contents: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history commit Session deletion: %w", err)
	}
	return nil
}
