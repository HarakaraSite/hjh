package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

func TestUsageStorageLocalSSEParentChildAndReopen(t *testing.T) {
	// Shapes observed in the histories cited by docs/plans/usage-storage.md:
	// Chat has no attribution; Responses includes attribution.
	// Values and Responses attribution contents are synthetic, not provider calls.
	for _, fixture := range []struct {
		provider, api string
		usage         json.RawMessage
	}{
		{"opencode-go-chat", "chat-completions", json.RawMessage(`{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":12}}`)},
		{"openai-chatgpt", "responses", json.RawMessage(`{"input_tokens":140,"output_tokens":35,"total_tokens":175,"input_tokens_details":{"cached_tokens":100,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":15},"attribution":{"items":{"synthetic-item":{"tokens":140}},"request_fields":{"instructions":20}}}`)},
	} {
		t.Run(fixture.provider, func(t *testing.T) {
			store, workspace := delegationStore(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Error(err)
					return
				}
				call := calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				const script = `return await api.agents.run({task:"child"});`
				args := string(contracts.JSON(map[string]string{"code": script}))
				if fixture.api == "chat-completions" {
					delta := map[string]any{"content": "Done."}
					finish := "stop"
					if call == 1 {
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "spawn-script", "type": "function", "function": map[string]string{"name": "run_js", "arguments": args}}}}
						finish = "tool_calls"
					}
					fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(map[string]any{"model": wire.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}))
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", contracts.JSON(map[string]any{"choices": []any{}, "usage": fixture.usage}))
					return
				}
				output := []any{map[string]any{"type": "message", "id": "msg_final", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "Done."}}}}
				if call == 1 {
					output = []any{map[string]any{"type": "function_call", "id": "fc_spawn", "call_id": "spawn-script", "name": "run_js", "namespace": "hjh", "arguments": args, "status": "completed"}}
				}
				fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(map[string]any{"type": "response.completed", "response": map[string]any{
					"id": fmt.Sprintf("resp_%d", call), "model": wire.Model, "status": "completed", "output": output, "usage": fixture.usage,
				}}))
			}))
			defer server.Close()
			configPath, modelID := standardLocalProviderConfig(t, workspace, fixture.provider, server.URL)
			var live []contracts.Observation
			result, err := Run(t.Context(), store, nil, RunOptions{Task: "parent", Workspace: workspace,
				ConfigPath: configPath, ProviderID: fixture.provider, ModelID: modelID,
				Progress: func(o contracts.Observation) error {
					if o.Kind == "usage" || o.Kind == "model_result" {
						live = append(live, o)
					}
					return nil
				}})
			if err != nil || !result.Result.OK || result.Result.FinalText != "Done." || calls.Load() != 3 {
				t.Fatalf("parent/child run: %+v calls=%d err=%v", result, calls.Load(), err)
			}
			children, err := store.Children(t.Context(), result.ExecutionID)
			if err != nil || len(children) != 1 || children[0].ParentExecutionID != result.ExecutionID || children[0].Outcome != "completed" {
				t.Fatalf("children=%+v err=%v", children, err)
			}
			childID := children[0].ID
			if len(live) != 6 {
				t.Fatalf("live usage/result observations=%d, want 6", len(live))
			}
			for _, o := range live {
				usage := o.Result
				if o.Kind == "model_result" {
					usage = usageStorageObject(t, o.Result)["usage"]
				}
				requireUsageStorageJSON(t, usage, fixture.usage)
				wantParent := ""
				if o.ExecutionID == childID {
					wantParent = result.ExecutionID
				}
				if o.ParentExecutionID != wantParent || o.ModelStep == 0 || o.RetryAttempt != 0 {
					t.Fatalf("live scope/step changed: %+v", o)
				}
			}

			var aggregate map[string]json.RawMessage
			if err := json.Unmarshal(fixture.usage, &aggregate); err != nil {
				t.Fatal(err)
			}
			delete(aggregate, "attribution")
			wantUsage := contracts.JSON(aggregate)
			checkSaved := func(reader *history.Store) {
				t.Helper()
				startsByExecution := map[string]string{}
				for _, executionID := range []string{result.ExecutionID, childID} {
					records, err := reader.Records(t.Context(), executionID, 0, -1)
					if err != nil {
						t.Fatal(err)
					}
					starts := map[string]string{}
					admissionLinked := false
					for _, record := range records {
						var o contracts.Observation
						if err := json.Unmarshal(record.Payload, &o); err != nil {
							t.Fatal(err)
						}
						if record.Kind == "execution_admission" && executionID == childID {
							for _, relation := range record.Relations {
								if relation.Relation == "parent_call" && relation.Resolved {
									parentRecords, err := reader.Records(t.Context(), result.ExecutionID, 0, -1)
									if err != nil {
										t.Fatal(err)
									}
									for _, parentRecord := range parentRecords {
										if parentRecord.ID == relation.TargetRecordID {
											admissionLinked = true
										}
									}
								}
							}
						}
						if o.Kind == "model_request" && o.Phase == "start" {
							starts[o.ID] = record.ID
							if startsByExecution[executionID] == "" {
								startsByExecution[executionID] = o.ID
							}
						}
					}
					if executionID == childID && !admissionLinked {
						t.Fatal("child admission lost parent call relation")
					}
					usageCount, resultCount := 0, 0
					for _, record := range records {
						var o contracts.Observation
						if err := json.Unmarshal(record.Payload, &o); err != nil {
							t.Fatal(err)
						}
						if o.Kind != "usage" && o.Kind != "model_result" {
							continue
						}
						if record.Kind != "model_result" || o.ExecutionID != executionID || o.ModelStep == 0 || o.RetryAttempt != 0 {
							t.Fatalf("saved scope/kind changed: %+v", o)
						}
						wantParent := ""
						if executionID == childID {
							wantParent = result.ExecutionID
						}
						if o.ParentExecutionID != wantParent {
							t.Fatalf("saved parent execution changed: %+v", o)
						}
						parentStart := starts[o.Parent]
						linked := false
						for _, relation := range record.Relations {
							if relation.Relation == "parent_call" && relation.TargetRecordID == parentStart && relation.Resolved {
								linked = true
							}
						}
						if parentStart == "" || !linked {
							t.Fatalf("request relation missing/crossed: %+v", record)
						}
						if o.Kind == "usage" {
							usageCount++
							requireUsageStorageJSON(t, o.Result, wantUsage)
						} else {
							resultCount++
							if _, exists := usageStorageObject(t, o.Result)["usage"]; exists {
								t.Fatalf("duplicate model_result usage: %s", o.Result)
							}
							matched := false
							for _, original := range live {
								if original.ExecutionID == executionID && original.ID == o.ID {
									wantResult := usageStorageObject(t, original.Result)
									delete(wantResult, "usage")
									requireUsageStorageJSON(t, o.Result, contracts.JSON(wantResult))
									matched = true
								}
							}
							if !matched {
								t.Fatalf("saved result has no matching live observation: %+v", o)
							}
							var saved struct {
								Request contracts.RequestIdentity `json:"request"`
							}
							if err := json.Unmarshal(record.Payload, &saved); err != nil {
								t.Fatal(err)
							}
							if saved.Request.ExecutionID != executionID || saved.Request.PhysicalRequestID != o.Parent || saved.Request.ModelStep != o.ModelStep || saved.Request.Lane != "parent" {
								t.Fatalf("model request identity changed: %+v", saved.Request)
							}
						}
						service := &Service{store: reader, snapshotOwner: NewSnapshotOwner()}
						one, err := service.Record(t.Context(), executionID, record.Ordinal)
						if err != nil || !reflect.DeepEqual(one, record) {
							t.Fatalf("Record differs from Records: %s err=%v", record.ID, err)
						}
					}
					wantCount := 2
					if executionID == childID {
						wantCount = 1
					}
					if usageCount != wantCount || resultCount != wantCount {
						t.Fatalf("%s usage/result=%d/%d want=%d/%d", executionID, usageCount, resultCount, wantCount, wantCount)
					}
				}
				if startsByExecution[result.ExecutionID] == "" || startsByExecution[result.ExecutionID] != startsByExecution[childID] {
					t.Fatalf("test did not exercise identical provider-instance physical IDs: %v", startsByExecution)
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

			// A direct provider caller still receives raw usage, independently of
			// the app's storage projection and live observer assertions above.
			direct := protocol.New(protocol.Config{ProviderID: fixture.provider, API: fixture.api, BaseURL: server.URL})
			raw, err := direct.Generate(t.Context(), contracts.ModelRequest{Model: contracts.ModelSelection{ModelID: modelID}, Messages: []contracts.Message{contracts.User("direct")}}, func(contracts.Observation) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			requireUsageStorageJSON(t, raw.Usage, fixture.usage)
		})
	}
}

func TestUsageStorageProjectionOnlyEditsDirectFields(t *testing.T) {
	raw := json.RawMessage(` {"total_tokens":9007199254740993,"provider_detail":{"attribution":{"keep":true},"value":1.2500},"attribution":{"remove":true}} `)
	observation := contracts.Observation{Kind: "usage", Result: raw}
	saved, err := observationForStorage(observation)
	if err != nil {
		t.Fatal(err)
	}
	requireUsageStorageJSON(t, saved.Result, json.RawMessage(`{"total_tokens":9007199254740993,"provider_detail":{"attribution":{"keep":true},"value":1.2500}}`))
	if !bytes.Contains(saved.Result, []byte("9007199254740993")) || !bytes.Contains(saved.Result, []byte("1.2500")) || !bytes.Equal(observation.Result, raw) {
		t.Fatalf("projection changed raw values or source: %s", saved.Result)
	}
	// No edits are imposed on raw JSON outside the confirmed object field.
	for _, raw := range []json.RawMessage{json.RawMessage(` {"total_tokens":0} `), json.RawMessage(`[1,{"attribution":2}]`), json.RawMessage(`null`)} {
		got, err := usageStorageJSON(raw, "attribution")
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("unmodified usage was transformed: %s err=%v", got, err)
		}
	}
}

func usageStorageObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func requireUsageStorageJSON(t *testing.T, got, want json.RawMessage) {
	t.Helper()
	var gotValue, wantValue any
	for _, pair := range []struct {
		raw json.RawMessage
		out *any
	}{{got, &gotValue}, {want, &wantValue}} {
		decoder := json.NewDecoder(bytes.NewReader(pair.raw))
		decoder.UseNumber()
		if err := decoder.Decode(pair.out); err != nil {
			t.Fatalf("decode usage %s: %v", pair.raw, err)
		}
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("usage mismatch:\n got %s\nwant %s", got, want)
	}
}
