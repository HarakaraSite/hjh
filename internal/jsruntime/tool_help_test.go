package jsruntime_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/jsruntime"
)

func writeHelpExtensions(t *testing.T, withHelp bool) string {
	t.Helper()
	dir := t.TempDir()
	config := extensions.Config{
		DefaultRole: "custom-role",
		Roles:       map[string]extensions.Role{"custom-role": {Tools: []string{"run_js", "custom", "undocumented"}}},
		Hooks:       json.RawMessage(`"hooks.js"`),
		Tools: []extensions.Tool{
			{Name: "custom", Module: "custom.js", Export: "custom", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
			{Name: "undocumented", Module: "custom.js", Export: "custom", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		},
	}
	if withHelp {
		config.RunJSHelpFile = "run-js.md"
		config.Tools[0].HelpFile = "custom-help.md"
	}
	files := map[string][]byte{
		"config.json":    contracts.JSON(config),
		"custom-help.md": []byte("# Custom help v1\nUse a path. 日本語\n"),
		"run-js.md":      []byte("# Generated JavaScript help v1\nReturn awaited results.\n"),
		"custom.js": []byte(`export function custom(args) {
  if (args.help === true) throw new Error('implementation executed for help');
  if (typeof args.path !== 'string') throw new Error('path is required');
  return {path:args.path, before:args.before, executed:true};
}`),
		"hooks.js": []byte(`export function beforeTool({name,args}) {
  if (args.help === true) throw new Error('beforeTool executed for help');
  return {...args,before:true};
}
export function afterTool({name,args,result}) {
  if (args.help === true) throw new Error('afterTool executed for help');
  return {...result,after:true};
}`),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newHelpWorker(t *testing.T, snapshot extensions.Snapshot, observer contracts.Observer) *jsruntime.Worker {
	t.Helper()
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: t.TempDir(), Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close() })
	return worker
}

func callHelp(t *testing.T, worker *jsruntime.Worker, name string) extensions.ToolHelp {
	t.Helper()
	data, err := worker.Call(context.Background(), name, json.RawMessage(`{"help":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var help extensions.ToolHelp
	if err = json.Unmarshal(data, &help); err != nil {
		t.Fatal(err)
	}
	return help
}

func TestToolHelpDirectAndNestedSkipHooksAndImplementationWithObservations(t *testing.T) {
	snapshot, err := extensions.CaptureDir(writeHelpExtensions(t, true))
	if err != nil {
		t.Fatal(err)
	}
	var observations []contracts.Observation
	worker := newHelpWorker(t, snapshot, func(o contracts.Observation) error {
		observations = append(observations, o)
		return nil
	})
	if got := worker.Definitions(); len(got) != 3 || got[0].Name != "run_js" || got[1].Name != "custom" || got[2].Name != "undocumented" {
		t.Fatalf("selected tools changed: %+v", got)
	}
	for _, name := range []string{"custom", "run_js", "undocumented"} {
		observations = nil
		help := callHelp(t, worker, name)
		if help.Name != name || help.Available != (name != "undocumented") || help.Text == "" {
			t.Fatalf("help result = %+v", help)
		}
		if len(observations) != 2 || observations[0].Kind != "tool" || observations[0].Name != name || observations[0].Phase != "started" ||
			observations[1].ID != observations[0].ID || observations[1].Phase != "completed" || string(observations[0].Input) != `{"help":true}` {
			t.Fatalf("direct help lost its ordinary tool exchange or executed hooks: %+v", observations)
		}
		var observed extensions.ToolHelp
		if err = json.Unmarshal(observations[1].Result, &observed); err != nil || observed != help {
			t.Fatalf("observed result differs from help: %+v, %v", observed, err)
		}
	}

	observations = nil
	data, err := worker.Call(context.Background(), "run_js", contracts.JSON(map[string]string{"code": `return {custom:await tools.custom({help:true}), builtin:await tools.run_js({help:true}), missing:await tools.undocumented({help:true})};`}))
	if err != nil {
		t.Fatal(err)
	}
	var nested struct{ Custom, Builtin, Missing extensions.ToolHelp }
	if err = json.Unmarshal(data, &nested); err != nil || !nested.Custom.Available || !nested.Builtin.Available || nested.Missing.Available {
		t.Fatalf("nested help = %s, %v", data, err)
	}
	root := observations[0].ID
	for _, name := range []string{"custom", "run_js", "undocumented"} {
		var pair []contracts.Observation
		for _, o := range observations {
			if o.Kind == "tool" && o.Name == name && o.Parent == root {
				pair = append(pair, o)
			}
		}
		if len(pair) != 2 || pair[0].Phase != "started" || pair[1].Phase != "completed" || pair[0].ID != pair[1].ID || string(pair[0].Input) != `{"help":true}` {
			t.Fatalf("nested help lacks its parented exchange: %+v", observations)
		}
	}
	// Only the outer normal run_js invocation uses hooks. Inner help calls do not.
	for _, o := range observations {
		if o.Kind != "tool" && (o.Kind != "hook" || o.Parent != root) {
			t.Fatalf("nested help ran a hook or implementation operation: %+v", o)
		}
	}

	for _, args := range []json.RawMessage{json.RawMessage(`{"path":"normal"}`), json.RawMessage(`{"path":"normal","help":false}`)} {
		data, err = worker.Call(context.Background(), "custom", args)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Path                    string
			Before, After, Executed bool
		}
		if err = json.Unmarshal(data, &result); err != nil || result.Path != "normal" || !result.Before || !result.After || !result.Executed {
			t.Fatalf("normal tool/hook execution changed: %s, %v", data, err)
		}
	}
}

func TestToolHelpFrozenCaptureAndNextCapture(t *testing.T) {
	dir := writeHelpExtensions(t, true)
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Edit after capture but before Worker creation to distinguish capture from
	// live disk reads even during runtime initialization.
	for _, name := range []string{"custom-help.md", "run-js.md"} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte("updated help v2\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	worker := newHelpWorker(t, snapshot, nil)
	original := callHelp(t, worker, "custom")
	if !strings.Contains(original.Text, "v1") || !strings.Contains(callHelp(t, worker, "run_js").Text, "v1") {
		t.Fatal("worker read edited disk instead of captured bytes")
	}
	if err = os.WriteFile(filepath.Join(dir, "custom-help.md"), []byte("updated help v3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if callHelp(t, worker, "custom") != original {
		t.Fatal("existing worker help changed after disk edit")
	}
	next, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	nextWorker := newHelpWorker(t, next, nil)
	if got := callHelp(t, nextWorker, "custom"); got.Text != "updated help v3\n" {
		t.Fatalf("next capture missed update: %+v", got)
	}
	if got := callHelp(t, nextWorker, "run_js"); got.Text != "updated help v2\n" {
		t.Fatalf("next builtin capture missed update: %+v", got)
	}
}

func TestToolHelpUnregisteredAndExactSelectedTools(t *testing.T) {
	dir := writeHelpExtensions(t, false)
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	worker := newHelpWorker(t, snapshot, nil)
	for _, name := range []string{"custom", "run_js"} {
		if got := callHelp(t, worker, name); got.Available || got.Name != name || got.Text == "" {
			t.Fatalf("unregistered help = %+v", got)
		}
	}
	selected := []string{"run_js"}
	onlyJS, err := extensions.CaptureWithOptions(filepath.Join(dir, "config.json"), extensions.CaptureOptions{Tools: &selected})
	if err != nil {
		t.Fatal(err)
	}
	jsWorker := newHelpWorker(t, onlyJS, nil)
	if _, err = jsWorker.Call(context.Background(), "custom", json.RawMessage(`{"help":true}`)); err == nil {
		t.Fatal("direct help exposed an unselected tool")
	}
	data, err := jsWorker.Call(context.Background(), "run_js", contracts.JSON(map[string]string{"code": `let blocked=false; try { await __invokeDirect('custom','{"help":true}',''); } catch (_) { blocked=true; } return {names:Object.keys(tools),blocked};`}))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Names   []string
		Blocked bool
	}
	if err = json.Unmarshal(data, &result); err != nil || !reflect.DeepEqual(result.Names, selected) || !result.Blocked {
		t.Fatalf("generated help exposed an unselected tool: %s, %v", data, err)
	}
	empty := []string{}
	emptySnapshot, err := extensions.CaptureWithOptions(filepath.Join(dir, "config.json"), extensions.CaptureOptions{Tools: &empty})
	if err != nil {
		t.Fatal(err)
	}
	emptyWorker := newHelpWorker(t, emptySnapshot, nil)
	if len(emptyWorker.Definitions()) != 0 {
		t.Fatal("help added tools to an empty selection")
	}
	for _, name := range []string{"run_js", "custom"} {
		if _, err = emptyWorker.Call(context.Background(), name, json.RawMessage(`{"help":true}`)); err == nil {
			t.Fatalf("empty selection exposed %s help", name)
		}
	}
}
