package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
)

func TestNestedToolResultLiveAndReplayFieldsMatch(t *testing.T) {
	store, workspace := delegationStore(t)
	const output = "alpha\nbeta\nこんにちは\n"
	if err := os.WriteFile(filepath.Join(workspace, "output.txt"), []byte(output), 0644); err != nil {
		t.Fatal(err)
	}
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if len(request.Messages) == 1 {
			return scriptedJS(`return await tools.read_file({path:"output.txt"});`), nil
		}
		return contracts.ModelResult{Text: "finished"}, nil
	})
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

	result, err := Run(context.Background(), store, provider, RunOptions{
		Task: "read output", Workspace: workspace, ModelID: "test/model", ExecutionDataOwner: owner,
	})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	var liveTool *contracts.ExecutionFact
	for index := range live {
		fact := &live[index]
		if fact.Kind == "tool_result" && fact.Name == "read_file" && fact.Phase == "completed" {
			copy := *fact
			liveTool = &copy
		}
	}
	mu.Unlock()
	if liveTool == nil {
		t.Fatal("live owner did not publish completed read_file result")
	}

	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var replayTool *contracts.ExecutionFact
	if err := store.WalkConversationCut(context.Background(), cut, func(fact contracts.ExecutionFact) error {
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
