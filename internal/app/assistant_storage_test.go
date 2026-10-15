package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/history"
)

// S1/S2/S3 must reduce raw copies without changing the real SSE -> journal ->
// public history -> resumed HTTP input path. Responses uses the readable
// reasoning/summary and opaque item shape already exercised in protocol_test;
// text and opaque values here are synthetic, not a live provider recording.
func TestAssistantStorageLocalSSEParentChildAndReopen(t *testing.T) {
	for _, providerID := range []string{"opencode-go-chat", "openai-chatgpt"} {
		t.Run(providerID, func(t *testing.T) {
			store, workspace := delegationStore(t)
			responses := providerID == "openai-chatgpt"
			var mu sync.Mutex
			var requests []json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
					return
				}
				var input struct {
					Model string `json:"model"`
				}
				if err := json.Unmarshal(wire, &input); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				requests = append(requests, wire)
				call := len(requests)
				mu.Unlock()
				assistantStorageSSE(w, responses, input.Model, call)
			}))
			defer server.Close()
			configPath, modelID := standardLocalProviderConfig(t, workspace, providerID, server.URL)
			owner := executiondata.NewOwner()
			var live []contracts.ExecutionFact
			var results []contracts.Observation
			_, subscription, err := executiondata.CaptureAndSubscribe(owner,
				func(string, uint64) (struct{}, error) { return struct{}{}, nil },
				func(commit contracts.ExecutionCommit) {
					mu.Lock()
					live = append(live, commit.Facts...)
					mu.Unlock()
				})
			if err != nil {
				t.Fatal(err)
			}
			defer subscription.Close()
			options := RunOptions{Task: "parent", Workspace: workspace, ConfigPath: configPath,
				ProviderID: providerID, ModelID: modelID, ExecutionDataOwner: owner,
				Progress: func(o contracts.Observation) error {
					if o.Kind == "model_result" {
						mu.Lock()
						results = append(results, o)
						mu.Unlock()
					}
					return nil
				}}
			run, err := Run(t.Context(), store, nil, options)
			if err != nil || !run.Result.OK || run.Result.FinalText != "answer-3" {
				t.Fatalf("parent/child run: %+v err=%v", run, err)
			}
			children, err := store.Children(t.Context(), run.ExecutionID)
			if err != nil || len(children) != 1 || children[0].Outcome != "completed" {
				t.Fatalf("children=%+v err=%v", children, err)
			}
			childID := children[0].ID
			mu.Lock()
			facts := append([]contracts.ExecutionFact(nil), live...)
			observations := append([]contracts.Observation(nil), results...)
			requestCount := len(requests)
			mu.Unlock()
			if len(observations) != 3 || requestCount != 3 {
				t.Fatalf("results/HTTP requests = %d/%d, want 3/3", len(observations), requestCount)
			}
			byExecution := map[string]map[int]contracts.Observation{}
			for _, o := range observations {
				if byExecution[o.ExecutionID] == nil {
					byExecution[o.ExecutionID] = map[int]contracts.Observation{}
				}
				byExecution[o.ExecutionID][o.ModelStep] = o
				var result struct {
					Text          string                   `json:"text"`
					ProviderState *contracts.ProviderState `json:"providerState"`
				}
				if err := json.Unmarshal(o.Result, &result); err != nil {
					t.Fatal(err)
				}
				call := o.ModelStep
				if o.ExecutionID == childID {
					call = 2
				} else if o.ModelStep == 2 {
					call = 3
				}
				if result.Text != fmt.Sprintf("answer-%d", call) || result.ProviderState == nil {
					t.Fatalf("live result lost body/state: %s", o.Result)
				}
				if responses {
					assistantStorageJSONEqual(t, contracts.JSON(result.ProviderState.ReplayItems), contracts.JSON(assistantStorageResponseOutput(call)))
				} else if result.ProviderState.Reasoning == nil || result.ProviderState.Reasoning.Field != "reasoning_content" || result.ProviderState.Reasoning.Text != fmt.Sprintf("reasoning-%d", call) {
					t.Fatalf("live Chat reasoning lost: %s", o.Result)
				}
				wantThinking := []contracts.ThinkingItem{{Key: "reasoning", Kind: "reasoning", Text: fmt.Sprintf("reasoning-%d", call)}}
				if responses {
					wantThinking = []contracts.ThinkingItem{
						{Key: fmt.Sprintf("rs_%d/reasoning/0", call), Kind: "reasoning", Text: fmt.Sprintf("reasoning-%d", call)},
						{Key: fmt.Sprintf("rs_%d/summary/0", call), Kind: "summary", Text: fmt.Sprintf("summary-%d", call)},
					}
				}
				if !reflect.DeepEqual(o.Thinking, wantThinking) {
					t.Fatalf("live thinking changed: %+v want %+v", o.Thinking, wantThinking)
				}
			}
			parentFirst, childFirst := byExecution[run.ExecutionID][1], byExecution[childID][1]
			if parentFirst.PhysicalRequestID == "" || parentFirst.PhysicalRequestID != childFirst.PhysicalRequestID || childFirst.ParentExecutionID != run.ExecutionID {
				t.Fatalf("fixture did not exercise same-step, same-physical-ID parent/child: parent=%+v child=%+v", parentFirst, childFirst)
			}
			latest := map[string]contracts.ExecutionFact{}
			progress := map[string]bool{}
			for _, fact := range facts {
				if fact.Conversation.EntityKind != "assistant" && fact.Conversation.EntityKind != "thinking" {
					continue
				}
				key := assistantStorageFactKey(fact)
				if previous, ok := latest[key]; ok && previous.Conversation.FirstSequence != fact.Conversation.FirstSequence {
					t.Fatalf("live entity moved: %+v -> %+v", previous, fact)
				}
				if fact.Phase == "streaming" {
					progress[key] = true
				}
				latest[key] = fact
			}
			wantEntities := 6 // Three responses, each with answer and reasoning.
			if responses {
				wantEntities = 9 // Plus one readable summary per response.
			}
			if len(latest) != wantEntities || len(progress) != wantEntities {
				t.Fatalf("live final/progress entities = %d/%d, want %d", len(latest), len(progress), wantEntities)
			}

			checkSaved := func(reader *history.Store) {
				t.Helper()
				service := NewService(reader, nil, RunOptions{Workspace: workspace})
				for _, executionID := range []string{run.ExecutionID, childID} {
					messages, err := reader.Messages(t.Context(), executionID, 0, -1)
					if err != nil {
						t.Fatal(err)
					}
					assistantMessages := map[int64]contracts.Message{}
					var wantReports []contracts.AgentReport
					for _, saved := range messages {
						assistantStorageNoReference(t, saved.JSON)
						var message contracts.Message
						if err := json.Unmarshal(saved.JSON, &message); err != nil {
							t.Fatal(err)
						}
						if message.Role == "assistant" {
							assistantMessages[saved.Ordinal] = message
							wantReports = append(wantReports, contracts.AgentReport{ExecutionID: executionID, MessageOrdinal: saved.Ordinal, Text: message.VisibleText()})
						}
					}
					reports, cursor, err := reader.AssistantReports(t.Context(), executionID, -1)
					if err != nil || !reflect.DeepEqual(reports, wantReports) || cursor != messages[len(messages)-1].Ordinal {
						t.Fatalf("assistant reports changed: %+v want=%+v cursor=%d err=%v", reports, wantReports, cursor, err)
					}
					records, err := reader.Records(t.Context(), executionID, 0, -1)
					if err != nil {
						t.Fatal(err)
					}
					assistantStoragePublicResults(t, reader, executionID, observations)
					mirrors := 0
					for _, record := range records {
						if record.Kind != "assistant_message" || record.Content == nil {
							continue
						}
						mirrors++
						assistantStorageNoReference(t, record.Payload)
						var payload struct {
							contracts.Message
							Phase        string                         `json:"phase"`
							ThinkingKind string                         `json:"thinkingKind"`
							Request      contracts.RequestIdentity      `json:"request"`
							Conversation contracts.ConversationMetadata `json:"conversation"`
							Locator      contracts.ExecutionLocator     `json:"locator"`
						}
						if err := json.Unmarshal(record.Payload, &payload); err != nil {
							t.Fatal(err)
						}
						message, ok := assistantMessages[payload.Locator.MessageOrdinal]
						if !ok {
							t.Fatalf("mirror has no assistant Message: %s", record.ID)
						}
						assistantStorageJSONEqual(t, record.Content, contracts.JSON(message))
						assistantStorageJSONEqual(t, payload.Content, message.Content)
						assistantStorageJSONEqual(t, contracts.JSON(payload.ProviderState), contracts.JSON(message.ProviderState))
						fact := latest[executionID+"/"+payload.Conversation.EntityKind+"/"+payload.Conversation.LocalKey]
						if payload.Text == nil || *payload.Text != fact.Text || payload.ThinkingKind != fact.ThinkingKind || payload.Request != fact.Request || payload.Conversation != fact.Conversation || payload.Phase != fact.Phase || record.ID != fact.Locator.RecordID {
							t.Fatalf("public mirror changed live body/kind/identity: %s live=%+v", record.Payload, fact)
						}
						original := byExecution[executionID][payload.Request.ModelStep]
						var modelResult struct {
							ProviderState *contracts.ProviderState `json:"providerState"`
						}
						if err := json.Unmarshal(original.Result, &modelResult); err != nil {
							t.Fatal(err)
						}
						assistantStorageJSONEqual(t, contracts.JSON(message.ProviderState), contracts.JSON(modelResult.ProviderState))
						one, err := service.Record(t.Context(), executionID, record.Ordinal)
						if err != nil || !reflect.DeepEqual(one, record) {
							t.Fatalf("Service.Record differs from Records for %s: %v", record.ID, err)
						}
					}
					wantMirrors := len(assistantMessages) * 2
					if responses {
						wantMirrors = len(assistantMessages) * 3
					}
					if mirrors != wantMirrors {
						t.Fatalf("assistant mirrors=%d want=%d", mirrors, wantMirrors)
					}
					evidence, err := reader.RecallEvidence(t.Context(), executionID)
					if err != nil {
						t.Fatal(err)
					}
					for _, message := range evidence.Messages {
						assistantStorageNoReference(t, message.JSON)
						if _, exists := usageStorageObject(t, message.JSON)["providerState"]; exists {
							t.Fatal("recall Message exposed provider state")
						}
					}
					for _, record := range evidence.Records {
						assistantStorageNoReference(t, record.Payload)
						fields := usageStorageObject(t, record.Payload)
						_, private := fields["providerState"]
						if result := fields["result"]; len(result) != 0 && bytes.HasPrefix(bytes.TrimSpace(result), []byte("{")) {
							_, nested := usageStorageObject(t, result)["providerState"]
							private = private || nested
						}
						if private || record.Content != nil {
							t.Fatalf("recall exposed private state/content: %s", record.ID)
						}
						if record.Kind == "assistant_message" {
							for _, public := range records {
								if public.ID == record.ID {
									want := usageStorageObject(t, public.Payload)
									delete(want, "providerState")
									assistantStorageJSONEqual(t, record.Payload, contracts.JSON(want))
								}
							}
						}
					}
				}
				cut, err := reader.CaptureConversationCut(t.Context(), run.ExecutionID)
				if err != nil {
					t.Fatal(err)
				}
				replayed := map[string]contracts.ExecutionFact{}
				if err := reader.WalkConversationCut(t.Context(), cut, func(fact contracts.ExecutionFact) error {
					key := assistantStorageFactKey(fact)
					if _, ok := latest[key]; ok {
						replayed[key] = fact
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if len(replayed) != len(latest) {
					t.Fatalf("walk replayed %d entities, want %d", len(replayed), len(latest))
				}
				for key, want := range latest {
					fact := replayed[key]
					if fact.Text != want.Text || fact.ThinkingKind != want.ThinkingKind || fact.Request != want.Request || fact.Conversation != want.Conversation || fact.Phase != want.Phase || fact.Locator.RecordID != want.Locator.RecordID || fact.Locator.Ordinal != want.Locator.Ordinal {
						t.Fatalf("walk changed body/kind/identity: got %+v want %+v", fact, want)
					}
				}
				canonical, err := reader.Conversation(t.Context(), run.SessionID, 0, -1)
				if err != nil {
					t.Fatal(err)
				}
				for _, message := range canonical {
					assistantStorageNoReference(t, message.JSON)
					if message.ExecutionID != run.ExecutionID {
						t.Fatal("child Message entered parent canonical conversation")
					}
				}
			}
			checkSaved(store)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reader, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			checkSaved(reader)
			before, err := reader.Conversation(t.Context(), run.SessionID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			options.Task, options.SessionID, options.Progress = "continue after reopen", run.SessionID, nil
			resumed, err := Run(t.Context(), reader, nil, options)
			if err != nil || !resumed.Result.OK || resumed.Result.FinalText != "answer-4" {
				t.Fatalf("resumed run=%+v err=%v", resumed, err)
			}
			mu.Lock()
			wires := append([]json.RawMessage(nil), requests...)
			mu.Unlock()
			if len(wires) != 4 {
				t.Fatalf("HTTP requests=%d, want 4", len(wires))
			}
			assistantStorageContinuation(t, wires[3], before, responses)
			assistantStorageRawMirrors(t, workspace, run.ExecutionID, childID, wantEntities)
			assistantStorageRawProviderState(t, reader, workspace, run.ExecutionID, observations, 2)
			assistantStorageRawProviderState(t, reader, workspace, childID, observations, 1)
		})
	}
}

func assistantStorageFactKey(fact contracts.ExecutionFact) string {
	return fact.ExecutionID + "/" + fact.Conversation.EntityKind + "/" + fact.Conversation.LocalKey
}

func assistantStorageSSE(w http.ResponseWriter, responses bool, model string, call int) {
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(value any) { fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(value)) }
	body, thought := fmt.Sprintf("answer-%d", call), fmt.Sprintf("reasoning-%d", call)
	args := string(contracts.JSON(map[string]string{"code": `return await api.agents.run({task:"child"});`}))
	if !responses {
		send(map[string]any{"model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"reasoning_content": thought}}}})
		delta, finish := map[string]any{"content": body}, "stop"
		if call == 1 {
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": "spawn-script", "type": "function", "function": map[string]string{"name": "run_js", "arguments": args}}}
			finish = "tool_calls"
		}
		send(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	id := fmt.Sprintf("rs_%d", call)
	send(map[string]any{"type": "response.output_text.delta", "delta": body})
	send(map[string]any{"type": "response.reasoning_text.delta", "item_id": id, "output_index": 0, "content_index": 0, "delta": thought})
	send(map[string]any{"type": "response.reasoning_summary_text.delta", "item_id": id, "output_index": 0, "summary_index": 0, "delta": fmt.Sprintf("summary-%d", call)})
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("resp_%d", call), "model": model, "status": "completed", "output": assistantStorageResponseOutput(call)}})
}

func assistantStorageResponseOutput(call int) []any {
	body, thought, summary := fmt.Sprintf("answer-%d", call), fmt.Sprintf("reasoning-%d", call), fmt.Sprintf("summary-%d", call)
	id := fmt.Sprintf("rs_%d", call)
	args := string(contracts.JSON(map[string]string{"code": `return await api.agents.run({task:"child"});`}))
	output := []any{
		map[string]any{"type": "reasoning", "id": id, "content": []any{map[string]string{"type": "reasoning_text", "text": thought}}, "summary": []any{map[string]string{"type": "summary_text", "text": summary}}, "encrypted_content": fmt.Sprintf("opaque-%d", call), "future": map[string]bool{"kept": true}},
		map[string]any{"type": "message", "id": fmt.Sprintf("msg_%d", call), "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": body}}},
	}
	if call == 1 {
		output = append(output, map[string]any{"type": "function_call", "id": "fc_spawn", "call_id": "spawn-script", "name": "run_js", "namespace": "hjh", "arguments": args, "status": "completed"})
	}
	return output
}

func assistantStorageJSONEqual(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var a, b any
	for _, pair := range []struct {
		raw   json.RawMessage
		value *any
	}{{got, &a}, {want, &b}} {
		decoder := json.NewDecoder(bytes.NewReader(pair.raw))
		decoder.UseNumber()
		if err := decoder.Decode(pair.value); err != nil {
			t.Fatalf("decode %s: %v", pair.raw, err)
		}
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("JSON differs:\n got %s\nwant %s", got, want)
	}
}

func assistantStorageNoReference(t *testing.T, payload json.RawMessage) {
	t.Helper()
	fields := usageStorageObject(t, payload)
	for _, name := range []string{"thinkingRecordId", "providerStateRecordId", "resultRef"} {
		if _, exists := fields[name]; exists {
			t.Fatalf("public JSON exposed internal %s: %s", name, payload)
		}
	}
}

// rawDB is only the fresh t.TempDir database returned by delegationStore.
func assistantStorageRawMirrors(t *testing.T, workspace, parentID, childID string, want int) {
	t.Helper()
	rawDB, err := sql.Open("sqlite", "file:"+filepath.Join(workspace, "history.sqlite3")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	rows, err := rawDB.QueryContext(t.Context(), `SELECT r.record_id,r.execution_id,r.ordinal,r.payload_json,
		r.content_digest,m.content_digest,source.record_id,source.execution_id,source.ordinal,source.payload_json
		FROM semantic_records r
		JOIN messages m ON m.execution_id=r.execution_id AND m.message_ordinal=json_extract(r.payload_json,'$.locator.messageOrdinal')
		LEFT JOIN semantic_records source ON source.record_id=json_extract(r.payload_json,'$.thinkingRecordId')
		WHERE r.execution_id IN (?,?) AND r.kind='assistant_message' AND r.content_digest IS NOT NULL
		ORDER BY r.execution_id,r.ordinal`, parentID, childID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id, executionID, payload, digest, messageDigest string
		var ordinal int64
		var sourceID, sourceExecutionID, sourcePayload sql.NullString
		var sourceOrdinal sql.NullInt64
		if err := rows.Scan(&id, &executionID, &ordinal, &payload, &digest, &messageDigest, &sourceID, &sourceExecutionID, &sourceOrdinal, &sourcePayload); err != nil {
			t.Fatal(err)
		}
		count++
		fields := usageStorageObject(t, json.RawMessage(payload))
		for _, field := range []string{"content", "providerState"} {
			if _, exists := fields[field]; exists {
				t.Fatalf("raw mirror %s duplicates %s", id, field)
			}
		}
		if digest != messageDigest {
			t.Fatalf("mirror %s references a different Message", id)
		}
		var entity struct {
			Conversation contracts.ConversationMetadata `json:"conversation"`
			Request      contracts.RequestIdentity      `json:"request"`
		}
		if err := json.Unmarshal([]byte(payload), &entity); err != nil {
			t.Fatal(err)
		}
		if entity.Conversation.EntityKind != "thinking" {
			continue
		}
		if _, exists := fields["text"]; exists {
			t.Fatalf("raw thinking mirror %s duplicates text", id)
		}
		if !sourceID.Valid || sourceExecutionID.String != executionID || !sourceOrdinal.Valid || sourceOrdinal.Int64 >= ordinal {
			t.Fatalf("thinking source missing/crossed/not preceding for %s", id)
		}
		var source struct {
			Text         string                         `json:"text"`
			ThinkingKind string                         `json:"thinkingKind"`
			Conversation contracts.ConversationMetadata `json:"conversation"`
			Request      contracts.RequestIdentity      `json:"request"`
		}
		if err := json.Unmarshal([]byte(sourcePayload.String), &source); err != nil {
			t.Fatal(err)
		}
		if source.Text == "" || source.ThinkingKind == "" || source.Request != entity.Request || source.Conversation.LocalKey != entity.Conversation.LocalKey || source.Conversation.ItemIndex != entity.Conversation.ItemIndex {
			t.Fatalf("thinking source mismatched physical request/item for %s: %s", id, sourcePayload.String)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("raw mirrors=%d want=%d", count, want)
	}
}

func assistantStorageContinuation(t *testing.T, wire json.RawMessage, before []history.Message, responses bool) {
	t.Helper()
	var request struct {
		Messages []json.RawMessage `json:"messages"`
		Input    []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(wire, &request); err != nil {
		t.Fatal(err)
	}
	assistantIndex, inputIndex := 0, 0
	var assistants []contracts.Message
	for _, saved := range before {
		var message contracts.Message
		if err := json.Unmarshal(saved.JSON, &message); err != nil {
			t.Fatal(err)
		}
		if message.Role == "assistant" {
			assistants = append(assistants, message)
		}
	}
	if len(assistants) != 2 {
		t.Fatalf("canonical assistants=%d want=2", len(assistants))
	}
	if !responses {
		for _, raw := range request.Messages {
			var message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
			}
			if err := json.Unmarshal(raw, &message); err != nil {
				t.Fatal(err)
			}
			if message.Role != "assistant" {
				continue
			}
			if assistantIndex >= len(assistants) {
				t.Fatal("unexpected resumed assistant")
			}
			want := assistants[assistantIndex]
			if want.ProviderState == nil || want.ProviderState.Reasoning == nil || want.ProviderState.Reasoning.Field != "reasoning_content" || message.Reasoning != want.ProviderState.Reasoning.Text || message.Content != want.VisibleText() {
				t.Fatalf("resumed Chat body/state changed: %s want=%s", raw, contracts.JSON(want))
			}
			assistantIndex++
		}
		if assistantIndex != len(assistants) {
			t.Fatal("resumed Chat omitted assistant state")
		}
		return
	}
	for _, message := range assistants {
		if message.ProviderState == nil || len(message.ProviderState.ReplayItems) < 2 {
			t.Fatal("missing Responses opaque replay state")
		}
		items := message.ProviderState.ReplayItems
		found := false
		for inputIndex+len(items) <= len(request.Input) {
			var got, want any
			if err := json.Unmarshal(request.Input[inputIndex], &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(items[0], &want); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(got, want) {
				for index, item := range items {
					assistantStorageJSONEqual(t, request.Input[inputIndex+index], item)
				}
				inputIndex += len(items)
				found = true
				break
			}
			inputIndex++
		}
		if !found {
			t.Fatalf("resumed Responses omitted/reordered complete replay items: %s", wire)
		}
	}
}
