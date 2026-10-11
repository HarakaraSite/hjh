package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ConversationState is the active latest value captured at the same cut as
// permanent records. EventJSON is copied while the short read transaction is
// open because the row may be replaced or removed after the cut.
type ConversationState struct {
	Lane              string
	ModelStep         int64
	RequestOrdinal    int64
	FirstEventOrdinal int64
	EventJSON         json.RawMessage
}

type ConversationExecutionCut struct {
	RootExecutionID      string
	ExecutionID          string
	ParentExecutionID    string
	SpawnCallID          string
	TaskContentDigest    string
	TerminalRecordID     string
	Lifecycle            string
	Outcome              string
	Adoption             string
	AgentName            string
	Turn                 int64
	DisplayOrdinal       int64
	LatestRecordOrdinal  int64
	LatestMessageOrdinal int64
	States               []ConversationState
}

// ConversationCut describes one root and its descendant executions at a
// fixed storage point. It contains metadata and active state only, never the
// permanent conversation body array.
type ConversationCut struct {
	SessionID            string
	RootExecutionID      string
	RootExecutionOrdinal int64
	ThroughSequence      *uint64
	Executions           []ConversationExecutionCut
}

type SessionConversationCut struct {
	SessionID     string
	StateRevision int64
	MessageCount  int64
	Roots         []ConversationCut
}

type TerminalResult struct {
	ExecutionID    string
	Task           string
	Outcome        Outcome
	RuntimeOutcome json.RawMessage
}

type conversationQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CaptureConversationCut fixes metadata, record/message high-water marks, and
// active latest states in a short transaction. It does not load permanent
// payloads, source contents, or tool details.
func (s *Store) CaptureConversationCut(ctx context.Context, rootExecutionID string) (ConversationCut, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ConversationCut{}, fmt.Errorf("history begin conversation cut: %w", err)
	}
	defer tx.Rollback()
	cut, err := captureConversationCut(ctx, tx, rootExecutionID)
	if err != nil {
		return ConversationCut{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConversationCut{}, fmt.Errorf("history commit conversation cut: %w", err)
	}
	return cut, nil
}

// CaptureSessionConversationCut fixes the saved root ordering and each root's
// descendant cuts using one short SQLite read transaction.
func (s *Store) CaptureSessionConversationCut(ctx context.Context, sessionID string) (SessionConversationCut, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return SessionConversationCut{}, fmt.Errorf("history begin Session conversation cut: %w", err)
	}
	defer tx.Rollback()
	var cut SessionConversationCut
	cut.SessionID = sessionID
	if err := tx.QueryRowContext(ctx,
		"SELECT state_revision, message_count FROM sessions WHERE session_id=?", sessionID,
	).Scan(&cut.StateRevision, &cut.MessageCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionConversationCut{}, ErrNotFound
		}
		return SessionConversationCut{}, fmt.Errorf("history read Session conversation cut: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT e.execution_id
		FROM executions e JOIN execution_display_positions p ON p.execution_id=e.execution_id
		WHERE e.session_correlation=? AND e.parent_execution_id IS NULL
		ORDER BY p.execution_ordinal`, sessionID)
	if err != nil {
		return SessionConversationCut{}, fmt.Errorf("history list root executions for conversation cut: %w", err)
	}
	var rootIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return SessionConversationCut{}, fmt.Errorf("history scan root execution for conversation cut: %w", err)
		}
		rootIDs = append(rootIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SessionConversationCut{}, fmt.Errorf("history read root executions for conversation cut: %w", err)
	}
	if err := rows.Close(); err != nil {
		return SessionConversationCut{}, fmt.Errorf("history close root executions for conversation cut: %w", err)
	}
	cut.Roots = make([]ConversationCut, 0, len(rootIDs))
	for _, id := range rootIDs {
		root, err := captureConversationCut(ctx, tx, id)
		if err != nil {
			return SessionConversationCut{}, err
		}
		cut.Roots = append(cut.Roots, root)
	}
	if err := tx.Commit(); err != nil {
		return SessionConversationCut{}, fmt.Errorf("history commit Session conversation cut: %w", err)
	}
	return cut, nil
}

func captureConversationCut(ctx context.Context, q conversationQueryer, rootExecutionID string) (ConversationCut, error) {
	cut := ConversationCut{RootExecutionID: rootExecutionID}
	if err := q.QueryRowContext(ctx, `
		SELECT e.session_correlation, p.execution_ordinal
		FROM executions e JOIN execution_display_positions p ON p.execution_id=e.execution_id
		WHERE e.execution_id=? AND e.parent_execution_id IS NULL`, rootExecutionID,
	).Scan(&cut.SessionID, &cut.RootExecutionOrdinal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ConversationCut{}, ErrNotFound
		}
		return ConversationCut{}, fmt.Errorf("history read root for conversation cut: %w", err)
	}
	rows, err := q.QueryContext(ctx, `
		WITH RECURSIVE tree(execution_id) AS (
			SELECT ?
			UNION ALL
			SELECT e.execution_id FROM executions e JOIN tree t ON e.parent_execution_id=t.execution_id
			WHERE EXISTS (
				SELECT 1 FROM semantic_records start_record
				WHERE start_record.execution_id=e.execution_id AND start_record.kind='execution_admission'
			)
		)
		SELECT e.execution_id, COALESCE(e.parent_execution_id,''), COALESCE(e.spawn_call_id,''),
			e.task_content_digest, COALESCE(e.terminal_record_id,''), e.lifecycle, e.outcome,
			e.adoption, e.agent_name, e.turn_number, COALESCE(p.execution_ordinal,-1),
			e.latest_ordinal,
			COALESCE((SELECT MAX(m.message_ordinal) FROM messages m WHERE m.execution_id=e.execution_id),-1)
		FROM tree t JOIN executions e ON e.execution_id=t.execution_id
		LEFT JOIN execution_display_positions p ON p.execution_id=e.execution_id
		ORDER BY COALESCE(p.execution_ordinal,-1), e.execution_id`, rootExecutionID)
	if err != nil {
		return ConversationCut{}, fmt.Errorf("history read execution metadata for conversation cut: %w", err)
	}
	for rows.Next() {
		var item ConversationExecutionCut
		item.RootExecutionID = rootExecutionID
		if err := rows.Scan(&item.ExecutionID, &item.ParentExecutionID, &item.SpawnCallID,
			&item.TaskContentDigest, &item.TerminalRecordID, &item.Lifecycle, &item.Outcome,
			&item.Adoption, &item.AgentName, &item.Turn, &item.DisplayOrdinal,
			&item.LatestRecordOrdinal, &item.LatestMessageOrdinal); err != nil {
			_ = rows.Close()
			return ConversationCut{}, fmt.Errorf("history scan execution metadata for conversation cut: %w", err)
		}
		cut.Executions = append(cut.Executions, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return ConversationCut{}, fmt.Errorf("history read execution metadata for conversation cut: %w", err)
	}
	if err := rows.Close(); err != nil {
		return ConversationCut{}, fmt.Errorf("history close execution metadata for conversation cut: %w", err)
	}
	if len(cut.Executions) == 0 {
		return ConversationCut{}, ErrNotFound
	}
	for i := range cut.Executions {
		item := &cut.Executions[i]
		states, err := q.QueryContext(ctx, `
			SELECT lane, model_step, request_ordinal, first_event_ordinal, event_json
			FROM assistant_text_states WHERE execution_id=?
			ORDER BY lane, model_step, request_ordinal`, item.ExecutionID)
		if err != nil {
			return ConversationCut{}, fmt.Errorf("history read active conversation states: %w", err)
		}
		for states.Next() {
			var state ConversationState
			var event string
			if err := states.Scan(&state.Lane, &state.ModelStep, &state.RequestOrdinal, &state.FirstEventOrdinal, &event); err != nil {
				_ = states.Close()
				return ConversationCut{}, fmt.Errorf("history scan active conversation state: %w", err)
			}
			state.EventJSON = append(json.RawMessage(nil), event...)
			item.States = append(item.States, state)
		}
		if err := states.Err(); err != nil {
			_ = states.Close()
			return ConversationCut{}, fmt.Errorf("history read active conversation states: %w", err)
		}
		if err := states.Close(); err != nil {
			return ConversationCut{}, fmt.Errorf("history close active conversation states: %w", err)
		}
	}
	var through sql.NullInt64
	if err := q.QueryRowContext(ctx, `
		WITH RECURSIVE tree(execution_id) AS (
			SELECT ?
			UNION ALL
			SELECT e.execution_id FROM executions e JOIN tree t ON e.parent_execution_id=t.execution_id
		), saved(sequence) AS (
			SELECT CASE WHEN json_type(r.payload_json, '$.conversation.sequence')='integer'
				THEN CAST(json_extract(r.payload_json, '$.conversation.sequence') AS INTEGER) END
			FROM semantic_records r JOIN tree t ON t.execution_id=r.execution_id
			WHERE r.kind IN ('assistant_message','execution_admission','host_decision','model_result','tool_call','tool_result','user_message')
			UNION ALL
			SELECT CAST(json_extract(s.event_json, '$.conversation.sequence') AS INTEGER)
			FROM assistant_text_states s JOIN tree t ON t.execution_id=s.execution_id
			WHERE json_type(s.event_json, '$.conversation.sequence')='integer'
		)
		SELECT MAX(sequence) FROM saved`, rootExecutionID).Scan(&through); err != nil {
		return ConversationCut{}, fmt.Errorf("history read conversation cut sequence: %w", err)
	}
	if through.Valid {
		sequence := uint64(through.Int64)
		cut.ThroughSequence = &sequence
	}
	return cut, nil
}

// TerminalResult reads only the task and runtime outcome needed to answer a
// completed run query. It does not inspect messages, records, contexts, or
// source contents.
func (s *Store) TerminalResult(ctx context.Context, executionID string) (TerminalResult, error) {
	var result TerminalResult
	var outcome string
	var runtime sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT e.execution_id, t.content_bytes, e.outcome, e.runtime_outcome_json
		FROM executions e JOIN contents t ON t.content_digest=e.task_content_digest
		WHERE e.execution_id=?`, executionID,
	).Scan(&result.ExecutionID, &result.Task, &outcome, &runtime); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TerminalResult{}, ErrNotFound
		}
		return TerminalResult{}, fmt.Errorf("history read terminal result: %w", err)
	}
	result.Outcome = Outcome(outcome)
	if runtime.Valid {
		result.RuntimeOutcome = append(json.RawMessage(nil), runtime.String...)
	}
	return result, nil
}

func (s *Store) ExecutionDisplayOrdinal(ctx context.Context, executionID string) (int64, error) {
	var ordinal int64
	if err := s.db.QueryRowContext(ctx, "SELECT execution_ordinal FROM execution_display_positions WHERE execution_id=?", executionID).Scan(&ordinal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("history read execution display ordinal: %w", err)
	}
	return ordinal, nil
}
