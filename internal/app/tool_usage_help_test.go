package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

// Every run below uses an injected provider and a temporary history store. Keep
// both user instruction lookup and extension installation away from real settings.
func toolUsageHelpConfig(t *testing.T, selected []string) (*history.Store, string, string, map[string]any) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store, workspace := delegationStore(t)
	// Deliberately outside the workspace: --config must not select its policy.
	dir := t.TempDir()
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	config["roles"].(map[string]any)["default"].(map[string]any)["tools"] = selected
	return store, workspace, path, config
}

func toolUsageHelpCall(name string, args any) contracts.ModelResult {
	return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "help-flow-call", Name: name, Arguments: contracts.JSON(args)}}}
}

func toolUsageHelpResult(t *testing.T, req contracts.ModelRequest) string {
	t.Helper()
	var results []contracts.ToolResult
	if err := json.Unmarshal(req.Messages[len(req.Messages)-1].Content, &results); err != nil || len(results) != 1 {
		t.Fatalf("tool result: %+v, %v", results, err)
	}
	if results[0].Outcome != "success" {
		t.Fatalf("tool failed: %+v", results[0])
	}
	return results[0].Text
}

func toolUsageHelpRequire(t *testing.T, req contracts.ModelRequest, name, text string, available bool) {
	t.Helper()
	var result extensions.ToolHelp
	if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &result); err != nil {
		t.Fatal(err)
	}
	if result.Name != name || result.Available != available || (available && result.Text != text) || (!available && result.Text == "") {
		t.Errorf("help = %+v; want name=%q available=%v text=%q", result, name, available, text)
	}
}

func toolUsageHelpSelected(t *testing.T, req contracts.ModelRequest, names ...string) {
	t.Helper()
	var got []string
	for _, tool := range req.Tools {
		got = append(got, tool.Name)
	}
	if !reflect.DeepEqual(got, names) {
		t.Errorf("selected tools = %v, want %v (help must not add a tool)", got, names)
	}
}

func TestToolUsageHelpPolicyStartupFrozenChildAndResume(t *testing.T) {
	store, workspace, configPath, config := toolUsageHelpConfig(t, []string{"read_file", "run_agent"})
	contextWrite(t, configPath, string(contracts.JSON(config)))
	const base = "REPLACING_BASE\n"
	const oldPolicy = "WORKSPACE_POLICY_ONE\n"
	const newPolicy = "WORKSPACE_POLICY_TWO\n"
	policyPath := filepath.Join(workspace, ".hjh", "instructions", "tool-usage.md")
	contextWrite(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hjh", "instructions", "base.md"), base)
	contextWrite(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hjh", "instructions", "tool-usage.md"), "USER_POLICY_NOT_SELECTED")
	contextWrite(t, filepath.Join(filepath.Dir(configPath), ".hjh", "instructions", "tool-usage.md"), "CONFIG_DIRECTORY_POLICY_NOT_SELECTED")
	contextWrite(t, policyPath, oldPolicy)
	contextWrite(t, filepath.Join(workspace, "note.txt"), "ordinary read still works\n")
	counts := map[string]int{}
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := req.Messages[0].VisibleText()
		// A resumed root has the original first message; identify the last user.
		for _, message := range req.Messages {
			if message.Role == "user" {
				task = message.VisibleText()
			}
		}
		counts[task]++
		wantPolicy := newPolicy
		if task == "policy parent" {
			wantPolicy = oldPolicy
		}
		if !strings.HasPrefix(req.Instruction, base+"\n\n"+wantPolicy) || strings.Contains(req.Instruction, "NOT_SELECTED") {
			t.Errorf("%s: replacing base/workspace policy order or selection incorrect: %s", task, req.Instruction)
		}
		if task == "policy parent" && strings.Contains(req.Instruction, newPolicy) {
			t.Error("current execution reread the edited policy")
		}
		if task != "empty child" {
			policyIndex := strings.Index(req.Instruction, wantPolicy)
			guidelineIndex := strings.Index(req.Instruction, "## Active tool guidelines")
			if guidelineIndex <= policyIndex {
				t.Errorf("%s: policy must precede active guidelines", task)
			}
		}
		switch task {
		case "policy parent":
			toolUsageHelpSelected(t, req, "read_file", "run_agent")
			switch counts[task] {
			case 1:
				contextWrite(t, policyPath, newPolicy)
				return toolUsageHelpCall("read_file", map[string]any{"path": "note.txt"}), nil
			case 2:
				if !strings.Contains(toolUsageHelpResult(t, req), "ordinary read still works") {
					t.Error("normal dedicated read failed")
				}
				return toolUsageHelpCall("run_agent", map[string]any{"task": "selected child", "tools": []string{"read_file"}}), nil
			case 3:
				return toolUsageHelpCall("run_agent", map[string]any{"task": "empty child", "tools": []string{}}), nil
			}
		case "selected child":
			toolUsageHelpSelected(t, req, "read_file")
		case "empty child":
			toolUsageHelpSelected(t, req)
			if strings.Contains(req.Instruction, "## Active tool guidelines") {
				t.Error("tool-free child received active guidelines")
			}
		case "policy resume":
			toolUsageHelpSelected(t, req, "read_file", "run_agent")
		default:
			return contracts.ModelResult{}, fmt.Errorf("unexpected task %q", task)
		}
		return contracts.ModelResult{Text: "completed " + task}, nil
	})
	ctx := t.Context()
	first, err := Run(ctx, store, provider, RunOptions{Task: "policy parent", Workspace: workspace, ConfigPath: configPath, ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("first: %+v, %v", first, err)
	}
	second, err := Run(ctx, store, provider, RunOptions{Task: "policy resume", SessionID: first.SessionID, ConfigPath: configPath})
	if err != nil || !second.Result.OK {
		t.Fatalf("resume: %+v, %v", second, err)
	}
	children, err := store.Children(ctx, first.ExecutionID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children: %+v, %v", children, err)
	}
	for _, execution := range append(children, mustExecution(t, store, first.ExecutionID), mustExecution(t, store, second.ExecutionID)) {
		want := newPolicy
		if execution.ID == first.ExecutionID {
			want = oldPolicy
		}
		saved, err := store.Configuration(ctx, execution.ConfigurationID)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot struct {
			InstructionComponents []struct {
				Identity string `json:"identity"`
				Text     string `json:"text"`
				Source   struct {
					Path   string `json:"path"`
					Digest string `json:"digest"`
				} `json:"source"`
			} `json:"instructionComponents"`
		}
		if err := json.Unmarshal(saved.Snapshot, &snapshot); err != nil {
			t.Fatal(err)
		}
		foundComponent := false
		for _, component := range snapshot.InstructionComponents {
			if component.Identity == "instruction:tool-usage-policy" {
				foundComponent = component.Text == want && component.Source.Path == policyPath && component.Source.Digest == extensions.Digest([]byte(want))
			}
		}
		records, err := store.Records(ctx, execution.ID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		foundSource, foundWire := false, false
		for _, record := range records {
			if record.Kind == "resource_revision" && string(record.Content) == want && strings.Contains(string(record.Payload), policyPath) && strings.Contains(string(record.Payload), extensions.Digest([]byte(want))) {
				foundSource = true
			}
			if record.Kind == "model_request" && strings.Contains(string(record.Content), strings.TrimSpace(want)) {
				foundWire = true
			}
		}
		if !foundComponent || !foundSource || !foundWire {
			t.Errorf("%s policy component/source/prepared request = %v/%v/%v", execution.Task, foundComponent, foundSource, foundWire)
		}
	}
}

func TestToolUsageHelpDirectNestedCaptureAndCanonicalResume(t *testing.T) {
	store, workspace, configPath, config := toolUsageHelpConfig(t, []string{"run_js", "read_file", "run_agent", "probe"})
	dir := filepath.Dir(configPath)
	const oldHelp = "READ_HELP_ONE: captured instructions\n"
	const newHelp = "READ_HELP_TWO: refreshed instructions\n"
	const jsHelp = "RUN_JS_HELP_ON_DEMAND\n"
	const dormantHelp = "UNREQUESTED_HELP_BODY_NOT_ARCHIVED"
	const module = `// MODULE_SOURCE_NOT_ARCHIVED
import * as fs from "node:fs";
import * as path from "node:path";
export async function readFile(args){return {text:await fs.promises.readFile(path.resolve(process.cwd(),args.path),"utf8")}}
export function probe(){return {normal:true}}
`
	for _, entry := range config["tools"].([]any) {
		tool := entry.(map[string]any)
		if tool["name"] == "read_file" {
			tool["module"], tool["export"], tool["helpFile"] = "help-probe.js", "readFile", "read-help.md"
		}
	}
	config["tools"] = append(config["tools"].([]any),
		map[string]any{"name": "probe", "module": "help-probe.js", "export": "probe", "inputSchema": map[string]any{"type": "object"}},
		map[string]any{"name": "dormant", "module": "help-probe.js", "export": "probe", "helpFile": "dormant.md", "inputSchema": map[string]any{"type": "object"}})
	config["runJSHelpFile"] = "js-help.md"
	contextWrite(t, configPath, string(contracts.JSON(config)))
	contextWrite(t, filepath.Join(dir, "help-probe.js"), module)
	contextWrite(t, filepath.Join(dir, "read-help.md"), oldHelp)
	contextWrite(t, filepath.Join(dir, "js-help.md"), jsHelp)
	contextWrite(t, filepath.Join(dir, "dormant.md"), dormantHelp)
	contextWrite(t, filepath.Join(workspace, "note.txt"), "NORMAL_READ_TEXT\n")
	counts := map[string]int{}
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := ""
		for _, message := range req.Messages {
			if message.Role == "user" {
				task = message.VisibleText()
			}
		}
		counts[task]++
		step := counts[task]
		if strings.Contains(req.Instruction, oldHelp) || strings.Contains(req.Instruction, newHelp) || strings.Contains(req.Instruction, jsHelp) || strings.Contains(req.Instruction, dormantHelp) {
			t.Error("help was eagerly injected into startup instructions")
		}
		switch task {
		case "help parent":
			toolUsageHelpSelected(t, req, "run_js", "read_file", "run_agent", "probe")
			switch step {
			case 1:
				// The provider first sees the captured instruction. A disk edit now
				// must affect child/resume, but not either help entry in this Worker.
				contextWrite(t, filepath.Join(dir, "read-help.md"), newHelp)
				return toolUsageHelpCall("read_file", map[string]bool{"help": true}), nil
			case 2:
				toolUsageHelpRequire(t, req, "read_file", oldHelp, true)
				return scriptedJS("// Retrieve captured help through the selected tool.\nreturn await tools.read_file({help:true});"), nil
			case 3:
				toolUsageHelpRequire(t, req, "read_file", oldHelp, true)
				return toolUsageHelpCall("run_js", map[string]bool{"help": true}), nil
			case 4:
				toolUsageHelpRequire(t, req, "run_js", jsHelp, true)
				return toolUsageHelpCall("read_file", map[string]any{"path": "note.txt"}), nil
			case 5:
				if !strings.Contains(toolUsageHelpResult(t, req), "NORMAL_READ_TEXT") {
					t.Error("direct normal read did not execute")
				}
				return scriptedJS("// Read normally with help explicitly disabled.\nreturn await tools.read_file({path:'note.txt',help:false});"), nil
			case 6:
				if !strings.Contains(toolUsageHelpResult(t, req), "NORMAL_READ_TEXT") {
					t.Error("nested normal read did not execute")
				}
				return toolUsageHelpCall("probe", map[string]bool{"help": true}), nil
			case 7:
				toolUsageHelpRequire(t, req, "probe", "", false)
				return toolUsageHelpCall("probe", map[string]any{}), nil
			case 8:
				if toolUsageHelpResult(t, req) != `{"normal":true}` {
					t.Error("unregistered help prevented normal tool use")
				}
				return toolUsageHelpCall("run_agent", map[string]any{"task": "help child", "tools": []string{"read_file"}}), nil
			case 9:
				if !strings.Contains(toolUsageHelpResult(t, req), "child acquired current help") {
					t.Error("child help result was not collected")
				}
			}
		case "help child":
			toolUsageHelpSelected(t, req, "read_file")
			if step == 1 {
				return toolUsageHelpCall("read_file", map[string]bool{"help": true}), nil
			}
			toolUsageHelpRequire(t, req, "read_file", newHelp, true)
			return contracts.ModelResult{Text: "child acquired current help"}, nil
		case "help resume":
			toolUsageHelpSelected(t, req, "run_js", "read_file", "run_agent", "probe")
			if step == 1 {
				if !strings.Contains(string(contracts.JSON(req.Messages)), "READ_HELP_ONE") {
					t.Error("canonical resume lost the previously acquired help")
				}
				return toolUsageHelpCall("read_file", map[string]bool{"help": true}), nil
			}
			toolUsageHelpRequire(t, req, "read_file", newHelp, true)
		default:
			return contracts.ModelResult{}, fmt.Errorf("unexpected task %q", task)
		}
		return contracts.ModelResult{Text: "completed " + task}, nil
	})
	ctx := t.Context()
	first, err := Run(ctx, store, provider, RunOptions{Task: "help parent", Workspace: workspace, ConfigPath: configPath, ModelID: "test/model"})
	if err != nil || !first.Result.OK || counts["help parent"] != 9 || counts["help child"] != 2 {
		t.Fatalf("parent: %+v, counts=%v, %v", first, counts, err)
	}
	second, err := Run(ctx, store, provider, RunOptions{Task: "help resume", SessionID: first.SessionID, ConfigPath: configPath})
	if err != nil || !second.Result.OK || counts["help resume"] != 2 {
		t.Fatalf("resume: %+v, counts=%v, %v", second, counts, err)
	}
	children, err := store.Children(ctx, first.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children: %+v, %v", children, err)
	}
	for _, execution := range append(children, mustExecution(t, store, first.ExecutionID), mustExecution(t, store, second.ExecutionID)) {
		saved, err := store.Configuration(ctx, execution.ConfigurationID)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot map[string]json.RawMessage
		if err := json.Unmarshal(saved.Snapshot, &snapshot); err != nil {
			t.Fatal(err)
		}
		if _, exists := snapshot["tools"]; exists {
			t.Error("configuration archived detailed tool definitions")
		}
		var agent struct {
			Tools []string `json:"tools"`
		}
		if err := json.Unmarshal(snapshot["agent"], &agent); err != nil {
			t.Fatal(err)
		}
		wantTools := []string{"run_js", "read_file", "run_agent", "probe"}
		if execution.ID == children[0].ID {
			wantTools = []string{"read_file"}
		}
		if !reflect.DeepEqual(agent.Tools, wantTools) {
			t.Errorf("saved selected tools = %v, want %v", agent.Tools, wantTools)
		}
		for _, marker := range []string{"READ_HELP_ONE", "READ_HELP_TWO", "RUN_JS_HELP_ON_DEMAND", dormantHelp, "MODULE_SOURCE_NOT_ARCHIVED"} {
			if strings.Contains(string(saved.Snapshot), marker) {
				t.Errorf("configuration eagerly archived %q", marker)
			}
		}
		records, err := store.Records(ctx, execution.ID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		directHelpObserved, nestedHelpObserved := false, false
		for _, record := range records {
			if record.Kind == "tool_result" {
				var observation contracts.Observation
				if err := json.Unmarshal(record.Payload, &observation); err != nil {
					t.Fatal(err)
				}
				if observation.Name == "read_file" && strings.Contains(string(observation.Result), "READ_HELP_ONE") {
					directHelpObserved = directHelpObserved || observation.Parent == ""
					nestedHelpObserved = nestedHelpObserved || observation.Parent != ""
				}
			}
			body := string(record.Payload) + string(record.Content)
			if strings.Contains(body, dormantHelp) || strings.Contains(body, "MODULE_SOURCE_NOT_ARCHIVED") {
				t.Error("unrequested help or extension module was archived")
			}
			if record.Kind == "resource_revision" && (strings.Contains(body, "READ_HELP_") || strings.Contains(body, jsHelp)) {
				t.Error("help source was automatically archived as a resource")
			}
		}
		if execution.ID == first.ExecutionID && (!directHelpObserved || !nestedHelpObserved) {
			t.Errorf("direct/nested help lost normal saved tool observations: %v/%v", directHelpObserved, nestedHelpObserved)
		}
		messages, err := store.Messages(ctx, execution.ID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		want := "READ_HELP_TWO"
		if execution.ID == first.ExecutionID {
			want = "READ_HELP_ONE"
		}
		found := false
		for _, saved := range messages {
			var message contracts.Message
			if err := json.Unmarshal(saved.JSON, &message); err != nil {
				t.Fatal(err)
			}
			if message.Role == "tool" && strings.Contains(string(message.Content), want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s acquired help did not persist as a normal tool message", execution.Task)
		}
	}
}

// Extract the code actually returned by help, rather than maintaining a second
// copy that could continue passing after a distributed recipe stops working.
func toolUsageHelpExample(t *testing.T, text, heading string) string {
	t.Helper()
	_, section, found := strings.Cut(text, "## "+heading+"\n")
	if !found {
		t.Fatalf("help section %q not found", heading)
	}
	_, fenced, found := strings.Cut(section, "```js\n")
	if !found {
		t.Fatalf("help section %q has no JavaScript example", heading)
	}
	code, _, found := strings.Cut(fenced, "```")
	if !found {
		t.Fatalf("help section %q has no closing fence", heading)
	}
	return code
}

func TestToolUsageHelpBundledStdlibExample(t *testing.T) {
	store, workspace, configPath, config := toolUsageHelpConfig(t, []string{"run_js"})
	contextWrite(t, configPath, string(contracts.JSON(config)))
	step := 0
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		switch step {
		case 1:
			return toolUsageHelpCall("run_js", map[string]bool{"help": true}), nil
		case 2:
			var help extensions.ToolHelp
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &help); err != nil || !help.Available {
				t.Fatalf("stdlib help: %+v %v", help, err)
			}
			return scriptedJS(toolUsageHelpExample(t, help.Text, "Built-in modules and timers")), nil
		case 3:
			var got struct{ Path, Text string }
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &got); err != nil || filepath.Dir(got.Path) != workspace || got.Text != "stdlib example\n" {
				t.Fatalf("stdlib example result: %+v %v", got, err)
			}
			data, err := os.ReadFile(got.Path)
			if err != nil || string(data) != got.Text {
				t.Fatalf("stdlib example file: %q %v", data, err)
			}
			return contracts.ModelResult{Text: "stdlib help example completed"}, nil
		}
		return contracts.ModelResult{}, fmt.Errorf("unexpected stdlib example step %d", step)
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "stdlib help example", Workspace: workspace, ConfigPath: configPath, ModelID: "test/model"})
	if err != nil || !result.Result.OK || step != 3 {
		t.Fatalf("stdlib help example: %+v step=%d %v", result, step, err)
	}
}

func TestToolUsageHelpBundledRunJSExamples(t *testing.T) {
	store, workspace, configPath, config := toolUsageHelpConfig(t, []string{"run_js", "read_file", "write_file", "edit_file"})
	contextWrite(t, configPath, string(contracts.JSON(config)))
	contextWrite(t, filepath.Join(workspace, "docs/design/extensions.md"), "EXTENSION_EXAMPLE_SOURCE\n")
	contextWrite(t, filepath.Join(workspace, "docs/design/base-instructions.md"), "BASE_EXAMPLE_SOURCE\n")
	step := 0
	var help string
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		switch step {
		case 1:
			return toolUsageHelpCall("run_js", map[string]bool{"help": true}), nil
		case 2:
			var result extensions.ToolHelp
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &result); err != nil || !result.Available || result.Name != "run_js" {
				t.Fatalf("bundled run_js help: %+v, %v", result, err)
			}
			help = result.Text
			return scriptedJS(toolUsageHelpExample(t, help, "Independent reads in parallel")), nil
		case 3:
			var reads []struct {
				Path   string `json:"path"`
				Text   string `json:"text"`
				Offset int    `json:"offset"`
			}
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &reads); err != nil || len(reads) != 2 || !strings.HasSuffix(reads[0].Path, "docs/design/extensions.md") || reads[0].Text != "EXTENSION_EXAMPLE_SOURCE\n" || reads[1].Text != "BASE_EXAMPLE_SOURCE\n" || reads[0].Offset != 1 || reads[1].Offset != 1 {
				t.Fatalf("bundled parallel reads: %+v, %v", reads, err)
			}
			// Substitute only the example's temporary destination. All executed
			// operations stay in this test's workspace, not global /tmp.
			code := toolUsageHelpExample(t, help, "Read JSON and aggregate it")
			return scriptedJS(strings.ReplaceAll(code, "/tmp/hjh-help-", filepath.Join(workspace, "hjh-help-"))), nil
		case 4:
			var aggregate struct {
				Path  string   `json:"path"`
				IDs   []string `json:"ids"`
				Total int      `json:"total"`
			}
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &aggregate); err != nil || aggregate.Total != 5 || !reflect.DeepEqual(aggregate.IDs, []string{"a", "b"}) || filepath.Dir(aggregate.Path) != workspace {
				t.Fatalf("bundled JSON aggregate: %+v, %v", aggregate, err)
			}
			code := toolUsageHelpExample(t, help, "Dependent read, edit, and verify")
			return scriptedJS(strings.ReplaceAll(code, "/tmp/hjh-help-", filepath.Join(workspace, "hjh-help-"))), nil
		case 5:
			var edit struct {
				Path         string `json:"path"`
				Text         string `json:"text"`
				Replacements int    `json:"replacements"`
				Edits        []struct {
					Replacements int `json:"replacements"`
				} `json:"edits"`
			}
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &edit); err != nil || edit.Text != "status: ready\n" || edit.Replacements != 1 || len(edit.Edits) != 1 || edit.Edits[0].Replacements != 1 || filepath.Dir(edit.Path) != workspace {
				t.Fatalf("bundled dependent read/edit: %+v, %v", edit, err)
			}
			data, err := os.ReadFile(edit.Path)
			if err != nil || string(data) != edit.Text {
				t.Errorf("bundled edit not saved: %q, %v", data, err)
			}
			return contracts.ModelResult{Text: "bundled examples completed"}, nil
		}
		return contracts.ModelResult{}, fmt.Errorf("unexpected bundled example step %d", step)
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Task: "bundled JS examples", Workspace: workspace, ConfigPath: configPath, ModelID: "test/model"})
	if err != nil || !result.Result.OK || step != 5 {
		t.Fatalf("bundled examples: %+v, step=%d, %v", result, step, err)
	}
}

func TestToolUsageHelpBundledSpawnParentWorkSteerCollect(t *testing.T) {
	store, workspace, configPath, config := toolUsageHelpConfig(t, []string{"run_js", "spawn_agent", "read_file", "agent_status", "steer_agent", "collect_agent"})
	// The recipe's configured explorer needs no tools (including no help tool).
	config["roles"].(map[string]any)["explorer"].(map[string]any)["tools"] = []string{}
	contextWrite(t, configPath, string(contracts.JSON(config)))
	contextWrite(t, filepath.Join(workspace, "docs/design/base-instructions.md"), "PARENT_INDEPENDENT_READ\n")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	childStarted, releaseChild := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(releaseChild) })
	var help, childID string
	parentStep, childStep := 0, 0
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if req.Messages[0].VisibleText() != "bundled child example" {
			childStep++
			toolUsageHelpSelected(t, req)
			if childStep == 1 {
				close(childStarted)
				select {
				case <-releaseChild:
				case <-ctx.Done():
					return contracts.ModelResult{}, ctx.Err()
				}
				return contracts.ModelResult{Text: "preliminary child result"}, nil
			}
			if !strings.Contains(strings.Join(steeringTexts(req.Messages), "\n"), "owner of the captured tool list") {
				t.Error("bundled steering was not delivered to the child")
			}
			return contracts.ModelResult{Text: "child followed bundled steering"}, nil
		}
		parentStep++
		switch parentStep {
		case 1:
			return toolUsageHelpCall("spawn_agent", map[string]bool{"help": true}), nil
		case 2:
			var result extensions.ToolHelp
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &result); err != nil || !result.Available || result.Name != "spawn_agent" {
				t.Fatalf("bundled child help: %+v, %v", result, err)
			}
			help = result.Text
			return scriptedJS(toolUsageHelpExample(t, help, "Spawn, then do independent parent work")), nil
		case 3:
			var spawned struct {
				ExecutionID string `json:"executionId"`
				Context     struct {
					Text string `json:"text"`
				} `json:"context"`
			}
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &spawned); err != nil || spawned.ExecutionID == "" || spawned.Context.Text != "PARENT_INDEPENDENT_READ\n" {
				t.Fatalf("parent work after bundled spawn: %+v, %v", spawned, err)
			}
			childID = spawned.ExecutionID
			// The parent reached another model step with its independent read
			// while the real child execution is still inside Generate.
			select {
			case <-childStarted:
			case <-ctx.Done():
				return contracts.ModelResult{}, ctx.Err()
			}
			code := toolUsageHelpExample(t, help, "Read saved progress only when useful")
			return scriptedJS(strings.ReplaceAll(code, "RETURNED_EXECUTION_ID", childID)), nil
		case 4:
			var status contracts.AgentStatus
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &status); err != nil || status.ExecutionID != childID || status.State != "running" {
				t.Fatalf("bundled status: %+v, %v", status, err)
			}
			code := toolUsageHelpExample(t, help, "Steer only if additional guidance is useful")
			return scriptedJS(strings.ReplaceAll(code, "RETURNED_EXECUTION_ID", childID)), nil
		case 5:
			var receipt contracts.ChildInstructionReceipt
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &receipt); err != nil || receipt.ExecutionID != childID || receipt.Status != "accepted" {
				t.Fatalf("bundled steer receipt: %+v, %v", receipt, err)
			}
			release.Do(func() { close(releaseChild) })
			code := toolUsageHelpExample(t, help, "Collect when the parent needs the final result")
			return scriptedJS(strings.ReplaceAll(code, "RETURNED_EXECUTION_ID", childID)), nil
		case 6:
			var collected struct {
				ExecutionID string `json:"executionId"`
				Outcome     string `json:"outcome"`
				FinalText   string `json:"finalText"`
			}
			if err := json.Unmarshal([]byte(toolUsageHelpResult(t, req)), &collected); err != nil || collected.ExecutionID != childID || collected.Outcome != "completed" || collected.FinalText != "child followed bundled steering" {
				t.Fatalf("bundled collect: %+v, %v", collected, err)
			}
			return contracts.ModelResult{Text: "bundled child workflow completed"}, nil
		}
		return contracts.ModelResult{}, fmt.Errorf("unexpected bundled child step %d", parentStep)
	})
	result, err := Run(ctx, store, provider, RunOptions{Task: "bundled child example", Workspace: workspace, ConfigPath: configPath, ModelID: "test/model"})
	if err != nil || !result.Result.OK || parentStep != 6 || childStep != 2 {
		t.Fatalf("bundled child workflow: %+v, steps=%d/%d, %v", result, parentStep, childStep, err)
	}
	child := mustExecution(t, store, childID)
	if child.ParentExecutionID != result.ExecutionID || child.Outcome != "completed" {
		t.Errorf("bundled example did not settle the app-owned child: %+v", child)
	}
}
