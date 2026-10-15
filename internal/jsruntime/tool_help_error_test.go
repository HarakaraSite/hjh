package jsruntime_test

import (
	"context"
	"encoding/json"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
)

func TestToolHelpConfiguredMissingResourceDoesNotBlockNormalOperation(t *testing.T) {
	snapshot, err := extensions.CaptureDir(writeHelpExtensions(t, true))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Tools[1].HelpFile = "missing.md"
	worker := newHelpWorker(t, snapshot, nil)
	if _, err = worker.Call(context.Background(), "custom", json.RawMessage(`{"path":"normal"}`)); err != nil {
		t.Fatalf("help reference prevented normal operation: %v", err)
	}
	if _, err = worker.Call(context.Background(), "custom", json.RawMessage(`{"help":true}`)); err == nil {
		t.Fatal("configured missing help resource did not return its lookup error")
	}
}

func TestToolHelpModelOwnedObservationIsNotDuplicated(t *testing.T) {
	snapshot, err := extensions.CaptureDir(writeHelpExtensions(t, true))
	if err != nil {
		t.Fatal(err)
	}
	var observations []contracts.Observation
	worker := newHelpWorker(t, snapshot, func(o contracts.Observation) error {
		observations = append(observations, o)
		return nil
	})
	ctx := contracts.WithModelToolCall(context.Background(), "model-help-call")
	if _, err = worker.Call(ctx, "custom", json.RawMessage(`{"help":true}`)); err != nil {
		t.Fatal(err)
	}
	if len(observations) != 0 {
		t.Fatalf("runtime duplicated the model-owned help exchange or ran hooks: %+v", observations)
	}
	if _, err = worker.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": "return await tools.custom({help:true});"})); err != nil {
		t.Fatal(err)
	}
	var nested []contracts.Observation
	for _, o := range observations {
		if o.Kind == "tool" {
			nested = append(nested, o)
		}
	}
	if len(nested) != 2 || nested[0].Name != "custom" || nested[0].Parent != "model-help-call" || nested[1].ID != nested[0].ID || nested[1].Phase != "completed" {
		t.Fatalf("nested help did not retain a distinct exchange under model-owned call: %+v", observations)
	}
}
