package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestStandardInspectionAndImplementationRolesDelegateAndSaveResults(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src/input.txt"), []byte("Target\nsecond\n"), 0644); err != nil {
		t.Fatal(err)
	}
	readTools := []string{"run_js", "read_file", "ls", "find", "grep", "wc", "git_inspect"}
	writeTools := append(append([]string{}, readTools...), "write_file", "edit_file", "command", "http", "web_fetch", "web_search")
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := request.Messages[0].VisibleText()
		if task == "parent" {
			if !strings.Contains(request.Instruction, "explorer:") || !strings.Contains(request.Instruction, "implementer:") || !strings.Contains(request.Instruction, "Normally omit tools") {
				t.Error("parent did not receive the configured role catalog and selection guidance")
			}
			if len(request.Messages) == 1 {
				return contracts.ModelResult{Calls: []contracts.ToolCall{
					{CallID: "explore", Name: "run_agent", Arguments: contracts.JSON(map[string]string{"role": "explorer", "task": "explorer"})},
					{CallID: "review", Name: "run_agent", Arguments: contracts.JSON(map[string]string{"role": "reviewer", "task": "reviewer"})},
					{CallID: "implement", Name: "run_agent", Arguments: contracts.JSON(map[string]string{"role": "implementer", "task": "implementer"})},
				}}, nil
			}
			text := string(contracts.JSON(request.Messages))
			for _, role := range []string{"explorer", "reviewer", "implementer"} {
				if !strings.Contains(text, role+" verified") {
					t.Errorf("parent did not receive %s result", role)
				}
			}
			return contracts.ModelResult{Text: "parent verified"}, nil
		}
		want := readTools
		if task == "implementer" {
			want = writeTools
		}
		if !sameNames(toolDefinitionNames(request.Tools), want) {
			t.Errorf("%s tools: %v", task, toolDefinitionNames(request.Tools))
		}
		if len(request.Messages) == 1 {
			switch task {
			case "explorer":
				return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "find", Name: "find", Arguments: contracts.JSON(map[string]string{"path": "src", "pattern": "*.txt"})}}}, nil
			case "reviewer":
				return scriptedJS(`return await tools.grep({path:"src",pattern:"Target",patternKind:"literal"});`), nil
			case "implementer":
				return scriptedJS(`await tools.write_file({path:"output/nested/result.txt",text:"alpha"}); const edit=await tools.edit_file({path:"output/nested/result.txt",edits:[{oldText:"alpha",newText:"beta"},{oldText:"beta",newText:"done"}]}); const read=await tools.read_file({path:"output/nested/result.txt",offset:1,limit:1}); return {edit,read};`), nil
			}
		}
		last := string(request.Messages[len(request.Messages)-1].Content)
		switch task {
		case "explorer":
			if !strings.Contains(last, "input.txt") {
				t.Errorf("explorer did not receive discovered paths: %s", last)
			}
		case "reviewer":
			if !strings.Contains(last, "Target") {
				t.Errorf("reviewer did not receive the search result: %s", last)
			}
		case "implementer":
			if !strings.Contains(last, "done") || !strings.Contains(last, "replacements") {
				t.Errorf("implementer did not receive the saved changes: %s", last)
			}
		}
		return contracts.ModelResult{Text: task + " verified"}, nil
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "parent", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("run: %+v (%v)", result, err)
	}
	children, err := store.Children(context.Background(), result.ExecutionID)
	if err != nil || len(children) != 3 {
		t.Fatalf("children: %+v (%v)", children, err)
	}
	for _, child := range children {
		want := readTools
		if child.AgentName == "implementer" {
			want = writeTools
		}
		if child.Outcome != "completed" || !sameNames(snapshotToolNames(t, store, child.ConfigurationID), want) {
			t.Errorf("saved child composition/outcome: %+v", child)
		}
		messages, err := store.Messages(context.Background(), child.ID, 0, -1)
		if err != nil || len(messages) < 3 {
			t.Errorf("child execution history: %s (%v)", child.ID, err)
		}
	}
	actual, err := os.ReadFile(filepath.Join(workspace, "output/nested/result.txt"))
	if err != nil || string(actual) != "done" {
		t.Fatalf("implementation output: %q (%v)", actual, err)
	}
}
