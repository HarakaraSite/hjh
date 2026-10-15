package history

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"hjh/internal/contracts"
)

func TestAssistantMirrorsReferenceMessageAndRestorePublicDetailsAfterReopen(t *testing.T) {
	chatState := &contracts.ProviderState{Provider: "chat", Model: "model",
		Reasoning: &contracts.Reasoning{Field: "reasoning_content", Text: "考える <&>"}}
	responsesState := &contracts.ProviderState{Provider: "responses", Model: "model",
		ReplayItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"summary"}]}`)}}
	calls := []contracts.ToolCall{{CallID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"file"}`)}}
	for _, test := range []struct {
		name    string
		message contracts.Message
		facts   bool
	}{
		{"answer and thinking", contracts.Assistant("answer <&>", chatState), true},
		{"answer without provider state", contracts.Assistant("public answer", nil), true},
		{"calls with text and thinking", contracts.AssistantCalls(calls, "call explanation", responsesState), true},
		{"calls without visible text", contracts.AssistantCalls(calls, "", responsesState), true},
		{"answer without Facts", contracts.Assistant("direct answer", chatState), false},
		{"calls without Facts", contracts.AssistantCalls(calls, "", responsesState), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, execution := openConversationFixture(t)
			var seq int
			var name, dbPath string
			if err := store.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &dbPath); err != nil {
				t.Fatal(err)
			}
			message := contracts.JSON(test.message)
			request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", ModelStep: 1,
				RequestOrdinal: 1, PhysicalRequestID: "physical-1"}
			var facts []contracts.ExecutionFact
			if test.facts {
				facts = []contracts.ExecutionFact{{Kind: "assistant_message", Role: "assistant", Phase: "incomplete",
					Text: test.message.VisibleText(), Request: request,
					Thinking: []contracts.ThinkingItem{{Key: "reasoning/0", Text: "mirror thought", Kind: "reasoning"},
						{Key: "summary/0", Text: "mirror summary", Kind: "summary"}},
					Conversation: contracts.ConversationMetadata{EntityKind: "assistant", LocalKey: request.AssistantKey("answer"), FirstSequence: 1}}}
			}
			ordinal, saved, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: execution.ID,
				ExecutionID: execution.ID, Sequence: 2, Message: message, Facts: facts, ObservedAt: "2026-10-16T00:00:00Z"})
			if err != nil {
				t.Fatal(err)
			}
			wantPayloads := map[string]json.RawMessage{}
			if test.facts {
				for _, fact := range saved {
					payload, err := messageFactPayload(message, fact, ordinal, "2026-10-16T00:00:00Z")
					if err != nil {
						t.Fatal(err)
					}
					wantPayloads[fact.Locator.RecordID] = payload
				}
			}
			rows, err := store.db.QueryContext(ctx, `SELECT r.record_id,r.payload_json,r.content_digest,m.content_digest
				FROM semantic_records r JOIN messages m ON m.execution_id=r.execution_id AND m.message_ordinal=?
				WHERE r.execution_id=? AND r.kind='assistant_message'`, ordinal, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for rows.Next() {
				var id, payload, digest, messageDigest string
				if err := rows.Scan(&id, &payload, &digest, &messageDigest); err != nil {
					t.Fatal(err)
				}
				fields := assistantMirrorFields(t, []byte(payload))
				if fields["content"] != nil || fields["providerState"] != nil || fields["thinkingRecordId"] != nil {
					t.Fatalf("raw mirror retained Message fields or added S-2 reference: %s", payload)
				}
				if digest != messageDigest || digest != contentDigest(message) {
					t.Fatalf("mirror FK = %q, Message FK = %q", digest, messageDigest)
				}
				if !test.facts {
					wantPayloads[id] = message
				}
				ids = append(ids, id)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			_ = rows.Close()
			if len(ids) != len(wantPayloads) || len(ids) == 0 {
				t.Fatalf("mirror count = %d, want %d", len(ids), len(wantPayloads))
			}
			if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted, json.RawMessage(`{"outcome":"completed"}`), ""); err != nil {
				t.Fatal(err)
			}
			if err := store.AdoptCanonical(ctx, execution.ID, ""); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reader, err := OpenReadOnly(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			for _, read := range []func() ([]Message, error){
				func() ([]Message, error) { return reader.Messages(ctx, execution.ID, 0, -1) },
				func() ([]Message, error) { return reader.Conversation(ctx, execution.SessionID, 1, 1) },
			} {
				messages, err := read()
				if err != nil || len(messages) != 1 {
					t.Fatalf("Message read = %#v, %v", messages, err)
				}
				assertAssistantMirrorJSON(t, messages[0].JSON, message)
			}
			records, err := reader.Records(ctx, execution.ID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			readMirrors := 0
			for _, record := range records {
				if want, ok := wantPayloads[record.ID]; ok {
					readMirrors++
					assertAssistantMirrorJSON(t, record.Payload, want)
					if !bytes.Equal(record.Content, message) {
						t.Fatalf("mirror %s Content changed: %s", record.ID, record.Content)
					}
					// Service.Record uses exactly this bounded read, not an execution-wide hydration.
					single, err := reader.Records(ctx, execution.ID, record.Ordinal, record.Ordinal)
					if err != nil || len(single) != 1 {
						t.Fatalf("single record = %#v, %v", single, err)
					}
					assertAssistantMirrorJSON(t, single[0].Payload, want)
				}
			}
			if readMirrors != len(ids) {
				t.Fatalf("Records returned %d mirrors, want %d", readMirrors, len(ids))
			}
			evidence, err := reader.RecallEvidence(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			recalledMirrors := 0
			for _, record := range evidence.Records {
				if want, ok := wantPayloads[record.ID]; ok {
					recalledMirrors++
					publicWant, err := stripProviderState(want)
					if err != nil {
						t.Fatal(err)
					}
					assertAssistantMirrorJSON(t, record.Payload, publicWant)
					if record.Content != nil {
						t.Fatal("recall mirror exposed Message envelope as Content")
					}
				}
			}
			if recalledMirrors != len(ids) {
				t.Fatalf("RecallEvidence returned %d mirrors, want %d", recalledMirrors, len(ids))
			}
			cut, err := reader.CaptureConversationCut(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			seen := 0
			if err := reader.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
				if _, ok := wantPayloads[fact.Locator.RecordID]; !ok {
					return nil
				}
				seen++
				if test.facts {
					for _, want := range saved {
						if want.Locator.RecordID == fact.Locator.RecordID && (fact.Text != want.Text || fact.Conversation != want.Conversation ||
							fact.Request != want.Request || fact.Phase != want.Phase || fact.ThinkingKind != want.ThinkingKind) {
							t.Fatalf("walk changed mirror: %#v, want %#v", fact, want)
						}
					}
				} else if fact.Text != test.message.VisibleText() {
					t.Fatalf("Facts-free walk text = %q", fact.Text)
				}
				if len(test.message.Content) > 0 && test.message.Content[0] == '[' && fact.Name != "read_file" {
					t.Fatalf("walk lost content-derived name: %#v", fact)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if seen != len(ids) {
				t.Fatalf("walk visited %d mirrors, want %d", seen, len(ids))
			}
		})
	}
}

func TestIndependentAssistantMessageKeepsInlineProviderState(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	message := contracts.JSON(contracts.Assistant("independent answer", &contracts.ProviderState{Provider: "chat",
		Reasoning: &contracts.Reasoning{Field: "reasoning_content", Text: "independent reasoning"}}))
	if _, err := store.AppendMessage(ctx, execution.ID, message); err != nil {
		t.Fatal(err)
	}
	messages, err := store.Messages(ctx, execution.ID, 0, -1)
	if err != nil || len(messages) != 1 {
		t.Fatalf("Messages = %#v, %v", messages, err)
	}
	assertAssistantMirrorJSON(t, messages[0].JSON, message)
	var mirrors int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM semantic_records WHERE execution_id=? AND kind='assistant_message'", execution.ID).Scan(&mirrors); err != nil {
		t.Fatal(err)
	}
	if mirrors != 0 {
		t.Fatalf("independent AppendMessage created %d mirrors", mirrors)
	}
}

func assistantMirrorFields(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func assertAssistantMirrorJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON changed:\ngot  %s\nwant %s", got, want)
	}
}
