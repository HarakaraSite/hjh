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

func TestFileToolsLiteralReplacementsAndPathEcho(t *testing.T) {
	workspace := t.TempDir()
	worker := inspectionWorker(t, workspace, nil)
	inputPath := "nested/../nested/literal.txt"
	original := "aaaaa\n$& 日本🙂\r\n\nend"
	value := inspectionCall(t, worker, "run_js", map[string]any{"code": `
const write = await tools.write_file({path:"nested/../nested/literal.txt", text:"aaaaa\n$& 日本🙂\r\n\nend"});
const edit = await tools.edit_file({path:write.path, edits:[
  {oldText:"aa",newText:"$&"}, {oldText:"$&",newText:"$\u0060$'$$"},
  {oldText:"",newText:"not inserted"}, {oldText:"absent",newText:"unused"}, {oldText:"end",newText:""}
]});
const read = await tools.read_file({path:write.path});
return {write,edit,read};`})
	var result struct {
		Write struct {
			Path  string
			Bytes int
		}
		Edit struct {
			Path                string
			Replacements, Bytes int
			Edits               []struct{ Replacements int }
		}
		Read struct {
			Path, Text                string
			Offset, Lines, TotalLines int
		}
	}
	if err := json.Unmarshal(value, &result); err != nil {
		t.Fatal(err)
	}
	want := "$`$'$$$`$'$$a\n$`$'$$ 日本🙂\r\n\n"
	if result.Write.Path != inputPath || result.Write.Bytes != len(original) || result.Edit.Path != inputPath || result.Edit.Bytes != len(want) || result.Edit.Replacements != 6 || result.Read.Path != inputPath || result.Read.Text != want || result.Read.Offset != 1 || result.Read.Lines != 3 || result.Read.TotalLines != 3 {
		t.Fatalf("literal file workflow: %s", value)
	}
	if len(result.Edit.Edits) != 5 || result.Edit.Edits[0].Replacements != 2 || result.Edit.Edits[1].Replacements != 3 || result.Edit.Edits[2].Replacements != 0 || result.Edit.Edits[3].Replacements != 0 || result.Edit.Edits[4].Replacements != 1 {
		t.Fatalf("literal edit counts: %s", value)
	}
	filename := filepath.Join(workspace, "nested/literal.txt")
	actual, err := os.ReadFile(filename)
	if err != nil || string(actual) != want {
		t.Fatalf("saved file = %q, %v", actual, err)
	}
	value = inspectionCall(t, worker, "read_file", map[string]any{"path": filename, "offset": 2, "limit": 2})
	var window struct {
		Path, Text                string
		Offset, Lines, TotalLines int
	}
	if err := json.Unmarshal(value, &window); err != nil || window.Path != filename || window.Text != "$`$'$$ 日本🙂\r\n\n" || window.Offset != 2 || window.Lines != 2 || window.TotalLines != 3 {
		t.Fatalf("absolute path and LF window: %s (%v)", value, err)
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
	value = inspectionCall(t, worker, "run_js", map[string]string{"code": "const full=await tools.git_inspect({op:'diff'}); const first=await tools.git_inspect({op:'diff',limit:2}); const next=await tools.git_inspect({op:'diff',offset:first.nextOffset}); return {full,first,next};"})
	var pages struct {
		Full, First, Next struct {
			Text          string
			Total, Offset int
			NextOffset    *int
		}
	}
	if err := json.Unmarshal(value, &pages); err != nil || pages.First.NextOffset == nil || *pages.First.NextOffset != 2 || pages.First.Total != pages.Full.Total || pages.Next.Offset != 2 || pages.Next.NextOffset != nil || pages.First.Text+"\n"+pages.Next.Text != pages.Full.Text {
		t.Fatalf("Git diff line pages: %s (%v)", value, err)
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

func TestInspectionFindCountAndReadWorkflow(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "data"), 0755); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{
		"data/a.txt": []byte("one\u0085two\u00a0three\u3000four\r\nfive\ufeffsix"),
		"data/b.txt": append([]byte("\ufeff \t\n\r\v\f\u1680\u2000\u200a\u2028\u2029\u202f\u205f\u3000"), 0xff),
	}
	for name, bytes := range contents {
		if err := os.WriteFile(filepath.Join(workspace, name), bytes, 0644); err != nil {
			t.Fatal(err)
		}
	}
	selected := []string{"run_js", "ls", "find", "wc", "read_file"}
	worker := inspectionWorker(t, workspace, &selected)
	type counts struct {
		Path                string
		Lines, Words, Bytes int
	}
	var direct struct {
		Files []counts
		Total counts
	}
	value := inspectionCall(t, worker, "wc", map[string]any{"paths": []string{"data/b.txt", "data/../data/a.txt"}})
	if err := json.Unmarshal(value, &direct); err != nil {
		t.Fatal(err)
	}
	if len(direct.Files) != 2 || direct.Files[0].Path != "data/b.txt" || direct.Files[1].Path != "data/../data/a.txt" || direct.Files[0].Words != 2 || direct.Files[1].Words != 5 || direct.Files[0].Lines != 1 || direct.Files[1].Lines != 1 || direct.Total.Words != 7 || direct.Total.Lines != 2 || direct.Total.Bytes != len(contents["data/a.txt"])+len(contents["data/b.txt"]) || direct.Total.Path != "" {
		t.Fatalf("byte, Go Unicode whitespace, LF counts, file order, and path echo: %s", value)
	}
	value = inspectionCall(t, worker, "run_js", map[string]string{"code": `const listing = await tools.ls({path:"./data/../data"});
const found = await tools.find({path:"data",pattern:"**/*.txt",type:"file"});
const counts = await tools.wc({paths:found.entries.map(entry=>entry.path)});
const read = await tools.read_file({path:found.entries[0].path});
return {listing,found,counts,read};`})
	var composed struct {
		Listing struct {
			Path    string
			Entries []struct{ Path string }
		}
		Found  struct{ Entries []struct{ Path string } }
		Counts struct {
			Files []counts
			Total counts
		}
		Read struct{ Text string }
	}
	if err := json.Unmarshal(value, &composed); err != nil {
		t.Fatal(err)
	}
	if composed.Listing.Path != "./data/../data" || len(composed.Listing.Entries) != 2 || composed.Listing.Entries[0].Path != "data/a.txt" || len(composed.Found.Entries) != 2 || len(composed.Counts.Files) != 2 || composed.Counts.Files[0].Path != filepath.Join(workspace, "data/a.txt") || composed.Counts.Total.Words != direct.Total.Words || composed.Counts.Total.Bytes != direct.Total.Bytes || composed.Read.Text != string(contents["data/a.txt"]) {
		t.Fatalf("discover-select-count-read composition: %s", value)
	}
}
