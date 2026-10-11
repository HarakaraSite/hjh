package jsruntime_test

import (
	"context"
	"encoding/json"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/jsruntime"
)

func TestModelCallOwnsRuntimeRootAndKeepsRepeatedNestedCalls(t *testing.T) {
	snapshot, err := extensions.CaptureDir(makeExtensions(t))
	if err != nil {
		t.Fatal(err)
	}
	var observations []contracts.Observation
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{
		Snapshot: snapshot, Workspace: t.TempDir(),
		Observer: func(o contracts.Observation) error { observations = append(observations, o); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	_, err = worker.Call(contracts.WithModelToolCall(context.Background(), "declared-call"), "run_js",
		json.RawMessage(`{"code":"await tools.count({}); return await tools.count({});"}`))
	if err != nil {
		t.Fatal(err)
	}
	starts, ends := map[string]int{}, map[string]int{}
	for _, o := range observations {
		if o.Kind != "tool" {
			continue
		}
		if o.Name != "count" || o.Parent != "declared-call" {
			t.Fatalf("runtime duplicated model root or lost child parent: %+v", o)
		}
		if o.Phase == "started" {
			starts[o.ID]++
		} else if o.Phase == "completed" {
			ends[o.ID]++
		}
	}
	if len(starts) != 2 || len(ends) != 2 {
		t.Fatalf("two actual same-name calls must remain distinct: starts=%v ends=%v", starts, ends)
	}
	for id, count := range starts {
		if count != 1 || ends[id] != 1 {
			t.Fatalf("nested call %s did not retain one start/result", id)
		}
	}
}
