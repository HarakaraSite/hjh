package jsruntime_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/jsruntime"
)

func inspectionWorker(t *testing.T, workspace string, tools *[]string) *jsruntime.Worker {
	t.Helper()
	dir := t.TempDir()
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureWithOptions(filepath.Join(dir, "config.json"), extensions.CaptureOptions{Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close() })
	return worker
}

func inspectionCall(t *testing.T, worker *jsruntime.Worker, name string, args any) json.RawMessage {
	t.Helper()
	value, err := worker.Call(context.Background(), name, contracts.JSON(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}

func TestFileWindowsOrderedEditsAndMissingParentDirectories(t *testing.T) {
	workspace := t.TempDir()
	worker := inspectionWorker(t, workspace, nil)
	inspectionCall(t, worker, "write_file", map[string]any{"path": "new/nested/example.txt", "text": "alpha alpha\r\nbeta\n最後"})
	value := inspectionCall(t, worker, "read_file", map[string]any{"path": "new/nested/example.txt", "offset": 2, "limit": 1})
	var read struct {
		Text       string `json:"text"`
		Lines      int    `json:"lines"`
		TotalLines int    `json:"totalLines"`
		NextOffset int    `json:"nextOffset"`
	}
	if err := json.Unmarshal(value, &read); err != nil || read.Text != "beta\n" || read.Lines != 1 || read.TotalLines != 3 || read.NextOffset != 3 {
		t.Fatalf("line window lost content or continuation: %s (%v)", value, err)
	}
	value = inspectionCall(t, worker, "read_file", map[string]any{"path": "new/nested/example.txt", "offset": read.NextOffset})
	read.NextOffset = 0
	if err := json.Unmarshal(value, &read); err != nil || read.Text != "最後" || read.Lines != 1 || read.NextOffset != 0 {
		t.Fatalf("continuation changed the final line: %s (%v)", value, err)
	}
	words := "hello\u3000world\n"
	inspectionCall(t, worker, "write_file", map[string]any{"path": "new/nested/words.txt", "text": words})
	value = inspectionCall(t, worker, "wc", map[string]any{"paths": []string{"new/nested/example.txt", "new/nested/words.txt"}})
	var counts struct {
		Files []struct{ Lines, Words, Bytes int } `json:"files"`
		Total struct{ Lines, Words, Bytes int }   `json:"total"`
	}
	if err := json.Unmarshal(value, &counts); err != nil || len(counts.Files) != 2 || counts.Files[1].Words != 2 || counts.Total.Lines != 3 || counts.Total.Words != 6 || counts.Total.Bytes != len("alpha alpha\r\nbeta\n最後")+len(words) {
		t.Fatalf("multi-file counts or Unicode whitespace: %s (%v)", value, err)
	}
	value = inspectionCall(t, worker, "run_js", map[string]string{"code": `
// Apply dependent replacements and read the saved file.
const edit = await tools.edit_file({path:"new/nested/example.txt", edits:[
  {oldText:"alpha",newText:"beta"}, {oldText:"beta",newText:"B"},
  {oldText:"absent",newText:"unused"}, {oldText:"最後",newText:""}
]});
const read = await tools.read_file({path:"new/nested/example.txt"});
return {edit,read};`})
	var result struct {
		Edit struct {
			Replacements int `json:"replacements"`
			Edits        []struct {
				Replacements int `json:"replacements"`
			} `json:"edits"`
		} `json:"edit"`
		Read struct{ Text string } `json:"read"`
	}
	if err := json.Unmarshal(value, &result); err != nil {
		t.Fatal(err)
	}
	if result.Read.Text != "B B\r\nB\n" || result.Edit.Replacements != 6 || len(result.Edit.Edits) != 4 || result.Edit.Edits[1].Replacements != 3 || result.Edit.Edits[2].Replacements != 0 {
		t.Fatalf("ordered edits did not use the preceding edit's text: %s", value)
	}
	actual, err := os.ReadFile(filepath.Join(workspace, "new/nested/example.txt"))
	if err != nil || string(actual) != result.Read.Text {
		t.Fatalf("saved file differs: %q (%v)", actual, err)
	}
}

func TestReadOnlyToolsDirectAndGeneratedOnFilesAndGit(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "src/empty"), 0755); err != nil {
		t.Fatal(err)
	}
	text := "one\nTarget\nthree\nTarget end"
	if err := os.WriteFile(filepath.Join(workspace, "src/example.txt"), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = workspace
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, output, err)
		}
	}
	git("init", "-q")
	git("add", "src/example.txt")
	git("commit", "-qm", "initial")
	git("commit", "--allow-empty", "-qm", "second")
	if err := os.WriteFile(filepath.Join(workspace, "src/example.txt"), []byte(text+"\nchanged\n"), 0644); err != nil {
		t.Fatal(err)
	}
	selected := []string{"run_js", "read_file", "ls", "find", "grep", "wc", "git_inspect"}
	worker := inspectionWorker(t, workspace, &selected)
	value := inspectionCall(t, worker, "ls", map[string]any{"path": "src", "tree": true, "limit": 1})
	var listing struct {
		Entries    []any `json:"entries"`
		Total      int   `json:"total"`
		NextOffset int   `json:"nextOffset"`
	}
	if err := json.Unmarshal(value, &listing); err != nil || listing.Total != 2 || len(listing.Entries) != 1 || listing.NextOffset != 1 {
		t.Fatalf("directory paging: %s (%v)", value, err)
	}
	for _, op := range []string{"status", "diff", "show", "log"} {
		args := map[string]any{"op": op, "paths": []string{"src"}, "limit": 1}
		if op == "show" {
			args["rev"] = "HEAD~1"
		}
		value = inspectionCall(t, worker, "git_inspect", args)
		if !strings.Contains(string(value), `"text":`) {
			t.Fatalf("git %s omitted output: %s", op, value)
		}
	}
	value = inspectionCall(t, worker, "run_js", map[string]string{"code": `
// Inspect the repository using only the selected read tools.
const found = await tools.find({path:"src", pattern:"**/*.txt", type:"file"});
const empty = await tools.find({path:"src", type:"directory"});
const grep = await tools.grep({path:"src", pattern:"target", patternKind:"literal",caseSensitive:false,context:1,limit:2});
const remaining = await tools.grep({path:"src",pattern:"target",caseSensitive:false,context:1,offset:grep.nextOffset});
const files = await tools.grep({path:"src",pattern:"Target",output:"files"});
const none = await tools.grep({path:"src",pattern:"does not exist"});
const counts = await tools.wc({paths:found.entries.map(e=>e.path)});
const diff = await tools.git_inspect({op:"diff",paths:["src"],context:0});
const log = await tools.git_inspect({op:"log",limit:1});
const older = await tools.git_inspect({op:"log",offset:log.nextOffset,limit:1});
return {found,empty,grep,remaining,files,none,counts,diff,log,older,hasCommand:typeof tools.command !== "undefined",hasWrite:typeof tools.write_file !== "undefined"};`})
	var result struct {
		Found struct{ Entries []any } `json:"found"`
		Empty struct{ Entries []any } `json:"empty"`
		Grep  struct {
			Matches int   `json:"matches"`
			Lines   []any `json:"lines"`
		} `json:"grep"`
		Remaining struct{ Lines []any }    `json:"remaining"`
		Files     struct{ Files []string } `json:"files"`
		None      struct{ Matches int }    `json:"none"`
		Counts    struct {
			Total struct{ Lines, Words, Bytes int64 } `json:"total"`
		} `json:"counts"`
		Diff       struct{ Text string } `json:"diff"`
		Log        struct{ Text string } `json:"log"`
		Older      struct{ Text string } `json:"older"`
		HasCommand bool                  `json:"hasCommand"`
		HasWrite   bool                  `json:"hasWrite"`
	}
	if err := json.Unmarshal(value, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Found.Entries) != 1 || len(result.Empty.Entries) != 1 || result.Grep.Matches != 2 || len(result.Grep.Lines) != 2 || len(result.Remaining.Lines) == 0 || len(result.Files.Files) != 1 || result.None.Matches != 0 || result.HasCommand || result.HasWrite {
		t.Fatalf("read-only discovery/search/tool selection: %s", value)
	}
	if result.Counts.Total.Lines != 5 || result.Counts.Total.Words != 6 || result.Counts.Total.Bytes != int64(len(text+"\nchanged\n")) || !strings.Contains(result.Diff.Text, "+changed") || !strings.Contains(result.Log.Text, "second") || !strings.Contains(result.Older.Text, "initial") {
		t.Fatalf("file counts or Git inspection: %s", value)
	}
	git("add", "src/example.txt")
	value = inspectionCall(t, worker, "git_inspect", map[string]any{"op": "diff", "staged": true, "stat": true, "context": 0, "paths": []string{"src"}})
	if !strings.Contains(string(value), "example.txt") || !strings.Contains(string(value), "insertion") {
		t.Fatalf("staged diffstat: %s", value)
	}
	if strings.Contains(string(value), "diff --git") || strings.Contains(string(value), "@@") {
		t.Fatalf("stat with context returned a patch: %s", value)
	}
	value = inspectionCall(t, worker, "git_inspect", map[string]any{"op": "show", "rev": "HEAD~1", "stat": true, "context": 0, "paths": []string{"src"}})
	if !strings.Contains(string(value), "example.txt") || strings.Contains(string(value), "diff --git") || strings.Contains(string(value), "@@") {
		t.Fatalf("show stat with context: %s", value)
	}
}
