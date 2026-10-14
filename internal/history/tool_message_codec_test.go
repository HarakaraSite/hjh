package history

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"hjh/internal/contracts"
)

func TestToolMessageBodiesShareExecutionOwnerAndExpandAfterReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "history.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	model := json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`)
	session, err := store.CreateSession(ctx, SessionInput{ID: "tool-message-session", WorkspaceRoot: t.TempDir(),
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, BeginExecutionInput{ExecutionID: "tool-message-execution", SessionID: session.ID,
		Task: "inspect a file and preserve tool output", Model: model, Build: json.RawMessage(`{"fixture":true}`)})
	if err != nil {
		t.Fatal(err)
	}

	// The exact S-3 result bytes also appear in the next tool Message. The
	// message must use that same execution-owned body rather than storing a copy.
	observedResult := []byte(`{"text":"<p>&"}`)
	if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "tool_result",
		Payload:        json.RawMessage(`{"callId":"call-read","name":"read_file","phase":"completed","result":{"text":"<p>&"}}`),
		ObservedResult: observedResult,
	}); err != nil {
		t.Fatal(err)
	}
	toolMessage := contracts.JSON(contracts.ToolResults([]contracts.ToolResult{{CallID: "call-read", Name: "read_file",
		Text: string(observedResult), Outcome: "success"}}))
	fact := contracts.ExecutionFact{Kind: "tool_result", Phase: "completed", Role: "tool", Text: string(observedResult),
		CallID: "call-read", Name: "read_file", Outcome: "success",
		Conversation: contracts.ConversationMetadata{EntityKind: "tool", LocalKey: "call-read"}}
	_, savedFacts, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: execution.ID,
		ExecutionID: execution.ID, Sequence: 1, Message: toolMessage, Facts: []contracts.ExecutionFact{fact}})
	if err != nil {
		t.Fatal(err)
	}
	if len(savedFacts) != 1 || savedFacts[0].Text != string(observedResult) {
		t.Fatalf("AppendConversationMessage changed live tool fact: %#v", savedFacts)
	}

	// A direct append represents a failed tool result without an S-3 observation.
	// It must create its own small semantic owner and preserve failure metadata.
	failureText := "read failed: 検索対象がありません"
	failureMessage := json.RawMessage(`{"role":"tool","content":[{"kind":"tool_result","callId":"call-missing","name":"read_file","text":"read failed: 検索対象がありません","outcome":"error","failure":{"code":"not_found"},"terminal":"tool_error"}]}`)
	if _, err := store.AppendMessage(ctx, execution.ID, failureMessage); err != nil {
		t.Fatal(err)
	}

	for _, body := range [][]byte{observedResult, []byte(failureText)} {
		digest := contentDigest(body)
		stored, err := store.Content(ctx, digest)
		if err != nil || !bytes.Equal(stored, body) {
			t.Fatalf("stored tool body %q = %q, %v", body, stored, err)
		}
	}
	var s3OwnerCount, fallbackOwnerCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM semantic_records WHERE execution_id=? AND content_digest=?`,
		execution.ID, contentDigest(observedResult)).Scan(&s3OwnerCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM semantic_records WHERE execution_id=? AND kind='tool_body' AND content_digest=?`,
		execution.ID, contentDigest([]byte(failureText))).Scan(&fallbackOwnerCount); err != nil {
		t.Fatal(err)
	}
	if s3OwnerCount != 1 || fallbackOwnerCount != 1 {
		t.Fatalf("same-execution owners: shared S-3=%d fallback=%d, want 1 each", s3OwnerCount, fallbackOwnerCount)
	}

	// The stored wire contains only locators, while the public reads restore the
	// original Message and semantic mirror fields.
	var storedMessage []byte
	if err := store.db.QueryRowContext(ctx, `SELECT body.content_bytes FROM messages m JOIN contents body
		ON body.content_digest=m.content_digest WHERE m.execution_id=? AND m.message_ordinal=0`, execution.ID).Scan(&storedMessage); err != nil {
		t.Fatal(err)
	}
	assertStoredToolMessageRef(t, storedMessage, "call-read", contentDigest(observedResult))
	var storedMirror string
	if err := store.db.QueryRowContext(ctx, `SELECT payload_json FROM semantic_records WHERE execution_id=? AND kind='tool_result'
		AND json_extract(payload_json,'$.conversation.localKey')='call-read'`, execution.ID).Scan(&storedMirror); err != nil {
		t.Fatal(err)
	}
	var storedMirrorObject map[string]json.RawMessage
	if err := json.Unmarshal([]byte(storedMirror), &storedMirrorObject); err != nil {
		t.Fatal(err)
	}
	if _, ok := storedMirrorObject["text"]; ok {
		t.Fatalf("stored semantic mirror duplicated top-level Text: %s", storedMirror)
	}
	if _, ok := storedMirrorObject["factTextRef"]; !ok {
		t.Fatalf("stored semantic mirror omitted top-level text reference: %s", storedMirror)
	}

	messages, err := store.Messages(ctx, execution.ID, 0, -1)
	if err != nil || len(messages) != 2 {
		t.Fatalf("Messages = %+v, %v", messages, err)
	}
	assertPublicToolMessageText(t, messages[0].JSON, "call-read", string(observedResult))
	assertPublicToolMessageFailure(t, messages[1].JSON, failureText)

	records, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var publicMirror, publicFallbackOwner bool
	for _, record := range records {
		if record.Kind == "tool_body" {
			publicFallbackOwner = true
			if record.Content != nil {
				t.Fatalf("tool_body owner leaked its result into Record.Content: %d bytes", len(record.Content))
			}
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(record.Payload, &payload) == nil && record.Kind == "tool_result" && payload["factTextRef"] == nil {
			if payload["conversation"] != nil {
				publicMirror = true
				var got string
				if err := json.Unmarshal(payload["text"], &got); err != nil || got != string(observedResult) {
					t.Fatalf("public semantic mirror Text = %q, %v", got, err)
				}
				assertPublicToolMessageText(t, record.Payload, "call-read", string(observedResult))
				if record.Content != nil {
					t.Fatalf("public tool message mirror duplicated body in Content: %d bytes", len(record.Content))
				}
			}
		}
	}
	if !publicMirror || !publicFallbackOwner {
		t.Fatalf("Records omitted expanded tool mirror or fallback owner: mirror=%v fallbackOwner=%v", publicMirror, publicFallbackOwner)
	}

	if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted, json.RawMessage(`{"outcome":"completed"}`), "2026-10-14T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-14T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	conversation, err := reopened.Conversation(ctx, session.ID, 1, 1)
	if err != nil || len(conversation) != 2 {
		t.Fatalf("reopened Conversation = %+v, %v", conversation, err)
	}
	assertPublicToolMessageText(t, conversation[0].JSON, "call-read", string(observedResult))
	assertPublicToolMessageFailure(t, conversation[1].JSON, failureText)
	evidence, err := reopened.RecallEvidence(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Messages) != 2 {
		t.Fatalf("RecallEvidence messages = %d, want 2", len(evidence.Messages))
	}
	assertPublicToolMessageText(t, evidence.Messages[0].JSON, "call-read", string(observedResult))
	assertPublicToolMessageFailure(t, evidence.Messages[1].JSON, failureText)
	var recalledMirror bool
	for _, record := range evidence.Records {
		var payload map[string]json.RawMessage
		if record.Kind == "tool_result" && json.Unmarshal(record.Payload, &payload) == nil && payload["conversation"] != nil {
			recalledMirror = true
			var got string
			if err := json.Unmarshal(payload["text"], &got); err != nil || got != string(observedResult) {
				t.Fatalf("RecallEvidence semantic mirror Text = %q, %v", got, err)
			}
		}
	}
	if !recalledMirror {
		t.Fatal("RecallEvidence omitted the expanded tool semantic mirror")
	}
}

func TestLatestRequestReplacementPreservesToolMessageBodies(t *testing.T) {
	for _, entry := range []string{"AppendMessage", "AppendConversationMessage"} {
		t.Run(entry, func(t *testing.T) {
			ctx := context.Background()
			store, execution := openConversationFixture(t)
			appendRequest := func(ordinal int, body []byte) Record {
				t.Helper()
				payload := contracts.JSON(map[string]any{"schemaVersion": 1, "requestOrdinal": ordinal, "modelStep": ordinal})
				record, err := store.AppendLatestRequest(ctx, execution.ID, OccurrenceInput{
					Kind: "model_request", Payload: payload, Content: body,
				}, nil)
				if err != nil {
					t.Fatal(err)
				}
				return record
			}
			firstBody := []byte("wire request A body <p>&")
			firstRequest := appendRequest(1, firstBody)
			message := contracts.JSON(contracts.ToolResults([]contracts.ToolResult{{CallID: "call-latest", Name: "read_file",
				Text: string(firstBody), Outcome: "success"}}))
			switch entry {
			case "AppendMessage":
				if _, err := store.AppendMessage(ctx, execution.ID, message); err != nil {
					t.Fatal(err)
				}
			case "AppendConversationMessage":
				fact := contracts.ExecutionFact{Kind: "tool_result", Phase: "completed", Role: "tool", Text: string(firstBody),
					CallID: "call-latest", Name: "read_file", Outcome: "success",
					Conversation: contracts.ConversationMetadata{EntityKind: "tool", LocalKey: "call-latest"}}
				if _, _, err := store.AppendConversationMessage(ctx, ConversationMessageInput{RootExecutionID: execution.ID,
					ExecutionID: execution.ID, Sequence: 1, Message: message, Facts: []contracts.ExecutionFact{fact}}); err != nil {
					t.Fatal(err)
				}
			}
			secondBody := []byte("wire request B body")
			secondRequest := appendRequest(2, secondBody)

			// Replacing the latest request leaves its old semantic metadata
			// without a body; the tool Message must own A through tool_body.
			if _, err := store.Content(ctx, contentDigest(firstBody)); err != nil {
				t.Fatalf("tool Message body was reclaimed with prior model_request: %v", err)
			}
			requests, err := store.Records(ctx, execution.ID, firstRequest.Ordinal, secondRequest.Ordinal)
			if err != nil {
				t.Fatal(err)
			}
			var firstReleased, latestRetained, fallbackOwner bool
			for _, record := range requests {
				if record.ID == firstRequest.ID {
					firstReleased = record.Content == nil
				}
				if record.ID == secondRequest.ID {
					latestRetained = bytes.Equal(record.Content, secondBody)
				}
				if record.Kind == "tool_body" && record.Content == nil {
					fallbackOwner = true
				}
			}
			if !firstReleased || !latestRetained || !fallbackOwner {
				t.Fatalf("request replacement ownership: oldReleased=%v latestRetained=%v toolBodyOwner=%v", firstReleased, latestRetained, fallbackOwner)
			}
			messages, err := store.Messages(ctx, execution.ID, 0, -1)
			if err != nil || len(messages) != 1 {
				t.Fatalf("Messages after replacement = %+v, %v", messages, err)
			}
			assertPublicToolMessageText(t, messages[0].JSON, "call-latest", string(firstBody))
			if entry == "AppendConversationMessage" {
				records, err := store.Records(ctx, execution.ID, 0, -1)
				if err != nil {
					t.Fatal(err)
				}
				var foundMirror bool
				for _, record := range records {
					if record.Kind != "tool_result" || record.Conversation == nil || record.Conversation.LocalKey != "call-latest" {
						continue
					}
					foundMirror = true
					assertPublicToolMessageText(t, record.Payload, "call-latest", string(firstBody))
					var public struct {
						Text string `json:"text"`
					}
					if err := json.Unmarshal(record.Payload, &public); err != nil || public.Text != string(firstBody) {
						t.Fatalf("Record top-level Text = %q, %v", public.Text, err)
					}
					if record.Content != nil {
						t.Fatalf("Record duplicated tool result body in Content: %d bytes", len(record.Content))
					}
				}
				if !foundMirror {
					t.Fatal("Records omitted the expanded tool semantic mirror")
				}
			}

			if err := store.EndExecution(ctx, execution.ID, OutcomeCompleted, json.RawMessage(`{"outcome":"completed"}`), "2026-10-14T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			if err := store.AdoptCanonical(ctx, execution.ID, "2026-10-14T00:00:01Z"); err != nil {
				t.Fatal(err)
			}
			conversation, err := store.Conversation(ctx, execution.SessionID, 1, 1)
			if err != nil || len(conversation) != 1 {
				t.Fatalf("Conversation after replacement = %+v, %v", conversation, err)
			}
			assertPublicToolMessageText(t, conversation[0].JSON, "call-latest", string(firstBody))
		})
	}
}

func assertStoredToolMessageRef(t *testing.T, raw []byte, callID, digest string) {
	t.Helper()
	var message struct {
		Role    string `json:"role"`
		Content []struct {
			Kind    string `json:"kind"`
			CallID  string `json:"callId"`
			Text    string `json:"text"`
			TextRef string `json:"textRef"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if message.Role != "tool" || len(message.Content) != 1 || message.Content[0].Kind != "tool_result" ||
		message.Content[0].CallID != callID || message.Content[0].Text != "" || message.Content[0].TextRef != digest {
		t.Fatalf("stored tool Message is not compact: %s", raw)
	}
}

func assertPublicToolMessageText(t *testing.T, raw []byte, callID, text string) {
	t.Helper()
	var message struct {
		Role    string `json:"role"`
		Content []struct {
			Kind    string `json:"kind"`
			CallID  string `json:"callId"`
			Text    string `json:"text"`
			TextRef string `json:"textRef"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if message.Role != "tool" || len(message.Content) == 0 {
		t.Fatalf("expanded tool Message has wrong shape: %s", raw)
	}
	for _, item := range message.Content {
		if item.CallID == callID && item.Kind == "tool_result" {
			if item.Text != text || item.TextRef != "" {
				t.Fatalf("expanded tool result = text %q ref %q, want exact text", item.Text, item.TextRef)
			}
			return
		}
	}
	t.Fatalf("expanded tool Message omitted call %q: %s", callID, raw)
}

func assertPublicToolMessageFailure(t *testing.T, raw []byte, text string) {
	t.Helper()
	var message struct {
		Content []struct {
			CallID   string          `json:"callId"`
			Text     string          `json:"text"`
			Outcome  string          `json:"outcome"`
			Failure  json.RawMessage `json:"failure"`
			Terminal string          `json:"terminal"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if len(message.Content) != 1 || message.Content[0].Text != text || message.Content[0].Outcome != "error" ||
		message.Content[0].CallID != "call-missing" || message.Content[0].Terminal != "tool_error" ||
		!bytes.Equal(message.Content[0].Failure, json.RawMessage(`{"code":"not_found"}`)) {
		t.Fatalf("expanded failure Message lost result metadata: %s", raw)
	}
}
