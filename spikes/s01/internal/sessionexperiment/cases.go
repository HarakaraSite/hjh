package sessionexperiment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hjh/spikes/s01/internal/configsnapshot"
	"hjh/spikes/s01/internal/session"
)

func (s *suite) adoption() (any, error) {
	r, err := s.normal("first", s.id, s.initial, false)
	if err != nil {
		return r, err
	}
	before, err := s.inspect("before_adoption", s.id, 0, 0, 3)
	if err != nil {
		return nil, err
	}
	if err = check(before.Info.Turns == 1 && before.Info.Adopted == 0 && len(before.Conversation.Pairs) == 0 && len(before.Turns) == 1 && before.Turns[0].Outcome == "completed" && before.Turns[0].Response != "" && !before.Turns[0].Adopted, "completed execution or unadopted draft readback mismatch"); err != nil {
		return before, err
	}
	store, err := open(s.db)
	if err != nil {
		return nil, err
	}
	view, err := store.Read(r.Run)
	if err == nil {
		err = session.Adopt(store, s.id, &r, nil)
	}
	err = errors.Join(err, store.Close())
	if err != nil {
		return nil, err
	}
	after, err := s.inspect("after_adoption", s.id, 0, 0, 3)
	if err != nil {
		return nil, err
	}
	return map[string]any{"turn": r.Turn, "configuration": r.Configuration, "savedExecutionStatus": view.Status, "before": "before_adoption-inspection.json", "after": "after_adoption-inspection.json", "execution": "first-execution.json"}, check(view.Status == "completed" && after.Info.Adopted == 1 && len(after.Conversation.Pairs) == 1 && len(after.Input) == 2 && after.Input[0].Role == "user" && after.Input[1].Role == "assistant" && after.Conversation.Pairs[0].Turn == r.Turn, "adoption did not restore a whole request/response pair")
}

func (s *suite) mixed() (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	failed, runErr := s.execute(ctx, "failed", s.id, `await tools.workflow(input); throw new Error('failure after saved report');`, s.server.URL, s.initial, false)
	cancel()
	if runErr == nil || !strings.Contains(runErr.Error(), "failure after saved report") {
		return failed, errors.New("expected parent failure after real report")
	}
	b, err := os.ReadFile(filepath.Join(s.Work, "failed.report.json"))
	if err != nil {
		return nil, err
	}
	ctx, cancel = context.WithTimeout(context.Background(), 8*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-s.arrived:
			cancel()
		case <-ctx.Done():
		}
	}()
	cancelled, cancelErr := s.execute(ctx, "cancelled", s.id, session.GeneratedJS, s.server.URL+"/wait", s.initial, false)
	cancel()
	<-done
	if !errors.Is(cancelErr, context.Canceled) {
		return cancelled, errors.Join(errors.New("expected HTTP context cancellation"), cancelErr)
	}
	pending, err := s.normal("unadopted", s.id, s.initial, false)
	if err != nil {
		return pending, err
	}
	v, err := s.inspect("mixed", s.id, 0, 0, 10)
	if err != nil {
		return nil, err
	}
	outcomes := map[string]string{}
	for _, t := range v.Turns {
		outcomes[t.ID] = t.Outcome
	}
	store, err := open(s.db)
	if err != nil {
		return nil, err
	}
	failedView, e1 := store.Read(failed.Run)
	cancelView, e2 := store.Read(cancelled.Run)
	err = errors.Join(e1, e2, store.Close())
	if err != nil {
		return nil, err
	}
	if err = writeJSON(filepath.Join(s.Out, "mixed-evidence.json"), map[string]any{"failed": failedView, "cancelled": cancelView, "reportReadback": json.RawMessage(b)}); err != nil {
		return nil, err
	}
	return map[string]any{"inspection": "mixed-inspection.json", "evidence": "mixed-evidence.json", "outcomes": outcomes, "retainedSideEffectBytes": len(b)}, check(v.Info.Turns == 4 && v.Info.Adopted == 1 && len(v.Conversation.Pairs) == 1 && outcomes[failed.Turn] == "failed" && outcomes[cancelled.Turn] == "cancelled" && outcomes[pending.Turn] == "completed" && failedView.Status == "failed" && cancelView.Status == "cancelled" && failed.Worker.HostOperationsAfterReturn == 0 && cancelled.Worker.HostOperationsAfterReturn == 0, "failed/cancelled/unadopted execution contaminated conversation or lost evidence")
}

func (s *suite) restart() (any, error) {
	a, err := s.runWorker("restart_a")
	if err != nil {
		return a, err
	}
	for _, edit := range []struct{ path, old, new string }{
		{filepath.Join(s.editable, "modules/catalog.js"), "tool-one", "tool-two"},
		{filepath.Join(s.editable, "hooks.js"), "hook-one", "hook-two"},
		{filepath.Join(s.editable, "hook-config.yaml"), "prepared", "changed"},
	} {
		if err = replace(edit.path, edit.old, edit.new); err != nil {
			return nil, err
		}
	}
	settings := configsnapshot.Settings{Model: "spike-model-b", Instruction: "resumed instruction", Extensions: s.editable, Hooks: "hooks.js"}
	if err = writeJSON(s.settings, settings); err != nil {
		return nil, err
	}
	b, err := s.runWorker("restart_b")
	if err != nil {
		return b, err
	}
	store, err := open(s.db)
	if err != nil {
		return nil, err
	}
	oldDescriptor, e1 := store.Configuration(a.Result.Configuration)
	newDescriptor, e2 := store.Configuration(b.Result.Configuration)
	sources := map[string]string{}
	for _, path := range []string{"modules/catalog.js", "hooks.js", "hook-config.yaml"} {
		data, e := store.ConfigurationAsset(a.Result.Configuration, path)
		if e != nil {
			store.Close()
			return nil, e
		}
		sources[path] = string(data)
	}
	vendor, e3 := store.ConfigurationAsset(a.Result.Configuration, "vendor/semver/index.mjs")
	err = errors.Join(e1, e2, e3, store.Close())
	if err != nil {
		return nil, err
	}
	if err = writeJSON(filepath.Join(s.Out, "configuration-archive.json"), map[string]any{"old": oldDescriptor, "current": newDescriptor, "oldSourceBytes": sources, "oldSemverBytes": len(vendor), "oldSemverSHA256": configsnapshot.Digest(vendor)}); err != nil {
		return nil, err
	}
	frozen, err := s.normal("frozen_after_edit", s.id, s.initial, false)
	if err != nil {
		return frozen, err
	}
	var oldValue, newValue, frozenValue struct {
		Report struct {
			ToolRevision string `json:"toolRevision"`
			HookRevision string `json:"hookRevision"`
			Note         string `json:"note"`
		} `json:"report"`
		Previous        string `json:"previous"`
		HistoryMessages int    `json:"historyMessages"`
		Instruction     string `json:"instruction"`
	}
	for _, pair := range []struct {
		data   json.RawMessage
		target any
	}{{a.Result.Worker.Value, &oldValue}, {b.Result.Worker.Value, &newValue}, {frozen.Worker.Value, &frozenValue}} {
		if err = json.Unmarshal(pair.data, pair.target); err != nil {
			return nil, err
		}
	}
	v, err := s.inspect("resumed", s.id, 0, 0, 10)
	if err != nil {
		return nil, err
	}
	return map[string]any{"oldPID": a.PID, "newPID": b.PID, "oldConfiguration": a.Result.Configuration, "currentConfiguration": b.Result.Configuration, "oldModel": oldDescriptor.Settings.Model, "currentModel": newDescriptor.Settings.Model, "archive": "configuration-archive.json", "inspection": "resumed-inspection.json", "oldSourceWorker": "frozen_after_edit-execution.json"}, check(a.PID != b.PID && a.Result.Configuration == s.initial.Configuration && a.Result.Configuration != b.Result.Configuration && oldDescriptor.Settings.Model == "spike-model-a" && newDescriptor.Settings.Model == "spike-model-b" && oldValue.Report.ToolRevision == "tool-one" && newValue.Report.ToolRevision == "tool-two" && newValue.Report.HookRevision == "hook-two" && newValue.Report.Note == "changed:hook-two" && oldValue.HistoryMessages == 2 && newValue.HistoryMessages == 4 && strings.Contains(newValue.Previous, "restart_a") && newValue.Instruction == "resumed instruction" && strings.Contains(sources["modules/catalog.js"], "tool-one") && strings.Contains(sources["hooks.js"], "hook-one") && sources["hook-config.yaml"] == "note: prepared\n" && len(vendor) > 0 && frozenValue.Report.ToolRevision == "tool-one" && frozenValue.Report.HookRevision == "hook-one" && frozenValue.Instruction == "initial instruction" && v.Info.Adopted == 3, "restart/configuration attribution or frozen source execution mismatch")
}
