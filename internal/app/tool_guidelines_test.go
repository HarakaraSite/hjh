package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

func TestToolGuidelinesFollowParentAndChildSelectionAndPreserveSource(t *testing.T) {
	store, workspace := delegationStore(t)
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config extensions.Config
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	// Preserve provider/model fields owned by the provider registry.
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	encode := func() ([]byte, error) {
		document["roles"] = contracts.JSON(config.Roles)
		document["tools"] = contracts.JSON(config.Tools)
		return json.MarshalIndent(document, "", "  ")
	}
	role := config.Roles["default"]
	role.Tools = []string{"run_js", "spawn_agent", "agent_status", "collect_agent", "guide_probe"}
	config.Roles["default"] = role
	guides := []string{"GUIDELINE_V1: Read the research note before reporting.", "Preserve the note's exact identifiers."}
	config.Tools = append(config.Tools, extensions.Tool{Name: "guide_probe", Revision: "1", Description: "Read a local research note.",
		InputSchema: contracts.JSON(map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string"}}, "required": []string{"path"}}),
		Module:      "guide-probe.js", Export: "guideProbe", PromptGuidelines: guides})
	if err := os.WriteFile(filepath.Join(dir, "guide-probe.js"), []byte(`export async function guideProbe(args,api){return api.readFile(args)}`), 0644); err != nil {
		t.Fatal(err)
	}
	configured, err := encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, configured, 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	requests := map[string]contracts.ModelRequest{}
	steps := map[string]int{}
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				task = m.VisibleText()
			}
		}
		mu.Lock()
		requests[task] = req
		steps[task]++
		step := steps[task]
		mu.Unlock()
		if task == "guideline parent" && step == 1 {
			return scriptedJS(`const a=await api.agents.spawn({task:'selected child',tools:['guide_probe']}); const b=await api.agents.spawn({task:'empty child',tools:[]}); return await Promise.all([api.agents.collect({executionId:a.executionId}),api.agents.collect({executionId:b.executionId})]);`), nil
		}
		return contracts.ModelResult{Text: "done " + task}, nil
	})
	first, err := Run(context.Background(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, ModelID: "test/model", Task: "guideline parent"})
	if err != nil || !first.Result.OK {
		t.Fatalf("parent %+v: %v", first, err)
	}
	children, err := store.Children(context.Background(), first.ExecutionID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children %+v: %v", children, err)
	}
	root, err := store.Execution(context.Background(), first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range append([]history.Execution{root}, children...) {
		req := requests[e.Task]
		selected := e.Task != "empty child"
		if strings.Contains(req.Instruction, "GUIDELINE_V1") != selected {
			t.Fatalf("wrong tool selection in %s instruction", e.Task)
		}
		if e.Task == "selected child" && (strings.Contains(req.Instruction, "api.agents.spawn") || strings.Contains(req.Instruction, "one-line // comment")) {
			t.Fatal("child received unselected run_js/child-operation guidance")
		}
		if !selected && strings.Contains(req.Instruction, "Active tool guidelines") {
			t.Fatal("tool-free child received guidelines")
		}
		// Provider contracts retain the public function description and schema.
		wireTools := string(contracts.JSON(req.Tools))
		if strings.Contains(wireTools, "promptGuidelines") || strings.Contains(wireTools, "GUIDELINE_V1") {
			t.Fatal("guideline metadata leaked into provider tool contracts")
		}
		saved, err := store.Configuration(context.Background(), e.ConfigurationID)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot struct {
			Tools []struct {
				Name             string   `json:"name"`
				PromptGuidelines []string `json:"promptGuidelines"`
			} `json:"tools"`
			InstructionComponents []struct {
				Identity string `json:"identity"`
				Text     string `json:"text"`
			} `json:"instructionComponents"`
		}
		if err := json.Unmarshal(saved.Snapshot, &snapshot); err != nil {
			t.Fatal(err)
		}
		foundGuide, foundComponent := false, false
		for _, tool := range snapshot.Tools {
			if tool.Name == "guide_probe" {
				foundGuide = reflect.DeepEqual(tool.PromptGuidelines, guides)
			}
		}
		for _, comp := range snapshot.InstructionComponents {
			if comp.Identity == "instruction:active-tool-guidelines" {
				foundComponent = strings.Contains(comp.Text, "GUIDELINE_V1") && strings.Contains(req.Instruction, comp.Text)
			}
		}
		if selected != (foundGuide && foundComponent) {
			t.Fatalf("saved guidelines/component differ from request for %s", e.Task)
		}
		records, err := store.Records(context.Background(), e.ID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		archived := false
		for _, r := range records {
			if r.Kind == "resource_revision" && string(r.Content) == string(configured) {
				archived = true
			}
		}
		if !archived {
			t.Fatal("guideline selection altered archived config bytes")
		}
	}
	// Editing the same external declaration affects the next turn only.
	for i := range config.Tools {
		if config.Tools[i].Name == "guide_probe" {
			config.Tools[i].PromptGuidelines = []string{"GUIDELINE_V2: Use the updated note instructions."}
		}
	}
	updated, err := encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0644); err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), store, provider, RunOptions{SessionID: first.SessionID, ConfigPath: path, Task: "next guideline turn"})
	if err != nil || !second.Result.OK {
		t.Fatalf("next %+v: %v", second, err)
	}
	if req := requests["next guideline turn"]; !strings.Contains(req.Instruction, "GUIDELINE_V2") || strings.Contains(req.Instruction, "GUIDELINE_V1") {
		t.Fatal("next turn did not recapture external guidelines")
	}
}
