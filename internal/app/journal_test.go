package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

func TestNestedToolResultLiveAndReplayFieldsMatch(t *testing.T) {
	store, workspace := delegationStore(t)
	const output = "<p>&alpha\nbeta\nこんにちは\n"
	if err := os.WriteFile(filepath.Join(workspace, "output.txt"), []byte(output), 0644); err != nil {
		t.Fatal(err)
	}
	var persistedRunJSResult []byte
	var nextModelToolResult string
	var mu sync.Mutex
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if len(request.Messages) == 1 {
			return scriptedJS("// Read output.txt and return its contents\n" + `return await tools.read_file({path:"output.txt"});`), nil
		}
		for _, message := range request.Messages {
			if message.Role != "tool" {
				continue
			}
			var results []contracts.ToolResult
			if err := json.Unmarshal(message.Content, &results); err != nil {
				return contracts.ModelResult{}, err
			}
			for _, result := range results {
				if result.Name == "run_js" {
					nextModelToolResult = result.Text
				}
			}
		}
		return contracts.ModelResult{Text: "finished"}, nil
	})
	owner := executiondata.NewOwner()
	var live []contracts.ExecutionFact
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

	result, err := Run(context.Background(), store, provider, RunOptions{
		Task: "read output", Workspace: workspace, ModelID: "test/model", ExecutionDataOwner: owner,
		Progress: func(observation contracts.Observation) error {
			if observation.Kind == "model_tool" && observation.Name == "run_js" && observation.Phase == "completed" {
				mu.Lock()
				persistedRunJSResult = append([]byte(nil), observation.Result...)
				mu.Unlock()
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	persistedRunJSResult = append([]byte(nil), persistedRunJSResult...)
	mu.Unlock()
	if len(persistedRunJSResult) == 0 || !bytes.Contains(persistedRunJSResult, []byte("<p>&")) {
		t.Fatalf("original run_js result did not retain HTML-sensitive text: %s", persistedRunJSResult)
	}
	if nextModelToolResult != string(persistedRunJSResult) {
		t.Fatalf("next model input changed run_js result bytes:\n got: %s\nwant: %s", nextModelToolResult, persistedRunJSResult)
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(persistedRunJSResult))
	storedRunJSResult, err := store.Content(context.Background(), digest)
	if err != nil || !bytes.Equal(storedRunJSResult, persistedRunJSResult) {
		t.Fatalf("stored result body = %s, err=%v; want exact Observation.Result %s", storedRunJSResult, err, persistedRunJSResult)
	}

	mu.Lock()
	var liveTool *contracts.ExecutionFact
	var liveScriptPreview string
	for index := range live {
		fact := &live[index]
		if fact.Kind == "tool_call" && fact.Name == "run_js" {
			liveScriptPreview = fact.Preview
		}
		if fact.Kind == "tool_result" && fact.Name == "read_file" && fact.Phase == "completed" {
			copy := *fact
			liveTool = &copy
		}
	}
	mu.Unlock()
	if liveScriptPreview != "Read output.txt and return its contents" {
		t.Fatalf("live run_js omitted the English purpose: %q", liveScriptPreview)
	}
	if liveTool == nil {
		t.Fatal("live owner did not publish completed read_file result")
	}

	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var replayTool *contracts.ExecutionFact
	var replayScriptPreview string
	if err := store.WalkConversationCut(context.Background(), cut, func(fact contracts.ExecutionFact) error {
		if fact.Kind == "tool_call" && fact.Name == "run_js" {
			replayScriptPreview = fact.Preview
		}
		if fact.Kind == "tool_result" && fact.Name == "read_file" && fact.Phase == "completed" {
			copy := fact
			replayTool = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replayTool == nil {
		t.Fatal("conversation replay omitted completed read_file result")
	}
	if replayScriptPreview != liveScriptPreview {
		t.Fatalf("saved run_js purpose changed on reopening: live=%q replay=%q", liveScriptPreview, replayScriptPreview)
	}
	want := contracts.ExecutionFact{
		RootExecutionID: liveTool.RootExecutionID, ExecutionID: liveTool.ExecutionID,
		ParentExecutionID: liveTool.ParentExecutionID, RootExecutionOrdinal: liveTool.RootExecutionOrdinal,
		Kind: liveTool.Kind, Phase: liveTool.Phase, Preview: "output.txt", Name: liveTool.Name,
		CallID: liveTool.CallID, ParentCallID: liveTool.ParentCallID, Outcome: liveTool.Outcome,
		Request: liveTool.Request, Conversation: liveTool.Conversation, Locator: liveTool.Locator,
	}
	got := contracts.ExecutionFact{
		RootExecutionID: replayTool.RootExecutionID, ExecutionID: replayTool.ExecutionID,
		ParentExecutionID: replayTool.ParentExecutionID, RootExecutionOrdinal: replayTool.RootExecutionOrdinal,
		Kind: replayTool.Kind, Phase: replayTool.Phase, Text: replayTool.Text, Preview: replayTool.Preview, Name: replayTool.Name,
		CallID: replayTool.CallID, ParentCallID: replayTool.ParentCallID, Outcome: replayTool.Outcome,
		Request: replayTool.Request, Conversation: replayTool.Conversation, Locator: replayTool.Locator,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("live tool fact and replay differ:\n live: %+v\n replay: %+v", want, got)
	}
	if liveTool.Text != "" || liveTool.Preview != "output.txt" || liveTool.Outcome != "completed" || liveTool.ParentCallID == "" {
		t.Fatalf("live tool result lacks display fields: %+v", liveTool)
	}
	record, err := NewService(store, nil, RunOptions{Workspace: workspace}).Record(context.Background(), result.ExecutionID, liveTool.Locator.Ordinal)
	if err != nil {
		t.Fatal(err)
	}
	if record.Content != nil || strings.Contains(string(record.Payload), `"resultRef"`) {
		t.Fatalf("tool result detail leaked the internal reference or duplicated Content: %+v", record)
	}
	var saved struct {
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
	}
	if err := json.Unmarshal(record.Payload, &saved); err != nil || saved.Result.Text != output {
		t.Fatalf("separate tool detail did not retain the original output: %s, %v", record.Payload, err)
	}
}

func TestModelResultIsReadableWhenGenerateStopsBeforeAppendingMessage(t *testing.T) {
	store, workspace := delegationStore(t)
	owner := executiondata.NewOwner()
	var mu sync.Mutex
	var live []contracts.ExecutionFact
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

	provider := delegationProvider(func(_ context.Context, _ contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		if err := observe(contracts.Observation{ID: "request-1-text-1", Parent: "request-1", Kind: "assistant_text",
			Phase: "progress", Result: contracts.JSON(map[string]string{"text": "partial body"}), Text: "partial body",
			PhysicalRequestID: "request-1"}); err != nil {
			return contracts.ModelResult{}, err
		}
		if err := observe(contracts.Observation{ID: "request-1-result", Parent: "request-1", Kind: "model_result", Name: "test.responses",
			Phase: "end", Result: contracts.JSON(map[string]any{"text": "final body", "finishReason": "stop", "calls": []any{}}),
			Text: "final body", Thinking: []contracts.ThinkingItem{{Key: "reasoning", Text: "final thought"}},
			PhysicalRequestID: "request-1"}); err != nil {
			return contracts.ModelResult{}, err
		}
		return contracts.ModelResult{}, errors.New("stopped after model_result")
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "answer", Workspace: workspace,
		ModelID: "test/model", ExecutionDataOwner: owner})
	if err == nil {
		t.Fatal("run unexpectedly completed after provider stopped before returning")
	}

	mu.Lock()
	var liveAnswer *contracts.ExecutionFact
	for index := range live {
		fact := &live[index]
		if fact.Text == "final body" && fact.Conversation.EntityKind == "assistant" {
			copy := *fact
			liveAnswer = &copy
		}
	}
	mu.Unlock()
	if liveAnswer == nil || liveAnswer.Name != "test.responses" || liveAnswer.Phase != "completed" || liveAnswer.Request.ModelStep != 1 ||
		liveAnswer.Request.PhysicalRequestID != "request-1" {
		t.Fatalf("live model result fact = %#v", liveAnswer)
	}

	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut.Executions) != 1 || len(cut.Executions[0].States) != 0 {
		t.Fatalf("model result left an active answer state: %#v", cut.Executions)
	}
	var replayAnswer, replayThought *contracts.ExecutionFact
	if err := store.WalkConversationCut(context.Background(), cut, func(fact contracts.ExecutionFact) error {
		if fact.Conversation.EntityKind == "assistant" && fact.Text == "final body" {
			copy := fact
			replayAnswer = &copy
		}
		if fact.Conversation.EntityKind == "thinking" && fact.Text == "final thought" {
			copy := fact
			replayThought = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replayAnswer == nil || replayAnswer.Name != liveAnswer.Name || replayAnswer.Phase != "completed" || replayAnswer.Conversation.LocalKey != liveAnswer.Conversation.LocalKey ||
		replayAnswer.Conversation.FirstSequence != liveAnswer.Conversation.FirstSequence ||
		replayAnswer.Request.PhysicalRequestID != liveAnswer.Request.PhysicalRequestID {
		t.Fatalf("replayed result fact = %#v, live = %#v", replayAnswer, liveAnswer)
	}
	if replayThought == nil || replayThought.Phase != "completed" || replayThought.Conversation.LocalKey != "parent/1/reasoning" {
		t.Fatalf("replayed result thinking = %#v", replayThought)
	}
}

func TestLatestRequestBodyMatchesHTTPAndPhysicalRequestKeepsMetadataOnly(t *testing.T) {
	workspace := t.TempDir()
	var wire []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		wire, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]any{"content": "finished"}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	provider := chatFixtureProvider(server.URL, "local-test-key", "body-reuse-test", protocol.Options{})
	result, err := Run(t.Context(), store, provider, RunOptions{Task: strings.Repeat("Retain this saved input.\n", 512), Workspace: workspace, ModelID: "test/model"})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	logicalRequests, bodyOwners, physicalRequests := 0, 0, 0
	for _, record := range records {
		var metadata struct {
			SchemaVersion int             `json:"schemaVersion"`
			Kind          string          `json:"kind"`
			Phase         string          `json:"phase"`
			Input         json.RawMessage `json:"input"`
		}
		if err = json.Unmarshal(record.Payload, &metadata); err != nil {
			t.Fatal(err)
		}
		if record.Kind == "context_item" {
			t.Fatal("request preparation retained a context_item archive record")
		}
		if record.Kind == "context_budget" && record.Content != nil {
			t.Fatal("context budget retained a duplicate WireBody")
		}
		if record.Kind == "model_request" {
			if metadata.SchemaVersion == 1 {
				logicalRequests++
				if record.Content != nil {
					bodyOwners++
					if !bytes.Equal(record.Content, wire) {
						t.Fatal("saved latest request body differs from bytes received by HTTP")
					}
				}
			} else if metadata.Kind == "model_request" && metadata.Phase == "start" {
				physicalRequests++
				if len(metadata.Input) != 0 || record.Content != nil {
					t.Fatal("physical request retained a duplicate WireBody")
				}
			}
		}
	}
	if logicalRequests != 1 || bodyOwners != 1 || physicalRequests != 1 {
		t.Fatalf("request history counts = logical %d/body owners %d/physical %d, want 1/1/1", logicalRequests, bodyOwners, physicalRequests)
	}
	contextView, err := store.SessionContext(t.Context(), result.SessionID)
	if err != nil || contextView.LatestRequest == nil {
		t.Fatalf("latest /context request = %+v, %v", contextView.LatestRequest, err)
	}
	request := contextView.LatestRequest
	if request.ItemCount != request.MessageCount+request.ToolContractCount+request.InstructionCount+1 || request.WireBytes != int64(len(wire)) {
		t.Fatalf("/context request counts = %+v", request)
	}
}
