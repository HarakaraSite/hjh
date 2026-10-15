package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
)

// An opaque-only Responses result has no display Fact from which to obtain the
// source ID. The journal must still hand that result to the saved Message.
// This reasoning item shape is already used by protocol's replay test.
func TestProviderStateStorageEmptyResultHandoffAndReopen(t *testing.T) {
	store, workspace := delegationStore(t)
	item := json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-value","custom":{"kept":true}}`)
	var mu sync.Mutex
	var wires []json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		wires = append(wires, wire)
		w.Header().Set("Content-Type", "text/event-stream")
		var request struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(wire, &request); err != nil {
			t.Error(err)
			return
		}
		output := []json.RawMessage{item}
		if len(wires) > 1 {
			output = []json.RawMessage{json.RawMessage(`{"type":"message","id":"msg_2","role":"assistant","content":[{"type":"output_text","text":"continued"}]}`)}
		}
		fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(map[string]any{"type": "response.completed", "response": map[string]any{
			"id": fmt.Sprintf("resp_%d", len(wires)), "model": request.Model, "status": "completed", "output": output}}))
	}))
	defer server.Close()
	configPath, modelID := standardLocalProviderConfig(t, workspace, "openai-chatgpt", server.URL)
	var results []contracts.Observation
	options := RunOptions{Task: "answer", Workspace: workspace, ConfigPath: configPath,
		ProviderID: "openai-chatgpt", ModelID: modelID, Progress: func(o contracts.Observation) error {
			if o.Kind == "model_result" {
				results = append(results, o)
			}
			return nil
		}}
	run, err := Run(t.Context(), store, nil, options)
	if err != nil || !run.Result.OK || run.Result.FinalText != "" {
		t.Fatalf("opaque-only result: %+v err=%v", run, err)
	}
	if len(results) != 1 || results[0].Text != "" || len(results[0].Thinking) != 0 {
		t.Fatalf("case must have no answer or Thinking: %+v", results)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	assistantStoragePublicResults(t, reader, run.ExecutionID, results)
	assistantStorageRawProviderState(t, reader, workspace, run.ExecutionID, results, 1)
	canonical, err := reader.Conversation(t.Context(), run.SessionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, saved := range canonical {
		assistantStorageNoReference(t, saved.JSON)
		var message contracts.Message
		if err := json.Unmarshal(saved.JSON, &message); err != nil {
			t.Fatal(err)
		}
		if message.Role == "assistant" {
			found = true
			if message.VisibleText() != "" || message.ProviderState == nil {
				t.Fatalf("empty Message lost its state: %s", saved.JSON)
			}
			assistantStorageJSONEqual(t, contracts.JSON(message.ProviderState.ReplayItems), contracts.JSON([]json.RawMessage{item}))
		}
	}
	if !found {
		t.Fatal("empty assistant missing from canonical conversation")
	}
	options.Task, options.SessionID, options.Progress = "continue", run.SessionID, nil
	continued, err := Run(t.Context(), reader, nil, options)
	mu.Lock()
	captured := append([]json.RawMessage(nil), wires...)
	mu.Unlock()
	if err != nil || !continued.Result.OK || continued.Result.FinalText != "continued" || len(captured) != 2 {
		t.Fatalf("continue: %+v requests=%d err=%v", continued, len(captured), err)
	}
	var request struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(captured[1], &request); err != nil {
		t.Fatal(err)
	}
	replayed := 0
	for _, input := range request.Input {
		if string(usageStorageObject(t, input)["id"]) == `"rs_1"` {
			assistantStorageJSONEqual(t, input, item)
			replayed++
		}
	}
	if replayed != 1 {
		t.Fatalf("opaque-only result not replayed exactly once: %s", captured[1])
	}
}

// Both a result committed before Message append and an incomplete length
// Message must remain readable after reopening, including their full state in
// detail. Recall uses the answer, not the provider's private continuation state.
func TestProviderStateStorageStoppedAndLengthResultReopen(t *testing.T) {
	for _, stopBeforeMessage := range []bool{true, false} {
		name := "length"
		if stopBeforeMessage {
			name = "result-before-message"
		}
		t.Run(name, func(t *testing.T) {
			store, workspace := delegationStore(t)
			var mu sync.Mutex
			var wires []json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				wires = append(wires, wire)
				w.Header().Set("Content-Type", "text/event-stream")
				if len(wires) > 1 {
					sendChunk(w, map[string]any{"content": "recalled"}, "stop")
				} else {
					sendChunk(w, map[string]any{"reasoning_content": "private-stopped-state"}, nil)
					finish := "length"
					if stopBeforeMessage {
						finish = "stop"
					}
					sendChunk(w, map[string]any{"content": "saved-result-body"}, finish)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			configPath, modelID := standardLocalProviderConfig(t, workspace, "opencode-go-chat", server.URL)
			var results []contracts.Observation
			options := RunOptions{Task: "source", Workspace: workspace, ConfigPath: configPath,
				ProviderID: "opencode-go-chat", ModelID: modelID, Progress: func(o contracts.Observation) error {
					if o.Kind == "model_result" {
						results = append(results, o)
					}
					// Generate notifies request end after committing model_result but
					// before returning it to the agent loop for Message creation.
					if stopBeforeMessage && o.Kind == "model_request" && o.Phase == "end" {
						return errors.New("stop after committed model_result")
					}
					return nil
				}}
			run, err := Run(t.Context(), store, nil, options)
			if err == nil || run.Result.OK || len(results) != 1 {
				t.Fatalf("source must stop after a result: %+v results=%d err=%v", run, len(results), err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reader, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			assistantStoragePublicResults(t, reader, run.ExecutionID, results)
			wantMessages := 1
			if stopBeforeMessage {
				wantMessages = 0
			}
			assistantStorageRawProviderState(t, reader, workspace, run.ExecutionID, results, wantMessages)
			session, err := reader.Session(t.Context(), run.SessionID)
			if err != nil || session.TurnCount != 0 {
				t.Fatalf("failed result adopted: %+v err=%v", session, err)
			}
			cut, err := reader.CaptureConversationCut(t.Context(), run.ExecutionID)
			if err != nil {
				t.Fatal(err)
			}
			bodyRead := false
			if err := reader.WalkConversationCut(t.Context(), cut, func(fact contracts.ExecutionFact) error {
				if fact.Conversation.EntityKind == "assistant" && fact.Text == "saved-result-body" {
					bodyRead = true
					if !stopBeforeMessage && fact.Phase != "incomplete" {
						t.Errorf("length replay phase=%s", fact.Phase)
					}
				}
				return nil
			}); err != nil || !bodyRead {
				t.Fatalf("saved result not replayed: body=%t err=%v", bodyRead, err)
			}
			evidence, err := reader.RecallEvidence(t.Context(), run.ExecutionID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, record := range evidence.Records {
				assistantStorageNoReference(t, record.Payload)
				if record.Kind != "model_result" {
					continue
				}
				var observation contracts.Observation
				if err := json.Unmarshal(record.Payload, &observation); err != nil {
					t.Fatal(err)
				}
				if observation.Kind == "model_result" {
					found = true
					fields := usageStorageObject(t, observation.Result)
					if string(fields["text"]) != `"saved-result-body"` || fields["providerState"] != nil || record.Content != nil {
						t.Fatalf("recall lost result body or exposed private state: %s", record.Payload)
					}
				}
			}
			if !found {
				t.Fatal("recall has no saved model result")
			}
			service := NewService(reader, nil, RunOptions{Workspace: workspace, ConfigPath: configPath})
			defer service.Close()
			continued, err := service.Run(t.Context(), RunRequest{Task: "use saved result", SessionID: run.SessionID,
				ProviderID: "opencode-go-chat", ModelID: modelID,
				Recall: &contracts.RecallReference{CoreID: "core", OperationID: "recall", SourceExecutionID: run.ExecutionID, Generation: 1}})
			mu.Lock()
			captured := append([]json.RawMessage(nil), wires...)
			mu.Unlock()
			if err != nil || !continued.Result.OK || len(captured) != 2 {
				t.Fatalf("recall continuation: %+v requests=%d err=%v", continued, len(captured), err)
			}
			if !strings.Contains(string(captured[1]), "saved-result-body") || strings.Contains(string(captured[1]), "private-stopped-state") {
				t.Fatalf("recall HTTP input lost body or included private state: %s", captured[1])
			}
		})
	}
}

func assistantStoragePublicResults(t *testing.T, reader *history.Store, executionID string, observations []contracts.Observation) {
	t.Helper()
	service := NewService(reader, nil, RunOptions{})
	defer service.Close()
	records, err := reader.Records(t.Context(), executionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, record := range records {
		if record.Kind != "model_result" {
			continue
		}
		var observation contracts.Observation
		if err := json.Unmarshal(record.Payload, &observation); err != nil {
			t.Fatal(err)
		}
		if observation.Kind != "model_result" {
			continue
		}
		found++
		assistantStorageNoReference(t, record.Payload)
		if record.Content != nil {
			t.Fatalf("result blob duplicated into public Record.Content: %s", record.ID)
		}
		var saved struct {
			Request contracts.RequestIdentity `json:"request"`
		}
		if err := json.Unmarshal(record.Payload, &saved); err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, live := range observations {
			if saved.Request == requestForObservation(live, live.ExecutionID) {
				assistantStorageJSONEqual(t, observation.Result, live.Result)
				matched = true
			}
		}
		if !matched {
			t.Fatalf("saved result has no matching live physical request: %s", record.Payload)
		}
		one, err := service.Record(t.Context(), executionID, record.Ordinal)
		if err != nil {
			t.Fatal(err)
		}
		assistantStorageJSONEqual(t, one.Payload, record.Payload)
		if one.Content != nil {
			t.Fatal("single result detail exposed internal Content")
		}
	}
	want := 0
	for _, observation := range observations {
		if observation.ExecutionID == executionID {
			want++
		}
	}
	if found != want {
		t.Fatalf("public model results=%d want=%d", found, want)
	}
}

// Inspect only the fresh isolated database. The ref must own the same complete
// result observed on that physical request, not a result guessed by model step.
func assistantStorageRawProviderState(t *testing.T, reader *history.Store, workspace, executionID string, observations []contracts.Observation, wantMessages int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(workspace, "history.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(t.Context(), `SELECT r.record_id,r.payload_json,r.content_digest,c.content_bytes
		FROM semantic_records r LEFT JOIN contents c ON c.content_digest=r.content_digest
		WHERE r.execution_id=? AND r.kind='model_result' AND json_extract(r.payload_json,'$.kind')='model_result'`, executionID)
	if err != nil {
		t.Fatal(err)
	}
	type source struct {
		request contracts.RequestIdentity
		result  json.RawMessage
	}
	sources := map[string]source{}
	for rows.Next() {
		var id, payload string
		var digest sql.NullString
		var content []byte
		if err := rows.Scan(&id, &payload, &digest, &content); err != nil {
			t.Fatal(err)
		}
		fields := usageStorageObject(t, json.RawMessage(payload))
		var ref string
		if err := json.Unmarshal(fields["resultRef"], &ref); err != nil || !digest.Valid || ref != digest.String || fields["result"] != nil {
			t.Fatalf("result does not own offloaded resultRef: %s digest=%v err=%v", payload, digest, err)
		}
		var request contracts.RequestIdentity
		if err := json.Unmarshal(fields["request"], &request); err != nil {
			t.Fatal(err)
		}
		if request.ExecutionID != executionID {
			t.Fatalf("result source crossed execution: %+v", request)
		}
		matched := false
		for _, live := range observations {
			if request == requestForObservation(live, live.ExecutionID) {
				assistantStorageJSONEqual(t, content, live.Result)
				matched = true
			}
		}
		if !matched {
			t.Fatalf("raw source has no live physical request: %s", payload)
		}
		sources[id] = source{request: request, result: content}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	wantResults := 0
	for _, live := range observations {
		if live.ExecutionID == executionID {
			wantResults++
		}
	}
	if len(sources) != wantResults {
		t.Fatalf("raw source results=%d want=%d", len(sources), wantResults)
	}
	messages, err := reader.Messages(t.Context(), executionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, saved := range messages {
		var message contracts.Message
		if err := json.Unmarshal(saved.JSON, &message); err != nil {
			t.Fatal(err)
		}
		if message.Role != "assistant" {
			continue
		}
		count++
		assistantStorageNoReference(t, saved.JSON)
		var encoded []byte
		if err := db.QueryRowContext(t.Context(), `SELECT c.content_bytes FROM messages m JOIN contents c ON c.content_digest=m.content_digest
			WHERE m.execution_id=? AND m.message_ordinal=?`, executionID, saved.Ordinal).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		fields := usageStorageObject(t, encoded)
		var sourceID string
		if err := json.Unmarshal(fields["providerStateRecordId"], &sourceID); err != nil || fields["providerState"] != nil {
			t.Fatalf("Message did not replace state with explicit source: %s err=%v", encoded, err)
		}
		source, ok := sources[sourceID]
		if !ok {
			t.Fatalf("Message source %q is not a result of this execution", sourceID)
		}
		var mirrorPayload string
		if err := db.QueryRowContext(t.Context(), `SELECT payload_json FROM semantic_records WHERE execution_id=? AND kind='assistant_message'
			AND json_extract(payload_json,'$.locator.messageOrdinal')=? AND json_extract(payload_json,'$.conversation.entityKind')='assistant'`, executionID, saved.Ordinal).Scan(&mirrorPayload); err != nil {
			t.Fatal(err)
		}
		var mirror struct {
			Request contracts.RequestIdentity `json:"request"`
		}
		if err := json.Unmarshal([]byte(mirrorPayload), &mirror); err != nil || mirror.Request != source.request {
			t.Fatalf("Message and source physical requests differ: %s source=%+v err=%v", mirrorPayload, source.request, err)
		}
		resultFields := usageStorageObject(t, source.result)
		if resultFields["providerState"] == nil || message.ProviderState == nil {
			t.Fatal("source or restored Message lost provider state")
		}
		assistantStorageJSONEqual(t, contracts.JSON(message.ProviderState), resultFields["providerState"])
	}
	if count != wantMessages {
		t.Fatalf("saved assistant Messages=%d want=%d", count, wantMessages)
	}
}
