package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type legacyToolSnapshotView struct {
	Name       string                     `json:"name"`
	Source     string                     `json:"source"`
	Entry      string                     `json:"entry"`
	Contract   map[string]json.RawMessage `json:"contract"`
	FileAccess string                     `json:"fileAccess"`
	Paths      struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"paths"`
}

func TestInstallCaptureAndLegacySnapshot(t *testing.T) {
	dir := t.TempDir()
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("// edited by user\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(dir, "index.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(installed) != "// edited by user\n" {
		t.Fatal("InstallDefaults replaced an existing extension")
	}
	configBytes, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	snapshot, err := CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ConfigurationID == "" || len(snapshot.Resources) < 3 {
		t.Fatalf("capture omitted its identifier or source resources: %+v", snapshot)
	}
	var source []byte
	var archivedConfig []byte
	var runAgentSource []byte
	var agentControlSource []byte
	var hooksSource []byte
	for _, resource := range snapshot.Resources {
		if resource.Path == "config.json" {
			archivedConfig = resource.Bytes
		}
		if resource.Path == "index.js" {
			source = resource.Bytes
			if resource.ContentDigest != Digest(source) {
				t.Fatalf("resource digest does not match captured bytes: %s", resource.ContentDigest)
			}
		}
		if resource.Path == "run-agent.js" {
			runAgentSource = resource.Bytes
		}
		if resource.Path == "agent-control.js" {
			agentControlSource = resource.Bytes
		}
		if resource.Path == "hooks.js" {
			hooksSource = resource.Bytes
		}
	}
	if string(source) != "// edited by user\n" {
		t.Fatalf("capture did not freeze the edited source: %q", source)
	}
	if string(archivedConfig) != string(configBytes) {
		t.Fatal("archived config bytes differ from the bytes read for capture")
	}
	if !snapshot.HasTool("run_js") || !snapshot.HasTool("run_agent") || !snapshot.HasTool("spawn_agent") ||
		!snapshot.HasTool("agent_status") || !snapshot.HasTool("steer_agent") || !snapshot.HasTool("collect_agent") || !snapshot.HasTool("cancel_agent") ||
		!strings.Contains(string(runAgentSource), "api.agents.run(args)") || !strings.Contains(string(agentControlSource), "api.agents.spawn(args)") ||
		!strings.Contains(string(agentControlSource), "api.agents.steer(args)") {
		t.Fatalf("default role does not capture child control tools: tools=%+v run_agent=%q agent_control=%q", snapshot.Tools, runAgentSource, agentControlSource)
	}
	if !strings.Contains(string(hooksSource), "export async function runtimeStart") ||
		!strings.Contains(string(hooksSource), "runtime.startedAt") || !strings.Contains(string(hooksSource), "runtime.timeZone") {
		t.Fatalf("default hooks.js does not demonstrate the Go-provided start time: %s", hooksSource)
	}
	if !strings.Contains(snapshot.Tools[0].Description, "api.agents.run") || !strings.Contains(snapshot.Tools[0].Description, "api.agents.spawn") ||
		!strings.Contains(snapshot.Tools[0].Description, "api.agents.status") || !strings.Contains(snapshot.Tools[0].Description, "api.agents.steer") || !strings.Contains(snapshot.Tools[0].Description, "api.agents.collect") ||
		!strings.Contains(snapshot.Tools[0].Description, "api.agents.cancel") {
		t.Fatalf("run_js definition omits selected child operation guidance: %s", snapshot.Tools[0].Description)
	}
	for _, expected := range []string{"Promise", "setTimeout", "setInterval", "api.agents.collect", "not waiting for completion", `tools.command({program:"sleep",args:["SECONDS"]})`} {
		if !strings.Contains(snapshot.Tools[0].Description, expected) {
			t.Errorf("run_js description omits selected runtime guidance %q: %s", expected, snapshot.Tools[0].Description)
		}
	}
	for _, expected := range []string{"setTimeout", "setInterval", "api.agents.collect", "Do not poll status", `tools.command({program:"sleep",args:["SECONDS"]})`} {
		if !strings.Contains(snapshot.SystemInstruction, expected) {
			t.Errorf("system instruction omits selected runtime guidance %q: %s", expected, snapshot.SystemInstruction)
		}
	}
	if !strings.Contains(snapshot.Tools[0].Description, "one-line // comment in English") || !strings.Contains(snapshot.SystemInstruction, "one-line // comment in English") {
		t.Fatal("run_js does not instruct the working role to write an English purpose comment")
	}
	if !strings.Contains(snapshot.SystemInstruction, "Use steer_agent") || !strings.Contains(snapshot.SystemInstruction, "api.agents.steer") ||
		!strings.Contains(snapshot.SystemInstruction, "accepted, applied, or unapplied") {
		t.Fatalf("default instruction omits the selected child steering path: %s", snapshot.SystemInstruction)
	}
	if !strings.Contains(snapshot.SystemInstruction, "Available roles for run_agent") || !strings.Contains(snapshot.SystemInstruction, "reviewer:") {
		t.Fatalf("default role instruction omits the available role catalog: %s", snapshot.SystemInstruction)
	}

	encoded, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"schemaVersion", "configurationId", "agent", "source", "systemInstruction", "instructionComponents", "toolPaths", "tools", "hooks", "rejections"} {
		if _, ok := stored[field]; !ok {
			t.Errorf("legacy snapshot is missing %q", field)
		}
	}
	if _, hasResources := stored["resources"]; hasResources {
		t.Error("captured source bytes were embedded in the legacy snapshot JSON")
	}
	var metadata struct {
		SchemaVersion   int    `json:"schemaVersion"`
		ConfigurationID string `json:"configurationId"`
		Source          struct {
			Kind string `json:"kind"`
		} `json:"source"`
	}
	if err = json.Unmarshal(encoded, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.SchemaVersion != 1 || metadata.ConfigurationID != snapshot.ConfigurationID || metadata.Source.Kind != "external" {
		t.Fatalf("unexpected legacy snapshot metadata: %+v", metadata)
	}
	var legacy struct {
		Agent     map[string]json.RawMessage `json:"agent"`
		Source    map[string]json.RawMessage `json:"source"`
		ToolPaths struct {
			Source string   `json:"source"`
			Deny   []string `json:"deny"`
		} `json:"toolPaths"`
		Tools []legacyToolSnapshotView `json:"tools"`
		Hooks json.RawMessage          `json:"hooks"`
	}
	if err = json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Agent) == 0 || len(legacy.Source) == 0 {
		t.Fatal("agent or source was stored as null instead of the legacy object shape")
	}
	if got := string(legacy.Source["file"]); got != "\""+filepath.Join(dir, "config.json")+"\"" {
		t.Fatalf("snapshot source file is not the absolute captured config locator: %s", got)
	}
	var builtin, external *legacyToolSnapshotView
	for i := range legacy.Tools {
		tool := &legacy.Tools[i]
		if tool.Name == "run_js" {
			builtin = tool
		}
		if tool.Name == "read_file" {
			external = tool
		}
	}
	if builtin == nil || builtin.Source != "bundled" || builtin.Entry != "" {
		t.Fatalf("unexpected built-in tool metadata: %+v", builtin)
	}
	if external == nil || external.Source != "external" || external.Entry != "index.js" || external.FileAccess != "unmanaged" {
		t.Fatalf("unexpected legacy external tool metadata: %+v", external)
	}
	for _, field := range []string{"name", "description", "inputSchema"} {
		if _, ok := external.Contract[field]; !ok {
			t.Errorf("tool contract is missing %q", field)
		}
	}
	if legacy.ToolPaths.Source != "index.js" || legacy.ToolPaths.Deny == nil || external.Paths.Allow == nil || external.Paths.Deny == nil {
		t.Fatalf("unexpected legacy path contract: toolPaths=%+v tool=%+v", legacy.ToolPaths, external)
	}
	var hooks []struct {
		Handlers []string `json:"handlers"`
	}
	if err = json.Unmarshal(legacy.Hooks, &hooks); err != nil {
		t.Fatalf("hooks were not stored as structured definitions: %v", err)
	}
	wantHandlers := []string{"runtimeStart", "beforeTurn", "beforeTool", "afterTool", "afterTurn", "runtimeStop"}
	if len(hooks) != 1 || strings.Join(hooks[0].Handlers, ",") != strings.Join(wantHandlers, ",") {
		t.Fatalf("default hook declaration handlers = %+v, want %v", hooks, wantHandlers)
	}
}

func TestDefaultHookHandlersForPathAndObjectDeclarations(t *testing.T) {
	want := []string{"runtimeStart", "beforeTurn", "beforeTool", "afterTool", "afterTurn", "runtimeStop"}
	for _, raw := range []string{`"hooks.js"`, `{"module":"hooks.js"}`} {
		hooks, module, err := legacyHooksFromConfig(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("legacyHooksFromConfig(%s): %v", raw, err)
		}
		if module != "hooks.js" || len(hooks) != 1 || strings.Join(hooks[0].Handlers, ",") != strings.Join(want, ",") {
			t.Errorf("legacyHooksFromConfig(%s) = hooks=%+v module=%q, want handlers=%v", raw, hooks, module, want)
		}
	}
}

func TestSpawnOnlyRoleCapturesRoleCatalogAndSelectedJSOperations(t *testing.T) {
	dir := t.TempDir()
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config.Roles["async"] = Role{Revision: "1", Tools: []string{"run_js", "spawn_agent", "agent_status", "collect_agent", "cancel_agent"}}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, encoded, 0644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Capture(configPath, "async")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Agent.Agents) != len(snapshot.Catalog) || len(snapshot.Agent.Agents) == 0 ||
		!strings.Contains(snapshot.SystemInstruction, "Available roles for spawn_agent") {
		t.Fatalf("spawn-only role omitted its role catalog: agent=%+v instruction=%s", snapshot.Agent, snapshot.SystemInstruction)
	}
	description := snapshot.Tools[0].Description
	for _, selected := range []string{"api.agents.spawn", "api.agents.status", "api.agents.collect", "api.agents.cancel", "setTimeout", "setInterval", "await api.agents.collect"} {
		if !strings.Contains(description, selected) {
			t.Errorf("run_js description omitted selected operation %s: %s", selected, description)
		}
	}
	if !strings.Contains(snapshot.SystemInstruction, "await api.agents.collect") || !strings.Contains(snapshot.SystemInstruction, "Do not poll status") {
		t.Errorf("spawn role does not direct JavaScript to collect results and use status for reports: %s", snapshot.SystemInstruction)
	}
	if strings.Contains(description, `tools.command({program:"sleep"`) {
		t.Errorf("run_js description advertises a delay tool that was not selected: %s", description)
	}
	if strings.Contains(description, "api.agents.run") || strings.Contains(description, "api.agents.steer") || snapshot.HasTool("run_agent") {
		t.Fatalf("spawn-only role advertised unselected synchronous run: description=%s tools=%+v", description, snapshot.Agent.Tools)
	}
}

func TestCaptureResolvesRoleToolsDefinitionsAndCatalog(t *testing.T) {
	dir := t.TempDir()
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err = json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	config.SystemInstruction = "Workspace-wide note."
	configBytes, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := CaptureDir(dir, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Agent.Name != "reviewer" || snapshot.Agent.Revision != "1" || snapshot.HasTool("run_agent") {
		t.Fatalf("unexpected reviewer role resolution: agent=%+v tools=%+v", snapshot.Agent, snapshot.Tools)
	}
	if len(snapshot.Tools) != 2 || snapshot.Tools[0].Name != "run_js" || snapshot.Tools[1].Name != "read_file" {
		t.Fatalf("reviewer tools do not match the role selection: %+v", snapshot.Tools)
	}
	definitions := snapshot.Definitions()
	if len(definitions) != len(snapshot.Tools) || definitions[0].Name != "run_js" || definitions[1].Name != "read_file" {
		t.Fatalf("definitions diverged from captured tools: %+v", definitions)
	}
	if !strings.Contains(definitions[0].Description, "one-line // comment in English") || !strings.Contains(snapshot.SystemInstruction, "one-line // comment in English") {
		t.Fatal("run_js does not instruct the reviewing role to write an English purpose comment")
	}
	if !strings.Contains(definitions[0].Description, "setTimeout") || strings.Contains(definitions[0].Description, "tools.command({program:\"sleep\"") {
		t.Fatalf("reviewer run_js guidance does not match its selected capabilities: %q", definitions[0].Description)
	}
	if strings.Contains(definitions[0].Description, "api.agents.run") || strings.Contains(snapshot.SystemInstruction, "Available roles for run_agent") {
		t.Fatalf("non-delegating role advertises delegation: definition=%q instruction=%q", definitions[0].Description, snapshot.SystemInstruction)
	}
	for _, expected := range []string{"Role description:", "Reviews source", "Role instruction:", "Review the stated goal", "Use run_js", "Additional system instruction:", "Workspace-wide note."} {
		if !strings.Contains(snapshot.SystemInstruction, expected) {
			t.Errorf("resolved instruction omits %q: %s", expected, snapshot.SystemInstruction)
		}
	}
	encoded, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Tools []struct {
			Name     string `json:"name"`
			Contract struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"contract"`
		} `json:"tools"`
	}
	if err = json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Tools) != len(definitions) {
		t.Fatalf("saved tool definitions diverged from captured definitions: saved=%+v defs=%+v", stored.Tools, definitions)
	}
	for i, definition := range definitions {
		if stored.Tools[i].Name != definition.Name || stored.Tools[i].Contract.Name != definition.Name || stored.Tools[i].Contract.Description != definition.Description || string(stored.Tools[i].Contract.InputSchema) != string(definition.InputSchema) {
			t.Fatalf("saved tool %d diverged from model definition: saved=%+v definition=%+v", i, stored.Tools[i], definition)
		}
	}

	catalog, err := LoadCatalog(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.DefaultRole != "default" || len(catalog.Roles) != 2 || catalog.Roles[0].Name != "default" || catalog.Roles[1].Name != "reviewer" {
		t.Fatalf("role catalog is not sorted or complete: %+v", catalog)
	}
	if catalog.Roles[1].Description != snapshot.Catalog[1].Description {
		t.Fatalf("catalog and captured snapshot disagree: catalog=%+v snapshot=%+v", catalog, snapshot.Catalog)
	}
}

func TestCaptureOptionsReplaceToolsWithoutChangingConfigurationBytes(t *testing.T) {
	dir := t.TempDir()
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	selected := []string{"command"}
	snapshot, err := CaptureWithOptions(configPath, CaptureOptions{Role: "reviewer", Tools: &selected})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Agent.Name != "reviewer" || len(snapshot.Agent.Tools) != 1 || snapshot.Agent.Tools[0] != "command" ||
		len(snapshot.Tools) != 1 || snapshot.Tools[0].Name != "command" || snapshot.HasTool("run_js") {
		t.Fatalf("tool override did not replace the role composition: agent=%+v tools=%+v", snapshot.Agent, snapshot.Tools)
	}
	if strings.Contains(snapshot.SystemInstruction, "Use run_js") {
		t.Fatalf("instruction still describes an unselected run_js tool: %s", snapshot.SystemInstruction)
	}
	var archived []byte
	for _, resource := range snapshot.Resources {
		if resource.Path == "config.json" {
			archived = resource.Bytes
			break
		}
	}
	if string(archived) != string(original) {
		t.Fatal("captured config resource bytes were changed by the runtime tool override")
	}
	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("runtime tool override rewrote the extension config file")
	}

	empty := []string{}
	emptySnapshot, err := CaptureWithOptions(configPath, CaptureOptions{Role: "reviewer", Tools: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if emptySnapshot.Agent.Tools == nil || len(emptySnapshot.Agent.Tools) != 0 || len(emptySnapshot.Tools) != 0 {
		t.Fatalf("explicit empty tool list was not preserved: agent=%+v tools=%+v", emptySnapshot.Agent, emptySnapshot.Tools)
	}

	defaultSnapshot, err := CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var definition Tool
	for _, tool := range defaultSnapshot.Tools {
		if tool.Name == "run_agent" {
			definition = tool
		}
	}
	if len(definition.InputSchema) == 0 {
		t.Fatal("default extension config omitted run_agent")
	}
	var schema struct {
		Properties map[string]struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(definition.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	model := schema.Properties["model"]
	if model.Type != "object" || model.Properties["provider"] == nil || model.Properties["modelId"] == nil || model.Properties["effort"] == nil ||
		schema.Properties["tools"].Type != "array" {
		t.Fatalf("run_agent schema omitted child selection inputs: %s", definition.InputSchema)
	}
}

func TestEmptyRoleHasNoModelToolsOrSnapshotTools(t *testing.T) {
	dir := t.TempDir()
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config.Roles["empty"] = Role{Revision: "1", Description: "No tool access", Instruction: "Work without tools.", Tools: []string{}}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0644); err != nil {
		t.Fatal(err)
	}

	snapshot, err := Capture(configPath, "empty")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tools) != 0 || len(snapshot.Definitions()) != 0 || snapshot.Agent.Tools == nil || snapshot.HasTool("run_js") {
		t.Fatalf("empty role received tools: agent=%+v tools=%+v definitions=%+v", snapshot.Agent, snapshot.Tools, snapshot.Definitions())
	}
	if strings.Contains(snapshot.SystemInstruction, "Use run_js") || strings.Contains(snapshot.SystemInstruction, "api.agents.run") {
		t.Fatalf("empty role instruction advertises unselected tools: %s", snapshot.SystemInstruction)
	}
	encoded, err := snapshot.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err = json.Unmarshal(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Tools == nil || len(stored.Tools) != 0 {
		t.Fatalf("empty role snapshot contains tools: %s", encoded)
	}
}

func TestCaptureReportsUnresolvedRoleAndTool(t *testing.T) {
	dir := t.TempDir()
	if err := InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config.Roles["broken"] = Role{Tools: []string{"missing_tool"}}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Capture(configPath, "missing_role"); err == nil || !strings.Contains(err.Error(), `role "missing_role"`) {
		t.Fatalf("unknown role was not reported as a preparation error: %v", err)
	}
	if _, err = Capture(configPath, "broken"); err == nil || !strings.Contains(err.Error(), `tool "missing_tool"`) {
		t.Fatalf("unknown selected tool was not reported as a preparation error: %v", err)
	}
}
