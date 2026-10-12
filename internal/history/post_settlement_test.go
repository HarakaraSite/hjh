package history

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"hjh/internal/contracts"
)

func TestAppendPostSettlementObservationPreservesAdoptedExecution(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, BeginExecutionInput{
		Task: "run the lifecycle hook", SessionID: session.ID, Model: model,
		Build: json.RawMessage(`{"goVersion":"go1.27.2"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	messageJSON := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":{"kind":"text","text":"run the lifecycle hook"}}`),
		json.RawMessage(`{"role":"assistant","content":{"kind":"text","text":"Done."}}`),
	}
	for _, message := range messageJSON {
		if _, err := store.AppendMessage(ctx, execution.ID, message); err != nil {
			t.Fatal(err)
		}
	}
	parentCall, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "tool_call", Payload: json.RawMessage(`{"callId":"turn-call","name":"run_turn"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminalJSON := json.RawMessage(`{"outcome":"completed","finalText":"Done.","kept":"terminal"}`)
	if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted, terminalJSON, "2026-10-12T01:02:03.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-12T01:02:04.000Z"); err != nil {
		t.Fatal(err)
	}

	settled, err := store.Execution(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeExecution := executionSnapshot(t, ctx, store, execution.ID)
	beforeSession, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeConversation, err := store.Conversation(ctx, session.ID, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	beforeMessages, err := store.Messages(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	beforeRecords, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	beforeLatest, beforeCount, beforeUnresolved := occurrenceCounters(t, ctx, store, execution.ID)

	metadata := &contracts.ConversationMetadata{
		RootExecutionID: execution.ID, Sequence: 9, EntityKind: "hook", LocalKey: "afterTurn",
		FirstSequence: 9,
	}
	inputs := make([]OccurrenceInput, 0, 5)
	stored := make([]Record, 0, cap(inputs))
	appendPostSettlement := func(input OccurrenceInput) Record {
		t.Helper()
		inputs = append(inputs, input)
		record, err := store.AppendPostSettlementObservation(ctx, execution.ID, input)
		if err != nil {
			t.Fatalf("append settled observation: %v", err)
		}
		stored = append(stored, record)
		return record
	}
	hookStart := appendPostSettlement(OccurrenceInput{
		Kind: "effect_observation", Payload: json.RawMessage(`{"callId":"hook-1","hook":"afterTurn","phase":"start","input":{"turn":1}}`),
		Content: []byte("hook input bytes"), ObservedAt: "2026-10-12T01:02:05.000Z", Conversation: metadata,
		Relations: []Relation{{Relation: "parent_call", TargetRecordID: parentCall.ID, Mandatory: true}},
	})
	appendPostSettlement(OccurrenceInput{
		Kind: "effect_observation", Payload: json.RawMessage(`{"callId":"hook-1","hook":"afterTurn","phase":"end","result":"saved"}`),
		Content: []byte("hook result bytes"), ObservedAt: "2026-10-12T01:02:06.000Z",
		Relations: []Relation{
			{Relation: "parent_call", TargetRecordID: parentCall.ID, Mandatory: true},
			{Relation: "call_start", TargetRecordID: hookStart.ID, Mandatory: true},
		},
	})
	operationStart := appendPostSettlement(OccurrenceInput{
		Kind: "tool_call", Payload: json.RawMessage(`{"callId":"hook-tool-1","operation":"read_file","phase":"start"}`),
		Content: []byte("nested operation input"), ObservedAt: "2026-10-12T01:02:07.000Z",
		Relations: []Relation{{Relation: "parent_call", TargetRecordID: hookStart.ID, Mandatory: true}},
	})
	appendPostSettlement(OccurrenceInput{
		Kind: "tool_result", Payload: json.RawMessage(`{"callId":"hook-tool-1","operation":"read_file","phase":"end","text":"contents"}`),
		Content: []byte("nested operation output"), ObservedAt: "2026-10-12T01:02:08.000Z",
		Relations: []Relation{{Relation: "call_start", TargetRecordID: operationStart.ID, Mandatory: true}},
	})
	appendPostSettlement(OccurrenceInput{
		Kind: "effect_observation", Payload: json.RawMessage(`{"notice":"afterTurn complete","level":"info"}`),
		ObservedAt: "2026-10-12T01:02:09.000Z",
		Relations:  []Relation{{Relation: "parent_call", TargetRecordID: hookStart.ID, Mandatory: true}},
	})
	hookStartID := hookStart.ID

	afterRecords, err := store.Records(ctx, execution.ID, beforeLatest+1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRecords) != len(inputs) {
		t.Fatalf("post-settlement readback has %d records, want %d", len(afterRecords), len(inputs))
	}
	wantTargets := [][]Relation{
		{{Relation: "parent_call", TargetRecordID: parentCall.ID, Mandatory: true, Resolved: true}, {Relation: "settlement", TargetRecordID: settled.TerminalRecordID, Mandatory: true, Resolved: true}},
		{{Relation: "parent_call", TargetRecordID: parentCall.ID, Mandatory: true, Resolved: true}, {Relation: "call_start", TargetRecordID: hookStartID, Mandatory: true, Resolved: true}, {Relation: "settlement", TargetRecordID: settled.TerminalRecordID, Mandatory: true, Resolved: true}},
		{{Relation: "parent_call", TargetRecordID: hookStartID, Mandatory: true, Resolved: true}, {Relation: "settlement", TargetRecordID: settled.TerminalRecordID, Mandatory: true, Resolved: true}},
		{{Relation: "call_start", TargetRecordID: operationStart.ID, Mandatory: true, Resolved: true}, {Relation: "settlement", TargetRecordID: settled.TerminalRecordID, Mandatory: true, Resolved: true}},
		{{Relation: "parent_call", TargetRecordID: hookStartID, Mandatory: true, Resolved: true}, {Relation: "settlement", TargetRecordID: settled.TerminalRecordID, Mandatory: true, Resolved: true}},
	}
	for index, record := range afterRecords {
		if record.ID != stored[index].ID || record.Ordinal != beforeLatest+int64(index)+1 ||
			record.Kind != inputs[index].Kind || record.ObservedAt != inputs[index].ObservedAt {
			t.Fatalf("post-settlement record %d identity = %#v, write result %#v", index, record, stored[index])
		}
		if !reflect.DeepEqual(record.Relations, wantTargets[index]) {
			t.Fatalf("post-settlement record %d relations = %#v, want %#v", index, record.Relations, wantTargets[index])
		}
		if !bytes.Equal(record.Content, inputs[index].Content) {
			t.Fatalf("post-settlement record %d content = %q, want %q", index, record.Content, inputs[index].Content)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatalf("decode post-settlement payload %d: %v", index, err)
		}
		if string(payload["postSettlement"]) != "true" || string(payload["terminalRecordId"]) != mustJSONString(t, settled.TerminalRecordID) {
			t.Fatalf("post-settlement payload %d markers = %s", index, record.Payload)
		}
		var expectedPayload map[string]json.RawMessage
		if err := json.Unmarshal(inputs[index].Payload, &expectedPayload); err != nil {
			t.Fatalf("decode input payload %d: %v", index, err)
		}
		expectedPayload["postSettlement"] = json.RawMessage(`true`)
		expectedPayload["terminalRecordId"] = json.RawMessage(mustJSONString(t, settled.TerminalRecordID))
		if inputs[index].Conversation != nil {
			expectedPayload["conversation"] = contracts.JSON(inputs[index].Conversation)
		}
		wantPayload, err := contracts.EncodeCanonicalJSON(contracts.JSON(expectedPayload))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(record.Payload, wantPayload) {
			t.Fatalf("post-settlement payload %d = %s, want %s", index, record.Payload, wantPayload)
		}
		if inputs[index].Conversation != nil && (record.Conversation == nil || *record.Conversation != *inputs[index].Conversation) {
			t.Fatalf("conversation metadata readback = %#v, want %#v", record.Conversation, inputs[index].Conversation)
		}
	}
	if afterRecords[3].Relations[0].TargetRecordID != operationStart.ID || afterRecords[4].Relations[0].TargetRecordID != hookStartID {
		t.Fatalf("nested operation or notice relation did not resolve: %#v %#v", afterRecords[3].Relations, afterRecords[4].Relations)
	}
	if afterRecords[1].Relations[1].TargetRecordID != hookStartID {
		t.Fatalf("hook end does not point to hook start: %#v", afterRecords[1].Relations)
	}
	fullAfterRecords, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullAfterRecords) != len(beforeRecords)+len(inputs) ||
		!reflect.DeepEqual(fullAfterRecords[:len(beforeRecords)], beforeRecords) {
		t.Fatal("post-settlement append changed earlier semantic records")
	}

	afterExecution := executionSnapshot(t, ctx, store, execution.ID)
	if afterExecution.lifecycle != beforeExecution.lifecycle || afterExecution.outcome != beforeExecution.outcome ||
		afterExecution.adoption != beforeExecution.adoption || afterExecution.settledAt != beforeExecution.settledAt ||
		afterExecution.terminalRecordID != beforeExecution.terminalRecordID ||
		afterExecution.runtimeOutcome != beforeExecution.runtimeOutcome ||
		afterExecution.latestOrdinal != beforeLatest+int64(len(inputs)) ||
		afterExecution.occurrenceCount != beforeCount+int64(len(inputs)) ||
		afterExecution.unresolvedCount != beforeUnresolved {
		t.Fatalf("execution settlement changed unexpectedly: before %#v, after %#v", beforeExecution, afterExecution)
	}
	afterSession, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterSession, beforeSession) {
		t.Fatalf("post-settlement append changed Session state: before %#v, after %#v", beforeSession, afterSession)
	}
	afterConversation, err := store.Conversation(ctx, session.ID, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	afterMessages, err := store.Messages(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterConversation, beforeConversation) || !reflect.DeepEqual(afterMessages, beforeMessages) {
		t.Fatal("post-settlement append changed canonical conversation or execution messages")
	}
	storedExecution, err := store.Execution(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedExecution.TerminalRecordID != settled.TerminalRecordID || storedExecution.Outcome != string(OutcomeCompleted) || storedExecution.Adoption != "canonical" {
		t.Fatalf("execution readback changed settlement: %#v", storedExecution)
	}
	terminalRecords, err := store.Records(ctx, execution.ID, settledLatestOrdinal(t, ctx, store, settled.TerminalRecordID), -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(terminalRecords) == 0 || terminalRecords[0].ID != settled.TerminalRecordID || terminalRecords[0].Kind != "host_decision" {
		t.Fatalf("saved terminal record changed or disappeared: %#v", terminalRecords)
	}
	var beforeTerminal Record
	for _, record := range beforeRecords {
		if record.ID == settled.TerminalRecordID {
			beforeTerminal = record
			break
		}
	}
	if beforeTerminal.ID == "" || !reflect.DeepEqual(terminalRecords[0], beforeTerminal) {
		t.Fatalf("saved terminal record changed: before %#v, after %#v", beforeTerminal, terminalRecords[0])
	}
	if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "effect_observation", Payload: json.RawMessage(`{"phase":"late"}`),
	}); !errors.Is(err, ErrExecutionClosed) {
		t.Fatalf("generic AppendOccurrence on settled execution error = %v, want %v", err, ErrExecutionClosed)
	}
}

func TestAppendPostSettlementObservationForSettledChild(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := json.RawMessage(`{"provider":"openrouter-chat","api":"openrouter-chat-completions","authProfile":"openrouter-api-key","modelId":"model/example","effort":"auto"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: "/tmp/hjh-work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.BeginExecution(ctx, BeginExecutionInput{
		Task: "parent", SessionID: session.ID, Model: model, Build: json.RawMessage(`{"goVersion":"go1.27.2"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	parentCall, err := store.AppendOccurrence(ctx, parent.ID, OccurrenceInput{
		Kind: "tool_call", Payload: json.RawMessage(`{"callId":"spawn-1","name":"run_agent"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{
		Task: "child", ParentExecutionID: parent.ID, SpawnCallID: "spawn-1", AgentName: "default",
		Model: model, Build: json.RawMessage(`{"goVersion":"go1.27.2"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EndExecution(ctx, child.ID, OutcomeFailed,
		json.RawMessage(`{"outcome":"failed","error":"child failure"}`), "2026-10-12T02:03:04.000Z"); err != nil {
		t.Fatal(err)
	}
	settledChild, err := store.Execution(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	parentSession, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	appended, err := store.AppendPostSettlementObservation(ctx, child.ID, OccurrenceInput{
		Kind: "effect_observation", Payload: json.RawMessage(`{"hook":"runtimeStop","phase":"end"}`),
		Relations: []Relation{{Relation: "parent_call", TargetRecordID: parentCall.ID, Mandatory: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if appended.ExecutionID != child.ID || appended.Relations[0].TargetRecordID != parentCall.ID || !appended.Relations[0].Resolved {
		t.Fatalf("settled child observation = %#v", appended)
	}
	if len(appended.Relations) != 2 || appended.Relations[1].Relation != "settlement" ||
		appended.Relations[1].TargetRecordID != settledChild.TerminalRecordID || !appended.Relations[1].Resolved {
		t.Fatalf("settled child terminal relation = %#v", appended.Relations)
	}
	afterChild, err := store.Execution(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterChild.Outcome != string(OutcomeFailed) || afterChild.Adoption != "non_canonical" || afterChild.TerminalRecordID != settledChild.TerminalRecordID {
		t.Fatalf("settled child result changed: %#v", afterChild)
	}
	afterSession, err := store.Session(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterSession.StateRevision != parentSession.StateRevision || afterSession.MessageCount != parentSession.MessageCount {
		t.Fatalf("settled child observation changed parent Session: before %#v, after %#v", parentSession, afterSession)
	}
}

type storedExecutionSnapshot struct {
	lifecycle        string
	outcome          string
	adoption         string
	settledAt        string
	latestOrdinal    int64
	occurrenceCount  int64
	unresolvedCount  int64
	terminalRecordID string
	runtimeOutcome   string
}

func executionSnapshot(t *testing.T, ctx context.Context, store *Store, executionID string) storedExecutionSnapshot {
	t.Helper()
	var snapshot storedExecutionSnapshot
	if err := store.db.QueryRowContext(ctx, `SELECT lifecycle,outcome,adoption,settled_at,latest_ordinal,
		occurrence_count,unresolved_mandatory_count,COALESCE(terminal_record_id,''),runtime_outcome_json
		FROM executions WHERE execution_id=?`, executionID).Scan(
		&snapshot.lifecycle, &snapshot.outcome, &snapshot.adoption, &snapshot.settledAt,
		&snapshot.latestOrdinal, &snapshot.occurrenceCount, &snapshot.unresolvedCount,
		&snapshot.terminalRecordID, &snapshot.runtimeOutcome,
	); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func occurrenceCounters(t *testing.T, ctx context.Context, store *Store, executionID string) (int64, int64, int64) {
	t.Helper()
	var latest, count, unresolved int64
	if err := store.db.QueryRowContext(ctx, `SELECT latest_ordinal,occurrence_count,unresolved_mandatory_count
		FROM executions WHERE execution_id=?`, executionID).Scan(&latest, &count, &unresolved); err != nil {
		t.Fatal(err)
	}
	return latest, count, unresolved
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func settledLatestOrdinal(t *testing.T, ctx context.Context, store *Store, terminalRecordID string) int64 {
	t.Helper()
	var ordinal int64
	if err := store.db.QueryRowContext(ctx, "SELECT ordinal FROM semantic_records WHERE record_id=?", terminalRecordID).Scan(&ordinal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("terminal record %q not found", terminalRecordID)
		}
		t.Fatal(err)
	}
	return ordinal
}
