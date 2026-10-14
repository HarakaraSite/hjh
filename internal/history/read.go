package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"hjh/internal/contracts"
)

type Message struct {
	ExecutionID string
	Ordinal     int64
	TurnNumber  int64
	JSON        json.RawMessage
}

// Messages returns execution messages in ordinal order. fromOrdinal is
// inclusive; toOrdinal < 0 leaves the upper end open.
func (s *Store) Messages(ctx context.Context, executionID string, fromOrdinal, toOrdinal int64) ([]Message, error) {
	query := `
		SELECT m.execution_id, m.message_ordinal, c.content_bytes
		FROM messages m JOIN contents c ON c.content_digest=m.content_digest
		WHERE m.execution_id=? AND m.message_ordinal>=?`
	args := []any{executionID, fromOrdinal}
	if toOrdinal >= 0 {
		query += " AND m.message_ordinal<=?"
		args = append(args, toOrdinal)
	}
	query += " ORDER BY m.message_ordinal"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("history read messages: %w", err)
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var message Message
		var bytes []byte
		if err := rows.Scan(&message.ExecutionID, &message.Ordinal, &bytes); err != nil {
			return nil, fmt.Errorf("history scan message: %w", err)
		}
		message.JSON = append(json.RawMessage(nil), bytes...)
		result = append(result, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history read messages: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history close messages: %w", err)
	}
	for index := range result {
		result[index].JSON, _, err = s.expandStoredToolMessage(ctx, executionID, result[index].JSON)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Conversation returns adopted messages in turn order. Turn bounds are
// inclusive; fromTurn <= 0 starts at the beginning and toTurn < 0 leaves the
// upper end open.
func (s *Store) Conversation(ctx context.Context, sessionID string, fromTurn, toTurn int64) ([]Message, error) {
	query := `
		SELECT c.execution_id, c.execution_message_ordinal, c.turn_number, body.content_bytes
		FROM conversation_messages c
		JOIN messages m ON m.execution_id=c.execution_id
			AND m.message_ordinal=c.execution_message_ordinal
		JOIN contents body ON body.content_digest=m.content_digest
		WHERE c.session_id=?`
	args := []any{sessionID}
	if fromTurn > 0 {
		query += " AND c.turn_number>=?"
		args = append(args, fromTurn)
	}
	if toTurn >= 0 {
		query += " AND c.turn_number<=?"
		args = append(args, toTurn)
	}
	query += " ORDER BY c.message_ordinal"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("history read conversation: %w", err)
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var message Message
		var bytes []byte
		if err := rows.Scan(&message.ExecutionID, &message.Ordinal, &message.TurnNumber, &bytes); err != nil {
			return nil, fmt.Errorf("history scan conversation message: %w", err)
		}
		message.JSON = append(json.RawMessage(nil), bytes...)
		result = append(result, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history read conversation: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history close conversation: %w", err)
	}
	for index := range result {
		result[index].JSON, _, err = s.expandStoredToolMessage(ctx, result[index].ExecutionID, result[index].JSON)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Records returns semantic records in ordinal order. fromOrdinal is inclusive;
// toOrdinal < 0 leaves the upper end open.
func (s *Store) Records(ctx context.Context, executionID string, fromOrdinal, toOrdinal int64) ([]Record, error) {
	query := `
		SELECT semantic_records.record_id, semantic_records.execution_id, semantic_records.ordinal,
			semantic_records.kind, semantic_records.observed_at, semantic_records.payload_json,
			semantic_records.content_digest, contents.content_digest, contents.content_bytes
		FROM semantic_records
		LEFT JOIN contents ON contents.content_digest=semantic_records.content_digest
		WHERE semantic_records.execution_id=? AND semantic_records.ordinal>=?`
	args := []any{executionID, fromOrdinal}
	if toOrdinal >= 0 {
		query += " AND semantic_records.ordinal<=?"
		args = append(args, toOrdinal)
	}
	query += " ORDER BY semantic_records.ordinal"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("history read semantic records: %w", err)
	}
	type storedRecord struct {
		record       Record
		digest       sql.NullString
		storedDigest sql.NullString
		content      []byte
	}
	var stored []storedRecord
	for rows.Next() {
		var item storedRecord
		var payload string
		if err := rows.Scan(
			&item.record.ID, &item.record.ExecutionID, &item.record.Ordinal,
			&item.record.Kind, &item.record.ObservedAt, &payload, &item.digest,
			&item.storedDigest, &item.content,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("history scan semantic record: %w", err)
		}
		item.record.Payload = json.RawMessage(payload)
		item.record.Conversation = conversationMetadata(item.record.Payload)
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("history read semantic records: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history close semantic records: %w", err)
	}
	result := make([]Record, 0, len(stored))
	for _, item := range stored {
		if item.digest.Valid {
			if !item.storedDigest.Valid {
				return nil, ErrNotFound
			}
			payload, expanded, err := expandStoredToolResultPayload(item.record.Kind,
				item.record.Payload, item.digest.String, item.content)
			if err != nil {
				return nil, err
			}
			if expanded {
				item.record.Payload = payload
			} else if item.record.Kind != "tool_body" {
				item.record.Content = append([]byte(nil), item.content...)
			}
		}
		item.record.Payload, _, err = s.expandStoredToolMessage(ctx, executionID, item.record.Payload)
		if err != nil {
			return nil, err
		}
		relations, err := s.readRelations(ctx, item.record.ID)
		if err != nil {
			return nil, err
		}
		item.record.Relations = relations
		result = append(result, item.record)
	}
	return result, nil
}

func conversationMetadata(payload json.RawMessage) *contracts.ConversationMetadata {
	var envelope struct {
		Conversation *contracts.ConversationMetadata `json:"conversation"`
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.Conversation == nil {
		return nil
	}
	metadata := *envelope.Conversation
	return &metadata
}

func (s *Store) readRelations(ctx context.Context, recordID string) ([]Relation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT relation, target_record_id, mandatory, resolved
		FROM semantic_relations WHERE record_id=? ORDER BY relation_ordinal`, recordID)
	if err != nil {
		return nil, fmt.Errorf("history read semantic relations: %w", err)
	}
	defer rows.Close()
	var result []Relation
	for rows.Next() {
		var relation Relation
		var mandatory, resolved int
		if err := rows.Scan(&relation.Relation, &relation.TargetRecordID, &mandatory, &resolved); err != nil {
			return nil, fmt.Errorf("history scan semantic relation: %w", err)
		}
		relation.Mandatory = mandatory != 0
		relation.Resolved = resolved != 0
		result = append(result, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history read semantic relations: %w", err)
	}
	return result, nil
}

func (s *Store) Executions(ctx context.Context, sessionID string) ([]Execution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id FROM executions
		WHERE session_correlation=? ORDER BY turn_number, created_at, execution_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("history list executions: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("history scan execution ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("history list executions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history close executions: %w", err)
	}
	result := make([]Execution, 0, len(ids))
	for _, id := range ids {
		execution, err := s.readExecution(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, execution)
	}
	return result, nil
}

// Children returns direct child executions in creation order.
func (s *Store) Children(ctx context.Context, parentID string) ([]Execution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT execution_id FROM executions
		WHERE parent_execution_id=? ORDER BY created_at, execution_id`, parentID)
	if err != nil {
		return nil, fmt.Errorf("history list child executions: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("history scan child execution ID: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("history list child executions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history close child executions: %w", err)
	}
	result := make([]Execution, 0, len(ids))
	for _, id := range ids {
		execution, err := s.readExecution(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, execution)
	}
	return result, nil
}

func (s *Store) readExecution(ctx context.Context, id string) (Execution, error) {
	var result Execution
	var task []byte
	var sessionID sql.NullString
	var settledAt sql.NullString
	var modelJSON, buildJSON string
	if err := s.db.QueryRowContext(ctx, `
		SELECT e.execution_id, e.task_id, t.content_bytes, e.canonical_session_id,
			e.session_correlation, COALESCE(e.parent_execution_id, ''),
			COALESCE(e.spawn_call_id, ''), e.turn_number, e.base_revision, e.base_message_count,
			e.created_at, e.settled_at, e.agent_name, e.model_json, e.build_json,
			COALESCE(e.configuration_id, ''), e.max_steps, e.lifecycle, e.outcome, e.adoption,
			COALESCE(e.terminal_record_id, '')
		FROM executions e JOIN contents t ON t.content_digest=e.task_content_digest
		WHERE e.execution_id=?`, id,
	).Scan(
		&result.ID, &result.TaskID, &task, &sessionID, &result.SessionCorrelation,
		&result.ParentExecutionID, &result.SpawnCallID,
		&result.Turn, &result.BaseRevision, &result.BaseMessageCount,
		&result.CreatedAt, &settledAt, &result.AgentName, &modelJSON, &buildJSON,
		&result.ConfigurationID, &result.MaxSteps, &result.Lifecycle, &result.Outcome,
		&result.Adoption, &result.TerminalRecordID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Execution{}, ErrNotFound
		}
		return Execution{}, fmt.Errorf("history read execution: %w", err)
	}
	result.Task = string(task)
	result.Model = json.RawMessage(modelJSON)
	result.Build = json.RawMessage(buildJSON)
	if sessionID.Valid {
		result.SessionID = sessionID.String
	}
	if settledAt.Valid {
		result.SettledAt = &settledAt.String
	}
	return result, nil
}

func readContent(ctx context.Context, db *sql.DB, digest string) ([]byte, error) {
	var content []byte
	if err := db.QueryRowContext(ctx,
		"SELECT content_bytes FROM contents WHERE content_digest=?", digest,
	).Scan(&content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read content: %w", err)
	}
	return append([]byte(nil), content...), nil
}

func (s *Store) Content(ctx context.Context, digest string) ([]byte, error) {
	return readContent(ctx, s.db, digest)
}

func (s *Store) Context(ctx context.Context, executionID string) (json.RawMessage, error) {
	var value string
	if err := s.db.QueryRowContext(ctx,
		"SELECT manifest_json FROM execution_contexts WHERE execution_id=?", executionID,
	).Scan(&value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read execution context: %w", err)
	}
	return json.RawMessage(value), nil
}
