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

type AssistantProgressInput struct {
	RootExecutionID string
	ExecutionID     string
	Sequence        uint64
	Request         contracts.RequestIdentity
	Text            string
	Thinking        []contracts.ThinkingItem
	ObservedAt      string
}

type AssistantResultInput struct {
	RootExecutionID string
	ExecutionID     string
	Sequence        uint64
	Request         contracts.RequestIdentity
	Payload         json.RawMessage
	Relations       []Relation
	Text            string
	Thinking        []contracts.ThinkingItem
	Phase           string
	ObservedAt      string
}

type ConversationMessageInput struct {
	RootExecutionID         string
	ExecutionID             string
	Sequence                uint64
	Message                 json.RawMessage
	ContentDigest           string
	Facts                   []contracts.ExecutionFact
	SteeringReceipt         *contracts.InputReceipt
	ChildInstructionReceipt *contracts.ChildInstructionReceipt
	FinalizeRequest         *contracts.RequestIdentity
	ObservedAt              string
}

// AppendConversationMessage stores a message, its typed semantic mirror, and
// any completed assistant/thinking entities in one transaction. A result
// clears the matching active latest-state rows in that same transaction.
func (s *Store) AppendConversationMessage(ctx context.Context, input ConversationMessageInput) (int64, []contracts.ExecutionFact, error) {
	if !validJSON(input.Message) {
		return 0, nil, ErrInvalidJSON
	}
	if input.Sequence == 0 {
		return 0, nil, fmt.Errorf("history append conversation message: missing sequence")
	}
	observedAt := input.ObservedAt
	if observedAt == "" {
		observedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("history begin conversation message: %w", err)
	}
	defer tx.Rollback()
	storedMessage, compactedToolMessage, err := compactToolMessageTx(ctx, tx, input.ExecutionID, input.Message)
	if err != nil {
		return 0, nil, err
	}
	var lifecycle string
	if err := tx.QueryRowContext(ctx, "SELECT lifecycle FROM executions WHERE execution_id=?", input.ExecutionID).Scan(&lifecycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil, ErrNotFound
		}
		return 0, nil, fmt.Errorf("history read execution for conversation message: %w", err)
	}
	if lifecycle != "active" {
		return 0, nil, ErrExecutionClosed
	}
	rootOrdinal, err := executionDisplayOrdinalTx(ctx, tx, input.RootExecutionID)
	if err != nil {
		return 0, nil, err
	}
	var messageOrdinal int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(message_ordinal),-1)+1 FROM messages WHERE execution_id=?", input.ExecutionID).Scan(&messageOrdinal); err != nil {
		return 0, nil, fmt.Errorf("history allocate conversation message ordinal: %w", err)
	}
	digest := input.ContentDigest
	if digest == "" || compactedToolMessage {
		digest, err = putContent(ctx, tx, storedMessage)
		if err != nil {
			return 0, nil, err
		}
	}
	var appliedReceipt contracts.InputReceipt
	var appliedChildInstruction contracts.ChildInstructionReceipt
	if input.SteeringReceipt != nil {
		if input.SteeringReceipt.Kind != "steering" || input.SteeringReceipt.ContentDigest != digest {
			return 0, nil, fmt.Errorf("history steering receipt does not match message content")
		}
	}
	if input.ChildInstructionReceipt != nil {
		receipt := *input.ChildInstructionReceipt
		if receipt.Status != "accepted" || receipt.ExecutionID != input.ExecutionID || receipt.ContentDigest != digest {
			return 0, nil, fmt.Errorf("history child instruction receipt does not match message content")
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO messages(execution_id,message_ordinal,content_digest) VALUES(?,?,?)", input.ExecutionID, messageOrdinal, digest); err != nil {
		return 0, nil, fmt.Errorf("history append conversation message: %w", err)
	}
	facts := append([]contracts.ExecutionFact(nil), input.Facts...)
	for _, base := range input.Facts {
		for index, item := range base.Thinking {
			key := item.Key
			if key == "" {
				key = fmt.Sprintf("item/%d", index)
			}
			facts = append(facts, contracts.ExecutionFact{RootExecutionID: input.RootExecutionID,
				ExecutionID: input.ExecutionID, ParentExecutionID: base.ParentExecutionID,
				Kind: "assistant_message", Phase: base.Phase, Role: "assistant", Text: item.Text,
				Request: base.Request, Thinking: []contracts.ThinkingItem{{Key: key, Text: item.Text, Kind: item.Kind}}, ThinkingKind: item.Kind,
				Conversation: contracts.ConversationMetadata{RootExecutionID: input.RootExecutionID,
					EntityKind: "thinking", LocalKey: base.Request.Lane + fmt.Sprintf("/%d/", base.Request.RequestOrdinal) + key,
					ItemIndex: index}})
		}
	}
	for index := range input.Facts {
		facts[index].Thinking = nil
	}
	stateByLocalKey := map[string]contracts.ExecutionFact{}
	if input.FinalizeRequest != nil {
		request := *input.FinalizeRequest
		request.ExecutionID = input.ExecutionID
		rows, err := tx.QueryContext(ctx, `SELECT lane,first_event_ordinal,event_json FROM assistant_text_states
			WHERE execution_id=? AND model_step=? AND request_ordinal=? ORDER BY lane`, input.ExecutionID, request.ModelStep, request.RequestOrdinal)
		if err != nil {
			return 0, nil, fmt.Errorf("history read final assistant state: %w", err)
		}
		var states []struct {
			lane  string
			first int64
			event json.RawMessage
		}
		for rows.Next() {
			var state struct {
				lane  string
				first int64
				event []byte
			}
			if err := rows.Scan(&state.lane, &state.first, &state.event); err != nil {
				_ = rows.Close()
				return 0, nil, fmt.Errorf("history scan final assistant state: %w", err)
			}
			states = append(states, struct {
				lane  string
				first int64
				event json.RawMessage
			}{state.lane, state.first, append(json.RawMessage(nil), state.event...)})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return 0, nil, fmt.Errorf("history read final assistant state: %w", err)
		}
		if err := rows.Close(); err != nil {
			return 0, nil, fmt.Errorf("history close final assistant state: %w", err)
		}
		for _, state := range states {
			stateFact, ok := factFromJSON(ConversationCut{RootExecutionID: input.RootExecutionID},
				ConversationExecutionCut{RootExecutionID: input.RootExecutionID, ExecutionID: input.ExecutionID},
				state.event, "", state.first, state.lane)
			if !ok {
				continue
			}
			stateByLocalKey[stateFact.Conversation.LocalKey] = stateFact
		}
		for index := range facts {
			fact := &facts[index]
			if fact.Request.ModelStep == 0 {
				fact.Request = request
			}
			if previous, ok := stateByLocalKey[fact.Conversation.LocalKey]; ok {
				fact.Conversation.FirstSequence = previous.Conversation.FirstSequence
				delete(stateByLocalKey, fact.Conversation.LocalKey)
			} else if fact.Conversation.FirstSequence == 0 {
				first, err := firstConversationSequenceTx(ctx, tx, input.ExecutionID,
					fact.Conversation.EntityKind, fact.Conversation.LocalKey)
				if err != nil {
					return 0, nil, err
				}
				if first == 0 {
					first = input.Sequence
				}
				fact.Conversation.FirstSequence = first
			}
			fact.Conversation.Sequence = input.Sequence
		}
		for _, stateFact := range stateByLocalKey {
			stateFact.Conversation.Sequence = input.Sequence
			stateFact.Phase = "completed"
			stateFact.Request = request
			facts = append(facts, stateFact)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM assistant_text_states WHERE execution_id=? AND model_step=? AND request_ordinal=?`, input.ExecutionID, request.ModelStep, request.RequestOrdinal); err != nil {
			return 0, nil, fmt.Errorf("history clear completed assistant state: %w", err)
		}
	}
	for index := range facts {
		fact := &facts[index]
		metadata := fact.Conversation
		metadata.RootExecutionID = input.RootExecutionID
		metadata.Sequence = input.Sequence
		if metadata.FirstSequence == 0 {
			metadata.FirstSequence = input.Sequence
		}
		fact.Conversation = metadata
		fact.RootExecutionID = input.RootExecutionID
		fact.ExecutionID = input.ExecutionID
		fact.RootExecutionOrdinal = rootOrdinal
		fact.Locator.ExecutionID = input.ExecutionID
		fact.Locator.MessageOrdinal = messageOrdinal
		if fact.Phase == "" {
			fact.Phase = "completed"
		}
		if fact.Kind == "" {
			fact.Kind = semanticKindForFact(*fact)
		}
		mirrorFact := *fact
		factTextRef := ""
		if compactedToolMessage && fact.Kind == "tool_result" && fact.Text != "" {
			body := []byte(fact.Text)
			factTextRef = contentDigest(body)
			if err := ensureToolBodyOwnerTx(ctx, tx, input.ExecutionID, factTextRef, body); err != nil {
				return 0, nil, err
			}
			mirrorFact.Text = ""
		}
		payload, err := messageFactPayload(storedMessage, mirrorFact, messageOrdinal, observedAt)
		if err != nil {
			return 0, nil, err
		}
		if factTextRef != "" {
			payload, err = addSemanticToolFactTextRef(payload, factTextRef)
			if err != nil {
				return 0, nil, err
			}
		}
		recordDigest := digest
		if compactedToolMessage {
			recordDigest = ""
		}
		record, err := appendSemanticRecordTxWithContent(ctx, tx, input.ExecutionID, fact.Kind, payload, observedAt, recordDigest)
		if err != nil {
			return 0, nil, err
		}
		fact.Locator.RecordID = record.ID
		fact.Locator.Ordinal = record.Ordinal
	}
	if len(facts) == 0 {
		var message struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(input.Message, &message)
		kind := message.Role + "_message"
		if message.Role == "tool" {
			kind = "tool_result"
		}
		recordDigest := digest
		if compactedToolMessage {
			recordDigest = ""
		}
		if _, err := appendSemanticRecordTxWithContent(ctx, tx, input.ExecutionID, kind, storedMessage, observedAt, recordDigest); err != nil {
			return 0, nil, err
		}
	}
	if input.SteeringReceipt != nil {
		updated := *input.SteeringReceipt
		updated.Status = "applied"
		updated.AppliedSequence = input.Sequence
		ordinal := messageOrdinal
		updated.AppliedMessageOrdinal = &ordinal
		updated.Reason = ""
		updated, err = updateRuntimeInputTx(ctx, tx, updated)
		if err != nil {
			return 0, nil, err
		}
		appliedReceipt = updated
	}
	if input.ChildInstructionReceipt != nil {
		updated := *input.ChildInstructionReceipt
		updated.Status = "applied"
		updated.AppliedSequence = input.Sequence
		ordinal := messageOrdinal
		updated.AppliedMessageOrdinal = &ordinal
		updated.Reason = ""
		updated.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		payload := contracts.JSON(map[string]any{
			"operation": "agent_instruction",
			"decision":  "applied",
			"receipt":   updated,
		})
		if _, err := appendSemanticRecordTxWithContent(ctx, tx, input.ExecutionID, "control_decision", payload, observedAt, digest); err != nil {
			return 0, nil, fmt.Errorf("history save applied child instruction: %w", err)
		}
		appliedChildInstruction = updated
	}
	if err := tx.Commit(); err != nil {
		return 0, nil, fmt.Errorf("history commit conversation message: %w", err)
	}
	if input.SteeringReceipt != nil {
		*input.SteeringReceipt = appliedReceipt
	}
	if input.ChildInstructionReceipt != nil {
		*input.ChildInstructionReceipt = appliedChildInstruction
	}
	return messageOrdinal, facts, nil
}

func firstConversationSequenceTx(ctx context.Context, tx *sql.Tx, executionID, entityKind, localKey string) (uint64, error) {
	if entityKind == "" || localKey == "" {
		return 0, nil
	}
	var first sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MIN(CAST(json_extract(payload_json,'$.conversation.firstSequence') AS INTEGER))
		FROM semantic_records WHERE execution_id=?
		AND json_extract(payload_json,'$.conversation.entityKind')=?
		AND json_extract(payload_json,'$.conversation.localKey')=?
		AND json_type(payload_json,'$.conversation.firstSequence')='integer'`, executionID, entityKind, localKey).Scan(&first); err != nil {
		return 0, fmt.Errorf("history read conversation entity first sequence: %w", err)
	}
	if !first.Valid || first.Int64 < 0 {
		return 0, nil
	}
	return uint64(first.Int64), nil
}

// SaveAssistantResult records a provider result, its final visible answer and
// thinking items, and removal of the matching active state in one transaction.
// This makes the result itself sufficient for replay if the later agent message
// append or run terminal write does not happen.
func (s *Store) SaveAssistantResult(ctx context.Context, input AssistantResultInput) ([]contracts.ExecutionFact, error) {
	if !validJSON(input.Payload) {
		return nil, ErrInvalidJSON
	}
	if input.Sequence == 0 {
		return nil, fmt.Errorf("history save assistant result: missing sequence")
	}
	observedAt := input.ObservedAt
	if observedAt == "" {
		observedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	request := input.Request
	request.ExecutionID = input.ExecutionID
	if request.Lane == "" {
		request.Lane = "parent"
	}
	if request.RequestOrdinal == 0 {
		request.RequestOrdinal = request.ModelStep
	}
	phase := input.Phase
	if phase == "" {
		phase = "completed"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("history begin assistant result: %w", err)
	}
	defer tx.Rollback()
	var lifecycle string
	if err := tx.QueryRowContext(ctx, "SELECT lifecycle FROM executions WHERE execution_id=?", input.ExecutionID).Scan(&lifecycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read execution for assistant result: %w", err)
	}
	if lifecycle != "active" {
		return nil, ErrExecutionClosed
	}
	rootOrdinal, err := executionDisplayOrdinalTx(ctx, tx, input.RootExecutionID)
	if err != nil {
		return nil, err
	}
	firstByLocalKey := map[string]uint64{}
	rows, err := tx.QueryContext(ctx, `SELECT event_json FROM assistant_text_states
		WHERE execution_id=? AND model_step=? AND request_ordinal=?`,
		input.ExecutionID, request.ModelStep, request.RequestOrdinal)
	if err != nil {
		return nil, fmt.Errorf("history read assistant state for result: %w", err)
	}
	for rows.Next() {
		var event []byte
		if err := rows.Scan(&event); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("history scan assistant state for result: %w", err)
		}
		if metadata := conversationMetadata(event); metadata != nil && metadata.LocalKey != "" && metadata.FirstSequence != 0 {
			firstByLocalKey[metadata.LocalKey] = metadata.FirstSequence
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("history read assistant state for result: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("history close assistant state for result: %w", err)
	}
	answerKey := request.Lane + fmt.Sprintf("/%d/answer", request.RequestOrdinal)
	answerFirst := firstByLocalKey[answerKey]
	if answerFirst == 0 {
		answerFirst = input.Sequence
	}
	resultMetadata := contracts.ConversationMetadata{RootExecutionID: input.RootExecutionID, Sequence: input.Sequence,
		EntityKind: "model_result", LocalKey: "model_result", FirstSequence: input.Sequence}
	if input.Text != "" {
		resultMetadata.EntityKind = "assistant"
		resultMetadata.LocalKey = answerKey
		resultMetadata.FirstSequence = answerFirst
	}
	payload, err := assistantResultPayload(input.Payload, input.Text, request)
	if err != nil {
		return nil, err
	}
	record, err := appendOccurrenceTx(ctx, tx, input.ExecutionID, OccurrenceInput{
		Kind: "model_result", Payload: payload, Relations: input.Relations,
		ObservedAt: observedAt, Conversation: &resultMetadata,
	})
	if err != nil {
		return nil, err
	}
	facts := make([]contracts.ExecutionFact, 0, 1+len(input.Thinking))
	if input.Text != "" {
		facts = append(facts, contracts.ExecutionFact{RootExecutionID: input.RootExecutionID, ExecutionID: input.ExecutionID,
			RootExecutionOrdinal: rootOrdinal, Kind: "assistant_message", Phase: phase, Role: "assistant", Text: input.Text,
			Request: request, Conversation: resultMetadata,
			Locator: contracts.ExecutionLocator{ExecutionID: input.ExecutionID, RecordID: record.ID, Ordinal: record.Ordinal,
				Lane: request.Lane, ModelStep: request.ModelStep}})
	}
	for index, item := range input.Thinking {
		if item.Text == "" {
			continue
		}
		key := item.Key
		if key == "" {
			key = fmt.Sprintf("item/%d", index)
		}
		localKey := request.Lane + fmt.Sprintf("/%d/", request.RequestOrdinal) + key
		firstSequence := firstByLocalKey[localKey]
		if firstSequence == 0 {
			firstSequence = input.Sequence
		}
		metadata := contracts.ConversationMetadata{RootExecutionID: input.RootExecutionID, Sequence: input.Sequence,
			EntityKind: "thinking", LocalKey: localKey, FirstSequence: firstSequence, ItemIndex: index}
		payload := contracts.JSON(map[string]any{"kind": "assistant_message", "phase": phase, "role": "assistant",
			"text": item.Text, "thinkingKind": item.Kind, "request": request, "conversation": metadata, "observedAt": observedAt})
		thinkingRecord, err := appendSemanticRecordTx(ctx, tx, input.ExecutionID, "assistant_message", payload, observedAt)
		if err != nil {
			return nil, err
		}
		facts = append(facts, contracts.ExecutionFact{RootExecutionID: input.RootExecutionID, ExecutionID: input.ExecutionID,
			RootExecutionOrdinal: rootOrdinal, Kind: "assistant_message", Phase: phase, Role: "assistant", Text: item.Text,
			Request: request, Thinking: []contracts.ThinkingItem{{Key: key, Text: item.Text, Kind: item.Kind}}, ThinkingKind: item.Kind, Conversation: metadata,
			Locator: contracts.ExecutionLocator{ExecutionID: input.ExecutionID, RecordID: thinkingRecord.ID, Ordinal: thinkingRecord.Ordinal,
				Lane: "thinking/" + key, ModelStep: request.ModelStep, ItemIndex: index}})
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM assistant_text_states
		WHERE execution_id=? AND model_step=? AND request_ordinal=?`, input.ExecutionID, request.ModelStep, request.RequestOrdinal); err != nil {
		return nil, fmt.Errorf("history clear assistant state for result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("history commit assistant result: %w", err)
	}
	return facts, nil
}

func assistantResultPayload(payload json.RawMessage, text string, request contracts.RequestIdentity) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return nil, fmt.Errorf("history assistant result must be an object: %w", ErrInvalidJSON)
	}
	if text != "" {
		var result struct {
			Text *string `json:"text"`
		}
		_ = json.Unmarshal(object["result"], &result)
		if result.Text == nil || *result.Text != text {
			object["text"] = contracts.JSON(text)
		}
	}
	object["request"] = contracts.JSON(request)
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("history encode assistant result text: %w", err)
	}
	return encoded, nil
}

func messageFactPayload(message json.RawMessage, fact contracts.ExecutionFact, messageOrdinal int64, observedAt string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if fact.Conversation.EntityKind == "steering" {
		// The full accepted Message is already stored in contents and referenced
		// by this semantic record's content_digest. Keep its typed mirror compact
		// so the steering body is not copied into payload_json as well.
		object = map[string]json.RawMessage{
			"role":     contracts.JSON(fact.Role),
			"steering": contracts.JSON(true),
		}
	} else if fact.Conversation.EntityKind == "recall" {
		if fact.Recall == nil {
			return nil, fmt.Errorf("history recall message is missing its marker")
		}
		// The full reference message is kept once in contents. Its semantic
		// mirror contains only the short display text and stable locators.
		object = map[string]json.RawMessage{
			"role":                 contracts.JSON(fact.Role),
			"recall":               contracts.JSON(fact.Recall),
			"messageContentDigest": contracts.JSON(recallMessageDigest(message)),
		}
	} else if err := json.Unmarshal(message, &object); err != nil || object == nil {
		return nil, fmt.Errorf("history conversation message must be an object: %w", ErrInvalidJSON)
	}
	object["kind"] = contracts.JSON(fact.Kind)
	object["phase"] = contracts.JSON(fact.Phase)
	object["executionId"] = contracts.JSON(fact.ExecutionID)
	object["parentExecutionId"] = contracts.JSON(fact.ParentExecutionID)
	object["observedAt"] = contracts.JSON(observedAt)
	object["conversation"] = contracts.JSON(fact.Conversation)
	object["locator"] = contracts.JSON(map[string]any{"executionId": fact.ExecutionID, "messageOrdinal": messageOrdinal,
		"itemIndex": fact.Conversation.ItemIndex})
	if fact.Text != "" && fact.Conversation.EntityKind != "steering" {
		object["text"] = contracts.JSON(fact.Text)
	}
	if fact.ThinkingKind != "" {
		object["thinkingKind"] = contracts.JSON(fact.ThinkingKind)
	}
	if fact.Request.ExecutionID != "" || fact.Request.ModelStep != 0 {
		object["request"] = contracts.JSON(fact.Request)
	}
	if fact.CallID != "" {
		object["callId"] = contracts.JSON(fact.CallID)
	}
	if fact.ParentCallID != "" {
		object["parentCallId"] = contracts.JSON(fact.ParentCallID)
	}
	if fact.Name != "" {
		object["name"] = contracts.JSON(fact.Name)
	}
	if fact.Preview != "" {
		object["preview"] = contracts.JSON(fact.Preview)
	}
	if fact.Outcome != "" {
		object["outcome"] = contracts.JSON(fact.Outcome)
	}
	if fact.Error != "" {
		object["error"] = contracts.JSON(fact.Error)
	}
	return json.Marshal(object)
}

func semanticKindForFact(fact contracts.ExecutionFact) string {
	switch fact.Conversation.EntityKind {
	case "recall":
		return "user_message"
	case "assistant", "thinking":
		return "assistant_message"
	case "tool":
		if fact.Phase == "started" {
			return "tool_call"
		}
		return "tool_result"
	case "task":
		return "user_message"
	case "execution":
		return "execution_admission"
	default:
		return fact.Kind
	}
}

// SaveAssistantProgress writes first-visible markers and replaces the latest
// body/thinking state in one transaction. Later updates replace only state.
func (s *Store) SaveAssistantProgress(ctx context.Context, input AssistantProgressInput) ([]contracts.ExecutionFact, error) {
	observedAt := input.ObservedAt
	if observedAt == "" {
		observedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if input.Sequence == 0 {
		return nil, fmt.Errorf("history save assistant progress: missing sequence")
	}
	request := input.Request
	request.ExecutionID = input.ExecutionID
	if request.Lane == "" {
		request.Lane = "parent"
	}
	if request.RequestOrdinal == 0 {
		request.RequestOrdinal = request.ModelStep
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("history begin assistant progress: %w", err)
	}
	defer tx.Rollback()
	var lifecycle string
	if err := tx.QueryRowContext(ctx, "SELECT lifecycle FROM executions WHERE execution_id=?", input.ExecutionID).Scan(&lifecycle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("history read execution for assistant progress: %w", err)
	}
	if lifecycle != "active" {
		return nil, ErrExecutionClosed
	}
	rootOrdinal, err := executionDisplayOrdinalTx(ctx, tx, input.RootExecutionID)
	if err != nil {
		return nil, err
	}
	type update struct {
		stateLane  string
		entityKind string
		localKey   string
		itemIndex  int
		text       string
		thinking   *contracts.ThinkingItem
	}
	updates := make([]update, 0, 1+len(input.Thinking))
	if input.Text != "" {
		updates = append(updates, update{stateLane: request.Lane, entityKind: "assistant", localKey: request.Lane + "/" + fmt.Sprint(request.RequestOrdinal) + "/answer", text: input.Text})
	}
	for index := range input.Thinking {
		item := input.Thinking[index]
		if item.Text == "" {
			continue
		}
		if item.Key == "" {
			item.Key = fmt.Sprintf("item/%d", index)
		}
		copy := item
		updates = append(updates, update{stateLane: "thinking/" + item.Key, entityKind: "thinking",
			localKey:  request.Lane + "/" + fmt.Sprint(request.RequestOrdinal) + "/" + item.Key,
			itemIndex: index, text: item.Text, thinking: &copy})
	}
	facts := make([]contracts.ExecutionFact, 0, len(updates))
	var latest int64
	if err := tx.QueryRowContext(ctx, "SELECT latest_ordinal FROM executions WHERE execution_id=?", input.ExecutionID).Scan(&latest); err != nil {
		return nil, fmt.Errorf("history read assistant progress ordinal: %w", err)
	}
	for _, item := range updates {
		var firstEventOrdinal int64
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT first_event_ordinal,event_json FROM assistant_text_states
			WHERE execution_id=? AND lane=? AND model_step=? AND request_ordinal=?`,
			input.ExecutionID, item.stateLane, request.ModelStep, request.RequestOrdinal).Scan(&firstEventOrdinal, &existing)
		metadata := contracts.ConversationMetadata{RootExecutionID: input.RootExecutionID, Sequence: input.Sequence,
			EntityKind: item.entityKind, LocalKey: item.localKey, ItemIndex: item.itemIndex}
		if err == nil {
			if previous := conversationMetadata(json.RawMessage(existing)); previous != nil {
				metadata.FirstSequence = previous.FirstSequence
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			metadata.FirstSequence = input.Sequence
			marker := contracts.JSON(map[string]any{"kind": "assistant_message", "phase": "started", "observedAt": observedAt,
				"request": request, "conversation": metadata})
			record, appendErr := appendSemanticRecordTx(ctx, tx, input.ExecutionID, "assistant_message", marker, observedAt)
			if appendErr != nil {
				return nil, appendErr
			}
			latest = record.Ordinal
			firstEventOrdinal = record.Ordinal
		} else {
			return nil, fmt.Errorf("history read assistant latest state: %w", err)
		}
		event := map[string]any{"kind": "assistant_message", "phase": "progress", "observedAt": observedAt,
			"payload": map[string]any{"text": item.text}, "request": request, "conversation": metadata}
		if item.thinking != nil && item.thinking.Kind != "" {
			event["thinkingKind"] = item.thinking.Kind
		}
		eventJSON, err := contracts.EncodeCanonicalJSON(contracts.JSON(event))
		if err != nil {
			return nil, fmt.Errorf("history encode assistant latest state: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO assistant_text_states(
			execution_id,lane,model_step,request_ordinal,first_event_ordinal,event_json)
			VALUES(?,?,?,?,?,?) ON CONFLICT(execution_id,lane,model_step,request_ordinal) DO UPDATE SET
			event_json=excluded.event_json`, input.ExecutionID, item.stateLane, request.ModelStep,
			request.RequestOrdinal, firstEventOrdinal, string(eventJSON)); err != nil {
			return nil, fmt.Errorf("history replace assistant latest state: %w", err)
		}
		fact := contracts.ExecutionFact{RootExecutionID: input.RootExecutionID, ExecutionID: input.ExecutionID,
			RootExecutionOrdinal: rootOrdinal, Kind: "assistant_message", Phase: "streaming", Role: "assistant",
			Text: item.text, Request: request, Conversation: metadata,
			Locator: contracts.ExecutionLocator{ExecutionID: input.ExecutionID, Ordinal: firstEventOrdinal,
				Lane: item.stateLane, ModelStep: request.ModelStep, ItemIndex: item.itemIndex}}
		if item.thinking != nil {
			fact.Thinking = []contracts.ThinkingItem{*item.thinking}
			fact.ThinkingKind = item.thinking.Kind
		}
		facts = append(facts, fact)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("history commit assistant progress: %w", err)
	}
	return facts, nil
}

func executionDisplayOrdinalTx(ctx context.Context, tx *sql.Tx, executionID string) (int64, error) {
	var ordinal int64
	if err := tx.QueryRowContext(ctx, `SELECT execution_ordinal FROM execution_display_positions WHERE execution_id=?`, executionID).Scan(&ordinal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("history read execution display ordinal: %w", err)
	}
	return ordinal, nil
}

func appendSemanticRecordTx(ctx context.Context, tx *sql.Tx, executionID, kind string, payload json.RawMessage, observedAt string) (Record, error) {
	encoded, err := contracts.EncodeCanonicalJSON(payload)
	if err != nil {
		return Record{}, fmt.Errorf("history canonicalize execution fact: %w", err)
	}
	var latest int64
	if err := tx.QueryRowContext(ctx, "SELECT latest_ordinal FROM executions WHERE execution_id=?", executionID).Scan(&latest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("history read execution fact ordinal: %w", err)
	}
	ordinal := latest + 1
	recordID := semanticRecordID(executionID, ordinal)
	if _, err := tx.ExecContext(ctx, `INSERT INTO semantic_records(record_id,execution_id,ordinal,kind,observed_at,payload_json,content_digest)
		VALUES(?,?,?,?,?,?,NULL)`, recordID, executionID, ordinal, kind, observedAt, string(encoded)); err != nil {
		return Record{}, fmt.Errorf("history append execution fact: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE executions SET latest_ordinal=?,occurrence_count=occurrence_count+1 WHERE execution_id=?", ordinal, executionID); err != nil {
		return Record{}, fmt.Errorf("history update execution fact ordinal: %w", err)
	}
	return Record{ID: recordID, ExecutionID: executionID, Ordinal: ordinal, Kind: kind, ObservedAt: observedAt,
		Payload: append(json.RawMessage(nil), encoded...), Conversation: conversationMetadata(encoded)}, nil
}

func appendSemanticRecordTxWithContent(ctx context.Context, tx *sql.Tx, executionID, kind string, payload json.RawMessage, observedAt, contentDigest string) (Record, error) {
	encoded, err := contracts.EncodeCanonicalJSON(payload)
	if err != nil {
		return Record{}, fmt.Errorf("history canonicalize conversation message fact: %w", err)
	}
	var latest int64
	if err := tx.QueryRowContext(ctx, "SELECT latest_ordinal FROM executions WHERE execution_id=?", executionID).Scan(&latest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Record{}, ErrNotFound
		}
		return Record{}, fmt.Errorf("history read conversation message ordinal: %w", err)
	}
	ordinal := latest + 1
	recordID := semanticRecordID(executionID, ordinal)
	var contentReference any
	if contentDigest != "" {
		contentReference = contentDigest
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO semantic_records(record_id,execution_id,ordinal,kind,observed_at,payload_json,content_digest)
		VALUES(?,?,?,?,?,?,?)`, recordID, executionID, ordinal, kind, observedAt, string(encoded), contentReference); err != nil {
		return Record{}, fmt.Errorf("history append conversation message fact: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE executions SET latest_ordinal=?,occurrence_count=occurrence_count+1 WHERE execution_id=?", ordinal, executionID); err != nil {
		return Record{}, fmt.Errorf("history update conversation message ordinal: %w", err)
	}
	return Record{ID: recordID, ExecutionID: executionID, Ordinal: ordinal, Kind: kind, ObservedAt: observedAt,
		Payload: append(json.RawMessage(nil), encoded...), Conversation: conversationMetadata(encoded)}, nil
}
