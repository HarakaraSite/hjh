package history

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"hjh/internal/contracts"
)

// RecallEvidence is the public saved evidence needed to project one settled
// execution into a provider-independent reference message. Record.Content is
// intentionally left empty; content remains available through message JSON.
type RecallEvidence struct {
	Execution Execution
	Messages  []Message
	Records   []Record
}

// SessionContext returns only Session and request metadata. It never expands
// message bodies, configuration snapshots, or provider wire content.
func (s *Store) SessionContext(ctx context.Context, sessionID string) (contracts.SessionContext, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return contracts.SessionContext{}, fmt.Errorf("history begin Session context: %w", err)
	}
	defer tx.Rollback()
	var result contracts.SessionContext
	result.SessionID = sessionID
	var activeModelJSON, agentChoiceJSON string
	if err := tx.QueryRowContext(ctx, `SELECT state_revision,turn_count,message_count,active_model_json,agent_choice_json
		FROM sessions WHERE session_id=?`, sessionID).Scan(
		&result.Canonical.StateRevision, &result.Canonical.TurnCount, &result.Canonical.MessageCount,
		&activeModelJSON, &agentChoiceJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.SessionContext{}, ErrNotFound
		}
		return contracts.SessionContext{}, fmt.Errorf("history read Session context metadata: %w", err)
	}
	if err := json.Unmarshal([]byte(activeModelJSON), &result.Canonical.ActiveModel); err != nil {
		return contracts.SessionContext{}, fmt.Errorf("history decode Session context model: %w", err)
	}
	result.Canonical.AgentName = agentName(json.RawMessage(agentChoiceJSON))
	var latest contracts.ContextExecution
	if err := tx.QueryRowContext(ctx, `SELECT e.execution_id,e.lifecycle,e.outcome
		FROM execution_display_positions p JOIN executions e ON e.execution_id=p.execution_id
		WHERE p.session_correlation=? AND e.parent_execution_id IS NULL
		ORDER BY p.execution_ordinal DESC LIMIT 1`, sessionID).Scan(
		&latest.ExecutionID, &latest.Lifecycle, &latest.Outcome,
	); err == nil {
		result.LatestRootExecution = &latest
	} else if !errors.Is(err, sql.ErrNoRows) {
		return contracts.SessionContext{}, fmt.Errorf("history read latest root execution: %w", err)
	}
	var latestRequest contracts.ContextRequest
	var selectionJSON string
	var requestSnapshotState string
	var requestSnapshotAvailable bool
	var requestOrdinal, modelStep int
	err = tx.QueryRowContext(ctx, `SELECT r.record_id,r.execution_id,r.ordinal,
		CAST(json_extract(r.payload_json,'$.requestOrdinal') AS INTEGER),
		CAST(json_extract(r.payload_json,'$.modelStep') AS INTEGER),
		json_extract(r.payload_json,'$.modelSelection'),
		CAST(COALESCE(json_extract(r.payload_json,'$.itemCount'),0) AS INTEGER),
		CAST(COALESCE(json_extract(r.payload_json,'$.messageCount'),0) AS INTEGER),
		CAST(COALESCE(json_extract(r.payload_json,'$.toolContractCount'),0) AS INTEGER),
		CAST(COALESCE(json_extract(r.payload_json,'$.instructionCount'),0) AS INTEGER),
		CAST(COALESCE(json_extract(r.payload_json,'$.wireBytes'),0) AS INTEGER),
		CASE WHEN latest.record_id IS NULL THEN 'unavailable' ELSE 'saved' END,
		(latest.record_id IS NOT NULL)
		FROM semantic_records r
		LEFT JOIN execution_latest_requests latest ON latest.execution_id=r.execution_id AND latest.record_id=r.record_id
		JOIN execution_display_positions p ON p.execution_id=r.execution_id
		JOIN executions e ON e.execution_id=r.execution_id
		WHERE p.session_correlation=? AND e.parent_execution_id IS NULL AND r.kind='model_request'
			AND json_extract(r.payload_json,'$.schemaVersion')=1
		ORDER BY p.execution_ordinal DESC,r.ordinal DESC LIMIT 1`, sessionID).Scan(
		&latestRequest.RecordID, &latestRequest.ExecutionID, &latestRequest.RecordOrdinal, &requestOrdinal, &modelStep,
		&selectionJSON, &latestRequest.ItemCount, &latestRequest.MessageCount,
		&latestRequest.ToolContractCount, &latestRequest.InstructionCount, &latestRequest.WireBytes,
		&requestSnapshotState, &requestSnapshotAvailable,
	)
	if err == nil {
		latestRequest.RequestOrdinal, latestRequest.ModelStep = requestOrdinal, modelStep
		latestRequest.SnapshotSave = contracts.SnapshotStatus{ExecutionID: latestRequest.ExecutionID,
			State: contracts.SnapshotSaveState(requestSnapshotState), DetailAvailable: requestSnapshotAvailable}
		if err := json.Unmarshal([]byte(selectionJSON), &latestRequest.Selection); err != nil {
			return contracts.SessionContext{}, fmt.Errorf("history decode latest model request selection: %w", err)
		}
		result.LatestRequest = &latestRequest
	} else if !errors.Is(err, sql.ErrNoRows) {
		return contracts.SessionContext{}, fmt.Errorf("history read latest parent model request: %w", err)
	}
	var admission contracts.RecallAdmission
	var budgetRecordID, budgetExecution, budgetJSON, budgetSnapshotState string
	var budgetFact []byte
	var budgetOrdinal int64
	var budgetSnapshotAvailable bool
	budgetErr := tx.QueryRowContext(ctx, `SELECT r.record_id,r.execution_id,r.ordinal,r.payload_json,b.fact_json,
		CASE WHEN b.record_id IS NULL THEN 'unavailable' ELSE 'saved' END,
		(b.record_id IS NOT NULL)
		FROM semantic_records r
		LEFT JOIN execution_latest_budgets b ON b.execution_id=r.execution_id AND b.record_id=r.record_id
		JOIN execution_display_positions p ON p.execution_id=r.execution_id
		JOIN executions e ON e.execution_id=r.execution_id
		WHERE p.session_correlation=? AND e.parent_execution_id IS NULL AND r.kind='context_budget'
		ORDER BY p.execution_ordinal DESC,r.ordinal DESC LIMIT 1`, sessionID).Scan(
		&budgetRecordID, &budgetExecution, &budgetOrdinal, &budgetJSON, &budgetFact, &budgetSnapshotState, &budgetSnapshotAvailable)
	if budgetErr == nil {
		var budget contracts.ContextBudgetStatus
		if err := json.Unmarshal([]byte(budgetJSON), &budget); err != nil {
			return contracts.SessionContext{}, err
		}
		budget.RecordID, budget.ExecutionID = budgetRecordID, budgetExecution
		budget.RecordOrdinal = budgetOrdinal
		budget.DetailAvailable = budgetSnapshotAvailable
		if budgetSnapshotAvailable {
			budget.Fact = append(json.RawMessage(nil), budgetFact...)
		}
		budget.SnapshotSave = contracts.SnapshotStatus{ExecutionID: budgetExecution,
			State: contracts.SnapshotSaveState(budgetSnapshotState), DetailAvailable: budgetSnapshotAvailable}
		result.LatestBudget = &budget
	} else if !errors.Is(budgetErr, sql.ErrNoRows) {
		return contracts.SessionContext{}, budgetErr
	}
	err = tx.QueryRowContext(ctx, `SELECT rr.source_execution_id,rr.target_execution_id,rr.record_id,r.observed_at
		FROM recall_relations rr JOIN semantic_records r ON r.record_id=rr.record_id
		JOIN executions target ON target.execution_id=rr.target_execution_id
		JOIN execution_display_positions p ON p.execution_id=target.execution_id
		WHERE target.session_correlation=?
		ORDER BY p.execution_ordinal DESC,r.ordinal DESC LIMIT 1`, sessionID).Scan(
		&admission.SourceExecutionID, &admission.TargetExecutionID, &admission.RecordID, &admission.CreatedAt,
	)
	if err == nil {
		var messageOrdinal sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MIN(CAST(json_extract(payload_json,'$.locator.messageOrdinal') AS INTEGER)) FROM semantic_records
			WHERE execution_id=? AND json_extract(payload_json,'$.recall.relationRecordId')=?`,
			admission.TargetExecutionID, admission.RecordID).Scan(&messageOrdinal); err != nil {
			return contracts.SessionContext{}, fmt.Errorf("history read recall reference message position: %w", err)
		}
		if messageOrdinal.Valid {
			admission.ReferenceMessageOrdinal = &messageOrdinal.Int64
		}
		var requestOrdinal sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MIN(r.ordinal) FROM semantic_records r
			WHERE r.execution_id=? AND r.kind='model_request'
			AND json_extract(r.payload_json,'$.schemaVersion')=1
			AND json_extract(r.payload_json,'$.recallRelationRecordId')=?`,
			admission.TargetExecutionID, admission.RecordID).Scan(&requestOrdinal); err != nil {
			return contracts.SessionContext{}, fmt.Errorf("history read recall request position: %w", err)
		}
		if requestOrdinal.Valid {
			admission.RequestRecordOrdinal = &requestOrdinal.Int64
		}
		result.LatestRecallAdmission = &admission
	} else if !errors.Is(err, sql.ErrNoRows) {
		return contracts.SessionContext{}, fmt.Errorf("history read latest recall admission: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return contracts.SessionContext{}, fmt.Errorf("history commit Session context read: %w", err)
	}
	return result, nil
}

// SelectRecallSource resolves a same-Session settled, noncanonical execution.
// Empty/latest selects the newest stopped root; an ID is exact when possible,
// otherwise it is treated as a unique prefix across roots and children.
func (s *Store) SelectRecallSource(ctx context.Context, sessionID, selector string) (contracts.ContextExecution, error) {
	selector = strings.TrimSpace(selector)
	base := `SELECT e.execution_id,e.lifecycle,e.outcome FROM executions e
		WHERE e.session_correlation=? AND e.lifecycle='settled' AND e.adoption='non_canonical'
		AND e.outcome IN ('cancelled','failed','interrupted','unknown')`
	if selector == "" || selector == "latest" {
		query := base + ` AND e.parent_execution_id IS NULL
			ORDER BY e.settled_at DESC,e.created_at DESC,e.execution_id DESC LIMIT 1`
		var result contracts.ContextExecution
		if err := s.db.QueryRowContext(ctx, query, sessionID).Scan(&result.ExecutionID, &result.Lifecycle, &result.Outcome); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return contracts.ContextExecution{}, ErrRecallNotFound
			}
			return contracts.ContextExecution{}, fmt.Errorf("history select latest recall source: %w", err)
		}
		return result, nil
	}
	var exact contracts.ContextExecution
	err := s.db.QueryRowContext(ctx, base+` AND e.execution_id=?`, sessionID, selector).Scan(
		&exact.ExecutionID, &exact.Lifecycle, &exact.Outcome,
	)
	if err == nil {
		return exact, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return contracts.ContextExecution{}, fmt.Errorf("history select recall source: %w", err)
	}
	var exactExists int
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM executions WHERE session_correlation=? AND execution_id=?)",
		sessionID, selector).Scan(&exactExists); err != nil {
		return contracts.ContextExecution{}, fmt.Errorf("history check exact recall source: %w", err)
	}
	if exactExists != 0 {
		return contracts.ContextExecution{}, ErrRecallNotFound
	}
	rows, err := s.db.QueryContext(ctx, base+` AND substr(e.execution_id,1,?)=? ORDER BY e.execution_id`, sessionID, len(selector), selector)
	if err != nil {
		return contracts.ContextExecution{}, fmt.Errorf("history resolve recall source prefix: %w", err)
	}
	defer rows.Close()
	var matches []contracts.ContextExecution
	for rows.Next() {
		var item contracts.ContextExecution
		if err := rows.Scan(&item.ExecutionID, &item.Lifecycle, &item.Outcome); err != nil {
			return contracts.ContextExecution{}, fmt.Errorf("history scan recall source prefix: %w", err)
		}
		matches = append(matches, item)
	}
	if err := rows.Err(); err != nil {
		return contracts.ContextExecution{}, fmt.Errorf("history read recall source prefix: %w", err)
	}
	if len(matches) == 0 {
		return contracts.ContextExecution{}, ErrRecallNotFound
	}
	if len(matches) > 1 {
		return contracts.ContextExecution{}, ErrRecallAmbiguous
	}
	return matches[0], nil
}

// SaveRecallOperation stores a compact, replayable operation result in the
// shared command receipt ID space without interpreting other operation types.
func (s *Store) SaveRecallOperation(ctx context.Context, receipt contracts.RecallReceipt, signature string) (contracts.RecallReceipt, error) {
	if receipt.CoreID == "" || receipt.SessionID == "" || receipt.OperationID == "" || signature == "" {
		return contracts.RecallReceipt{}, fmt.Errorf("history save recall operation: missing identity")
	}
	if receipt.Action != "prepare" && receipt.Action != "clear" {
		return contracts.RecallReceipt{}, fmt.Errorf("history save recall operation: unsupported action %q", receipt.Action)
	}
	if receipt.SchemaVersion == 0 {
		receipt.SchemaVersion = 1
	}
	if receipt.CompletedAt == "" {
		receipt.CompletedAt = runtimeInputTimestamp()
	}
	operation := "recall." + receipt.Action
	resultJSON, err := json.Marshal(receipt)
	if err != nil {
		return contracts.RecallReceipt{}, fmt.Errorf("history encode recall operation receipt: %w", err)
	}
	targetJSON, err := json.Marshal(map[string]string{"sessionId": receipt.SessionID})
	if err != nil {
		return contracts.RecallReceipt{}, fmt.Errorf("history encode recall operation target: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.RecallReceipt{}, fmt.Errorf("history begin recall operation: %w", err)
	}
	defer tx.Rollback()
	var oldSignature, oldOperation, oldResult string
	err = tx.QueryRowContext(ctx, `SELECT signature_digest,operation,result_json FROM command_receipts
		WHERE core_epoch=? AND command_id=?`, receipt.CoreID, receipt.OperationID).Scan(&oldSignature, &oldOperation, &oldResult)
	if err == nil {
		if oldSignature != signature || oldOperation != operation {
			return contracts.RecallReceipt{}, ErrCommandIDConflict
		}
		var stored contracts.RecallReceipt
		if err := json.Unmarshal([]byte(oldResult), &stored); err != nil {
			return contracts.RecallReceipt{}, fmt.Errorf("history decode recall operation receipt: %w", err)
		}
		if stored.CoreID != receipt.CoreID || stored.SessionID != receipt.SessionID || stored.OperationID != receipt.OperationID || stored.Action != receipt.Action {
			return contracts.RecallReceipt{}, ErrCommandIDConflict
		}
		if err := tx.Commit(); err != nil {
			return contracts.RecallReceipt{}, fmt.Errorf("history commit recall operation replay: %w", err)
		}
		return stored, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return contracts.RecallReceipt{}, fmt.Errorf("history read recall operation receipt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO command_receipts(
		core_epoch,command_id,operation,signature_digest,result_json,target_json,execution_id,completed_at
	) VALUES(?,?,?,?,?,?,NULL,?)`, receipt.CoreID, receipt.OperationID, operation, signature,
		string(resultJSON), string(targetJSON), receipt.CompletedAt); err != nil {
		return contracts.RecallReceipt{}, fmt.Errorf("history save recall operation receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return contracts.RecallReceipt{}, fmt.Errorf("history commit recall operation: %w", err)
	}
	return receipt, nil
}

// RecallOperation returns only recall receipts; an I-09 receipt sharing this
// key remains outside the recall decoder and reports not found.
func (s *Store) RecallOperation(ctx context.Context, coreID, operationID string) (contracts.RecallReceipt, string, error) {
	var raw, signature string
	if err := s.db.QueryRowContext(ctx, `SELECT result_json,signature_digest FROM command_receipts
		WHERE core_epoch=? AND command_id=? AND operation IN ('recall.prepare','recall.clear')`, coreID, operationID).Scan(&raw, &signature); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.RecallReceipt{}, "", ErrNotFound
		}
		return contracts.RecallReceipt{}, "", fmt.Errorf("history read recall operation: %w", err)
	}
	var receipt contracts.RecallReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return contracts.RecallReceipt{}, "", fmt.Errorf("history decode recall operation: %w", err)
	}
	return receipt, signature, nil
}

// RecallEvidence reads messages and public semantic evidence for one settled
// execution. Private provider state is removed from message JSON and no record
// content blob (including provider wire) is expanded.
func (s *Store) RecallEvidence(ctx context.Context, executionID string) (RecallEvidence, error) {
	execution, err := s.readExecution(ctx, executionID)
	if err != nil {
		return RecallEvidence{}, err
	}
	if execution.Lifecycle != "settled" {
		return RecallEvidence{}, fmt.Errorf("history recall evidence requires a settled execution")
	}
	messages, err := s.Messages(ctx, executionID, 0, -1)
	if err != nil {
		return RecallEvidence{}, err
	}
	for index := range messages {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(messages[index].JSON, &object); err != nil {
			return RecallEvidence{}, fmt.Errorf("history decode recall message: %w", err)
		}
		delete(object, "providerState")
		messages[index].JSON, err = json.Marshal(object)
		if err != nil {
			return RecallEvidence{}, fmt.Errorf("history encode public recall message: %w", err)
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.record_id,r.execution_id,r.ordinal,r.kind,r.observed_at,
		r.payload_json,r.content_digest,body.content_digest,body.content_bytes
		FROM semantic_records r LEFT JOIN contents body ON body.content_digest=r.content_digest
		WHERE r.execution_id=? AND (
			kind IN ('assistant_message','model_result','tool_call','tool_result','execution_admission',
				'host_decision','effect_observation','context_update','control_decision','recall_relation') OR
			(r.kind='user_message' AND json_extract(r.payload_json,'$.conversation.entityKind') IN ('steering','recall'))
		) ORDER BY r.ordinal`, executionID)
	if err != nil {
		return RecallEvidence{}, fmt.Errorf("history read recall evidence records: %w", err)
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
		if err := rows.Scan(&item.record.ID, &item.record.ExecutionID, &item.record.Ordinal,
			&item.record.Kind, &item.record.ObservedAt, &payload, &item.digest, &item.storedDigest, &item.content); err != nil {
			_ = rows.Close()
			return RecallEvidence{}, fmt.Errorf("history scan recall evidence record: %w", err)
		}
		item.record.Payload = json.RawMessage(payload)
		item.record.Conversation = conversationMetadata(item.record.Payload)
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return RecallEvidence{}, fmt.Errorf("history read recall evidence records: %w", err)
	}
	if err := rows.Close(); err != nil {
		return RecallEvidence{}, fmt.Errorf("history close recall evidence records: %w", err)
	}
	records := make([]Record, 0, len(stored))
	for _, item := range stored {
		record := item.record
		if item.digest.Valid {
			if !item.storedDigest.Valid {
				return RecallEvidence{}, ErrNotFound
			}
			record.Payload, _, err = expandStoredToolResultPayload(record.Kind, record.Payload,
				item.digest.String, item.content)
			if err != nil {
				return RecallEvidence{}, err
			}
		}
		if record.Kind == "model_result" || record.Kind == "assistant_message" {
			record.Payload, err = stripProviderState(record.Payload)
			if err != nil {
				return RecallEvidence{}, err
			}
		}
		record.Payload, _, err = s.expandStoredToolMessage(ctx, executionID, record.Payload)
		if err != nil {
			return RecallEvidence{}, err
		}
		record.Relations, err = s.readRelations(ctx, record.ID)
		if err != nil {
			return RecallEvidence{}, err
		}
		records = append(records, record)
	}
	return RecallEvidence{Execution: execution, Messages: messages, Records: records}, nil
}

func recallOutcome(value string) bool {
	switch value {
	case "cancelled", "failed", "interrupted", "unknown":
		return true
	default:
		return false
	}
}

func recallMessageDigest(message []byte) string {
	digest := sha256.Sum256(message)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func stripProviderState(payload json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, fmt.Errorf("history decode public model result: %w", err)
	}
	delete(object, "providerState")
	if raw := object["result"]; len(raw) != 0 {
		var result map[string]json.RawMessage
		if err := json.Unmarshal(raw, &result); err == nil && result != nil {
			delete(result, "providerState")
			object["result"], err = json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("history encode public model result: %w", err)
			}
		}
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("history encode public model result: %w", err)
	}
	return encoded, nil
}
