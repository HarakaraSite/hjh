package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"hjh/internal/contracts"
)

func TestStandardResponsesProvidersPreserveReasoningAcrossToolCalls(t *testing.T) {
	for _, providerID := range []string{"openrouter-responses", "opencode-go-responses", "openai-responses", "openai-chatgpt"} {
		t.Run(providerID, func(t *testing.T) {
			store, workspace := delegationStore(t)
			if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("A small project."), 0644); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Model     string                       `json:"model"`
					Input     []map[string]json.RawMessage `json:"input"`
					Reasoning map[string]string            `json:"reasoning"`
					Store     *bool                        `json:"store"`
				}
				if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
					t.Errorf("decode model input: %v", err)
				}
				if wire.Store == nil || *wire.Store || wire.Reasoning["summary"] != "auto" {
					t.Errorf("stateless summary request: store=%v reasoning=%v", wire.Store, wire.Reasoning)
				}
				if providerID == "opencode-go-responses" && (r.Header.Get("x-opencode-session") == "" || r.Header.Get("User-Agent") != "hjh") {
					t.Errorf("Go session headers missing")
				}
				call := calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if call == 1 {
					tool := map[string]any{"type": "function_call", "id": "fc_read", "call_id": "read-call", "name": "read_file", "arguments": `{"path":"README.md"}`, "status": "completed"}
					if providerID == "openai-chatgpt" {
						tool["namespace"] = "hjh"
					}
					// The summary is public; encrypted_content is synthetic replay state.
					fmt.Fprint(w, "data: "+string(contracts.JSON(map[string]any{
						"type": "response.reasoning_summary_text.delta", "item_id": "rs_read", "output_index": 0, "summary_index": 0, "delta": "Inspecting README.",
					}))+"\n\n")
					fmt.Fprint(w, "data: "+string(contracts.JSON(map[string]any{
						"type": "response.completed", "response": map[string]any{"id": "resp_read", "model": wire.Model, "status": "completed", "output": []any{
							map[string]any{"type": "reasoning", "id": "rs_read", "encrypted_content": "synthetic-replay-state", "summary": []any{map[string]any{"type": "summary_text", "text": "Inspecting README."}}},
							tool,
						}},
					}))+"\n\n")
					return
				}
				foundReasoning, foundCall, foundResult := false, false, false
				for _, item := range wire.Input {
					switch string(item["type"]) {
					case `"reasoning"`:
						foundReasoning = string(item["id"]) == `"rs_read"` && string(item["encrypted_content"]) == `"synthetic-replay-state"` && strings.Contains(string(item["summary"]), "Inspecting README.")
					case `"function_call"`:
						foundCall = foundReasoning && string(item["id"]) == `"fc_read"` && string(item["call_id"]) == `"read-call"` && string(item["name"]) == `"read_file"`
					case `"function_call_output"`:
						foundResult = foundCall && string(item["call_id"]) == `"read-call"` && strings.Contains(string(item["output"]), "A small project.")
						if _, exists := item["name"]; exists {
							t.Error("function output must match by call_id without name")
						}
					}
				}
				if !foundReasoning || !foundCall || !foundResult {
					t.Errorf("tool follow-up omitted or reordered reasoning/call/result: %v/%v/%v", foundReasoning, foundCall, foundResult)
				}
				fmt.Fprint(w, "data: "+string(contracts.JSON(map[string]any{
					"type": "response.completed", "response": map[string]any{"id": "resp_final", "model": wire.Model, "status": "completed", "output": []any{
						map[string]any{"type": "message", "id": "msg_final", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Done."}}},
					}},
				}))+"\n\n")
			}))
			defer server.Close()
			configPath, modelID := standardLocalProviderConfig(t, workspace, providerID, server.URL)
			liveSummary := false
			result, err := Run(context.Background(), store, nil, RunOptions{Task: "Summarize README", Workspace: workspace,
				ConfigPath: configPath, ProviderID: providerID, ModelID: modelID,
				Progress: func(observation contracts.Observation) error {
					for _, item := range observation.Thinking {
						if item.Kind == "summary" && item.Text == "Inspecting README." {
							liveSummary = true
						}
					}
					return nil
				}})
			if err != nil || !result.Result.OK || result.Result.FinalText != "Done." || calls.Load() != 2 {
				t.Fatalf("Responses run = %+v, calls=%d, %v", result, calls.Load(), err)
			}
			cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
			if err != nil {
				t.Fatal(err)
			}
			restoredSummary := false
			if err := store.WalkConversationCut(context.Background(), cut, func(fact contracts.ExecutionFact) error {
				if fact.Conversation.EntityKind == "thinking" && fact.ThinkingKind == "summary" && fact.Text == "Inspecting README." {
					restoredSummary = true
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if !liveSummary || !restoredSummary {
				t.Fatalf("summary missing from live or restored history: live=%v restored=%v", liveSummary, restoredSummary)
			}
		})
	}
}
