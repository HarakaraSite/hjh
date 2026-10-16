package history

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"hjh/internal/contracts"
	"hjh/internal/toolactivity"
)

const conversationPageSize = 256

// WalkConversationCut reads the permanent display facts through the supplied
// heads a page at a time. Each page is closed before a visitor runs, so visitor
// work never holds the SQLite connection. Active state is the immutable copy
// captured by CaptureConversationCut.
func (s *Store) WalkConversationCut(ctx context.Context, cut ConversationCut, visit func(contracts.ExecutionFact) error) error {
	if visit == nil {
		return fmt.Errorf("history walk conversation: missing visitor")
	}
	byID := make(map[string]ConversationExecutionCut, len(cut.Executions))
	childrenByCall := make(map[string][]ConversationExecutionCut)
	for _, execution := range cut.Executions {
		byID[execution.ExecutionID] = execution
		if execution.ParentExecutionID != "" && execution.SpawnCallID != "" {
			key := execution.ParentExecutionID + "\x00" + execution.SpawnCallID
			childrenByCall[key] = append(childrenByCall[key], execution)
		}
	}
	for key := range childrenByCall {
		sort.Slice(childrenByCall[key], func(i, j int) bool {
			return childrenByCall[key][i].DisplayOrdinal < childrenByCall[key][j].DisplayOrdinal
		})
	}
	state := legacyWalkState{enabled: cut.ThroughSequence == nil, first: map[string]uint64{}, visited: map[string]bool{},
		legacyProgress: map[string]map[int]contracts.ExecutionFact{}, legacyResults: map[string]map[int]contracts.ExecutionFact{},
		legacyTransferStep: map[string]int{}, legacyResultMessages: map[string]map[int]bool{}}
	var walkExecution func(ConversationExecutionCut) error
	walkExecution = func(execution ConversationExecutionCut) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if state.visited[execution.ExecutionID] {
			return nil
		}
		state.visited[execution.ExecutionID] = true
		task, err := s.readTask(ctx, execution)
		if err != nil {
			return err
		}
		task.RootExecutionOrdinal = cut.RootExecutionOrdinal
		task.Conversation = contracts.ConversationMetadata{RootExecutionID: cut.RootExecutionID, EntityKind: "task", LocalKey: "task"}
		task.Locator.ExecutionID = execution.ExecutionID
		if firstSequence, ok, err := s.firstConversationSequence(ctx, execution); err != nil {
			return err
		} else if ok {
			task.Conversation.Sequence = firstSequence
			task.Conversation.FirstSequence = firstSequence
		}
		state.assign(&task)
		if err := visit(task); err != nil {
			return err
		}
		emitState := func(savedState ConversationState) error {
			fact, ok := factFromJSON(cut, execution, savedState.EventJSON, "", savedState.FirstEventOrdinal, savedState.Lane)
			if !ok {
				return nil
			}
			if conversationMetadata(savedState.EventJSON) == nil {
				lane, thinkingKey := "parent", ""
				if len(savedState.Lane) >= len("thinking/") && savedState.Lane[:len("thinking/")] == "thinking/" {
					thinkingKey = savedState.Lane[len("thinking/"):]
				}
				fact.Request.ExecutionID = execution.ExecutionID
				fact.Request.Lane = lane
				fact.Request.ModelStep = int(savedState.ModelStep)
				fact.Request.RequestOrdinal = int(savedState.RequestOrdinal)
				if thinkingKey != "" {
					fact.Conversation.EntityKind = "thinking"
					fact.Conversation.LocalKey = lane + fmt.Sprintf("/%d/%s", savedState.RequestOrdinal, thinkingKey)
					fact.Conversation.ItemIndex = 0
					fact.Thinking = []contracts.ThinkingItem{{Key: thinkingKey, Text: fact.Text, Kind: fact.ThinkingKind}}
				} else {
					fact.Conversation.EntityKind = "assistant"
					fact.Conversation.LocalKey = lane + fmt.Sprintf("/%d/answer", savedState.RequestOrdinal)
				}
			}
			fact.RootExecutionOrdinal = cut.RootExecutionOrdinal
			fact.Phase = "streaming"
			fact.Locator.ExecutionID = execution.ExecutionID
			fact.Locator.Lane = savedState.Lane
			fact.Locator.ModelStep = int(savedState.ModelStep)
			if fact.Locator.RecordID == "" {
				fact.Locator.Ordinal = savedState.FirstEventOrdinal
			}
			state.assign(&fact)
			return visit(fact)
		}
		if err := s.walkRecordFacts(ctx, cut, execution, execution.States, &state, emitState,
			func(fact contracts.ExecutionFact) error {
				state.assign(&fact)
				return visit(fact)
			}, func(fact contracts.ExecutionFact) error {
				if fact.Conversation.EntityKind != "tool" || (fact.Phase != "started" && fact.Kind != "tool_call") {
					return nil
				}
				key := execution.ExecutionID + "\x00" + fact.CallID
				for _, child := range childrenByCall[key] {
					if err := walkExecution(child); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
			return err
		}
		return nil
	}
	if len(cut.Executions) == 0 {
		return nil
	}
	root, ok := byID[cut.RootExecutionID]
	if !ok {
		return ErrNotFound
	}
	if err := walkExecution(root); err != nil {
		return err
	}
	for _, execution := range cut.Executions {
		if !state.visited[execution.ExecutionID] {
			if err := walkExecution(execution); err != nil {
				return err
			}
		}
	}
	return nil
}

type legacyWalkState struct {
	enabled              bool
	next                 uint64
	first                map[string]uint64
	visited              map[string]bool
	legacyProgress       map[string]map[int]contracts.ExecutionFact
	legacyResults        map[string]map[int]contracts.ExecutionFact
	legacyResultMessages map[string]map[int]bool
	legacyTransferStep   map[string]int
}

func (s *legacyWalkState) assign(fact *contracts.ExecutionFact) {
	if !s.enabled {
		return
	}
	s.next++
	fact.Conversation.Sequence = s.next
	key := fact.ExecutionID + "\x00" + fact.Conversation.EntityKind + "\x00" + fact.Conversation.LocalKey
	first, ok := s.first[key]
	if !ok {
		first = s.next
		s.first[key] = first
	}
	fact.Conversation.FirstSequence = first
}

// Correlation candidates need their legacy fallback position before they are
// copied into the per-request index. Otherwise a later raw assistant Message
// would inherit the right request key but no firstSequence.
func (s *legacyWalkState) assignLegacyCorrelationSource(item storedConversationFact, fact *contracts.ExecutionFact) bool {
	if !s.enabled || fact.Kind == "" || fact.Request.ModelStep <= 0 || item.entityKind != "" {
		return false
	}
	if item.kind == "model_result" || (item.kind == "assistant_message" && item.sourceKind == "assistant_text") {
		s.assign(fact)
		return true
	}
	return false
}

func (s *Store) firstConversationSequence(ctx context.Context, execution ConversationExecutionCut) (uint64, bool, error) {
	var sequence sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(CAST(json_extract(payload_json,'$.conversation.sequence') AS INTEGER))
		FROM semantic_records WHERE execution_id=? AND ordinal<=?
		AND json_type(payload_json,'$.conversation.sequence')='integer'`, execution.ExecutionID, execution.LatestRecordOrdinal).Scan(&sequence); err != nil {
		return 0, false, fmt.Errorf("history read first conversation sequence: %w", err)
	}
	if !sequence.Valid || sequence.Int64 < 0 {
		return 0, false, nil
	}
	return uint64(sequence.Int64), true, nil
}

func (s *Store) WalkSessionConversationCut(ctx context.Context, cut SessionConversationCut, visit func(contracts.ExecutionFact) error) error {
	for _, root := range cut.Roots {
		if err := s.WalkConversationCut(ctx, root, visit); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) readTask(ctx context.Context, execution ConversationExecutionCut) (contracts.ExecutionFact, error) {
	var task string
	if err := s.db.QueryRowContext(ctx, `SELECT content_bytes FROM contents WHERE content_digest=?`, execution.TaskContentDigest).Scan(&task); err != nil {
		return contracts.ExecutionFact{}, fmt.Errorf("history read conversation task: %w", err)
	}
	return contracts.ExecutionFact{
		RootExecutionID:      execution.RootExecutionID,
		ExecutionID:          execution.ExecutionID,
		ParentExecutionID:    execution.ParentExecutionID,
		ParentCallID:         execution.SpawnCallID,
		RootExecutionOrdinal: execution.DisplayOrdinal,
		Kind:                 "task", Role: "user", Text: task,
		Conversation: contracts.ConversationMetadata{RootExecutionID: execution.RootExecutionID, EntityKind: "task", LocalKey: "task"},
		Locator:      contracts.ExecutionLocator{ExecutionID: execution.ExecutionID},
	}, nil
}

type storedConversationFact struct {
	id, kind, observedAt, executionID, parentExecutionID                                             string
	sourceKind, contentKind, phase, finishReason, role, name, callID, parentCallID, outcome, failure string
	text, entityKind, localKey, thinkingKind, lane, physicalRequestID                                string
	preview, previewInput                                                                            string
	recallJSON                                                                                       string
	diagnosticJSON                                                                                   string
	contentDigest                                                                                    sql.NullString
	sequence, firstSequence                                                                          sql.NullInt64
	itemIndex, modelStep, requestOrdinal, retryAttempt                                               sql.NullInt64
	ordinal                                                                                          int64
}

func (s *Store) walkRecordFacts(ctx context.Context, cut ConversationCut, execution ConversationExecutionCut,
	activeStates []ConversationState, state *legacyWalkState, emitState func(ConversationState) error,
	visit func(contracts.ExecutionFact) error, afterFact func(contracts.ExecutionFact) error) error {
	states := append([]ConversationState(nil), activeStates...)
	sort.SliceStable(states, func(i, j int) bool {
		if states[i].FirstEventOrdinal != states[j].FirstEventOrdinal {
			return states[i].FirstEventOrdinal < states[j].FirstEventOrdinal
		}
		if states[i].Lane != states[j].Lane {
			return states[i].Lane < states[j].Lane
		}
		if states[i].ModelStep != states[j].ModelStep {
			return states[i].ModelStep < states[j].ModelStep
		}
		return states[i].RequestOrdinal < states[j].RequestOrdinal
	})
	stateIndex := 0
	emitWhile := func(ordinal int64, inclusive bool) error {
		for stateIndex < len(states) {
			first := states[stateIndex].FirstEventOrdinal
			if first > ordinal || (!inclusive && first == ordinal) {
				break
			}
			if err := emitState(states[stateIndex]); err != nil {
				return err
			}
			stateIndex++
		}
		return nil
	}
	// Reuse the projection statement while paging through this execution.
	pageQuery, err := s.db.PrepareContext(ctx, `
			SELECT r.record_id, r.ordinal, r.kind, r.observed_at,
				COALESCE(json_extract(r.payload_json,'$.kind'),''),
				COALESCE(json_extract(r.payload_json,'$.content.kind'),json_extract(assistant_body.content_bytes,'$.content.kind'),''),
				COALESCE(json_extract(r.payload_json,'$.executionId'),''),
				COALESCE(json_extract(r.payload_json,'$.parentExecutionId'),''),
				COALESCE(json_extract(r.payload_json,'$.phase'),''),
				COALESCE(json_extract(r.payload_json,'$.result.finishReason'),json_extract(result_body.content_bytes,'$.finishReason'),''),
				COALESCE(json_extract(r.payload_json,'$.role'),''),
				COALESCE(json_extract(r.payload_json,'$.name'),json_extract(r.payload_json,'$.content[0].name'),json_extract(assistant_body.content_bytes,'$.content[0].name'),''),
			CASE WHEN r.kind IN ('tool_call','tool_result') THEN COALESCE(json_extract(r.payload_json,'$.callId'),json_extract(r.payload_json,'$.content[0].callId'),json_extract(r.payload_json,'$.id'),'') ELSE COALESCE(json_extract(r.payload_json,'$.callId'),'') END,
			COALESCE(json_extract(r.payload_json,'$.parentCallId'),json_extract(r.payload_json,'$.parent'),''),
				COALESCE(json_extract(r.payload_json,'$.outcome'),json_extract(r.payload_json,'$.content[0].outcome'),json_extract(assistant_body.content_bytes,'$.content[0].outcome'),''),
				COALESCE(json_extract(r.payload_json,'$.error'),''),
				CASE WHEN r.kind IN ('tool_call','tool_result') THEN '' ELSE
				COALESCE(json_extract(thinking_source.payload_json,'$.text'),json_extract(r.payload_json,'$.text'),json_extract(r.payload_json,'$.payload.text'),json_extract(r.payload_json,'$.result.text'),json_extract(result_body.content_bytes,'$.text'),json_extract(r.payload_json,'$.content.text'),json_extract(assistant_body.content_bytes,'$.content.text'),'') END,
				COALESCE(json_extract(r.payload_json,'$.preview'),''),
				CASE WHEN r.kind='tool_call' AND json_extract(r.payload_json,'$.preview') IS NULL THEN json_object(
					'path',COALESCE(json_extract(r.payload_json,'$.input.path'),json_extract(r.payload_json,'$.arguments.path'),json_extract(r.payload_json,'$.content[0].arguments.path')),
					'program',COALESCE(json_extract(r.payload_json,'$.input.program'),json_extract(r.payload_json,'$.arguments.program'),json_extract(r.payload_json,'$.content[0].arguments.program')),
					'args',COALESCE(json_extract(r.payload_json,'$.input.args'),json_extract(r.payload_json,'$.arguments.args'),json_extract(r.payload_json,'$.content[0].arguments.args')),
					'url',COALESCE(json_extract(r.payload_json,'$.input.url'),json_extract(r.payload_json,'$.arguments.url'),json_extract(r.payload_json,'$.content[0].arguments.url')),
					'role',COALESCE(json_extract(r.payload_json,'$.input.role'),json_extract(r.payload_json,'$.arguments.role'),json_extract(r.payload_json,'$.content[0].arguments.role')),
					'task',COALESCE(json_extract(r.payload_json,'$.input.task'),json_extract(r.payload_json,'$.arguments.task'),json_extract(r.payload_json,'$.content[0].arguments.task'))
				) ELSE '{}' END,
				COALESCE(json_extract(r.payload_json,'$.conversation.entityKind'),''),
				COALESCE(json_extract(r.payload_json,'$.conversation.localKey'),''),
				COALESCE(json_extract(r.payload_json,'$.thinkingKind'),''),
				json_extract(r.payload_json,'$.conversation.sequence'),
				json_extract(r.payload_json,'$.conversation.firstSequence'),
				json_extract(r.payload_json,'$.conversation.itemIndex'),
				COALESCE(json_extract(r.payload_json,'$.request.modelStep'),json_extract(r.payload_json,'$.modelStep')),
				COALESCE(json_extract(r.payload_json,'$.request.requestOrdinal'),json_extract(r.payload_json,'$.modelStep')),
			COALESCE(json_extract(r.payload_json,'$.request.lane'),'parent'),
				COALESCE(json_extract(r.payload_json,'$.request.physicalRequestId'),''),
				r.content_digest,
				COALESCE(json_extract(r.payload_json,'$.recall'),''),
                COALESCE(json_extract(r.payload_json,'$.diagnostic'),''),
				json_extract(r.payload_json,'$.request.retryAttempt')
			FROM semantic_records r
			LEFT JOIN semantic_records thinking_source
				ON thinking_source.record_id=json_extract(r.payload_json,'$.thinkingRecordId')
				AND thinking_source.execution_id=r.execution_id AND thinking_source.ordinal<r.ordinal
			LEFT JOIN contents result_body ON result_body.content_digest=CASE
				WHEN r.kind='model_result' AND json_type(r.payload_json,'$.resultRef')='text' THEN r.content_digest END
			LEFT JOIN contents assistant_body ON assistant_body.content_digest=CASE
				WHEN r.kind='assistant_message' AND json_extract(r.payload_json,'$.role')='assistant' THEN r.content_digest END
			WHERE r.execution_id=? AND r.ordinal>? AND r.ordinal<=?
				AND (r.kind IN ('user_message','assistant_message','model_result','tool_call','tool_result','execution_admission','host_decision')
					OR (r.kind='context_update' AND json_extract(r.payload_json,'$.conversation.entityKind')='notice'))
			ORDER BY r.ordinal LIMIT ?`)
	if err != nil {
		return fmt.Errorf("history prepare conversation record page: %w", err)
	}
	defer pageQuery.Close()
	var after int64
	for after < execution.LatestRecordOrdinal {
		rows, err := pageQuery.QueryContext(ctx, execution.ExecutionID, after, execution.LatestRecordOrdinal, conversationPageSize)
		if err != nil {
			return fmt.Errorf("history read conversation record page: %w", err)
		}
		var page []storedConversationFact
		for rows.Next() {
			var item storedConversationFact
			if err := rows.Scan(&item.id, &item.ordinal, &item.kind, &item.observedAt, &item.sourceKind,
				&item.contentKind, &item.executionID, &item.parentExecutionID, &item.phase, &item.finishReason, &item.role,
				&item.name, &item.callID, &item.parentCallID, &item.outcome, &item.failure,
				&item.text, &item.preview, &item.previewInput, &item.entityKind, &item.localKey, &item.thinkingKind, &item.sequence,
				&item.firstSequence, &item.itemIndex, &item.modelStep,
				&item.requestOrdinal, &item.lane, &item.physicalRequestID, &item.contentDigest, &item.recallJSON, &item.diagnosticJSON, &item.retryAttempt); err != nil {
				_ = rows.Close()
				return fmt.Errorf("history scan conversation record page: %w", err)
			}
			page = append(page, item)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("history read conversation record page: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("history close conversation record page: %w", err)
		}
		if len(page) == 0 {
			break
		}
		for _, item := range page {
			after = item.ordinal
			if err := emitWhile(item.ordinal, false); err != nil {
				return err
			}
			if item.entityKind == "steering" {
				if err := s.hydrateSteeringFact(ctx, &item); err != nil {
					return err
				}
			}
			fact := normalizeStoredFact(cut, execution, item)
			preassigned := state.assignLegacyCorrelationSource(item, &fact)
			state.enrichLegacyFact(execution, item, &fact)
			if fact.Kind != "" {
				if !preassigned {
					state.assign(&fact)
				}
				if err := visit(fact); err != nil {
					return err
				}
			}
			if err := emitWhile(item.ordinal, true); err != nil {
				return err
			}
			if fact.Kind != "" && afterFact != nil {
				if err := afterFact(fact); err != nil {
					return err
				}
			}
		}
	}
	return emitWhile(1<<63-1, true)
}

func (s *Store) hydrateSteeringFact(ctx context.Context, item *storedConversationFact) error {
	if !item.contentDigest.Valid {
		return fmt.Errorf("history read steering conversation message: missing content digest")
	}
	body, err := readContent(ctx, s.db, item.contentDigest.String)
	if err != nil {
		return fmt.Errorf("history read steering conversation message: %w", err)
	}
	var message contracts.Message
	if err := json.Unmarshal(body, &message); err != nil {
		return fmt.Errorf("history decode steering conversation message: %w", err)
	}
	item.role = message.Role
	item.text = message.VisibleText()
	return nil
}

func normalizeStoredFact(cut ConversationCut, execution ConversationExecutionCut, item storedConversationFact) contracts.ExecutionFact {
	metadata := contracts.ConversationMetadata{
		RootExecutionID: cut.RootExecutionID,
		Sequence:        uint64OrZero(item.sequence),
		EntityKind:      item.entityKind,
		LocalKey:        item.localKey,
		FirstSequence:   uint64OrZero(item.firstSequence),
		ItemIndex:       intOrZero(item.itemIndex),
	}
	fact := contracts.ExecutionFact{
		RootExecutionID:      cut.RootExecutionID,
		ExecutionID:          execution.ExecutionID,
		ParentExecutionID:    execution.ParentExecutionID,
		RootExecutionOrdinal: cut.RootExecutionOrdinal,
		Kind:                 item.kind,
		Phase:                item.phase,
		Role:                 item.role,
		Text:                 item.text,
		Preview:              item.preview,
		Name:                 item.name,
		CallID:               item.callID,
		ParentCallID:         item.parentCallID,
		Outcome:              item.outcome,
		Error:                item.failure,
		ThinkingKind:         item.thinkingKind,
		Request: contracts.RequestIdentity{ExecutionID: execution.ExecutionID, Lane: item.lane,
			RequestOrdinal: intOrZero(item.requestOrdinal), ModelStep: intOrZero(item.modelStep), PhysicalRequestID: item.physicalRequestID, RetryAttempt: intOrZero(item.retryAttempt)},
		Conversation: metadata,
		Locator: contracts.ExecutionLocator{ExecutionID: execution.ExecutionID, RecordID: item.id,
			Ordinal: item.ordinal, ItemIndex: intOrZero(item.itemIndex), Lane: item.lane, ModelStep: intOrZero(item.modelStep)},
	}
	if item.recallJSON != "" && json.Unmarshal([]byte(item.recallJSON), &fact.Recall) != nil {
		fact.Recall = nil
	}
	if item.diagnosticJSON != "" {
		_ = json.Unmarshal([]byte(item.diagnosticJSON), &fact.Diagnostic)
	}

	if fact.Conversation.EntityKind == "thinking" {
		key := item.localKey
		prefix := fact.Request.AssistantKey("")
		if fact.Request.Lane != "" && fact.Request.RequestOrdinal != 0 && strings.HasPrefix(key, prefix) {
			key = strings.TrimPrefix(key, prefix)
		}
		fact.Locator.Lane = "thinking/" + key
		fact.Thinking = []contracts.ThinkingItem{{Key: key, Text: fact.Text, Kind: fact.ThinkingKind}}
	}
	if item.kind == "tool_call" && fact.Preview == "" {
		fact.Preview = toolactivity.Preview(fact.Name, json.RawMessage(item.previewInput))
	}
	if item.kind == "model_result" {
		if item.entityKind == "assistant" && item.localKey != "" {
			fact.Kind = "assistant_message"
			fact.Role = "assistant"
			fact.Phase = "completed"
			if item.phase == "superseded" {
				fact.Phase = "superseded"
			}
			if item.finishReason == "length" {
				fact.Phase = "incomplete"
			}
		} else if item.entityKind == "" && item.text != "" {
			// HEAD's provider persisted a normal model_result Observation whose
			// modelStep and result.text are source fields; it had no request or
			// conversation metadata to read.
			fact.Kind = "assistant_message"
			fact.Role = "assistant"
			fact.Phase = "completed"
		} else {
			fact.Kind = ""
		}
	}
	if (fact.Conversation.EntityKind == "execution" || fact.Kind == "execution_admission" || fact.Kind == "host_decision") && fact.ParentCallID == "" && execution.ParentExecutionID != "" {
		fact.ParentCallID = execution.SpawnCallID
	}
	if fact.ExecutionID == "" {
		fact.ExecutionID = execution.ExecutionID
	}
	if fact.Conversation.EntityKind == "" || fact.Conversation.LocalKey == "" {
		switch item.kind {
		case "assistant_message", "model_result":
			fact.Conversation.EntityKind = "assistant"
			lane := item.lane
			if lane == "" {
				lane = "parent"
			}
			request := intOrZero(item.requestOrdinal)
			if request == 0 {
				request = intOrZero(item.modelStep)
			}
			fact.Conversation.LocalKey = fmt.Sprintf("%s/%d/answer", lane, request)
		case "tool_call", "tool_result":
			fact.Conversation.EntityKind = "tool"
			fact.Conversation.LocalKey = item.callID
		case "execution_admission", "host_decision":
			fact.Conversation.EntityKind = "execution"
			fact.Conversation.LocalKey = "execution"
		default:
			fact.Kind = ""
		}
	}
	if fact.Phase == "" {
		switch item.kind {
		case "assistant_message":
			if item.sourceKind == "assistant_text" || item.text != "" {
				fact.Phase = "streaming"
			}
		case "host_decision":
			fact.Phase = execution.Outcome
		case "tool_call":
			fact.Phase = "started"
		case "tool_result":
			fact.Phase = "completed"
		}
	}
	if item.kind == "assistant_message" && item.sourceKind == "assistant_text" {
		fact.Phase = "streaming"
		fact.Role = "assistant"
	}
	if fact.Conversation.EntityKind == "thinking" {
		key := item.localKey
		prefix := fact.Request.AssistantKey("")
		if fact.Request.Lane != "" && fact.Request.RequestOrdinal != 0 && strings.HasPrefix(key, prefix) {
			key = strings.TrimPrefix(key, prefix)
		}
		fact.Locator.Lane = "thinking/" + key
		fact.Locator.ItemIndex = fact.Conversation.ItemIndex
		fact.Thinking = []contracts.ThinkingItem{{Key: key, Text: fact.Text, Kind: fact.ThinkingKind}}
	}
	if fact.Outcome == "" && item.kind == "tool_result" {
		// JS-internal tool observations encode their result phase rather than a
		// separate outcome field. Keep the replay value identical to the live
		// typed fact for those records.
		fact.Outcome = fact.Phase
	}
	return fact
}

func (s *legacyWalkState) enrichLegacyFact(execution ConversationExecutionCut, item storedConversationFact, fact *contracts.ExecutionFact) {
	if fact.Kind == "" {
		return
	}
	if item.kind == "assistant_message" && item.sourceKind == "assistant_text" && item.entityKind == "" {
		if s.legacyProgress[execution.ExecutionID] == nil {
			s.legacyProgress[execution.ExecutionID] = map[int]contracts.ExecutionFact{}
		}
		if fact.Request.ModelStep > 0 {
			s.legacyProgress[execution.ExecutionID][fact.Request.ModelStep] = *fact
		}
	}
	if item.kind == "model_result" && item.entityKind == "" && fact.Request.ModelStep > 0 {
		if s.legacyResults[execution.ExecutionID] == nil {
			s.legacyResults[execution.ExecutionID] = map[int]contracts.ExecutionFact{}
		}
		s.legacyResults[execution.ExecutionID][fact.Request.ModelStep] = *fact
	}
	if item.kind != "assistant_message" || item.entityKind != "" ||
		item.modelStep.Valid || item.requestOrdinal.Valid || item.phase != "" {
		return
	}
	if item.sourceKind == "" && item.role == "assistant" && item.contentKind == "text" {
		var matchedStep int
		var matched contracts.ExecutionFact
		for candidateStep, candidate := range s.legacyResults[execution.ExecutionID] {
			if candidate.Text != fact.Text || s.legacyResultMessages[execution.ExecutionID][candidateStep] {
				continue
			}
			if candidateStep > matchedStep {
				matchedStep, matched = candidateStep, candidate
			}
		}
		if matchedStep == 0 {
			return
		}
		locator := fact.Locator
		*fact = matched
		fact.Locator = locator
		fact.Kind = "assistant_message"
		fact.Role = "assistant"
		fact.Text = item.text
		if fact.Phase == "" {
			fact.Phase = "completed"
		}
		if s.legacyResultMessages[execution.ExecutionID] == nil {
			s.legacyResultMessages[execution.ExecutionID] = map[int]bool{}
		}
		s.legacyResultMessages[execution.ExecutionID][matchedStep] = true
		return
	}
	if item.sourceKind != "assistant_message" {
		return
	}
	progressByStep := s.legacyProgress[execution.ExecutionID]
	var step int
	var progress contracts.ExecutionFact
	for candidateStep, candidate := range progressByStep {
		if candidate.Text != fact.Text || candidateStep <= s.legacyTransferStep[execution.ExecutionID] {
			continue
		}
		if step == 0 || candidateStep < step {
			step, progress = candidateStep, candidate
		}
	}
	if step == 0 {
		fact.Kind = ""
		return
	}
	*fact = contracts.ExecutionFact{
		RootExecutionID: fact.RootExecutionID, ExecutionID: fact.ExecutionID,
		ParentExecutionID: fact.ParentExecutionID, RootExecutionOrdinal: fact.RootExecutionOrdinal,
		Kind: "assistant_message", Role: "assistant", Text: fact.Text,
		Request: progress.Request, Conversation: progress.Conversation, Locator: fact.Locator,
	}
	if result, ok := s.legacyResults[execution.ExecutionID][step]; ok && result.Text == fact.Text {
		fact.Phase = "completed"
	} else {
		fact.Phase = "incomplete"
	}
	s.legacyTransferStep[execution.ExecutionID] = step
}

func factFromJSON(cut ConversationCut, execution ConversationExecutionCut, raw json.RawMessage, recordID string, ordinal int64, fallbackLane string) (contracts.ExecutionFact, bool) {
	var event struct {
		ExecutionID       string `json:"executionId"`
		ParentExecutionID string `json:"parentExecutionId"`
		Kind              string `json:"kind"`
		Phase             string `json:"phase"`
		Role              string `json:"role"`
		Text              string `json:"text"`
		ThinkingKind      string `json:"thinkingKind"`
		Payload           struct {
			Text string `json:"text"`
		} `json:"payload"`
		Name         string                         `json:"name"`
		CallID       string                         `json:"callId"`
		ParentCallID string                         `json:"parentCallId"`
		Outcome      string                         `json:"outcome"`
		Error        string                         `json:"error"`
		ModelStep    int                            `json:"modelStep"`
		Request      contracts.RequestIdentity      `json:"request"`
		Conversation contracts.ConversationMetadata `json:"conversation"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return contracts.ExecutionFact{}, false
	}
	if event.Kind != "assistant_message" && event.Conversation.EntityKind != "thinking" {
		return contracts.ExecutionFact{}, false
	}
	if event.Text == "" {
		event.Text = event.Payload.Text
	}
	if event.Request.Lane == "" {
		event.Request.Lane = fallbackLane
	}
	if event.Request.ModelStep == 0 {
		event.Request.ModelStep = event.ModelStep
	}
	metadata := event.Conversation
	if metadata.RootExecutionID == "" {
		metadata.RootExecutionID = cut.RootExecutionID
	}
	if metadata.EntityKind == "" {
		metadata.EntityKind = "assistant"
	}
	if metadata.LocalKey == "" {
		metadata.LocalKey = fmt.Sprintf("%s/%d/answer", event.Request.Lane, event.Request.RequestOrdinal)
	}
	fact := contracts.ExecutionFact{
		RootExecutionID:      cut.RootExecutionID,
		ExecutionID:          execution.ExecutionID,
		ParentExecutionID:    execution.ParentExecutionID,
		RootExecutionOrdinal: cut.RootExecutionOrdinal,
		Kind:                 event.Kind,
		Phase:                event.Phase,
		Role:                 event.Role,
		Text:                 event.Text,
		Name:                 event.Name,
		CallID:               event.CallID,
		ParentCallID:         event.ParentCallID,
		Outcome:              event.Outcome,
		Error:                event.Error,
		Request:              event.Request,
		ThinkingKind:         event.ThinkingKind,
		Conversation:         metadata,
		Locator:              contracts.ExecutionLocator{ExecutionID: execution.ExecutionID, RecordID: recordID, Ordinal: ordinal},
	}
	if metadata.EntityKind == "thinking" {
		key := metadata.LocalKey
		prefix := fact.Request.AssistantKey("")
		if fact.Request.Lane != "" && fact.Request.RequestOrdinal != 0 && strings.HasPrefix(key, prefix) {
			key = strings.TrimPrefix(key, prefix)
		}
		fact.Locator.Lane = "thinking/" + key
		fact.Locator.ItemIndex = metadata.ItemIndex
		fact.Thinking = []contracts.ThinkingItem{{Key: key, Text: fact.Text, Kind: fact.ThinkingKind}}
	}
	return fact, true
}

func uint64OrZero(value sql.NullInt64) uint64 {
	if !value.Valid || value.Int64 < 0 {
		return 0
	}
	return uint64(value.Int64)
}

func intOrZero(value sql.NullInt64) int {
	if !value.Valid {
		return 0
	}
	return int(value.Int64)
}
