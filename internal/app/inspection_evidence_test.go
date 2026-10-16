package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestFilteredInspectionRetainsExplicitSelectedPage(t *testing.T) {
	for _, tool := range []string{"grep", "git_inspect"} {
		t.Run(tool, func(t *testing.T) {
			store, workspace := delegationStore(t)
			const selected = "EXPLICIT_INSPECTION_PAGE"
			const unselected = "UNSELECTED_INSPECTION_PAGE"
			if err := os.WriteFile(filepath.Join(workspace, "source.txt"), []byte(selected+"\n"+unselected+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			args := map[string]any{"pattern": "INSPECTION_PAGE", "path": "source.txt", "limit": 1}
			if tool == "git_inspect" {
				for _, argv := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", unselected}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", selected}} {
					cmd := exec.Command("git", argv...)
					cmd.Dir = workspace
					if raw, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("git: %s (%v)", raw, err)
					}
				}
				args = map[string]any{"op": "log", "limit": 1}
			}
			step := 0
			provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
				step++
				if step == 1 {
					return scriptedJS("// Filter the outer result, retaining the explicitly requested inner page.\nawait tools." + tool + "(" + string(contracts.JSON(args)) + ");return {done:true};"), nil
				}
				var results []contracts.ToolResult
				if err := json.Unmarshal(req.Messages[len(req.Messages)-1].Content, &results); err != nil || len(results) != 1 || results[0].Text != `{"done":true}` {
					t.Fatalf("filtered inspection result %+v: %v", results, err)
				}
				return contracts.ModelResult{Text: "Done."}, nil
			})
			result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ModelID: "test/model", Task: "Inspect one page"})
			if err != nil || !result.Result.OK || step != 2 {
				t.Fatalf("inspection run %+v: %v", result, err)
			}
			records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			retained := false
			callRecords := map[string]string{}
			for _, record := range records {
				if strings.Contains(string(record.Payload), unselected) || strings.Contains(string(record.Content), unselected) {
					t.Fatal("unselected native page was saved")
				}
				var observation contracts.Observation
				if err := json.Unmarshal(record.Payload, &observation); err != nil {
					t.Fatal(err)
				}
				if record.Kind == "effect_observation" {
					t.Fatal("inspection recorded a retired host effect")
				}
				if record.Kind == "tool_call" {
					callRecords[observation.ID] = record.ID
				}
				if record.Kind == "tool_result" && observation.Kind == "tool" && observation.Name == tool && observation.Phase == "completed" {
					if !strings.Contains(string(observation.Result), selected) || !strings.Contains(string(observation.Result), `"nextOffset":1`) {
						t.Fatalf("explicit selected page lost: %s", observation.Result)
					}
					requireUsageSummaryRelation(t, record, "parent_call", callRecords[observation.Parent])
					retained = true
				}
			}
			if !retained {
				t.Fatal("filtering the outer return lost explicit inner inspection evidence")
			}
		})
	}
}
