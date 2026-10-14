package history

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func recallTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session, err := store.CreateSession(context.Background(), SessionInput{ID: "recall-session", WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return store, session.ID
}

func beginRecallTestExecution(t *testing.T, store *Store, sessionID, id, task string) Execution {
	t.Helper()
	execution, err := store.BeginExecution(context.Background(), BeginExecutionInput{ExecutionID: id, Task: task, SessionID: sessionID,
		Model: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`), Build: json.RawMessage(`{"fixture":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

func settleRecallTestExecution(t *testing.T, store *Store, execution Execution, outcome Outcome, settledAt string) {
	t.Helper()
	if err := store.EndExecution(context.Background(), execution.ID, outcome,
		contracts.JSON(map[string]any{"outcome": string(outcome)}), settledAt); err != nil {
		t.Fatal(err)
	}
}

func TestSelectRecallSourceUsesSettledNoncanonicalSessionExecutions(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	old := beginRecallTestExecution(t, store, sessionID, "root-old", "old")
	settleRecallTestExecution(t, store, old, OutcomeCancelled, "2026-10-10T00:00:00Z")
	latest := beginRecallTestExecution(t, store, sessionID, "root-latest", "latest")
	settleRecallTestExecution(t, store, latest, OutcomeFailed, "2026-10-10T00:00:01Z")
	completed := beginRecallTestExecution(t, store, sessionID, "root-completed", "completed")
	settleRecallTestExecution(t, store, completed, OutcomeCompleted, "2026-10-10T00:00:02Z")
	ineligiblePrefix, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{ExecutionID: "prefix-ineligible", Task: "completed exact ID",
		ParentExecutionID: latest.ID, Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	settleRecallTestExecution(t, store, ineligiblePrefix, OutcomeCompleted, "2026-10-10T00:00:03Z")
	eligiblePrefix, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{ExecutionID: "prefix-ineligible-eligible", Task: "eligible similar ID",
		ParentExecutionID: latest.ID, Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	settleRecallTestExecution(t, store, eligiblePrefix, OutcomeCancelled, "2026-10-10T00:00:04Z")

	selected, err := store.SelectRecallSource(ctx, sessionID, "latest")
	if err != nil || selected.ExecutionID != latest.ID || selected.Outcome != string(OutcomeFailed) {
		t.Fatalf("latest recall source = %+v, %v", selected, err)
	}
	if _, err := store.SelectRecallSource(ctx, sessionID, "missing"); !errors.Is(err, ErrRecallNotFound) {
		t.Fatalf("unknown selector error = %v, want ErrRecallNotFound", err)
	}
	if _, err := store.SelectRecallSource(ctx, sessionID, ineligiblePrefix.ID); !errors.Is(err, ErrRecallNotFound) {
		t.Fatalf("ineligible full ID fell back to another prefix match: %v", err)
	}
	childA, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{ExecutionID: "child-prefix-a", Task: "child A",
		ParentExecutionID: latest.ID, Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	settleRecallTestExecution(t, store, childA, OutcomeInterrupted, "2026-10-10T00:00:03Z")
	childB, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{ExecutionID: "child-prefix-b", Task: "child B",
		ParentExecutionID: latest.ID, Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	settleRecallTestExecution(t, store, childB, OutcomeCancelled, "2026-10-10T00:00:04Z")
	if _, err := store.SelectRecallSource(ctx, sessionID, "child-prefix"); !errors.Is(err, ErrRecallAmbiguous) {
		t.Fatalf("ambiguous selector error = %v, want ErrRecallAmbiguous", err)
	}
	selected, err = store.SelectRecallSource(ctx, sessionID, childA.ID)
	if err != nil || selected.ExecutionID != childA.ID {
		t.Fatalf("explicit child source = %+v, %v", selected, err)
	}
}

func TestSaveRecallOperationReplaysTypedReceiptAndSharesCommandIDSpace(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	receipt := contracts.RecallReceipt{CoreID: "core-a", SessionID: sessionID, OperationID: "op-a",
		Action: "prepare", Selector: "latest", SourceExecutionID: "source-a", Generation: 1, Result: "prepared",
		CompletedAt: "2026-10-11T01:00:00Z"}
	saved, err := store.SaveRecallOperation(ctx, receipt, "signature-a")
	if err != nil || saved.OperationID != receipt.OperationID {
		t.Fatalf("save recall receipt = %+v, %v", saved, err)
	}
	replayed, err := store.SaveRecallOperation(ctx, receipt, "signature-a")
	if err != nil || replayed.CompletedAt != receipt.CompletedAt {
		t.Fatalf("replay recall receipt = %+v, %v", replayed, err)
	}
	if _, err := store.SaveRecallOperation(ctx, receipt, "signature-b"); !errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("changed recall operation signature error = %v, want conflict", err)
	}
	read, signature, err := store.RecallOperation(ctx, receipt.CoreID, receipt.OperationID)
	if err != nil || read != saved || signature != "signature-a" {
		t.Fatalf("read recall operation = %+v, %q, %v", read, signature, err)
	}
	if _, err := store.SaveRuntimeInput(ctx, contracts.InputReceipt{CoreID: "core-a", SessionID: sessionID, RunID: "run-a",
		ExecutionID: "exec-a", InputID: "shared-id", Kind: "steering", Sequence: 1, Status: "accepted"}, "same-signature", contracts.JSON(contracts.User("input"))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveRecallOperation(ctx, contracts.RecallReceipt{CoreID: "core-a", SessionID: sessionID,
		OperationID: "shared-id", Action: "clear", Result: "already_clear"}, "same-signature"); !errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("cross-operation command ID error = %v, want conflict", err)
	}
	if _, _, err := store.RecallOperation(ctx, "core-a", "shared-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("I-09 receipt decoded as recall: %v", err)
	}
}

func TestBeginExecutionCommitsRecallRelationAndReturnsItsRecord(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	source := beginRecallTestExecution(t, store, sessionID, "source-cancelled", "source task")
	settleRecallTestExecution(t, store, source, OutcomeCancelled, "2026-10-11T02:00:00Z")
	target, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "target-recall", Task: "new task", SessionID: sessionID,
		Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{"fixture":true}`),
		Recall: &contracts.RecallReference{CoreID: "core-a", OperationID: "op-a", SourceExecutionID: source.ID, Generation: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if target.RecallRecordID == "" || target.Lifecycle != "active" || target.ID != "target-recall" {
		t.Fatalf("admission return = %+v", target)
	}
	var sourceID, recordID, kind string
	if err := store.db.QueryRowContext(ctx, `SELECT rr.source_execution_id,rr.record_id,r.kind FROM recall_relations rr
		JOIN semantic_records r ON r.record_id=rr.record_id WHERE rr.target_execution_id=?`, target.ID).Scan(&sourceID, &recordID, &kind); err != nil {
		t.Fatal(err)
	}
	if sourceID != source.ID || recordID != target.RecallRecordID || kind != "recall_relation" {
		t.Fatalf("stored recall relation = %s/%s/%s", sourceID, recordID, kind)
	}
	var payload struct {
		SchemaVersion     int    `json:"schemaVersion"`
		SourceExecutionID string `json:"sourceExecutionId"`
		TargetExecutionID string `json:"targetExecutionId"`
		CoreID            string `json:"coreId"`
		OperationID       string `json:"operationId"`
		Generation        uint64 `json:"generation"`
	}
	if err := store.db.QueryRowContext(ctx, "SELECT payload_json FROM semantic_records WHERE record_id=?", recordID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(kind), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != 1 || payload.SourceExecutionID != source.ID || payload.TargetExecutionID != target.ID ||
		payload.CoreID != "core-a" || payload.OperationID != "op-a" || payload.Generation != 4 {
		t.Fatalf("recall relation payload = %+v", payload)
	}
	var ordinal, occurrenceCount int64
	if err := store.db.QueryRowContext(ctx, "SELECT latest_ordinal,occurrence_count FROM executions WHERE execution_id=?", target.ID).Scan(&ordinal, &occurrenceCount); err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 || occurrenceCount != 1 {
		t.Fatalf("target relation position = %d/%d, want 1/1", ordinal, occurrenceCount)
	}
}

func TestSessionContextReadsRequestMetadataAndSeparatesLatestRoot(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	requested := beginRecallTestExecution(t, store, sessionID, "root-with-request", "first")
	payload := contracts.JSON(map[string]any{
		"schemaVersion": 1, "requestOrdinal": 3, "modelStep": 4,
		"modelSelection": contracts.ModelSelection{Provider: "fixture", API: "fixture-api", ModelID: "model/used"},
		"itemCount":      4, "messageCount": 1, "toolContractCount": 1, "instructionCount": 1, "wireBytes": 512,
	})
	request, err := store.AppendLatestRequest(ctx, requested.ID, OccurrenceInput{Kind: "model_request", Payload: payload, Content: []byte(strings.Repeat("x", 512))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	budgetPayload := contracts.JSON(map[string]any{"modelStep": 7, "modelSelection": contracts.ModelSelection{
		Provider: "fixture", API: "fixture-api", ModelID: "model/used"}, "summary": map[string]any{
		"estimatedTokens": 90, "selectedMessageCount": 2, "omittedCurrentMessageCount": 0, "historyStatus": "none"}, "detailAvailable": false})
	budgetRecord, err := store.AppendLatestBudget(ctx, requested.ID, budgetPayload, json.RawMessage(`{"step":7,"sources":[{"kind":"current","messageOrdinal":0}]}`))
	if err != nil {
		t.Fatal(err)
	}
	settleRecallTestExecution(t, store, requested, OutcomeCancelled, "2026-10-11T03:00:00Z")
	latestRoot := beginRecallTestExecution(t, store, sessionID, "latest-root-no-request", "second")
	child, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{ExecutionID: "child-with-request", Task: "child",
		ParentExecutionID: latestRoot.ID, Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendLatestRequest(ctx, child.ID, OccurrenceInput{Kind: "model_request", Payload: payload, Content: []byte(strings.Repeat("child", 100))}, nil); err != nil {
		t.Fatal(err)
	}
	childBudget := contracts.JSON(map[string]any{"modelStep": 8, "modelSelection": contracts.ModelSelection{
		Provider: "fixture", API: "fixture-api", ModelID: "model/child"}, "summary": map[string]any{
		"estimatedTokens": 180, "selectedMessageCount": 1, "omittedCurrentMessageCount": 3, "historyStatus": "omitted"}, "detailAvailable": false})
	if _, err := store.AppendLatestBudget(ctx, child.ID, childBudget, json.RawMessage(`{"step":8,"sources":[{"kind":"current","messageOrdinal":1}]}`)); err != nil {
		t.Fatal(err)
	}

	result, err := store.SessionContext(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Canonical.TurnCount != 0 || result.Canonical.MessageCount != 0 || result.Canonical.AgentName != "default" {
		t.Fatalf("canonical context = %+v", result.Canonical)
	}
	if result.LatestRootExecution == nil || result.LatestRootExecution.ExecutionID != latestRoot.ID {
		t.Fatalf("latest root = %+v", result.LatestRootExecution)
	}
	if result.LatestRequest == nil || result.LatestRequest.ExecutionID != requested.ID || result.LatestRequest.RecordOrdinal != request.Ordinal ||
		result.LatestRequest.RequestOrdinal != 3 || result.LatestRequest.ModelStep != 4 || result.LatestRequest.ItemCount != 4 ||
		result.LatestRequest.MessageCount != 1 || result.LatestRequest.ToolContractCount != 1 || result.LatestRequest.InstructionCount != 1 ||
		result.LatestRequest.WireBytes != 512 || result.LatestRequest.Selection.ModelID != "model/used" {
		t.Fatalf("latest parent request = %+v", result.LatestRequest)
	}
	var selectedFact struct {
		Step int `json:"step"`
	}
	if result.LatestBudget == nil || result.LatestBudget.ExecutionID != requested.ID || result.LatestBudget.RecordOrdinal != budgetRecord.Ordinal ||
		result.LatestBudget.ModelStep != 7 || !result.LatestBudget.DetailAvailable || result.LatestBudget.Summary.EstimatedTokens != 90 ||
		json.Unmarshal(result.LatestBudget.Fact, &selectedFact) != nil || selectedFact.Step != 7 {
		t.Fatalf("latest parent budget = %+v, fact=%+v", result.LatestBudget, selectedFact)
	}
}

func TestRecallEvidenceKeepsPublicMessagesAndSemanticFactsWithoutBlobExpansion(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	source := beginRecallTestExecution(t, store, sessionID, "evidence-source", "source task")
	assistant, err := contracts.EncodeJSON(contracts.Assistant("public answer", &contracts.ProviderState{
		Provider: "fixture", ReplayItems: []json.RawMessage{json.RawMessage(`{"opaque":"private-state"}`)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: source.ID, ExecutionID: source.ID,
		Sequence: 1, Message: assistant, Facts: []contracts.ExecutionFact{{Kind: "assistant_message", Phase: "completed", Role: "assistant",
			Text: "public answer", Request: contracts.RequestIdentity{ExecutionID: source.ID, Lane: "parent", RequestOrdinal: 1, ModelStep: 1},
			Conversation: contracts.ConversationMetadata{EntityKind: "assistant", LocalKey: "parent/1/answer"}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, source.ID, OccurrenceInput{Kind: "model_result", Payload: json.RawMessage(
		`{"kind":"model_result","result":{"text":"public answer","providerState":{"opaque":"private-result-state"}}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, source.ID, OccurrenceInput{Kind: "tool_call", Payload: json.RawMessage(
		`{"callId":"call-1","name":"read","arguments":{"path":"a.txt"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, source.ID, OccurrenceInput{Kind: "tool_result", Payload: json.RawMessage(
		`{"callId":"call-1","name":"read","outcome":"success","result":{"text":"tool output"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, source.ID, OccurrenceInput{Kind: "effect_observation", Payload: json.RawMessage(
		`{"callId":"host-1","kind":"host","name":"command","phase":"completed","result":{"stdout":"command output"}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendLatestRequest(ctx, source.ID, OccurrenceInput{Kind: "model_request", Payload: contracts.JSON(map[string]any{
		"schemaVersion": 1, "requestOrdinal": 1, "modelStep": 1,
		"modelSelection": contracts.ModelSelection{Provider: "fixture", ModelID: "fixture/model"},
		"itemCount":      2, "messageCount": 1, "toolContractCount": 0, "instructionCount": 0, "wireBytes": len("private wire"),
	}), Content: []byte("private wire")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, source.ID, OccurrenceInput{Kind: "resource_revision", Payload: json.RawMessage(`{"kind":"resource"}`), Content: []byte("configuration bytes")}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAssistantProgress(ctx, AssistantProgressInput{RootExecutionID: source.ID, ExecutionID: source.ID,
		Sequence: 1, Request: contracts.RequestIdentity{ExecutionID: source.ID, Lane: "parent", RequestOrdinal: 1, ModelStep: 1},
		Text: "partial answer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EndExecutionWithConversation(ctx, source.ID, OutcomeCancelled,
		json.RawMessage(`{"outcome":"cancelled"}`), "2026-10-11T04:00:00Z", source.ID, 2); err != nil {
		t.Fatal(err)
	}
	evidence, err := store.RecallEvidence(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Messages) != 1 || strings.Contains(string(evidence.Messages[0].JSON), "private-state") {
		t.Fatalf("public evidence messages = %+v", evidence.Messages)
	}
	var modelResult string
	var hasToolCall, hasToolResult, hasHostResult, hasPartial bool
	for _, record := range evidence.Records {
		if record.Content != nil {
			t.Fatalf("RecallEvidence expanded content for %s", record.Kind)
		}
		switch record.Kind {
		case "model_result":
			modelResult = string(record.Payload)
		case "tool_call":
			hasToolCall = true
		case "tool_result":
			var observation struct {
				Result struct {
					Text string `json:"text"`
				} `json:"result"`
			}
			if err := json.Unmarshal(record.Payload, &observation); err != nil || observation.Result.Text != "tool output" {
				t.Fatalf("RecallEvidence omitted the expanded tool result: %s, %v", record.Payload, err)
			}
			hasToolResult = true
		case "effect_observation":
			var observation struct {
				Result struct {
					Stdout string `json:"stdout"`
				} `json:"result"`
			}
			if err := json.Unmarshal(record.Payload, &observation); err != nil || observation.Result.Stdout != "command output" {
				t.Fatalf("RecallEvidence omitted the expanded host result: %s, %v", record.Payload, err)
			}
			hasHostResult = true
		case "assistant_message":
			if strings.Contains(string(record.Payload), "private-state") {
				t.Fatalf("assistant semantic mirror retained provider state: %s", record.Payload)
			}
			hasPartial = hasPartial || strings.Contains(string(record.Payload), "partial answer")
		case "resource_revision", "model_request":
			t.Fatalf("recall evidence included private or captured record kind %q", record.Kind)
		}
	}
	if strings.Contains(modelResult, "private-result-state") || !hasToolCall || !hasToolResult || !hasHostResult || !hasPartial {
		t.Fatalf("public model/tool/partial evidence missing or private: result=%s call=%t tool=%t host=%t partial=%t", modelResult, hasToolCall, hasToolResult, hasHostResult, hasPartial)
	}
}

func TestRecallSemanticMirrorAndConversationWalkUseShortMarker(t *testing.T) {
	ctx := context.Background()
	store, sessionID := recallTestStore(t)
	source := beginRecallTestExecution(t, store, sessionID, "mirror-source", "source task")
	settleRecallTestExecution(t, store, source, OutcomeCancelled, "2026-10-11T05:00:00Z")
	target, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "mirror-target", Task: "next task", SessionID: sessionID,
		Model: json.RawMessage(`{"provider":"fixture"}`), Build: json.RawMessage(`{}`),
		Recall: &contracts.RecallReference{CoreID: "core-a", OperationID: "op-a", SourceExecutionID: source.ID, Generation: 2}})
	if err != nil {
		t.Fatal(err)
	}
	marker := &contracts.RecallMarker{SourceExecutionID: source.ID, RelationRecordID: target.RecallRecordID, OperationID: "op-a", Generation: 2}
	largeBody := strings.Repeat("reference body ", 2048)
	body, err := contracts.EncodeJSON(contracts.User(largeBody))
	if err != nil {
		t.Fatal(err)
	}
	shortDisplay := "過去の停止実行を参照: " + source.ID
	_, facts, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: target.ID, ExecutionID: target.ID,
		Sequence: 2, Message: body, Facts: []contracts.ExecutionFact{{Kind: "user_message", Role: "user", Text: shortDisplay,
			Recall: marker, Conversation: contracts.ConversationMetadata{EntityKind: "recall", LocalKey: "recall/" + target.RecallRecordID}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Recall == nil || facts[0].Locator.RecordID == "" {
		t.Fatalf("stored recall facts = %+v", facts)
	}
	var payload string
	if err := store.db.QueryRowContext(ctx, "SELECT payload_json FROM semantic_records WHERE record_id=?", facts[0].Locator.RecordID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload, largeBody) || !strings.Contains(payload, shortDisplay) || !strings.Contains(payload, `"messageContentDigest"`) ||
		!strings.Contains(payload, `"relationRecordId":"`+target.RecallRecordID+`"`) {
		t.Fatalf("recall semantic mirror is not compact/typed: %s", payload)
	}
	requestPayload := contracts.JSON(map[string]any{
		"schemaVersion": 1, "requestOrdinal": 1, "modelStep": 1,
		"modelSelection": contracts.ModelSelection{Provider: "fixture", ModelID: "fixture/model"},
		"itemCount":      2, "messageCount": 1, "toolContractCount": 0, "instructionCount": 0,
		"wireBytes": len("wire"), "recallRelationRecordId": target.RecallRecordID,
	})
	request, err := store.AppendLatestRequest(ctx, target.ID, OccurrenceInput{Kind: "model_request", Payload: requestPayload, Content: []byte("wire")}, contracts.JSON([]map[string]any{{
		"kind": "recall", "sourceExecutionId": source.ID, "targetExecutionId": target.ID, "recordId": target.RecallRecordID,
	}}))
	if err != nil {
		t.Fatal(err)
	}
	settleRecallTestExecution(t, store, target, OutcomeFailed, "2026-10-11T05:00:01Z")
	contextView, err := store.SessionContext(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	admission := contextView.LatestRecallAdmission
	if admission == nil || admission.SourceExecutionID != source.ID || admission.TargetExecutionID != target.ID ||
		admission.RecordID != target.RecallRecordID || admission.ReferenceMessageOrdinal == nil || *admission.ReferenceMessageOrdinal != 0 ||
		admission.RequestRecordOrdinal == nil || *admission.RequestRecordOrdinal != request.Ordinal {
		t.Fatalf("latest recall admission metadata = %+v", admission)
	}
	cut, err := store.CaptureConversationCut(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	var walked *contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		if fact.Conversation.EntityKind == "recall" {
			copy := fact
			walked = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if walked == nil || walked.Text != shortDisplay || walked.Recall == nil || walked.Recall.RelationRecordID != target.RecallRecordID {
		t.Fatalf("walked recall fact = %+v", walked)
	}
	var storedDigest string
	if err := store.db.QueryRowContext(ctx, "SELECT content_digest FROM messages WHERE execution_id=? AND message_ordinal=0", target.ID).Scan(&storedDigest); err != nil {
		t.Fatal(err)
	}
	storedBody, err := store.Content(ctx, storedDigest)
	if err != nil || !strings.Contains(string(storedBody), largeBody) {
		t.Fatal(err)
	}
}
