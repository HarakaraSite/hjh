package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/host"
)

func TestGitAndEditDoNotArchiveInternalFullFiles(t *testing.T) {
	for _, generated := range []bool{false, true} {
		name := "direct"
		if generated {
			name = "run_js"
		}
		t.Run(name, func(t *testing.T) {
			store, workspace := delegationStore(t)
			const marker = "UNCHANGED_FILE_BODY_MARKER"
			original := strings.Repeat(marker+"\r\n", 3000) + "replace me\n"
			path := filepath.Join(workspace, "large file.txt")
			if err := os.WriteFile(path, []byte(original), 0644); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"init", "-q"}, {"add", "large file.txt"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--no-verify", "-qm", "initial"}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = workspace
				if raw, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %s %v", raw, err)
				}
			}
			for _, tool := range []string{"git_inspect", "edit_file"} {
				args := map[string]any{"op": "show", "paths": "large file.txt", "limit": 2}
				if tool == "edit_file" {
					args = map[string]any{"path": "large file.txt", "edits": []any{map[string]string{"oldText": "replace me", "newText": "replacement"}}}
				}
				step := 0
				provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
					step++
					if step == 1 {
						if generated {
							return scriptedJS("// Inspect or edit without expanding internal file bodies.\nreturn tools." + tool + "(" + string(contracts.JSON(args)) + ");"), nil
						}
						return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "tool-call", Name: tool, Arguments: contracts.JSON(args)}}}, nil
					}
					var results []contracts.ToolResult
					if err := json.Unmarshal(req.Messages[len(req.Messages)-1].Content, &results); err != nil || len(results) != 1 {
						t.Fatalf("tool results %v: %v", results, err)
					}
					if results[0].Outcome != "success" || strings.Contains(results[0].Text, marker) {
						t.Fatalf("public tool result: %s", results[0].Text)
					}
					if tool == "git_inspect" {
						var page struct {
							Text              string
							Total, NextOffset *int
						}
						if err := json.Unmarshal([]byte(results[0].Text), &page); err != nil {
							t.Fatal(err)
						}
						if !strings.HasPrefix(page.Text, "commit ") || page.Total == nil || *page.Total < 3000 || page.NextOffset == nil || *page.NextOffset != 2 {
							t.Fatalf("git path/page metadata: %+v", page)
						}
					} else {
						var result struct {
							Replacements int `json:"replacements"`
						}
						if err := json.Unmarshal([]byte(results[0].Text), &result); err != nil || result.Replacements != 1 {
							t.Fatalf("edit: %+v %v", result, err)
						}
					}
					return contracts.ModelResult{Text: "Done."}, nil
				})
				result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ModelID: "test/model", Task: "Inspect or edit the file"})
				if err != nil || !result.Result.OK || step != 2 {
					t.Fatalf("run %+v: %v", result, err)
				}
				records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
				if err != nil {
					t.Fatal(err)
				}
				toolCompleted := 0
				callRecords := map[string]string{}
				for _, r := range records {
					if strings.Contains(string(r.Payload), marker) || strings.Contains(string(r.Content), marker) {
						t.Fatalf("%s archived internal full file", tool)
					}
					var o contracts.Observation
					if err := json.Unmarshal(r.Payload, &o); err != nil {
						t.Fatal(err)
					}
					if r.Kind == "tool_call" {
						callRecords[o.ID] = r.ID
					}
					if r.Kind == "tool_result" && (o.Kind == "model_tool" || o.Kind == "tool") && o.Name == tool && o.Phase == "completed" {
						toolCompleted++
						if tool == "git_inspect" {
							var stored struct {
								Text              string
								Total, NextOffset *int
							}
							if err := json.Unmarshal(o.Result, &stored); err != nil || !strings.HasPrefix(stored.Text, "commit ") || stored.Total == nil || *stored.Total < 3000 || stored.NextOffset == nil || *stored.NextOffset != 2 {
								t.Fatalf("stored git page %s: %v", o.Result, err)
							}
						} else {
							var stored struct {
								Replacements int `json:"replacements"`
							}
							if err := json.Unmarshal(o.Result, &stored); err != nil || stored.Replacements != 1 {
								t.Fatalf("stored edit result %s: %v", o.Result, err)
							}
						}
						if generated {
							requireUsageSummaryRelation(t, r, "parent_call", callRecords[o.Parent])
						}
					}
					if r.Kind == "effect_observation" {
						t.Fatalf("%s recorded a retired per-I/O host effect: %s", tool, r.Payload)
					}
				}
				if toolCompleted != 1 {
					t.Fatalf("%s completed tool results %d", tool, toolCompleted)
				}
				if tool == "edit_file" {
					saved, err := os.ReadFile(path)
					if err != nil || string(saved) != strings.ReplaceAll(original, "replace me", "replacement") {
						t.Fatal("file edit changed other contents")
					}
				}
			}
		})
	}
}

func TestWebSearchStoresOneBodyAcrossDirectAndGeneratedCalls(t *testing.T) {
	for _, mode := range []string{"direct", "run_js", "run_js_filtered"} {
		t.Run(mode, func(t *testing.T) {
			store, workspace := delegationStore(t)
			ext := filepath.Join(workspace, ".hjh/extensions")
			if err := extensions.InstallDefaults(ext); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(ext, "config.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var cfg map[string]any
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HJH_TEST_SEARCH_BODY_KEY", "local-search-key")
			cfg["credentials"].(map[string]any)["exa-api-key"] = map[string]string{"env": "HJH_TEST_SEARCH_BODY_KEY"}
			if err := os.WriteFile(path, contracts.JSON(cfg), 0644); err != nil {
				t.Fatal(err)
			}
			const marker = "SEARCH_BODY_STORAGE_MARKER"
			text := strings.Repeat(marker+" <p>& 日本語\u2028\n", 10)
			response := contracts.JSON(map[string]any{"requestId": "search-request", "results": []any{map[string]any{"title": "Only title", "highlights": []string{text}}}, "future_field": map[string]any{"preserved": true}})
			client := &http.Client{Transport: webSearchTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer local-search-key" {
					t.Fatal("search credential was not resolved")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(response)))}, nil
			})}
			step := 0
			provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
				step++
				if step == 1 {
					if mode == "run_js" {
						return scriptedJS("// Return complete search results.\nreturn tools.web_search({query:'search'});"), nil
					}
					if mode == "run_js_filtered" {
						return scriptedJS("// Return the title while keeping the inner search evidence.\nconst r=await tools.web_search({query:'search'});return {title:r.results[0].title};"), nil
					}
					return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "search-call", Name: "web_search", Arguments: contracts.JSON(map[string]string{"query": "search"})}}}, nil
				}
				var results []contracts.ToolResult
				if err := json.Unmarshal(req.Messages[len(req.Messages)-1].Content, &results); err != nil || len(results) != 1 {
					t.Fatalf("results %v: %v", results, err)
				}
				if results[0].Outcome != "success" {
					t.Fatal(results[0].Text)
				}
				if mode == "run_js_filtered" {
					if results[0].Text != `{"title":"Only title"}` {
						t.Fatal("filtered value changed")
					}
				} else {
					var decoded struct {
						Results []struct{ Highlights []string }
						Future  map[string]any `json:"future_field"`
					}
					if err := json.Unmarshal([]byte(results[0].Text), &decoded); err != nil || len(decoded.Results) != 1 || len(decoded.Results[0].Highlights) != 1 || decoded.Results[0].Highlights[0] != text || decoded.Future["preserved"] != true {
						t.Fatal("complete search result changed")
					}
				}
				return contracts.ModelResult{Text: "Done."}, nil
			})
			result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, ModelID: "test/model", Task: "Search", HTTPClient: client, CredentialStateDir: t.TempDir()})
			if err != nil || !result.Result.OK || step != 2 {
				t.Fatalf("search %+v: %v", result, err)
			}
			records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			summaries := 0
			searchEvidence := mode == "direct"
			callRecords := map[string]string{}
			for _, r := range records {
				if strings.Contains(string(r.Payload), "local-search-key") {
					t.Fatal("credential archived")
				}
				var o contracts.Observation
				if err := json.Unmarshal(r.Payload, &o); err != nil {
					t.Fatal(err)
				}
				if r.Kind == "tool_call" {
					callRecords[o.ID] = r.ID
				}
				if r.Kind == "tool_result" && o.Kind == "tool" && o.Name == "web_search" && o.Phase == "completed" {
					var decoded struct {
						Results []struct{ Highlights []string }
					}
					if err := json.Unmarshal(o.Result, &decoded); err != nil || len(decoded.Results) != 1 || len(decoded.Results[0].Highlights) != 1 || decoded.Results[0].Highlights[0] != text {
						t.Fatal("complete inner search evidence missing from history")
					}
					if mode != "direct" {
						requireUsageSummaryRelation(t, r, "parent_call", callRecords[o.Parent])
					}
					searchEvidence = true
				}
				if r.Kind != "effect_observation" {
					continue
				}
				if o.Name != "requestProvider" {
					t.Fatalf("search still used full-response host operation %s", o.Name)
				}
				if strings.Contains(string(o.Result), marker) {
					t.Fatal("search body duplicated in host observation")
				}
				if o.Phase == "completed" {
					summaries++
					var summary host.ProviderObservation
					if err := json.Unmarshal(o.Result, &summary); err != nil || summary.Status != 200 || summary.ResponseBytes != len(response) {
						t.Fatalf("search summary %+v: %v", summary, err)
					}
				}
			}
			if summaries != 1 || !searchEvidence {
				t.Fatalf("search summaries %d, retained evidence %t", summaries, searchEvidence)
			}
			// Check physical contents, rather than reconstructed public record JSON.
			db, err := sql.Open("sqlite", "file:"+filepath.Join(workspace, "history.sqlite3")+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var copies, bodyBytes int
			if err := db.QueryRow("SELECT COUNT(*),COALESCE(SUM(byte_length),0) FROM contents WHERE instr(CAST(content_bytes AS TEXT),?)>0", marker).Scan(&copies, &bodyBytes); err != nil {
				t.Fatal(err)
			}
			if copies != 1 {
				t.Fatalf("search body stored %d times (%d bytes)", copies, bodyBytes)
			}
			t.Logf("mode=%s HTTP bytes=%d single stored body=%d", mode, len(response), bodyBytes)
		})
	}
}

func TestRunJSFiltersModelInputButRetainsExplicitInnerRead(t *testing.T) {
	store, workspace := delegationStore(t)
	const marker = "INNER_READ_EVIDENCE_MARKER"
	if err := os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("first line\n"+strings.Repeat(marker+"\n", 3000)), 0644); err != nil {
		t.Fatal(err)
	}
	step := 0
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		if step == 1 {
			return scriptedJS("// Return only the first line of an explicit full read.\nconst r=await tools.read_file({path:'source.txt'});return r.text.split('\\n')[0];"), nil
		}
		var results []contracts.ToolResult
		if err := json.Unmarshal(req.Messages[len(req.Messages)-1].Content, &results); err != nil || len(results) != 1 || results[0].Text != `"first line"` {
			t.Fatalf("filtered result %+v: %v", results, err)
		}
		return contracts.ModelResult{Text: "Done."}, nil
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ModelID: "test/model", Task: "Return first line"})
	if err != nil || !result.Result.OK {
		t.Fatalf("run %+v: %v", result, err)
	}
	records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	retained := false
	callRecords := map[string]string{}
	for _, r := range records {
		var o contracts.Observation
		if err := json.Unmarshal(r.Payload, &o); err != nil {
			t.Fatal(err)
		}
		if r.Kind == "effect_observation" {
			t.Fatalf("read_file recorded a per-file host effect: %s", r.Payload)
		}
		if r.Kind == "tool_call" {
			callRecords[o.ID] = r.ID
		}
		if r.Kind == "tool_result" && o.Name == "read_file" && o.Phase == "completed" && strings.Contains(string(o.Result), marker) {
			requireUsageSummaryRelation(t, r, "parent_call", callRecords[o.Parent])
			retained = true
		}
	}
	if !retained {
		t.Fatal("explicit read_file evidence was lost after filtering its JS return value")
	}
}
