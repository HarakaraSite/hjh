package corerun

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/api"
)

func TestOverlappingTerminalConsumersShareReadAndReleaseBody(t *testing.T) {
	run := &managedRun{}
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	load := func() (*api.AgentResult, error) {
		if reads.Add(1) == 1 {
			close(entered)
		}
		<-release
		return &api.AgentResult{Task: "saved task", FinalText: "saved body", Outcome: "final", OK: true}, nil
	}
	type response struct {
		result *api.AgentResult
		err    error
	}
	done := make(chan response, 2)
	read := func() { value, err := run.readTerminal(context.Background(), load); done <- response{value, err} }
	go read()
	<-entered
	go read()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run.resultMu.Lock()
		refs := run.resultFlight.refs
		run.resultMu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("second result consumer did not join")
		}
		runtime.Gosched()
	}
	close(release)
	for range 2 {
		response := <-done
		if response.err != nil || response.result == nil || response.result.FinalText != "saved body" || response.result.Task != "saved task" {
			t.Fatalf("shared terminal result: %+v", response)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("overlapping consumers repeated saved result read: %d", reads.Load())
	}
	run.resultMu.Lock()
	retained := run.resultFlight != nil
	run.resultMu.Unlock()
	if retained {
		t.Fatal("run index retained terminal body after its consumers ended")
	}
	// A later explicit read restores again rather than extending body lifetime.
	if _, err := run.readTerminal(context.Background(), load); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatal("later consumer did not restore its own result")
	}
}
