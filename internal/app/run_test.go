package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
			sendTool(w, "provider-resume", `await tools.edit_file({path:'output.txt',edits:[{oldText:'ALPHA',newText:'ALPHA resumed'}]}); return await tools.read_file({path:'output.txt'});`)
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
	callRecords := map[string]string{}
	for _, r := range records {
		if strings.Contains(string(r.Payload), "transport-secret") || strings.Contains(string(r.Content), "transport-secret") {
			t.Fatal("credential reached history")
		}
		var observation contracts.Observation
		if err := json.Unmarshal(r.Payload, &observation); err != nil {
			t.Fatal(err)
		}
		if r.Kind == "effect_observation" && (observation.Name == "readFile" || observation.Name == "writeFile" || observation.Name == "editFile") {
			t.Fatalf("file tool recorded a per-file host effect: %s", r.Payload)
		}
		if r.Kind == "tool_call" {
			callRecords[observation.ID] = r.ID
		}
		if r.Kind == "tool_result" && observation.Name == "write_file" && observation.Phase == "completed" {
			for _, relation := range r.Relations {
				if relation.Relation == "parent_call" && relation.Resolved && relation.TargetRecordID == callRecords[observation.Parent] && observation.Parent != "" {
					parentLinked = true
				}
			}
		}
	}
	if !parentLinked {
		t.Fatal("inner write_file result was not linked to its run_js call")
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
	var selectedConfiguration struct {
		Agent struct {
			Tools []string `json:"tools"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(configuration.Snapshot, &selectedConfiguration); err != nil {
		t.Fatal(err)
	}
	var selectedRunJS bool
	for _, name := range selectedConfiguration.Agent.Tools {
		selectedRunJS = selectedRunJS || name == "run_js"
	}
	if !selectedRunJS {
		t.Fatal("runtime tool name absent from used configuration")
	}
	manifest, err := store.Context(context.Background(), first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var contextManifest struct {
		SchemaVersion int `json:"schemaVersion"`
		LatestRequest struct {
			RecordID      string `json:"recordId"`
			RecordOrdinal int64  `json:"recordOrdinal"`
		} `json:"latestRequest"`
	}
	if err = json.Unmarshal(manifest, &contextManifest); err != nil {
		t.Fatal(err)
	}
	if contextManifest.SchemaVersion != 1 || contextManifest.LatestRequest.RecordID == "" || contextManifest.LatestRequest.RecordOrdinal == 0 ||
		strings.Contains(string(manifest), `"requests"`) || strings.Contains(string(manifest), `"occurrences"`) || strings.Contains(string(manifest), `"splices"`) {
		t.Fatalf("Context did not reconstruct the compact latest-request locator: %s", manifest)
	}
	requestRecords, err := store.Records(context.Background(), first.ExecutionID, contextManifest.LatestRequest.RecordOrdinal, contextManifest.LatestRequest.RecordOrdinal)
	if err != nil || len(requestRecords) != 1 || requestRecords[0].ID != contextManifest.LatestRequest.RecordID || len(requestRecords[0].Content) == 0 {
		t.Fatalf("reconstructed latest request locator does not resolve to its saved body: %+v %v", requestRecords, err)
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
	var wire []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var readErr error
		wire, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
			return
		}
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
	startedMarkers, incompleteBodies, requestBodies := 0, 0, 0
	for _, r := range records {
		var event struct {
			Kind          string `json:"kind"`
			Phase         string `json:"phase"`
			SchemaVersion int    `json:"schemaVersion"`
			Payload       struct {
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
		if r.Kind == "model_request" && event.SchemaVersion == 1 {
			if !bytes.Equal(r.Content, wire) {
				t.Fatal("failed HTTP request body was not retained exactly")
			}
			requestBodies++
		}
	}
	if startedMarkers != 1 || incompleteBodies != 1 || requestBodies != 1 {
		t.Fatalf("assistant marker/incomplete/request body records = %d/%d/%d, want one each", startedMarkers, incompleteBodies, requestBodies)
	}
}

func TestLatestRequestBodySurvivesProviderCancellation(t *testing.T) {
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		received <- body
		<-r.Context().Done()
	}))
	defer server.Close()
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	type runResult struct {
		result RunResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, err := Run(ctx, store,
			chatFixtureProvider(server.URL, "", "cancelled-request-fixture", protocol.Options{}),
			RunOptions{Task: "cancel after request preparation", Workspace: workspace, ModelID: "test/model"})
		done <- runResult{result: result, err: err}
	}()
	var wire []byte
	select {
	case wire = <-received:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("provider did not receive the prepared request")
	}
	cancel()
	var completed runResult
	select {
	case completed = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled provider run did not finish")
	}
	if completed.err == nil {
		t.Fatal("cancelled provider run completed without an error")
	}
	execution, err := store.Execution(context.Background(), completed.result.ExecutionID)
	if err != nil || execution.Outcome != string(history.OutcomeCancelled) {
		t.Fatalf("cancelled execution = %+v, %v", execution, err)
	}
	records, err := store.Records(context.Background(), execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var retained int
	for _, record := range records {
		if record.Kind == "model_request" && record.Content != nil {
			if !bytes.Equal(record.Content, wire) {
				t.Fatal("cancelled provider request body differs from the retained latest wire")
			}
			retained++
		}
	}
	if retained != 1 {
		t.Fatalf("cancelled execution retained %d prepared request bodies, want 1", retained)
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
