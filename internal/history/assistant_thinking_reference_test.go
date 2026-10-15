package history

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"hjh/internal/contracts"
)

func TestThinkingMirrorsReferencePriorResultAcrossReadPathsAndPages(t *testing.T) {
	for _, variant := range []string{"chat", "responses"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			store, execution := openConversationFixture(t)
			var databaseSequence int
			var databaseName, databasePath string
			if err := store.db.QueryRowContext(ctx, "PRAGMA database_list").Scan(&databaseSequence, &databaseName, &databasePath); err != nil {
				t.Fatal(err)
			}
			state := &contracts.ProviderState{Provider: variant, Model: "model"}
			thinking := []contracts.ThinkingItem{{Key: "reasoning/0", Text: "考える <&>", Kind: "reasoning"}}
			if variant == "chat" {
				state.Reasoning = &contracts.Reasoning{Field: "reasoning_content", Text: thinking[0].Text}
			} else {
				thinking = append(thinking, contracts.ThinkingItem{Key: "summary/0", Text: "readable summary", Kind: "summary"})
				state.ReplayItems = []json.RawMessage{json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[{"type":"summary_text","text":"readable summary"}]}`)}
			}
			request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", ModelStep: 1,
				RequestOrdinal: 1, PhysicalRequestID: "winning-attempt", RetryAttempt: 1}
			sources := map[contracts.RequestIdentity]map[string]contracts.ExecutionLocator{}
			firstSequences := map[string]uint64{}
			for attempt := 0; attempt <= 1; attempt++ {
				physical := request
				physical.RetryAttempt = attempt
				items := thinking
				phase := "completed"
				if attempt == 0 {
					physical.PhysicalRequestID = "superseded-attempt"
					items = []contracts.ThinkingItem{{Key: "reasoning/0", Text: "old attempt thought", Kind: "reasoning"}}
					phase = "superseded"
				}
				sequence := uint64(attempt*2 + 1)
				progress, err := store.SaveAssistantProgress(ctx, AssistantProgressInput{RootExecutionID: execution.ID,
					ExecutionID: execution.ID, Request: physical, Sequence: sequence, Thinking: items})
				if err != nil {
					t.Fatal(err)
				}
				for _, fact := range progress {
					firstSequences["thinking/"+fact.Conversation.LocalKey] = fact.Conversation.FirstSequence
				}
				result, err := store.SaveAssistantResult(ctx, AssistantResultInput{RootExecutionID: execution.ID,
					ExecutionID: execution.ID, Request: physical, Sequence: sequence + 1, Phase: phase,
					Payload: contracts.JSON(map[string]any{"kind": "model_result", "result": map[string]any{"text": "answer", "providerState": state}}),
					Text:    "answer", Thinking: items})
				if err != nil {
					t.Fatal(err)
				}
				facts := result.Facts
				sources[physical] = map[string]contracts.ExecutionLocator{}
				for _, fact := range facts {
					if fact.Conversation.EntityKind == "thinking" {
						sources[physical][fact.Conversation.LocalKey] = fact.Locator
					}
				}
			}
			// Put the referenced source and its mirror on different 64-record pages.
			for index := 0; index < conversationPageSize; index++ {
				if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "tool_call",
					Payload: contracts.JSON(map[string]any{"kind": "tool_call", "id": fmt.Sprintf("call-%d", index), "name": "read_file"})}); err != nil {
					t.Fatal(err)
				}
			}
			beforeMessage, err := store.CaptureConversationCut(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			message := contracts.JSON(contracts.Assistant("answer", state))
			base := contracts.ExecutionFact{Kind: "assistant_message", Role: "assistant", Phase: "incomplete", Text: "answer",
				Request: request, Thinking: thinking,
				Conversation: contracts.ConversationMetadata{EntityKind: "assistant", LocalKey: request.AssistantKey("answer")}}
			const observedAt = "2026-10-16T00:00:00Z"
			ordinal, saved, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: execution.ID,
				ExecutionID: execution.ID, Sequence: 5, Message: message, Facts: []contracts.ExecutionFact{base},
				ThinkingRecords: sources, FirstSequences: firstSequences, FinalizeRequest: &request, ObservedAt: observedAt})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]json.RawMessage{}
			for _, fact := range saved {
				payload, err := messageFactPayload(message, fact, ordinal, observedAt)
				if err != nil {
					t.Fatal(err)
				}
				want[fact.Locator.RecordID] = payload
				var raw string
				if err := store.db.QueryRowContext(ctx, "SELECT payload_json FROM semantic_records WHERE record_id=?", fact.Locator.RecordID).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				fields := assistantMirrorFields(t, []byte(raw))
				if fields["content"] != nil || fields["providerState"] != nil {
					t.Fatalf("raw mirror retained Message-owned fields: %s", raw)
				}
				if fact.Conversation.EntityKind == "thinking" {
					source := sources[request][fact.Conversation.LocalKey]
					if string(fields["thinkingRecordId"]) != string(contracts.JSON(source.RecordID)) || fields["text"] != nil {
						t.Fatalf("raw thinking mirror did not reference its exact physical result: %s", raw)
					}
					if fact.Conversation.FirstSequence != 3 || fact.Conversation.Sequence != 5 {
						t.Fatalf("mirror lost first-visible identity: %#v", fact)
					}
				} else if fields["thinkingRecordId"] != nil || string(fields["text"]) != `"answer"` {
					t.Fatalf("answer text was changed: %s", raw)
				}
			}
			cut, err := store.CaptureConversationCut(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			// A later append must not appear when replaying either captured cut.
			if _, _, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: execution.ID,
				ExecutionID: execution.ID, Sequence: 6, Message: contracts.JSON(contracts.Assistant("after cut", nil))}); err != nil {
				t.Fatal(err)
			}
			if err := store.EndExecution(ctx, execution.ID, OutcomeFailed, json.RawMessage(`{"outcome":"failed"}`), ""); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reader, err := OpenReadOnly(databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			messages, err := reader.Messages(ctx, execution.ID, ordinal, ordinal)
			if err != nil || len(messages) != 1 {
				t.Fatalf("Messages = %#v, %v", messages, err)
			}
			assertAssistantMirrorJSON(t, messages[0].JSON, message)
			for _, fact := range saved {
				records, err := reader.Records(ctx, execution.ID, fact.Locator.Ordinal, fact.Locator.Ordinal)
				if err != nil || len(records) != 1 {
					t.Fatalf("bounded record read = %#v, %v", records, err)
				}
				assertAssistantMirrorJSON(t, records[0].Payload, want[fact.Locator.RecordID])
				assertAssistantMirrorJSON(t, records[0].Content, message)
			}
			records, err := reader.Records(ctx, execution.ID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				if expected, ok := want[record.ID]; ok {
					assertAssistantMirrorJSON(t, record.Payload, expected)
				}
			}
			evidence, err := reader.RecallEvidence(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			recalled := 0
			for _, record := range evidence.Records {
				if expected, ok := want[record.ID]; ok {
					recalled++
					public, err := stripProviderState(expected)
					if err != nil {
						t.Fatal(err)
					}
					assertAssistantMirrorJSON(t, record.Payload, public)
					if record.Content != nil {
						t.Fatal("recall exposed the Message envelope")
					}
				}
			}
			if recalled != len(saved) {
				t.Fatalf("recalled %d mirrors, want %d", recalled, len(saved))
			}
			for _, captured := range []ConversationCut{beforeMessage, cut} {
				seen := 0
				if err := reader.WalkConversationCut(ctx, captured, func(fact contracts.ExecutionFact) error {
					if fact.Text == "after cut" {
						t.Fatal("walk crossed its fixed cut")
					}
					for _, expected := range saved {
						if expected.Locator.RecordID != fact.Locator.RecordID {
							continue
						}
						seen++
						if fact.Text != expected.Text || fact.ThinkingKind != expected.ThinkingKind || fact.Request != expected.Request ||
							fact.Conversation != expected.Conversation || fact.Phase != expected.Phase {
							t.Fatalf("walk overwrote mirror text or metadata: %#v, want %#v", fact, expected)
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				expectedCount := 0
				if captured.Executions[0].LatestRecordOrdinal == cut.Executions[0].LatestRecordOrdinal {
					expectedCount = len(saved)
				}
				if seen != expectedCount {
					t.Fatalf("walked %d mirrors, want %d", seen, expectedCount)
				}
			}
		})
	}
}
