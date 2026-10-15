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
	"hjh/internal/provider/protocol"
)

func TestSingleRetryPreservesFailedAttemptAndExecutesOnlyRegeneratedTool(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("tool result"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests [][]byte
	var started []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		started = append(started, time.Now())
		call := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch call {
		case 1:
			sendChunk(w, map[string]any{"content": "FAILED_ATTEMPT_BODY", "reasoning_content": "FAILED_ATTEMPT_THINKING", "tool_calls": []any{map[string]any{"index": 0, "id": "bad-call", "function": map[string]any{"name": "read_file", "arguments": `{"path":"input.txt"`}}}}, "tool_calls")
		case 2:
			sendChunk(w, map[string]any{"content": "REGENERATED_NOTE", "reasoning_content": "REGENERATED_THINKING", "tool_calls": []any{map[string]any{"index": 0, "id": "good-call", "function": map[string]any{"name": "read_file", "arguments": `{"path":"input.txt"}`}}}}, "tool_calls")
		default:
			sendChunk(w, map[string]any{"content": "RETRY_FINISHED"}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	result, err := Run(context.Background(), store, chatFixtureProvider(server.URL, "secret", "retry-test", protocol.Options{ReasoningField: "reasoning_content"}), RunOptions{Task: "read input", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !result.Result.OK || result.Result.FinalText != "RETRY_FINISHED" {
		t.Fatalf("retry failed: %+v %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 || !bytes.Equal(requests[0], requests[1]) || started[1].Sub(started[0]) < 2*time.Second {
		t.Fatalf("request retry changed input or timing: calls=%d gap=%s", len(requests), started[1].Sub(started[0]))
	}
	if bytes.Contains(requests[2], []byte("FAILED_ATTEMPT")) || !bytes.Contains(requests[2], []byte("good-call")) || !bytes.Contains(requests[2], []byte("tool result")) {
		t.Fatalf("model continuation included failed response or lost successful tool: %s", requests[2])
	}
	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var failedKey, successKey string
	var failedThinking, goodThinking, notice, tool bool
	if err := store.WalkConversationCut(context.Background(), cut, func(f contracts.ExecutionFact) error {
		switch {
		case f.Text == "FAILED_ATTEMPT_BODY":
			if f.Phase != "superseded" {
				t.Errorf("failed attempt phase = %s", f.Phase)
			}
			failedKey = f.Conversation.LocalKey
		case f.Text == "FAILED_ATTEMPT_THINKING":
			failedThinking = f.Phase == "superseded"
		case f.Text == "REGENERATED_NOTE":
			successKey = f.Conversation.LocalKey
			if f.Request.RetryAttempt != 1 {
				t.Errorf("retry identity = %+v", f.Request)
			}
		case f.Text == "REGENERATED_THINKING":
			goodThinking = f.Request.RetryAttempt == 1 && strings.HasPrefix(f.Locator.Lane, "thinking/") && !strings.Contains(f.Locator.Lane, "retry/1")
		case f.Conversation.EntityKind == "notice" && strings.HasPrefix(f.Text, "RETRYING"):
			notice = true
		case f.Kind == "tool_call" && f.Name == "read_file":
			if tool {
				t.Error("regenerated tool executed twice")
			}
			tool = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if failedKey == "" || successKey == "" || failedKey == successKey || !failedThinking || !goodThinking || !notice || !tool {
		t.Fatalf("attempt display/history not separated: failed=%q good=%q thinking=%t/%t notice=%t tool=%t", failedKey, successKey, failedThinking, goodThinking, notice, tool)
	}
	messages, err := store.Conversation(context.Background(), result.SessionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 {
		t.Fatal("successful retry did not adopt its conversation")
	}
	for _, message := range messages {
		if bytes.Contains(contracts.JSON(message), []byte("FAILED_ATTEMPT")) {
			t.Fatal("failed response entered canonical conversation")
		}
	}
}

func TestMixedFailuresShareOneRetryBudget(t *testing.T) {
	store, workspace := delegationStore(t)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(429)
			fmt.Fprint(w, "temporarily busy")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]any{"content": "SECOND_FAILED_BODY", "tool_calls": []any{map[string]any{"index": 0, "id": "call", "function": map[string]any{"name": "read_file", "arguments": "{"}}}}, "tool_calls")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	result, err := Run(context.Background(), store, chatFixtureProvider(server.URL, "", "mixed-retry", protocol.Options{}), RunOptions{Task: "try", Workspace: workspace})
	if err == nil || result.Result.OK || calls != 2 || result.Result.Diagnostic == nil || result.Result.Diagnostic.ParseReason != "invalid_tool_arguments" {
		t.Fatalf("shared retry budget result=%+v calls=%d err=%v", result, calls, err)
	}
	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var partial bool
	if err := store.WalkConversationCut(context.Background(), cut, func(f contracts.ExecutionFact) error {
		if f.Text == "SECOND_FAILED_BODY" {
			partial = f.Phase == "incomplete" && f.Request.RetryAttempt == 1
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !partial {
		t.Fatal("final failed response lost recall/display evidence")
	}
}

func TestOutputLimitWithMalformedToolDoesNotRetry(t *testing.T) {
	for _, doneMarker := range []bool{true, false} {
		t.Run(fmt.Sprintf("doneMarker=%t", doneMarker), func(t *testing.T) {
			store, workspace := delegationStore(t)
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				sendChunk(w, map[string]any{"content": "OUTPUT_LIMIT_BODY", "tool_calls": []any{map[string]any{"index": 0, "id": "call", "function": map[string]any{"name": "read_file", "arguments": "{"}}}}, "length")
				if doneMarker {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			result, err := Run(context.Background(), store, chatFixtureProvider(server.URL, "", "limit-retry", protocol.Options{}), RunOptions{Task: "try", Workspace: workspace})
			if err == nil || calls != 1 || result.Result.Diagnostic == nil || result.Result.Diagnostic.FinishReason != "length" {
				t.Fatalf("output limit retried: result=%+v calls=%d err=%v", result, calls, err)
			}
			records, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				var observation contracts.Observation
				_ = json.Unmarshal(record.Payload, &observation)
				if observation.Kind == "model_retry" {
					t.Fatal("output limit archived for retry")
				}
			}
		})
	}
}
