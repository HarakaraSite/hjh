package history

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"hjh/internal/contracts"
)

type Configuration struct {
	ID            string
	CreatedAt     string
	ContentDigest string
	Snapshot      []byte
}

func (s *Store) SaveConfiguration(ctx context.Context, id string, snapshot json.RawMessage) error {
	if !validJSON(snapshot) {
		return ErrInvalidJSON
	}
	createdAt := timestampNow()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history begin configuration: %w", err)
	}
	defer tx.Rollback()
	digest, err := putContent(ctx, tx, snapshot)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO configurations(configuration_id, created_at, snapshot_content_digest)
		VALUES(?, ?, ?) ON CONFLICT(configuration_id) DO NOTHING`, id, createdAt, digest,
	); err != nil {
		return fmt.Errorf("history save configuration: %w", err)
	}
	var existingDigest string
	if err := tx.QueryRowContext(ctx,
		"SELECT snapshot_content_digest FROM configurations WHERE configuration_id=?", id,
	).Scan(&existingDigest); err != nil {
		return fmt.Errorf("history verify configuration: %w", err)
	}
	if existingDigest != digest {
		return fmt.Errorf("history configuration ID already has different content")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history commit configuration: %w", err)
	}
	return nil
}

func (s *Store) Configuration(ctx context.Context, id string) (Configuration, error) {
	var result Configuration
	var digest string
	if err := s.db.QueryRowContext(ctx, `
		SELECT configuration_id, created_at, snapshot_content_digest
		FROM configurations WHERE configuration_id=?`, id,
	).Scan(&result.ID, &result.CreatedAt, &digest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Configuration{}, ErrNotFound
		}
		return Configuration{}, fmt.Errorf("history read configuration: %w", err)
	}
	result.ContentDigest = digest
	content, err := readContent(ctx, s.db, digest)
	if err != nil {
		return Configuration{}, err
	}
	result.Snapshot = content
	return result, nil
}

type Outcome string

const (
	OutcomeCompleted   Outcome = "completed"
	OutcomeFailed      Outcome = "failed"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeInterrupted Outcome = "interrupted"
)

type Relation struct {
	Relation       string
	TargetRecordID string
	Mandatory      bool
	Resolved       bool // AppendOccurrence derives this from the target row's existence.
}

type OccurrenceInput struct {
	Kind    string
	Payload json.RawMessage
	// Content is explicit attached content; when set, result compaction is skipped.
	Content []byte
	// ObservedResult preserves the observer's original result JSON before payload encoding.
	ObservedResult json.RawMessage
	ObservedAt     string
	Relations      []Relation
	Conversation   *contracts.ConversationMetadata
}

type Record struct {
	ID           string
	ExecutionID  string
	Ordinal      int64
	Kind         string
	ObservedAt   string
	Payload      json.RawMessage
	Content      []byte
	Relations    []Relation
	Conversation *contracts.ConversationMetadata
	SnapshotSave *contracts.SnapshotStatus `json:"-"`
}

func (s *Store) AppendOccurrence(ctx context.Context, executionID string, input OccurrenceInput) (Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Record{}, fmt.Errorf("history begin occurrence: %w", err)
	}
	defer tx.Rollback()
	record, err := appendOccurrenceTx(ctx, tx, executionID, input)
	if err != nil {
		return Record{}, err
	}
	publicRecord, err := presentOccurrenceRecord(record)
	if err != nil {
		return Record{}, err
	}
	if err := tx.Commit(); err != nil {
		return Record{}, fmt.Errorf("history commit occurrence: %w", err)
	}
	return publicRecord, nil
}

func appendOccurrenceTx(ctx context.Context, tx *sql.Tx, executionID string, input OccurrenceInput) (Record, error) {
	return appendOccurrenceTxWithMode(ctx, tx, executionID, input, false)
}

func appendPostSettlementOccurrenceTx(ctx context.Context, tx *sql.Tx, executionID string, input OccurrenceInput) (Record, error) {
	return appendOccurrenceTxWithMode(ctx, tx, executionID, input, true)
}

func appendOccurrenceTxWithMode(ctx context.Context, tx *sql.Tx, executionID string, input OccurrenceInput, postSettlement bool) (Record, error) {
	if !validJSON(input.Payload) {
		return Record{}, ErrInvalidJSON
	}
	if input.Content == nil {
		compactPayload, resultContent, hasToolResult, err := compactToolResult(input.Kind, input.Payload, input.ObservedResult)
		if err != nil {
			return Record{}, err
		}
		if hasToolResult {
			input.Payload = compactPayload
			input.Content = resultContent
		}
	}
	observedAt := input.ObservedAt
	if observedAt == "" {
		observedAt = timestampNow()
	}
	var latest int64
	var lifecycle string
	var parentID, spawnID sql.NullString
	var terminalRecordID string
	if err := tx.QueryRowContext(ctx,
		"SELECT latest_ordinal, lifecycle, parent_execution_id, spawn_call_id, COALESCE(terminal_record_id,'') FROM executions WHERE execution_id=?", executionID,
	).Scan(&latest, &lifecycle, &parentID, &spawnID, &terminalRecordID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("history read execution for occurrence: %w", err)
	}
	if postSettlement {
		if lifecycle != "settled" {
			return Record{}, ErrExecutionClosed
		}
		if terminalRecordID == "" {
			return Record{}, fmt.Errorf("history settled execution has no terminal record")
		}
		exists, err := semanticRecordExists(ctx, tx, terminalRecordID)
		if err != nil {
			return Record{}, err
		}
		if !exists {
			return Record{}, fmt.Errorf("history settled execution terminal record %q not found", terminalRecordID)
		}
		input.Payload, err = addPostSettlementFields(input.Payload, terminalRecordID)
		if err != nil {
			return Record{}, err
		}
		input.Relations = append(append([]Relation(nil), input.Relations...), Relation{
			Relation: "settlement", TargetRecordID: terminalRecordID, Mandatory: true,
		})
	} else if lifecycle != "active" {
		return Record{}, ErrExecutionClosed
	}
	withMetadata, err := addConversationMetadata(input.Payload, input.Conversation)
	if err != nil {
		return Record{}, err
	}
	payload, err := contracts.EncodeCanonicalJSON(withMetadata)
	if err != nil {
		return Record{}, fmt.Errorf("history canonicalize occurrence: %w", err)
	}
	ordinal := latest + 1
	recordID := semanticRecordID(executionID, ordinal)
	var contentDigest any
	if input.Content != nil {
		digest, err := putContent(ctx, tx, input.Content)
		if err != nil {
			return Record{}, err
		}
		contentDigest = digest
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO semantic_records(record_id, execution_id, ordinal, kind, observed_at, payload_json, content_digest)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		recordID, executionID, ordinal, input.Kind, observedAt, string(payload), contentDigest,
	); err != nil {
		return Record{}, fmt.Errorf("history append occurrence: %w", err)
	}
	var unresolvedAdded int64
	persistedRelations := make([]Relation, 0, len(input.Relations))
	for index, relation := range input.Relations {
		mandatory, resolved := 0, 0
		if relation.Mandatory {
			mandatory = 1
		}
		targetExists, err := semanticRecordExists(ctx, tx, relation.TargetRecordID)
		if err != nil {
			return Record{}, err
		}
		if targetExists {
			resolved = 1
		}
		if mandatory == 1 && resolved == 0 {
			unresolvedAdded++
		}
		relation.Resolved = resolved == 1
		persistedRelations = append(persistedRelations, relation)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO semantic_relations(
				record_id, relation_ordinal, relation, target_record_id, mandatory, resolved
			) VALUES(?, ?, ?, ?, ?, ?)`,
			recordID, index, relation.Relation, relation.TargetRecordID, mandatory, resolved,
		); err != nil {
			return Record{}, fmt.Errorf("history append occurrence relation: %w", err)
		}
	}
	if unresolvedAdded != 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE executions SET unresolved_mandatory_count=unresolved_mandatory_count+?
			WHERE execution_id=?`, unresolvedAdded, executionID,
		); err != nil {
			return Record{}, fmt.Errorf("history update unresolved relation count: %w", err)
		}
	}
	if err := resolveIncomingRelations(ctx, tx, recordID); err != nil {
		return Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE executions SET latest_ordinal=?, occurrence_count=occurrence_count+1
		WHERE execution_id=?`, ordinal, executionID,
	); err != nil {
		return Record{}, fmt.Errorf("history update occurrence ordinal: %w", err)
	}
	return Record{
		ID: recordID, ExecutionID: executionID, Ordinal: ordinal, Kind: input.Kind,
		ObservedAt: observedAt, Payload: append(json.RawMessage(nil), payload...),
		Content: append([]byte(nil), input.Content...), Relations: persistedRelations,
		Conversation: cloneConversation(input.Conversation),
	}, nil
}

func addConversationMetadata(payload json.RawMessage, metadata *contracts.ConversationMetadata) (json.RawMessage, error) {
	if metadata == nil {
		return append(json.RawMessage(nil), payload...), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("history conversation metadata requires an object payload: %w", ErrInvalidJSON)
	}
	object["conversation"] = contracts.JSON(metadata)
	return json.Marshal(object)
}

func cloneConversation(value *contracts.ConversationMetadata) *contracts.ConversationMetadata {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func semanticRecordExists(ctx context.Context, tx *sql.Tx, recordID string) (bool, error) {
	var exists int
	if err := tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM semantic_records WHERE record_id=?)", recordID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("history check semantic relation target: %w", err)
	}
	return exists != 0, nil
}

func resolveIncomingRelations(ctx context.Context, tx *sql.Tx, targetRecordID string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT source.execution_id, COUNT(*)
		FROM semantic_relations relation
		JOIN semantic_records source ON source.record_id=relation.record_id
		WHERE relation.target_record_id=? AND relation.resolved=0 AND relation.mandatory=1
		GROUP BY source.execution_id`, targetRecordID)
	if err != nil {
		return fmt.Errorf("history read incoming relations: %w", err)
	}
	type count struct {
		executionID string
		value       int64
	}
	var counts []count
	for rows.Next() {
		var item count
		if err := rows.Scan(&item.executionID, &item.value); err != nil {
			_ = rows.Close()
			return fmt.Errorf("history scan incoming relations: %w", err)
		}
		counts = append(counts, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("history read incoming relations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("history close incoming relations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE semantic_relations SET resolved=1
		WHERE target_record_id=? AND resolved=0`, targetRecordID,
	); err != nil {
		return fmt.Errorf("history resolve incoming relations: %w", err)
	}
	for _, item := range counts {
		if _, err := tx.ExecContext(ctx, `
			UPDATE executions SET unresolved_mandatory_count=unresolved_mandatory_count-?
			WHERE execution_id=?`, item.value, item.executionID,
		); err != nil {
			return fmt.Errorf("history update resolved relation count: %w", err)
		}
	}
	return nil
}

func (s *Store) EndExecution(ctx context.Context, executionID string, outcome Outcome, terminal json.RawMessage, settledAt string) error {
	_, err := s.EndExecutionWithConversation(ctx, executionID, outcome, terminal, settledAt, "", 0)
	return err
}

// EndExecutionWithConversation moves every active latest state to an
// incomplete assistant fact and stores the terminal fact in one transaction.
func (s *Store) EndExecutionWithConversation(ctx context.Context, executionID string, outcome Outcome, terminal json.RawMessage, settledAt, rootExecutionID string, sequence uint64) ([]contracts.ExecutionFact, error) {
	if !validJSON(terminal) {
		return nil, ErrInvalidJSON
	}
	switch outcome {
	case OutcomeCompleted, OutcomeFailed, OutcomeCancelled, OutcomeInterrupted:
	default:
		return nil, fmt.Errorf("history unsupported execution outcome %q", outcome)
	}
	runtimePayload, err := contracts.EncodeCanonicalJSON(terminal)
	if err != nil {
		return nil, fmt.Errorf("history canonicalize terminal: %w", err)
	}
	if settledAt == "" {
		settledAt = timestampNow()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("history begin execution end: %w", err)
	}
	defer tx.Rollback()
	var latest int64
	var lifecycle string
	var parentID, spawnID sql.NullString
	if err := tx.QueryRowContext(ctx,
		"SELECT latest_ordinal, lifecycle, parent_execution_id, spawn_call_id FROM executions WHERE execution_id=?", executionID,
	).Scan(&latest, &lifecycle, &parentID, &spawnID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read execution to end: %w", err)
	}
	if lifecycle != "active" {
		return nil, ErrExecutionClosed
	}
	var rootOrdinal int64
	if rootExecutionID != "" {
		rootOrdinal, err = executionDisplayOrdinalTx(ctx, tx, rootExecutionID)
		if err != nil {
			return nil, err
		}
	}
	terminalFirstSequence := sequence
	if rootExecutionID != "" && sequence != 0 {
		var first sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MIN(CAST(json_extract(payload_json,'$.conversation.firstSequence') AS INTEGER))
			FROM semantic_records WHERE execution_id=? AND kind='execution_admission'
			AND json_extract(payload_json,'$.conversation.entityKind')='execution'
			AND json_extract(payload_json,'$.conversation.localKey')='execution'
			AND json_type(payload_json,'$.conversation.firstSequence')='integer'`, executionID).Scan(&first); err != nil {
			return nil, fmt.Errorf("history read execution start position: %w", err)
		}
		if first.Valid && first.Int64 >= 0 {
			terminalFirstSequence = uint64(first.Int64)
		}
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT lane, model_step, request_ordinal, first_event_ordinal, event_json
		FROM assistant_text_states WHERE execution_id=?
		ORDER BY lane, model_step, request_ordinal`, executionID)
	if err != nil {
		return nil, fmt.Errorf("history read assistant text state: %w", err)
	}
	type assistantState struct {
		lane              string
		modelStep         int64
		requestOrdinal    int64
		firstEventOrdinal int64
		eventJSON         string
		observedAt        string
	}
	var states []assistantState
	for rows.Next() {
		var state assistantState
		if err := rows.Scan(&state.lane, &state.modelStep, &state.requestOrdinal, &state.firstEventOrdinal, &state.eventJSON); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("history scan assistant text state: %w", err)
		}
		state.observedAt = eventObservedAt(json.RawMessage(state.eventJSON), settledAt)
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("history read assistant text state: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history close assistant text state: %w", err)
	}
	facts := make([]contracts.ExecutionFact, 0, len(states)+1)
	for _, state := range states {
		latest++
		recordID := semanticRecordID(executionID, latest)
		var event map[string]json.RawMessage
		if err := json.Unmarshal([]byte(state.eventJSON), &event); err != nil {
			return nil, fmt.Errorf("history decode assistant text state: %w", err)
		}
		event["phase"] = contracts.JSON("incomplete")
		metadata := conversationMetadata(json.RawMessage(state.eventJSON))
		var request contracts.RequestIdentity
		_ = json.Unmarshal(event["request"], &request)
		if metadata == nil {
			lane := request.Lane
			if lane == "" {
				lane = "parent"
			}
			entityKind, localKey := "assistant", lane+fmt.Sprintf("/%d/answer", request.RequestOrdinal)
			if len(state.lane) >= len("thinking/") && state.lane[:len("thinking/")] == "thinking/" {
				entityKind = "thinking"
				localKey = lane + fmt.Sprintf("/%d/", request.RequestOrdinal) + state.lane[len("thinking/"):]
			}
			metadata = &contracts.ConversationMetadata{RootExecutionID: rootExecutionID, EntityKind: entityKind, LocalKey: localKey, FirstSequence: sequence}
		}
		if sequence != 0 {
			metadata.RootExecutionID = rootExecutionID
			metadata.Sequence = sequence
			event["conversation"] = contracts.JSON(metadata)
		}
		if request.ExecutionID == "" {
			request.ExecutionID = executionID
		}
		if _, ok := event["request"]; !ok {
			event["request"] = contracts.JSON(request)
		}
		encodedEvent, err := json.Marshal(event)
		if err != nil {
			return nil, fmt.Errorf("history encode assistant text state: %w", err)
		}
		payload, err := contracts.EncodeCanonicalJSON(encodedEvent)
		if err != nil {
			return nil, fmt.Errorf("history canonicalize assistant text state: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO semantic_records(record_id, execution_id, ordinal, kind, observed_at, payload_json, content_digest)
			VALUES(?, ?, ?, 'assistant_message', ?, ?, NULL)`,
			recordID, executionID, latest, state.observedAt, string(payload),
		); err != nil {
			return nil, fmt.Errorf("history persist assistant text state: %w", err)
		}
		var statePayload struct {
			Text         string `json:"text"`
			ThinkingKind string `json:"thinkingKind"`
		}
		_ = json.Unmarshal(event["payload"], &statePayload)
		if thinkingKind := event["thinkingKind"]; len(thinkingKind) != 0 {
			_ = json.Unmarshal(thinkingKind, &statePayload.ThinkingKind)
		}
		fact := contracts.ExecutionFact{RootExecutionID: rootExecutionID, ExecutionID: executionID, ParentExecutionID: parentID.String,
			RootExecutionOrdinal: rootOrdinal, Kind: "assistant_message", Phase: "incomplete", Role: "assistant",
			Text: statePayload.Text, ThinkingKind: statePayload.ThinkingKind, Request: request, Conversation: *metadata,
			Locator: contracts.ExecutionLocator{ExecutionID: executionID, RecordID: recordID, Ordinal: latest,
				Lane: state.lane, ModelStep: int(state.modelStep), ItemIndex: metadata.ItemIndex}}
		if metadata.EntityKind == "thinking" {
			fact.Thinking = []contracts.ThinkingItem{{Key: state.lane[len("thinking/"):], Text: statePayload.Text, Kind: statePayload.ThinkingKind}}
		}
		facts = append(facts, fact)
	}
	latest++
	terminalRecordID := semanticRecordID(executionID, latest)
	terminalConversation := (*contracts.ConversationMetadata)(nil)
	terminalRecordPayload := append(json.RawMessage(nil), runtimePayload...)
	if rootExecutionID != "" && sequence != 0 {
		terminalConversation = &contracts.ConversationMetadata{RootExecutionID: rootExecutionID, Sequence: sequence,
			EntityKind: "execution", LocalKey: "execution", FirstSequence: terminalFirstSequence, ItemIndex: 1}
		terminalRecordPayload, err = addConversationMetadata(terminalRecordPayload, terminalConversation)
		if err != nil {
			return nil, err
		}
	}
	terminalRecordPayload, err = contracts.EncodeCanonicalJSON(terminalRecordPayload)
	if err != nil {
		return nil, fmt.Errorf("history canonicalize terminal fact: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO semantic_records(record_id, execution_id, ordinal, kind, observed_at, payload_json, content_digest)
		VALUES(?, ?, ?, 'host_decision', ?, ?, NULL)`,
		terminalRecordID, executionID, latest, settledAt, string(terminalRecordPayload),
	); err != nil {
		return nil, fmt.Errorf("history persist terminal record: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE executions SET lifecycle='settled', outcome=?, adoption='non_canonical',
			settled_at=?, latest_ordinal=?, occurrence_count=occurrence_count+?,
			terminal_record_id=?, runtime_outcome_json=?
		WHERE execution_id=? AND lifecycle='active'`,
		string(outcome), settledAt, latest, len(states)+1, terminalRecordID,
		string(runtimePayload), executionID,
	)
	if err != nil {
		return nil, fmt.Errorf("history settle execution: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM assistant_text_states WHERE execution_id=?", executionID,
	); err != nil {
		return nil, fmt.Errorf("history clear assistant text state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("history commit execution end: %w", err)
	}
	if terminalConversation != nil {
		var runtimeResult struct {
			Error   string `json:"error"`
			Outcome string `json:"outcome"`
		}
		_ = json.Unmarshal(runtimePayload, &runtimeResult)
		facts = append(facts, contracts.ExecutionFact{RootExecutionID: rootExecutionID, ExecutionID: executionID, ParentExecutionID: parentID.String, ParentCallID: spawnID.String,
			RootExecutionOrdinal: rootOrdinal, Kind: "host_decision", Phase: string(outcome), Outcome: runtimeResult.Outcome,
			Error: runtimeResult.Error, Conversation: *terminalConversation,
			Locator: contracts.ExecutionLocator{ExecutionID: executionID, RecordID: terminalRecordID, Ordinal: latest}})
	}
	return facts, nil
}

func (s *Store) AdoptCanonical(ctx context.Context, executionID string, updatedAt string) error {
	if updatedAt == "" {
		updatedAt = timestampNow()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history begin canonical adoption: %w", err)
	}
	defer tx.Rollback()
	var sessionID sql.NullString
	var lifecycle, outcome, adoption, executionModel, executionAgent string
	var turn, baseRevision, baseMessageCount, latestOrdinal, occurrenceCount, unresolvedCount int64
	var terminalRecordID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT canonical_session_id, lifecycle, outcome, adoption, turn_number, base_revision,
			base_message_count, latest_ordinal, occurrence_count, unresolved_mandatory_count,
			terminal_record_id, model_json, agent_name
		FROM executions WHERE execution_id=?`, executionID,
	).Scan(&sessionID, &lifecycle, &outcome, &adoption, &turn, &baseRevision,
		&baseMessageCount, &latestOrdinal, &occurrenceCount, &unresolvedCount,
		&terminalRecordID, &executionModel, &executionAgent,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("history read execution for adoption: %w", err)
	}
	if !sessionID.Valid {
		return fmt.Errorf("history execution has no canonical Session")
	}
	if lifecycle != "settled" || outcome != string(OutcomeCompleted) || adoption != "non_canonical" || !terminalRecordID.Valid || latestOrdinal != occurrenceCount || unresolvedCount != 0 {
		return fmt.Errorf("history execution is not ready for canonical adoption")
	}
	var revision, messageCount, nextTurn, modelChangeCount, privateStateFromTurn int64
	var activeModel string
	if err := tx.QueryRowContext(ctx, `
		SELECT state_revision, message_count, next_turn, active_model_json,
			model_change_count, private_state_from_turn
		FROM sessions WHERE session_id=?`,
		sessionID.String,
	).Scan(&revision, &messageCount, &nextTurn, &activeModel, &modelChangeCount, &privateStateFromTurn); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("history read Session for adoption: %w", err)
	}
	if revision != baseRevision || messageCount != baseMessageCount || nextTurn != turn {
		return ErrRevisionConflict
	}
	activeModelCanonical, err := contracts.EncodeCanonicalJSON(json.RawMessage(activeModel))
	if err != nil {
		return fmt.Errorf("history canonicalize active Session model: %w", err)
	}
	executionModelCanonical, err := contracts.EncodeCanonicalJSON(json.RawMessage(executionModel))
	if err != nil {
		return fmt.Errorf("history canonicalize execution model: %w", err)
	}
	modelChanged := !bytes.Equal(activeModelCanonical, executionModelCanonical)
	var activeModelValue any
	modelChangeCountDelta := 0
	if modelChanged {
		var previousSelection, nextSelection struct {
			Provider string `json:"provider"`
			ModelID  string `json:"modelId"`
		}
		if err := json.Unmarshal([]byte(activeModel), &previousSelection); err != nil {
			return fmt.Errorf("history decode active Session model: %w", err)
		}
		if err := json.Unmarshal([]byte(executionModel), &nextSelection); err != nil {
			return fmt.Errorf("history decode execution model: %w", err)
		}
		if previousSelection.Provider != nextSelection.Provider || previousSelection.ModelID != nextSelection.ModelID {
			privateStateFromTurn = turn
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO session_model_changes(
				session_id, change_ordinal, effective_from_turn, changed_at, selection_json
			) VALUES(?, ?, ?, ?, ?)`, sessionID.String, modelChangeCount, turn, updatedAt, executionModel,
		); err != nil {
			return fmt.Errorf("history save adopted model selection: %w", err)
		}
		activeModelValue = executionModel
		modelChangeCountDelta = 1
	}
	var turnOrdinal int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(turn_ordinal), -1) + 1 FROM session_turns WHERE session_id=?`,
		sessionID.String,
	).Scan(&turnOrdinal); err != nil {
		return fmt.Errorf("history allocate Session turn: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_turns(session_id, turn_ordinal, turn_number, execution_id)
		VALUES(?, ?, ?, ?)`, sessionID.String, turnOrdinal, turn, executionID,
	); err != nil {
		return fmt.Errorf("history save Session turn: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT message_ordinal FROM messages WHERE execution_id=? ORDER BY message_ordinal`, executionID)
	if err != nil {
		return fmt.Errorf("history read messages for adoption: %w", err)
	}
	type messageOrdinal struct{ value int64 }
	var messages []messageOrdinal
	for rows.Next() {
		var item messageOrdinal
		if err := rows.Scan(&item.value); err != nil {
			_ = rows.Close()
			return fmt.Errorf("history scan messages for adoption: %w", err)
		}
		messages = append(messages, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("history read messages for adoption: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("history close messages for adoption: %w", err)
	}
	for index, item := range messages {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO conversation_messages(
				session_id, message_ordinal, turn_number, execution_id, execution_message_ordinal
			) VALUES(?, ?, ?, ?, ?)`, sessionID.String, baseMessageCount+int64(index), turn,
			executionID, item.value,
		); err != nil {
			return fmt.Errorf("history adopt conversation message: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions SET state_revision=state_revision+1, next_turn=next_turn+1,
			message_count=message_count+?, turn_count=turn_count+1, updated_at=?,
			active_model_json=COALESCE(?, active_model_json), agent_choice_json=?,
			model_change_count=model_change_count+?, private_state_from_turn=?
		WHERE session_id=? AND state_revision=? AND message_count=? AND next_turn=?`,
		len(messages), updatedAt, activeModelValue, string(contracts.JSON(map[string]string{"name": executionAgent})), modelChangeCountDelta,
		privateStateFromTurn, sessionID.String, baseRevision, baseMessageCount, turn,
	); err != nil {
		return fmt.Errorf("history update Session after adoption: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE executions SET adoption='canonical' WHERE execution_id=?
			AND lifecycle='settled' AND outcome='completed' AND adoption='non_canonical'`,
		executionID,
	); err != nil {
		return fmt.Errorf("history mark canonical adoption: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history commit canonical adoption: %w", err)
	}
	return nil
}

func (s *Store) SetAssistantText(
	ctx context.Context,
	executionID, lane string,
	modelStep, requestOrdinal, firstEventOrdinal int64,
	event json.RawMessage,
) error {
	if !validJSON(event) {
		return ErrInvalidJSON
	}
	encoded, err := contracts.EncodeCanonicalJSON(event)
	if err != nil {
		return fmt.Errorf("history canonicalize assistant text: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history begin assistant text state: %w", err)
	}
	defer tx.Rollback()
	var lifecycle string
	if err := tx.QueryRowContext(ctx,
		"SELECT lifecycle FROM executions WHERE execution_id=?", executionID,
	).Scan(&lifecycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("history read execution for assistant text: %w", err)
	}
	if lifecycle != "active" {
		return ErrExecutionClosed
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO assistant_text_states(
			execution_id, lane, model_step, request_ordinal, first_event_ordinal, event_json
		) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(execution_id, lane, model_step, request_ordinal) DO UPDATE SET
			first_event_ordinal=MIN(assistant_text_states.first_event_ordinal, excluded.first_event_ordinal),
			event_json=excluded.event_json`,
		executionID, lane, modelStep, requestOrdinal, firstEventOrdinal, string(encoded),
	)
	if err != nil {
		return fmt.Errorf("history save assistant text state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history commit assistant text state: %w", err)
	}
	return nil
}

func (s *Store) SetContext(ctx context.Context, executionID string, manifest json.RawMessage) error {
	if !validJSON(manifest) {
		return ErrInvalidJSON
	}
	encoded, err := contracts.EncodeCanonicalJSON(manifest)
	if err != nil {
		return fmt.Errorf("history canonicalize execution context: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("history begin execution context: %w", err)
	}
	defer tx.Rollback()
	var lifecycle string
	if err := tx.QueryRowContext(ctx,
		"SELECT lifecycle FROM executions WHERE execution_id=?", executionID,
	).Scan(&lifecycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("history read execution for context: %w", err)
	}
	if lifecycle != "active" {
		return ErrExecutionClosed
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO execution_contexts(execution_id, manifest_json) VALUES(?, ?)
		ON CONFLICT(execution_id) DO UPDATE SET manifest_json=excluded.manifest_json`,
		executionID, string(encoded),
	)
	if err != nil {
		return fmt.Errorf("history save execution context: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("history commit execution context: %w", err)
	}
	return nil
}

func semanticRecordID(executionID string, ordinal int64) string {
	return fmt.Sprintf("%s:semantic:%d", executionID, ordinal)
}

func eventObservedAt(event json.RawMessage, fallback string) string {
	var payload struct {
		ObservedAt string `json:"observedAt"`
	}
	if json.Unmarshal(event, &payload) == nil && strings.TrimSpace(payload.ObservedAt) != "" {
		return payload.ObservedAt
	}
	return fallback
}
