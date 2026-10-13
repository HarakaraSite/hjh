package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
)

func contextWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceContextDirectJSResumeAndChild(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("HOME", t.TempDir())
	basePath := filepath.Join(xdg, "hjh", "instructions", "base.md")
	contextWrite(t, basePath, "BASE_ONE\n")
	store, workspace := delegationStore(t)
	agents := filepath.Join(workspace, "AGENTS.md")
	skill := filepath.Join(workspace, ".hjh", "skills", "report", "SKILL.md")
	body := func(description, marker string) string {
		return "---\nname: report\ndescription: " + description + "\n---\n" + marker + "\n"
	}
	contextWrite(t, agents, "AGENTS_ONE\n")
	contextWrite(t, skill, body("DESCRIPTION_ONE", "BODY_ONE"))
	configPath, err := prepareConfig(workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["roles"].(map[string]any)["default"].(map[string]any)["instruction"] = "PARENT_ROLE_ONLY"
	cfg["roles"].(map[string]any)["reviewer"].(map[string]any)["instruction"] = "CHILD_ROLE_ONLY"
	contextWrite(t, configPath, string(contracts.JSON(cfg)))
	counts := map[string]int{}
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				task = m.VisibleText()
			}
		}
		counts[task]++
		switch task {
		case "first":
			if !strings.Contains(req.Instruction, "BASE_ONE") || strings.Contains(req.Instruction, "BASE_TWO") || strings.Contains(req.Instruction, "You are a generic agent") {
				t.Error("first execution did not freeze the replacing base instruction")
			}
			if !strings.Contains(req.Instruction, "AGENTS_ONE") || !strings.Contains(req.Instruction, "DESCRIPTION_ONE") || strings.Contains(req.Instruction, "BODY_ONE") {
				t.Errorf("incorrect first instruction: %s", req.Instruction)
			}
			if counts[task] == 1 {
				contextWrite(t, basePath, "BASE_TWO\n")
				contextWrite(t, skill, body("DESCRIPTION_TWO", "BODY_TWO"))
				contextWrite(t, agents, "AGENTS_TWO\n")
				return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "read", Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": skill})}}}, nil
			}
			if !strings.Contains(string(req.Messages[len(req.Messages)-1].Content), "BODY_TWO") {
				t.Error("direct read did not return current skill body")
			}
			return contracts.ModelResult{Text: "first done"}, nil
		case "second":
			if !strings.Contains(req.Instruction, "BASE_TWO") || strings.Contains(req.Instruction, "BASE_THREE") || strings.Contains(req.Instruction, "BASE_ONE") {
				t.Error("resume did not refresh and freeze the replacing base")
			}
			if !strings.Contains(req.Instruction, "AGENTS_TWO") || !strings.Contains(req.Instruction, "DESCRIPTION_TWO") {
				t.Error("resume did not refresh instruction and catalog")
			}
			if counts[task] == 1 {
				contextWrite(t, skill, body("DESCRIPTION_THREE", "BODY_THREE"))
				return scriptedJS(fmt.Sprintf("return await tools.read_file({path:%s});", contracts.JSON(skill))), nil
			}
			if counts[task] == 2 {
				if !strings.Contains(string(req.Messages[len(req.Messages)-1].Content), "BODY_THREE") {
					t.Error("JS outer result did not return skill body")
				}
				contextWrite(t, agents, "AGENTS_THREE\n")
				contextWrite(t, basePath, "BASE_THREE\n")
				return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "child", Name: "run_agent", Arguments: contracts.JSON(map[string]string{"role": "reviewer", "task": "child"})}}}, nil
			}
			return contracts.ModelResult{Text: "second done"}, nil
		case "child":
			if !strings.Contains(req.Instruction, "BASE_THREE") || strings.Contains(req.Instruction, "BASE_TWO") || strings.Contains(req.Instruction, "You are a generic agent") {
				t.Error("child did not read the current replacing base")
			}
			if !strings.Contains(req.Instruction, "AGENTS_THREE") || !strings.Contains(req.Instruction, "DESCRIPTION_THREE") || !strings.Contains(req.Instruction, "CHILD_ROLE_ONLY") || strings.Contains(req.Instruction, "PARENT_ROLE_ONLY") {
				t.Errorf("child context incorrect: %s", req.Instruction)
			}
			if counts[task] == 1 {
				return scriptedJS(fmt.Sprintf("return await tools.read_file({path:%s});", contracts.JSON(skill))), nil
			}
			if !strings.Contains(string(req.Messages[len(req.Messages)-1].Content), "BODY_THREE") {
				t.Error("child body not delivered")
			}
			return contracts.ModelResult{Text: "child done"}, nil
		}
		return contracts.ModelResult{}, fmt.Errorf("unexpected task %q", task)
	})
	ctx := context.Background()
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	preview, err := service.Orientation(ctx, RunRequest{})
	if err != nil || preview.Base.Path != basePath || preview.Base.Digest != extensions.Digest([]byte("BASE_ONE\n")) || preview.Base.BuiltinVersion != "" {
		t.Fatalf("base preview: %+v %v", preview.Base, err)
	}
	first, err := Run(ctx, store, provider, RunOptions{Task: "first", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := Run(ctx, store, provider, RunOptions{Task: "second", SessionID: first.SessionID})
	if err != nil || !second.Result.OK || counts["child"] != 2 {
		t.Fatalf("resume/child: %+v %v %v", second, counts, err)
	}
	records, err := store.Records(ctx, first.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	foundSource, foundWire, foundBudget, foundBase := false, false, false, false
	for _, r := range records {
		if r.Kind == "resource_revision" && string(r.Content) == "BASE_ONE\n" && strings.Contains(string(r.Payload), basePath) {
			foundBase = true
		}
		if r.Kind == "resource_revision" && string(r.Content) == "AGENTS_ONE\n" {
			foundSource = true
		}
		if r.Kind == "model_request" && strings.Contains(string(r.Content), "BODY_TWO") {
			foundWire = true
		}
		if r.Kind == "model_request" && strings.Contains(string(r.Payload), "contextBudget") {
			foundBudget = true
		}
	}
	if !foundSource || !foundWire || !foundBudget || !foundBase {
		t.Fatalf("source/wire/budget/base=%v/%v/%v/%v", foundSource, foundWire, foundBudget, foundBase)
	}
	configuration, err := store.Configuration(ctx, mustExecution(t, store, first.ExecutionID).ConfigurationID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configuration.Snapshot), "AGENTS_ONE") || strings.Contains(string(configuration.Snapshot), "BODY_ONE") {
		t.Fatalf("configuration body was eager-loaded: %s", configuration.Snapshot)
	}
	var saved struct {
		Base contracts.BaseInstruction `json:"baseInstruction"`
	}
	if err := json.Unmarshal(configuration.Snapshot, &saved); err != nil || saved.Base != preview.Base {
		t.Fatalf("actual stored base differs from the initial preview: %+v %v", saved.Base, err)
	}
}

func TestOrientationPreviewDoesNotCreateSessionOrExtensions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	store, workspace := delegationStore(t)
	contextWrite(t, filepath.Join(workspace, "AGENTS.md"), "preview instructions")
	contextWrite(t, filepath.Join(workspace, ".agents", "skills", "preview", "SKILL.md"), "---\ndescription: Preview skill\n---\nPRIVATE_BODY")
	service := NewService(store, nil, RunOptions{Workspace: workspace})
	defer service.Close()
	preview, err := service.Orientation(context.Background(), RunRequest{})
	if err != nil || preview.Mode != "preview" || len(preview.Skills) != 1 || len(preview.Instructions) == 0 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".hjh", "extensions")); !os.IsNotExist(err) {
		t.Fatalf("preview installed extensions: %v", err)
	}
	sessions, err := store.ListSessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("preview created Session: %v %v", sessions, err)
	}
	if strings.Contains(string(contracts.JSON(preview)), "PRIVATE_BODY") {
		t.Error("preview contains skill body")
	}
	data, _ := extensions.EmbeddedConfig()
	var cfg map[string]any
	_ = json.Unmarshal(data, &cfg)
	cfg["roles"].(map[string]any)["text"] = map[string]any{"tools": []string{}, "instruction": "text only"}
	path := filepath.Join(workspace, "custom", "config.json")
	contextWrite(t, path, string(contracts.JSON(cfg)))
	textService := NewService(store, nil, RunOptions{Workspace: workspace, ConfigPath: path})
	defer textService.Close()
	textPreview, err := textService.Orientation(context.Background(), RunRequest{Role: "text"})
	if err != nil || len(textPreview.Skills) != 0 || len(textPreview.DiscoveredSkills) != 1 {
		t.Fatalf("reader-free role: %+v %v", textPreview, err)
	}
}

func TestWorkspaceContextOverlappingExtensionCaptureUsesCurrentReadWithoutArchive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	store, workspace := delegationStore(t)
	if err := extensions.InstallDefaults(workspace); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(workspace, ".agents", "skills", "overlap", "SKILL.md")
	skillText := "---\ndescription: Overlapping source skill\n---\n"
	contextWrite(t, skill, skillText+"ARCHIVED_OLD_BODY")
	step := 0
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		if strings.Contains(request.Instruction, "ARCHIVED_OLD_BODY") {
			t.Error("extension archive was injected as skill instructions")
		}
		if step == 1 {
			contextWrite(t, skill, skillText+"CURRENT_READ_BODY")
			return scriptedJS(fmt.Sprintf("return await tools.read_file({path:%s});", contracts.JSON(skill))), nil
		}
		if !strings.Contains(string(request.Messages[len(request.Messages)-1].Content), "CURRENT_READ_BODY") {
			t.Error("read used archived bytes instead of the current file")
		}
		return contracts.ModelResult{Text: "overlap done"}, nil
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "use skill", Workspace: workspace,
		ConfigPath: filepath.Join(workspace, "config.json"), ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("overlap run: %+v %v", result, err)
	}
	records, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.Kind == "resource_revision" && string(record.Content) == skillText+"ARCHIVED_OLD_BODY" {
			t.Error("unread skill body was automatically archived with the extension tree")
		}
	}
}
