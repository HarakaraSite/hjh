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
	"hjh/internal/extensions"
)

func TestStandardOpenCodeChatSavesPublishesAndReplaysThinking(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("A small project."), 0644); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wire struct {
			Messages []map[string]json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Errorf("decode model input: %v", err)
		}
		call := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			fmt.Fprint(w, `data: {"model":"deepseek-v4.1-flash","choices":[{"index":0,"delta":{"reasoning_content":"Inspect README."}}]}`+"\n\n"+
				`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"read-call","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\n"+
				"data: [DONE]\n\n")
			return
		}
		foundReasoning, foundResult := false, false
		for _, message := range wire.Messages {
			if string(message["reasoning_content"]) == `"Inspect README."` {
				foundReasoning = true
			}
			if string(message["role"]) == `"tool"` && strings.Contains(string(message["content"]), "A small project.") {
				foundResult = true
			}
		}
		if !foundReasoning || !foundResult {
			t.Errorf("tool follow-up omitted thinking or result: thinking=%v result=%v", foundReasoning, foundResult)
		}
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"reasoning_content":"Summarize the result."}}]}`+"\n\n"+
			`data: {"choices":[{"index":0,"delta":{"content":"Done."},"finish_reason":"stop"}]}`+"\n\n"+
			"data: [DONE]\n\n")
	}))
	defer server.Close()
	configPath, modelID := standardLocalProviderConfig(t, workspace, "opencode-go-chat", server.URL)
	live := map[string]bool{}
	result, err := Run(context.Background(), store, nil, RunOptions{Task: "Summarize README", Workspace: workspace,
		ConfigPath: configPath, ProviderID: "opencode-go-chat", ModelID: modelID, Effort: "max",
		Progress: func(observation contracts.Observation) error {
			for _, item := range observation.Thinking {
				live[item.Text] = true
			}
			return nil
		}})
	if err != nil || !result.Result.OK || result.Result.FinalText != "Done." || calls.Load() != 2 {
		t.Fatalf("Chat run = %+v, calls=%d, %v", result, calls.Load(), err)
	}
	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	restored := map[string]bool{}
	if err := store.WalkConversationCut(context.Background(), cut, func(fact contracts.ExecutionFact) error {
		if fact.Conversation.EntityKind == "thinking" && fact.ThinkingKind == "reasoning" {
			restored[fact.Text] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Inspect README.", "Summarize the result."} {
		if !live[text] || !restored[text] {
			t.Fatalf("thinking missing from live or restored history: %q, live=%v restored=%v", text, live[text], restored[text])
		}
	}
}

// Use the shipped configuration through the real factory, retaining its adapter
// settings while routing model requests to the local test server.
func standardLocalProviderConfig(t *testing.T, workspace, providerID, endpoint string) (string, string) {
	t.Helper()
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var definitions map[string]map[string]any
	if err := json.Unmarshal(document["providers"], &definitions); err != nil {
		t.Fatal(err)
	}
	definition := definitions[providerID]
	modelID := definition["defaultModel"].(string)
	// Keep the shipped adapter settings; replace only the endpoint, auth and
	// capacity metadata needed for the local model response.
	definition["baseURL"] = endpoint
	definition["auth"] = map[string]string{"kind": "none"}
	delete(definition, "catalog")
	definition["models"] = []map[string]any{{"id": modelID, "contextTokens": 100000}}
	document["providers"] = contracts.JSON(definitions)
	if err := os.WriteFile(configPath, contracts.JSON(document), 0644); err != nil {
		t.Fatal(err)
	}
	return configPath, modelID
}
