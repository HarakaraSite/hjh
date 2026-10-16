package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

// Exercise the real JS -> native search -> journal boundary, including generated
// calls, so unselected native output cannot silently return to stored history.
func TestGrepPagesBeforeSavingNativeResults(t *testing.T) {
	grepPath, err := exec.LookPath("grep")
	if err != nil {
		t.Fatal(err)
	}
	rgPath, rgErr := exec.LookPath("rg")
	for _, backend := range []string{"rg", "grep"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "rg" && rgErr != nil {
				t.Skip("rg not installed")
			}
			bin := t.TempDir()
			for name, target := range map[string]string{"grep": grepPath, "rg": rgPath} {
				if name == "rg" && backend != "rg" {
					continue
				}
				if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			store, workspace := delegationStore(t)
			if err := os.Mkdir(filepath.Join(workspace, "src"), 0755); err != nil {
				t.Fatal(err)
			}
			const unselected = "Target UNSELECTED-END-OF-NATIVE-OUTPUT"
			files := map[string]string{
				"main.go":      strings.Repeat("Target retained line\n", 11999) + unselected + "\n",
				"other.go":     "Target another file\n",
				"main_test.go": "Target excluded-test-file\n",
			}
			for name, text := range files {
				if err := os.WriteFile(filepath.Join(workspace, "src", name), []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			step := 0
			provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
				step++
				if step > 1 {
					var results []contracts.ToolResult
					if err := json.Unmarshal(req.Messages[len(req.Messages)-1].Content, &results); err != nil || len(results) != 1 {
						t.Fatalf("tool results: %v, %v", results, err)
					}
					raw := []byte(results[0].Text)
					if strings.Contains(string(raw), unselected) || strings.Contains(string(raw), "excluded-test-file") {
						t.Fatal("unselected native output reached the model")
					}
					var result struct {
						Lines                  []any
						Files                  []any
						Matches, Total, Offset int
						NextOffset             *int
					}
					if err := json.Unmarshal(raw, &result); err != nil {
						t.Fatal(err)
					}
					if result.Matches != 12001 {
						t.Fatalf("whole-search match count: %s", raw)
					}
					if step <= 3 {
						if len(result.Lines) != 30 || result.Total != 12001 || result.NextOffset == nil || *result.NextOffset != result.Offset+30 {
							t.Fatalf("line page metadata: %s", raw)
						}
					} else if len(result.Files) != 1 || result.Total != 2 || (step == 4 && (result.NextOffset == nil || *result.NextOffset != 1)) || (step == 5 && result.NextOffset != nil) {
						t.Fatalf("file page metadata: %s", raw)
					}
				}
				args := map[string]any{"path": "src", "pattern": "Target", "glob": "!*_test.go", "limit": 30}
				switch step {
				case 1:
				case 2:
					return scriptedJS(`// Read the next search page.
return tools.grep({path:"src",pattern:"Target",glob:"!*_test.go",offset:30,limit:30});`), nil
				case 3:
					args["output"] = "files"
					args["limit"] = 1
				case 4:
					args["output"] = "files"
					args["limit"] = 1
					args["offset"] = 1
				default:
					return contracts.ModelResult{Text: "Search complete."}, nil
				}
				return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: fmt.Sprintf("search-%d", step), Name: "grep", Arguments: contracts.JSON(args)}}}, nil
			})
			result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ModelID: "test/model", Task: "Find relevant source lines"})
			if err != nil || !result.Result.OK || step != 5 {
				t.Fatalf("grep run: %+v, step %d: %v", result, step, err)
			}
			records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			pages, savedBytes := 0, 0
			for _, record := range records {
				if strings.Contains(string(record.Payload), unselected) || strings.Contains(string(record.Content), unselected) || strings.Contains(string(record.Payload), "excluded-test-file") {
					t.Fatal("unselected native output was archived")
				}
				if record.Kind == "effect_observation" {
					t.Fatalf("grep recorded a retired native host effect: %s", record.Payload)
				}
				if record.Kind != "tool_result" {
					continue
				}
				var observation contracts.Observation
				if err := json.Unmarshal(record.Payload, &observation); err != nil {
					t.Fatal(err)
				}
				if (observation.Kind == "tool" || observation.Kind == "model_tool") && observation.Name == "grep" && observation.Phase == "completed" {
					pages++
					savedBytes += len(observation.Result)
					var page struct {
						Lines, Files           []any
						Matches, Total, Offset int
						NextOffset             *int
						Backend                string
					}
					if err := json.Unmarshal(observation.Result, &page); err != nil {
						t.Fatalf("saved grep observation %s: %v", record.Payload, err)
					}
					if page.Backend != backend || page.Matches != 12001 {
						t.Fatalf("saved complete page metadata: %s", observation.Result)
					}
					if page.Total == 12001 {
						if len(page.Lines) != 30 || page.NextOffset == nil || *page.NextOffset != page.Offset+30 {
							t.Fatalf("saved line page: %s", observation.Result)
						}
					} else if page.Total != 2 || len(page.Files) != 1 {
						t.Fatalf("saved file page: %s", observation.Result)
					}
				}
			}
			if pages != 4 {
				t.Fatalf("saved tool pages = %d", pages)
			}
			t.Logf("backend=%s matches=12001 tool pages=%d saved tool result bytes=%d", backend, pages, savedBytes)
		})
	}
}
