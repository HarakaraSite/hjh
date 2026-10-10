package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

func textObservation(text string) contracts.Observation {
	return contracts.Observation{Kind: "assistant_text", Phase: "progress", Result: contracts.JSON(map[string]string{"text": text})}
}

func TestRunOutputModesAndModelSteps(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			out := runOutput{stdout: &stdout, stderr: &stderr, stream: stream}
			for _, event := range []contracts.Observation{
				{Kind: "model_request", Phase: "start"}, textObservation("同じ🙂"), textObservation("同じ🙂前半"),
				{Kind: "model_request", Phase: "end"}, {Kind: "tool", Name: "read_file", Phase: "started"},
				{Kind: "model_request", Phase: "start"}, textObservation("同じ🙂"), textObservation("同じ🙂最終\n"),
				{Kind: "model_request", Phase: "end"},
			} {
				if err := out.observe(event); err != nil {
					t.Fatal(err)
				}
			}
			if !stream && stdout.Len() != 0 {
				t.Fatal("non-stream mode printed partial text")
			}
			if err := out.finish("同じ🙂最終\n"); err != nil {
				t.Fatal(err)
			}
			expected := "同じ🙂最終\n\n"
			if stream {
				expected = "同じ🙂前半\n同じ🙂最終\n"
			}
			if stdout.String() != expected {
				t.Fatalf("stdout: %q, want %q", stdout.String(), expected)
			}
			if !strings.Contains(stderr.String(), "[tool] read_file started") {
				t.Fatal("missing tool progress")
			}
		})
	}
}

func TestRunOutputKeepsChildProgressOffRootTextStream(t *testing.T) {
	var stdout, stderr bytes.Buffer
	out := runOutput{stdout: &stdout, stderr: &stderr, stream: true}
	if err := out.observe(contracts.Observation{ID: "root-1", Kind: "execution", Name: "run", Phase: "started", Input: contracts.JSON(map[string]string{"sessionId": "root-session", "role": "default"})}); err != nil {
		t.Fatal(err)
	}
	if err := out.observe(textObservation("root prefix")); err != nil {
		t.Fatal(err)
	}
	for _, event := range []contracts.Observation{
		{ExecutionID: "child-1", ParentExecutionID: "root-1", Kind: "execution", Name: "run", Phase: "started", Input: contracts.JSON(map[string]string{"sessionId": "child-session", "role": "reviewer"})},
		{ExecutionID: "child-1", ParentExecutionID: "root-1", Kind: "model_request", Name: "openrouter.chat.completions", Phase: "start", ModelStep: 4},
		{ExecutionID: "child-1", ParentExecutionID: "root-1", Kind: "assistant_text", Phase: "progress", ModelStep: 4, Result: contracts.JSON(map[string]string{"text": "child-only body"})},
	} {
		if err := out.observe(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := out.observe(textObservation("root prefix tail")); err != nil {
		t.Fatal(err)
	}
	if err := out.finish(""); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "root prefix tail\n"; got != want {
		t.Fatalf("stdout = %q, want root-only stream %q", got, want)
	}
	for _, fragment := range []string{"child execution=child-1 parent=root-1", "role reviewer", "model_request openrouter.chat.completions start", "step 4"} {
		if !strings.Contains(stderr.String(), fragment) {
			t.Fatalf("stderr missing child progress %q: %s", fragment, stderr.String())
		}
	}
	if !strings.Contains(stderr.String(), "Session: root-session") || !strings.Contains(stderr.String(), "Execution: root-1") || !strings.Contains(stderr.String(), "Role: default") {
		t.Fatalf("root execution identifiers missing: %s", stderr.String())
	}
	if strings.Contains(stderr.String(), "child-session") || strings.Contains(stderr.String(), "child-only body") || strings.Contains(stderr.String(), "Session: child") || strings.Contains(stderr.String(), "Execution: child") {
		t.Fatalf("child input or text leaked into generic progress: %s", stderr.String())
	}
}

type liveTextWriter struct {
	bytes.Buffer
	writes chan string
}

func (w *liveTextWriter) WriteString(text string) (int, error) {
	n, err := w.Buffer.WriteString(text)
	w.writes <- text
	return n, err
}

// Pause the real provider adapter's SSE response after the first text chunk.
// The CLI renderer must deliver that chunk while the app run is still active.
func TestRunOutputDeliversProviderTextBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"前半🙂\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"後半。\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stdout := &liveTextWriter{writes: make(chan string, 8)}
	var stderr bytes.Buffer
	out := runOutput{stdout: stdout, stderr: &stderr, stream: true}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := app.Run(ctx, store, protocol.New(protocol.Config{ProviderID: "stream-fixture", API: "chat-completions", BaseURL: server.URL}), app.RunOptions{Task: "answer", Workspace: workspace, ModelID: "test/model", Progress: out.observe})
		if err == nil {
			err = out.finish(result.Result.FinalText)
		}
		done <- err
	}()
	select {
	case text := <-stdout.writes:
		if text != "前半🙂" {
			t.Fatalf("first visible text: %q", text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no text delivered before provider completion")
	}
	select {
	case err := <-done:
		t.Fatalf("run ended before SSE release: %v", err)
	default:
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run did not finish")
	}
	if stdout.String() != "前半🙂後半。\n" {
		t.Fatalf("stream text duplicated or lost: %q", stdout.String())
	}
}
