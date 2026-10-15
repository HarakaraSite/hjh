package jsruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/jsruntime"
)

type contextEcho struct{}

func (contextEcho) Call(_ context.Context, operation string, input json.RawMessage) (json.RawMessage, error) {
	return contracts.JSON(map[string]any{"operation": operation, "input": input}), nil
}

type blockingContext struct{ started chan struct{} }

func (c blockingContext) Call(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	close(c.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestProjectRequestCancellationJoinsContextOperation(t *testing.T) {
	dir := makeExtensions(t)
	if err := os.WriteFile(filepath.Join(dir, "hooks.js"), []byte(`export async function projectRequest(_,api) { await api.context.get(); }`), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jsruntime.New(t.Context(), jsruntime.Options{Snapshot: snapshot, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	owner := blockingContext{started: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- worker.ProjectRequest(ctx, contracts.ProjectRequestHookInput{ModelStep: 1}, owner) }()
	<-owner.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
	if worker.Available() {
		t.Fatal("cancelled Worker remains available")
	}
}

func TestProjectRequestContextBridgeKeepsModuleStateWithoutTranscriptObservations(t *testing.T) {
	dir := makeExtensions(t)
	source := `let n=0;
export async function projectRequest({modelStep},api) {
  n++;
  if (n!==modelStep) throw new Error('module state lost');
  const view=await api.context.get();
  if(view.operation!=='get') throw new Error('context result lost');
  await api.context.replace({messages:[{role:'user',content:{kind:'text',text:'PRIVATE_TRANSCRIPT'}}]});
  return 'PRIVATE_RETURN';
}`
	if err := os.WriteFile(filepath.Join(dir, "hooks.js"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var observations []contracts.Observation
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: t.TempDir(),
		Observer: func(o contracts.Observation) error { observations = append(observations, o); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	for step := 1; step <= 2; step++ {
		if err := worker.ProjectRequest(context.Background(), contracts.ProjectRequestHookInput{ModelStep: step}, contextEcho{}); err != nil {
			t.Fatal(err)
		}
	}
	starts, ends := 0, 0
	for _, observation := range observations {
		encoded := string(contracts.JSON(observation))
		if strings.Contains(encoded, "PRIVATE_") {
			t.Fatalf("context body archived in observation: %s", encoded)
		}
		if strings.HasPrefix(observation.Name, "context.") {
			if observation.Phase == "started" {
				starts++
			}
			if observation.Phase == "completed" {
				ends++
			}
		}
	}
	if starts != 4 || ends != 4 {
		t.Fatalf("context operations not awaited: %d starts, %d completions", starts, ends)
	}
}
