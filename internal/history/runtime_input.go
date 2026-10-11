package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"hjh/internal/contracts"
)

var ErrInputIDConflict = errors.New("history: runtime input ID conflicts with an existing input")

// SaveRuntimeInput stores the durable receipt and its one shared body. Steering
// callers pass the serialized Message, while follow-up callers pass raw UTF-8.
func (s *Store) SaveRuntimeInput(ctx context.Context, receipt contracts.InputReceipt, signature string, body []byte) (contracts.InputReceipt, error) {
	if receipt.CoreID == "" || receipt.SessionID == "" || receipt.RunID == "" || receipt.InputID == "" {
		return contracts.InputReceipt{}, fmt.Errorf("history save runtime input: missing identity")
	}
	if receipt.Kind != "steering" && receipt.Kind != "follow-up" {
		return contracts.InputReceipt{}, fmt.Errorf("history save runtime input: unsupported kind %q", receipt.Kind)
	}
	if receipt.SchemaVersion == 0 {
		receipt.SchemaVersion = 1
	}
	if receipt.AcceptedAt == "" {
		receipt.AcceptedAt = runtimeInputTimestamp()
	}
	if receipt.UpdatedAt == "" {
		receipt.UpdatedAt = receipt.AcceptedAt
	}
	receipt.Text = ""

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history begin runtime input: %w", err)
	}
	defer tx.Rollback()

	var oldSignature, oldResult string
	err = tx.QueryRowContext(ctx, `SELECT signature_digest, result_json FROM command_receipts
		WHERE core_epoch=? AND command_id=?`, receipt.CoreID, receipt.InputID).Scan(&oldSignature, &oldResult)
	if err == nil {
		if oldSignature != signature {
			return contracts.InputReceipt{}, ErrInputIDConflict
		}
		var stored contracts.InputReceipt
		if err := json.Unmarshal([]byte(oldResult), &stored); err != nil {
			return contracts.InputReceipt{}, fmt.Errorf("history decode runtime input receipt: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return contracts.InputReceipt{}, fmt.Errorf("history commit runtime input replay: %w", err)
		}
		return stored, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return contracts.InputReceipt{}, fmt.Errorf("history read runtime input receipt: %w", err)
	}

	digest, err := putContent(ctx, tx, body)
	if err != nil {
		return contracts.InputReceipt{}, err
	}
	receipt.ContentDigest = digest
	receipt.Revision, err = nextRuntimeInputRevision(ctx, tx, receipt.CoreID, receipt.SessionID)
	if err != nil {
		return contracts.InputReceipt{}, err
	}
	target, err := json.Marshal(map[string]any{
		"sessionId":   receipt.SessionID,
		"runId":       receipt.RunID,
		"executionId": receipt.ExecutionID,
		"sequence":    receipt.Sequence,
	})
	if err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history encode runtime input target: %w", err)
	}
	result, err := json.Marshal(receipt)
	if err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history encode runtime input receipt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO command_receipts(
		core_epoch,command_id,operation,signature_digest,result_json,target_json,execution_id,completed_at
	) VALUES(?,?,?,?,?,?,?,?)`, receipt.CoreID, receipt.InputID, receipt.Kind, signature,
		string(result), string(target), nullableInputExecution(receipt.ExecutionID), receipt.AcceptedAt); err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history save runtime input receipt: %w", err)
	}
	if receipt.Kind == "follow-up" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO follow_up_receipts(
			core_epoch,queue_id,session_correlation,command_id,parent_execution_id,text_content_digest,status,reason,
			started_execution_id,created_at,updated_at
		) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, receipt.CoreID, receipt.InputID, receipt.SessionID, receipt.InputID,
			nullableInputExecution(receipt.ExecutionID), digest, receipt.Status, nullableInputReason(receipt.Reason),
			nullableInputExecution(receipt.StartedExecutionID), receipt.AcceptedAt, receipt.UpdatedAt); err != nil {
			return contracts.InputReceipt{}, fmt.Errorf("history save follow-up receipt: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history commit runtime input: %w", err)
	}
	return receipt, nil
}

// UpdateRuntimeInput replaces the one stored state for an admitted input and
// advances the Session cursor in the same transaction.
func (s *Store) UpdateRuntimeInput(ctx context.Context, receipt contracts.InputReceipt) (contracts.InputReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history begin runtime input update: %w", err)
	}
	defer tx.Rollback()
	updated, err := updateRuntimeInputTx(ctx, tx, receipt)
	if err != nil {
		return contracts.InputReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history commit runtime input update: %w", err)
	}
	return updated, nil
}

func updateRuntimeInputTx(ctx context.Context, tx *sql.Tx, receipt contracts.InputReceipt) (contracts.InputReceipt, error) {
	if receipt.Text != "" {
		return contracts.InputReceipt{}, fmt.Errorf("history update runtime input: receipt text is not persisted")
	}
	var target string
	err := tx.QueryRowContext(ctx, `SELECT target_json FROM command_receipts
		WHERE core_epoch=? AND command_id=?`, receipt.CoreID, receipt.InputID).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.InputReceipt{}, ErrNotFound
	}
	if err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history read runtime input for update: %w", err)
	}
	var stored contracts.InputReceipt
	var resultJSON string
	if err := tx.QueryRowContext(ctx, `SELECT result_json FROM command_receipts WHERE core_epoch=? AND command_id=?`, receipt.CoreID, receipt.InputID).Scan(&resultJSON); err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history read runtime input state: %w", err)
	}
	if err := json.Unmarshal([]byte(resultJSON), &stored); err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history decode runtime input state: %w", err)
	}
	if stored.SessionID != receipt.SessionID || stored.RunID != receipt.RunID || stored.ExecutionID != receipt.ExecutionID || stored.Kind != receipt.Kind {
		return contracts.InputReceipt{}, fmt.Errorf("history update runtime input: receipt identity changed")
	}
	if receipt.ContentDigest == "" {
		receipt.ContentDigest = stored.ContentDigest
	}
	if receipt.ContentDigest != stored.ContentDigest {
		return contracts.InputReceipt{}, fmt.Errorf("history update runtime input: content digest changed")
	}
	receipt.AcceptedAt = stored.AcceptedAt
	if receipt.Sequence == 0 {
		receipt.Sequence = stored.Sequence
	}
	receipt.SchemaVersion = stored.SchemaVersion
	receipt.Revision, err = nextRuntimeInputRevision(ctx, tx, receipt.CoreID, receipt.SessionID)
	if err != nil {
		return contracts.InputReceipt{}, err
	}
	receipt.UpdatedAt = runtimeInputTimestamp()
	result, err := json.Marshal(receipt)
	if err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history encode runtime input update: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE command_receipts SET result_json=?,target_json=?,execution_id=?,completed_at=?
		WHERE core_epoch=? AND command_id=?`, string(result), target, nullableInputExecution(receipt.ExecutionID),
		receipt.AcceptedAt, receipt.CoreID, receipt.InputID); err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history update runtime input receipt: %w", err)
	}
	if receipt.Kind == "follow-up" {
		if _, err := tx.ExecContext(ctx, `UPDATE follow_up_receipts SET status=?,reason=?,started_execution_id=?,updated_at=?
			WHERE core_epoch=? AND queue_id=?`, receipt.Status, nullableInputReason(receipt.Reason),
			nullableInputExecution(receipt.StartedExecutionID), receipt.UpdatedAt, receipt.CoreID, receipt.InputID); err != nil {
			return contracts.InputReceipt{}, fmt.Errorf("history update follow-up receipt: %w", err)
		}
	}
	return receipt, nil
}

// RuntimeInput reads one receipt. includeText expands the stored message or task
// only for an explicit detail request.
func (s *Store) RuntimeInput(ctx context.Context, coreID, inputID string, includeText bool) (contracts.InputReceipt, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT result_json FROM command_receipts
		WHERE core_epoch=? AND command_id=? AND operation IN ('steering','follow-up')`, coreID, inputID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.InputReceipt{}, ErrNotFound
		}
		return contracts.InputReceipt{}, fmt.Errorf("history read runtime input: %w", err)
	}
	var receipt contracts.InputReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return contracts.InputReceipt{}, fmt.Errorf("history decode runtime input: %w", err)
	}
	if includeText {
		body, err := readContent(ctx, s.db, receipt.ContentDigest)
		if err != nil {
			return contracts.InputReceipt{}, err
		}
		if receipt.Kind == "steering" {
			var message contracts.Message
			if err := json.Unmarshal(body, &message); err != nil {
				return contracts.InputReceipt{}, fmt.Errorf("history decode steering message content: %w", err)
			}
			receipt.Text = message.VisibleText()
		} else {
			receipt.Text = string(body)
		}
	}
	return receipt, nil
}

// RuntimeInputs reads the receipt rows and their change watermark in one SQL
// read transaction. An omitted coreID returns the first-resume view across all
// epochs; subsequent reads can follow one core's revision series.
func (s *Store) RuntimeInputs(ctx context.Context, sessionID, coreID, currentCoreID string, afterRevision uint64, includeText bool) (contracts.InputReceiptPage, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return contracts.InputReceiptPage{}, fmt.Errorf("history begin runtime inputs read: %w", err)
	}
	defer tx.Rollback()
	watermarkCore := coreID
	if watermarkCore == "" {
		watermarkCore = currentCoreID
	}
	var revision uint64
	if watermarkCore != "" {
		var raw sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT public_revision FROM core_session_cursors
			WHERE core_epoch=? AND session_id=?`, watermarkCore, sessionID).Scan(&raw); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return contracts.InputReceiptPage{}, fmt.Errorf("history read runtime input cursor: %w", err)
		} else if raw.Valid && raw.Int64 > 0 {
			revision = uint64(raw.Int64)
		}
	}
	// acceptedAt and sequence order receipts across the first-resume view and
	// within a Core epoch respectively.
	query := `SELECT core_epoch,result_json FROM command_receipts
		WHERE operation IN ('steering','follow-up') AND json_extract(target_json,'$.sessionId')=?`
	args := []any{sessionID}
	if coreID != "" {
		query += ` AND core_epoch=? AND CAST(json_extract(result_json,'$.revision') AS INTEGER)>?`
		args = append(args, coreID, afterRevision)
	}
	query += ` ORDER BY completed_at, CAST(json_extract(result_json,'$.sequence') AS INTEGER), core_epoch, command_id`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return contracts.InputReceiptPage{}, fmt.Errorf("history list runtime inputs: %w", err)
	}
	page := contracts.InputReceiptPage{CoreID: watermarkCore, SessionID: sessionID, Revision: revision, Receipts: []contracts.InputReceipt{}}
	for rows.Next() {
		var storedCore, raw string
		if err := rows.Scan(&storedCore, &raw); err != nil {
			_ = rows.Close()
			return contracts.InputReceiptPage{}, fmt.Errorf("history scan runtime input: %w", err)
		}
		var receipt contracts.InputReceipt
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
			_ = rows.Close()
			return contracts.InputReceiptPage{}, fmt.Errorf("history decode runtime input: %w", err)
		}
		receipt.ManagedByThisCore = currentCoreID != "" && storedCore == currentCoreID
		if includeText {
			body, err := readContentTx(ctx, tx, receipt.ContentDigest)
			if err != nil {
				_ = rows.Close()
				return contracts.InputReceiptPage{}, err
			}
			if receipt.Kind == "steering" {
				var message contracts.Message
				if err := json.Unmarshal(body, &message); err != nil {
					_ = rows.Close()
					return contracts.InputReceiptPage{}, fmt.Errorf("history decode steering message content: %w", err)
				}
				receipt.Text = message.VisibleText()
			} else {
				receipt.Text = string(body)
			}
		}
		page.Receipts = append(page.Receipts, receipt)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return contracts.InputReceiptPage{}, fmt.Errorf("history list runtime inputs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return contracts.InputReceiptPage{}, fmt.Errorf("history close runtime inputs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return contracts.InputReceiptPage{}, fmt.Errorf("history commit runtime inputs read: %w", err)
	}
	return page, nil
}

// SaveInputControl records the latest compact control fact for an execution.
func (s *Store) SaveInputControl(ctx context.Context, executionID string, control json.RawMessage) error {
	if !validJSON(control) {
		return ErrInvalidJSON
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO execution_control_facts(execution_id,control_json,updated_at)
		VALUES(?,?,?) ON CONFLICT(execution_id) DO UPDATE SET control_json=excluded.control_json,updated_at=excluded.updated_at`,
		executionID, string(control), timestampNow())
	if err != nil {
		return fmt.Errorf("history save input control fact: %w", err)
	}
	return nil
}

func nextRuntimeInputRevision(ctx context.Context, tx *sql.Tx, coreID, sessionID string) (uint64, error) {
	if _, err := tx.ExecContext(ctx, `INSERT INTO core_session_cursors(core_epoch,session_id,public_revision)
		VALUES(?,?,1) ON CONFLICT(core_epoch,session_id) DO UPDATE SET public_revision=public_revision+1`, coreID, sessionID); err != nil {
		return 0, fmt.Errorf("history advance runtime input cursor: %w", err)
	}
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT public_revision FROM core_session_cursors WHERE core_epoch=? AND session_id=?`, coreID, sessionID).Scan(&revision); err != nil {
		return 0, fmt.Errorf("history read updated runtime input cursor: %w", err)
	}
	return uint64(revision), nil
}

func nullableInputExecution(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInputReason(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func runtimeInputTimestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func readContentTx(ctx context.Context, tx *sql.Tx, digest string) ([]byte, error) {
	var content []byte
	if err := tx.QueryRowContext(ctx, "SELECT content_bytes FROM contents WHERE content_digest=?", digest).Scan(&content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read content: %w", err)
	}
	return append([]byte(nil), content...), nil
}
