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
	"sync"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

func chatFixtureProvider(endpoint, apiKey, fixtureID string, options protocol.Options) *protocol.Provider {
	return protocol.New(protocol.Config{
		ProviderID:    fixtureID,
		StateIdentity: fixtureID,
		API:           "chat-completions",
		BaseURL:       endpoint,
		Path:          "/chat/completions",
		APIKey:        apiKey,
		Options:       options,
	})
}

func sendChunk(w http.ResponseWriter, delta any, finish any) {
	data := map[string]any{"model": "test/model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(data))
}
func sendTool(w http.ResponseWriter, id, code string) {
	args := string(contracts.JSON(map[string]any{"code": code}))
	mid := len(args) / 2
	fmt.Fprint(w, ": OPENROUTER PROCESSING\n\n")
	sendChunk(w, map[string]any{"content": "実行します。", "reasoning_details": []any{map[string]any{"type": "reasoning.encrypted", "data": "opaque", "format": "opaque-v1"}}, "tool_calls": []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]any{"name": "run_js", "arguments": args[:mid]}}}}, nil)
	sendChunk(w, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": args[mid:]}}}}, "tool_calls")
	fmt.Fprint(w, "data: [DONE]\n\n")
}
func TestRunPersistsRealToolPathAndResumes(t *testing.T) {
	workspace := t.TempDir()
	db := filepath.Join(workspace, "history.sqlite3")
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests []json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer transport-secret" {
			t.Error("credential not attached to transport")
		}
		var request json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		requests = append(requests, request)
		step := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch step {
		case 1:
			sendTool(w, "provider-first", `const r=await tools.read_file({path:'input.txt'}); await tools.write_file({path:'output.txt',text:r.text.toUpperCase()}); return await tools.read_file({path:'output.txt'});`)
		case 3:
			sendTool(w, "provider-resume", `await tools.edit_file({path:'output.txt',oldText:'ALPHA',newText:'ALPHA resumed'}); return await tools.read_file({path:'output.txt'});`)
		default:
			sendChunk(w, map[string]any{"content": "作業完了。"}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer server.Close()
	store, err := history.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	provider := chatFixtureProvider(server.URL, "transport-secret", "run-session-fixture", protocol.Options{
		ReasoningReplay: true,
		ReasoningField:  "reasoning_details",
	})
	first, err := Run(context.Background(), store, provider, RunOptions{Task: "process input", Workspace: workspace, ModelID: "test/model", Effort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Result.OK || first.Result.FinalText != "作業完了。" {
		t.Fatalf("result: %+v", first)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen through the product operation using persisted tool-call messages.
	store, err = history.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	second, err := Run(context.Background(), store, provider, RunOptions{Task: "continue previous work", SessionID: first.SessionID, Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != first.SessionID {
		t.Fatal("resume allocated another Session")
	}
	body, err := os.ReadFile(filepath.Join(workspace, "output.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ALPHA resumed" {
		t.Fatalf("file result %q", body)
	}
	conversation, err := store.Conversation(context.Background(), first.SessionID, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(conversation) == 0 || conversation[0].TurnNumber != 2 {
		t.Fatal("turn projection did not return resumed turn")
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(string(requests[1]), `"tool_call_id":"provider-first"`) || !strings.Contains(string(requests[1]), "reasoning_details") {
		t.Fatal("tool continuation or opaque replay was lost")
	}
	if !strings.Contains(string(requests[2]), "process input") || !strings.Contains(string(requests[2]), "作業完了。") {
		t.Fatal("resume omitted persisted conversation")
	}
	records, err := store.Records(context.Background(), first.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var parentLinked bool
	for _, r := range records {
		if strings.Contains(string(r.Payload), "transport-secret") || strings.Contains(string(r.Content), "transport-secret") {
			t.Fatal("credential reached history")
		}
		if len(r.Relations) > 0 && strings.Contains(string(r.Payload), "writeFile") {
			parentLinked = true
		}
	}
	if !parentLinked {
		t.Fatal("inner host write was not linked to its call")
	}
	configuration, err := store.Configuration(context.Background(), mustExecution(t, store, first.ExecutionID).ConfigurationID)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(requests[0], &wire); err != nil {
		t.Fatal(err)
	}
	var rootInstruction string
	for _, message := range wire.Messages {
		if message.Role == "system" {
			rootInstruction = message.Content
			break
		}
	}
	execution := mustExecution(t, store, first.ExecutionID)
	var rootModel contracts.ModelSelection
	if err := json.Unmarshal(execution.Model, &rootModel); err != nil {
		t.Fatal(err)
	}
	requireExecutionContextInstruction(t, rootInstruction, rootModel, "default", workspace)
	requireSavedExecutionContext(t, store, execution.ConfigurationID, rootInstruction, rootModel, "default", workspace)
	if !strings.Contains(string(configuration.Snapshot), `"name":"run_js"`) {
		t.Fatal("runtime tool absent from used configuration")
	}
	manifest, err := store.Context(context.Background(), first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var contextManifest map[string]any
	if err = json.Unmarshal(manifest, &contextManifest); err != nil {
		t.Fatal(err)
	}
	expected := contextManifest["digest"]
	delete(contextManifest, "digest")
	actual, err := canonicalDigest(contextManifest)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatal("context manifest digest does not cover its old-schema body")
	}
}

func mustExecution(t *testing.T, s *history.Store, id string) history.Execution {
	t.Helper()
	e, err := s.Execution(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStreamFailureRetainsTextWithoutAdopting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]any{"content": "途中の"}, nil)
		sendChunk(w, map[string]any{"content": "出力"}, nil)
		fmt.Fprint(w, "data: {\"error\":{\"message\":\"observed stream failure\"}}\n\n")
	}))
	defer server.Close()
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := Run(context.Background(), store, chatFixtureProvider(server.URL, "secret", "stream-failure-fixture", protocol.Options{}), RunOptions{Task: "task", Workspace: workspace, ModelID: "test/model"})
	if err == nil {
		t.Fatal("stream failure treated as completion")
	}
	session, err := store.Session(context.Background(), result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.TurnCount != 0 {
		t.Fatal("failed turn was adopted")
	}
	records, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	startedMarkers, incompleteBodies := 0, 0
	for _, r := range records {
		var event struct {
			Kind    string `json:"kind"`
			Phase   string `json:"phase"`
			Payload struct {
				Text string `json:"text"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(r.Payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind == "assistant_message" {
			switch event.Phase {
			case "started":
				if event.Payload.Text != "" {
					t.Fatalf("start marker retained body text: %q", event.Payload.Text)
				}
				startedMarkers++
			case "incomplete":
				if event.Payload.Text != "途中の出力" {
					t.Fatalf("incomplete terminal body: %q", event.Payload.Text)
				}
				incompleteBodies++
			default:
				t.Fatalf("unexpected permanent assistant phase %q", event.Phase)
			}
		}
	}
	if startedMarkers != 1 || incompleteBodies != 1 {
		t.Fatalf("assistant marker/incomplete records = %d/%d, want one each", startedMarkers, incompleteBodies)
	}
}

// OpenRouter documents that reasoning may exhaust the output budget before
// any visible answer, returning length. Neither partial nor empty answers are
// completed conversation turns.
func TestLengthAnswerRetainedWithoutAdopting(t *testing.T) {
	for _, text := range []string{"途中の回答", ""} {
		t.Run(text, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				sendChunk(w, map[string]any{"content": text}, "length")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			workspace := t.TempDir()
			store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			result, err := Run(context.Background(), store, chatFixtureProvider(server.URL, "", "length-fixture", protocol.Options{}), RunOptions{Task: "task", Workspace: workspace, ModelID: "test/model"})
			if err == nil || !strings.Contains(err.Error(), "finish_reason=length") {
				t.Fatalf("incomplete answer: %v", err)
			}
			if e := mustExecution(t, store, result.ExecutionID); e.Outcome != string(history.OutcomeFailed) {
				t.Fatalf("outcome: %s", e.Outcome)
			}
			session, err := store.Session(context.Background(), result.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if session.TurnCount != 0 {
				t.Fatal("incomplete answer adopted")
			}
			messages, err := store.Messages(context.Background(), result.ExecutionID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 2 {
				t.Fatalf("retained messages: %d", len(messages))
			}
			var assistant contracts.Message
			if err = json.Unmarshal(messages[1].JSON, &assistant); err != nil {
				t.Fatal(err)
			}
			if assistant.Role != "assistant" || assistant.VisibleText() != text {
				t.Fatal("partial answer lost")
			}
		})
	}
}

func TestToolFailureReturnedToModelBeforeNextStep(t *testing.T) {
	workspace := t.TempDir()
	step := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role       string          `json:"role"`
				Content    json.RawMessage `json:"content"`
				ToolCallID string          `json:"tool_call_id"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		step++
		w.Header().Set("Content-Type", "text/event-stream")
		switch step {
		case 1:
			sendTool(w, "missing-file-call", `return await tools.read_file({path:'missing.txt'});`)
		case 2:
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "tool" || last.ToolCallID != "missing-file-call" || !strings.Contains(string(last.Content), "missing.txt") {
				t.Error("failed tool result was not returned to the model")
			}
			sendTool(w, "recovery-call", `return await tools.write_file({path:'recovered.txt',text:'recovered'});`)
		case 3:
			sendChunk(w, map[string]any{"content": "recovered"}, "stop")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			t.Error("unexpected model step")
		}
	}))
	defer server.Close()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := Run(context.Background(), store, chatFixtureProvider(server.URL, "", "tool-recovery-fixture", protocol.Options{}), RunOptions{Task: "read then recover", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("recovery: %+v, %v", result, err)
	}
	data, err := os.ReadFile(filepath.Join(workspace, "recovered.txt"))
	if err != nil || string(data) != "recovered" {
		t.Fatalf("recovered file: %q, %v", data, err)
	}
	messages, err := store.Conversation(context.Background(), result.SessionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var failure contracts.Message
	if err := json.Unmarshal(messages[2].JSON, &failure); err != nil {
		t.Fatal(err)
	}
	var toolResults []contracts.ToolResult
	if err := json.Unmarshal(failure.Content, &toolResults); err != nil {
		t.Fatal(err)
	}
	if len(toolResults) != 1 || toolResults[0].Outcome != "error" || toolResults[0].CallID != "missing-file-call" {
		t.Fatal("failed step missing from completed conversation")
	}
}
