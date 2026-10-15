package history

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"hjh/internal/contracts"
)

func TestAssistantProviderStateResultOwnershipAndPublicReadsAfterReopen(t *testing.T) {
	for _, variant := range []string{"chat", "responses-calls", "empty"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			store, execution := openConversationFixture(t)
			var sequence int
			var name, path string
			if err := store.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
				t.Fatal(err)
			}
			state := &contracts.ProviderState{Provider: "chat", Model: "model",
				Reasoning: &contracts.Reasoning{Field: "reasoning_content", Text: "考える <&>"}}
			text := "answer <&>"
			var calls []contracts.ToolCall
			if variant == "responses-calls" {
				text = ""
				calls = []contracts.ToolCall{{CallID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"file"}`)}}
				state = &contracts.ProviderState{Provider: "responses", Model: "model", ReplayItems: []json.RawMessage{
					json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"summary"}]}`),
					json.RawMessage(`{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"file\"}"}`),
				}}
			} else if variant == "empty" {
				text = ""
			}
			request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", ModelStep: 1,
				RequestOrdinal: 1, PhysicalRequestID: "physical-result", RetryAttempt: 1}
			finishReason, phase := "stop", "completed"
			if variant == "chat" {
				finishReason, phase = "length", "incomplete"
			}
			resultObject := contracts.JSON(map[string]any{"text": text, "calls": calls, "finishReason": finishReason, "providerState": state})
			result, err := store.SaveAssistantResult(ctx, AssistantResultInput{RootExecutionID: execution.ID, ExecutionID: execution.ID,
				Sequence: 1, Request: request, Text: text, Phase: phase, Payload: contracts.JSON(map[string]any{"kind": "model_result", "result": resultObject})})
			if err != nil {
				t.Fatal(err)
			}
			if result.Record.ID == "" || result.Record.Content != nil || (text == "" && len(result.Facts) != 0) {
				t.Fatalf("result without display Facts lost its independent record: %#v", result)
			}
			var raw, digest string
			var body []byte
			if err := store.db.QueryRowContext(ctx, `SELECT r.payload_json,r.content_digest,c.content_bytes
				FROM semantic_records r JOIN contents c ON c.content_digest=r.content_digest WHERE r.record_id=?`, result.Record.ID).Scan(&raw, &digest, &body); err != nil {
				t.Fatal(err)
			}
			fields := assistantMirrorFields(t, []byte(raw))
			if fields["result"] != nil || string(fields["resultRef"]) != string(contracts.JSON(digest)) || digest != contentDigest(body) {
				t.Fatalf("result is not owned once by its existing FK: %s", raw)
			}
			assertAssistantMirrorJSON(t, body, resultObject)
			beforeMessage, err := store.CaptureConversationCut(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			message := contracts.Assistant(text, state)
			if len(calls) != 0 {
				message = contracts.AssistantCalls(calls, text, state)
			}
			messageJSON := contracts.JSON(message)
			fact := contracts.ExecutionFact{Kind: "assistant_message", Role: "assistant", Text: text, Request: request,
				Conversation: contracts.ConversationMetadata{EntityKind: "assistant", LocalKey: request.AssistantKey("answer")}}
			ordinal, saved, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: execution.ID,
				ExecutionID: execution.ID, Sequence: 2, Message: messageJSON, Facts: []contracts.ExecutionFact{fact},
				ProviderStateRecordID: result.Record.ID, FinalizeRequest: &request, ContentDigest: contentDigest(messageJSON)})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.db.QueryRowContext(ctx, `SELECT c.content_bytes FROM messages m JOIN contents c ON c.content_digest=m.content_digest
				WHERE m.execution_id=? AND m.message_ordinal=?`, execution.ID, ordinal).Scan(&body); err != nil {
				t.Fatal(err)
			}
			fields = assistantMirrorFields(t, body)
			if fields["providerState"] != nil || string(fields["providerStateRecordId"]) != string(contracts.JSON(result.Record.ID)) {
				t.Fatalf("Message did not reference its prior result: %s", body)
			}
			assertAssistantMirrorJSON(t, fields["content"], assistantMirrorFields(t, messageJSON)["content"])
			reports, cursor, err := store.AssistantReports(ctx, execution.ID, -1)
			if err != nil || cursor != ordinal || (text == "" && len(reports) != 0) || (text != "" && (len(reports) != 1 || reports[0].Text != text)) {
				t.Fatalf("reports/cursor after compaction = %#v / %d, %v", reports, cursor, err)
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
			reader, err := OpenReadOnly(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			messages, err := reader.Messages(ctx, execution.ID, ordinal, ordinal)
			if err != nil || len(messages) != 1 {
				t.Fatalf("Messages = %#v, %v", messages, err)
			}
			assertAssistantMirrorJSON(t, messages[0].JSON, messageJSON)
			conversation, err := reader.Conversation(ctx, execution.SessionID, 0, -1)
			if err != nil || len(conversation) != 1 {
				t.Fatalf("Conversation = %#v, %v", conversation, err)
			}
			assertAssistantMirrorJSON(t, conversation[0].JSON, messageJSON)
			for _, source := range []Record{result.Record, {Ordinal: saved[0].Locator.Ordinal}} {
				records, err := reader.Records(ctx, execution.ID, source.Ordinal, source.Ordinal)
				if err != nil || len(records) != 1 {
					t.Fatalf("single record = %#v, %v", records, err)
				}
				record := records[0]
				fields := assistantMirrorFields(t, record.Payload)
				if record.Kind == "model_result" {
					if record.Content != nil || fields["resultRef"] != nil {
						t.Fatalf("public result leaked storage body: %#v", record)
					}
					assertAssistantMirrorJSON(t, record.Payload, result.Record.Payload)
				} else {
					if fields["providerStateRecordId"] != nil {
						t.Fatalf("public mirror leaked internal state locator: %s", record.Payload)
					}
					assertAssistantMirrorJSON(t, fields["providerState"], contracts.JSON(state))
					assertAssistantMirrorJSON(t, record.Content, messageJSON)
				}
			}
			var resultAnswer, resultPhase string
			if err := reader.WalkConversationCut(ctx, beforeMessage, func(f contracts.ExecutionFact) error {
				if f.Locator.RecordID == result.Record.ID {
					resultAnswer, resultPhase = f.Text, f.Phase
				}
				return nil
			}); err != nil || resultAnswer != text || (text != "" && resultPhase != phase) {
				t.Fatalf("result-only cut = %q / %q, %v", resultAnswer, resultPhase, err)
			}
			evidence, err := reader.RecallEvidence(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range evidence.Records {
				if record.Content != nil {
					t.Fatal("recall exposed content blob")
				}
				fields := assistantMirrorFields(t, record.Payload)
				if fields["providerState"] != nil || fields["providerStateRecordId"] != nil || fields["resultRef"] != nil {
					t.Fatalf("recall leaked storage reference or provider state: %s", record.Payload)
				}
				if record.ID == result.Record.ID {
					publicResult := assistantMirrorFields(t, fields["result"])
					if publicResult["providerState"] != nil || string(publicResult["text"]) != string(contracts.JSON(text)) {
						t.Fatalf("recall result not restored/projected: %s", record.Payload)
					}
				}
			}
		})
	}
}

func TestAssistantResultRetryAndGenericContentRemainIndependent(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	result, err := store.SaveAssistantResult(ctx, AssistantResultInput{RootExecutionID: execution.ID, ExecutionID: execution.ID,
		Sequence: 1, Request: contracts.RequestIdentity{ModelStep: 1}, Phase: "superseded", Text: "partial",
		Payload: json.RawMessage(`{"kind":"model_retry","text":"partial"}`)})
	if err != nil || result.Record.ID == "" {
		t.Fatalf("result-less retry = %#v, %v", result, err)
	}
	attachment := []byte("generic attachment, not model result JSON")
	record, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "model_result",
		Payload: json.RawMessage(`{"kind":"custom","result":{"text":"kept inline"}}`), Content: attachment})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(ctx, execution.ID, record.Ordinal, record.Ordinal)
	if err != nil || len(records) != 1 || string(records[0].Content) != string(attachment) {
		t.Fatalf("generic Content changed: %#v, %v", records, err)
	}
	assertAssistantMirrorJSON(t, records[0].Payload, record.Payload)
	message := contracts.JSON(contracts.Assistant("direct", &contracts.ProviderState{Provider: "chat", Model: "model",
		Reasoning: &contracts.Reasoning{Field: "reasoning_content", Text: "inline state"}}))
	ordinal, err := store.AppendMessage(ctx, execution.ID, message)
	if err != nil {
		t.Fatal(err)
	}
	var storedMessage []byte
	if err := store.db.QueryRowContext(ctx, `SELECT c.content_bytes FROM messages m JOIN contents c ON c.content_digest=m.content_digest
		WHERE m.execution_id=? AND m.message_ordinal=?`, execution.ID, ordinal).Scan(&storedMessage); err != nil {
		t.Fatal(err)
	}
	assertAssistantMirrorJSON(t, storedMessage, message)
	messages, err := store.Messages(ctx, execution.ID, ordinal, ordinal)
	if err != nil || len(messages) != 1 {
		t.Fatalf("direct Message = %#v, %v", messages, err)
	}
	assertAssistantMirrorJSON(t, messages[0].JSON, message)
}

func TestAssistantResultContentsCollectedWithSessionAndSharedOwnersPreserved(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	model := json.RawMessage(`{"provider":"test","modelId":"model"}`)
	sharedResult := json.RawMessage(`{"text":"shared","providerState":{"provider":"test","model":"model"}}`)
	uniqueResult := json.RawMessage(`{"text":"unique"}`)
	for _, id := range []string{"deleted", "kept"} {
		if _, err := store.CreateSession(ctx, SessionInput{ID: id, WorkspaceRoot: "/work", AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model}); err != nil {
			t.Fatal(err)
		}
		execution, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: id, SessionID: id, Task: "task", Model: model, Build: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		results := []json.RawMessage{sharedResult}
		if id == "deleted" {
			results = append(results, uniqueResult)
		}
		for index, object := range results {
			if _, err := store.SaveAssistantResult(ctx, AssistantResultInput{RootExecutionID: execution.ID, ExecutionID: execution.ID,
				Sequence: uint64(index + 1), Payload: contracts.JSON(map[string]any{"kind": "model_result", "result": object})}); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.EndExecution(ctx, execution.ID, OutcomeFailed, json.RawMessage(`{"outcome":"failed"}`), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteSession(ctx, "deleted"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		body  json.RawMessage
		count int
	}{{sharedResult, 1}, {uniqueResult, 0}} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM contents WHERE content_digest=?", contentDigest(test.body)).Scan(&count); err != nil || count != test.count {
			t.Fatalf("remaining result owners = %d, want %d: %v", count, test.count, err)
		}
	}
}
