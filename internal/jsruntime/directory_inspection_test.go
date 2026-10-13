package jsruntime_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryTreeDepthExclusionsAndContinuation(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{"src/branch/deep/target.txt", "src/branch/keep.txt", "src/direct.txt", "src/excluded/hidden.txt"} {
		path := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	selected := []string{"run_js", "ls", "find"}
	worker := inspectionWorker(t, workspace, &selected)
	type entry struct {
		Path  string `json:"path"`
		Depth int    `json:"depth"`
	}
	var entries []entry
	for offset := 0; ; {
		value := inspectionCall(t, worker, "ls", map[string]any{"path": "src", "tree": true, "depth": 2, "offset": offset, "limit": 2})
		var page struct {
			Entries    []entry `json:"entries"`
			Total      int     `json:"total"`
			NextOffset *int    `json:"nextOffset"`
		}
		if err := json.Unmarshal(value, &page); err != nil || page.Total != 6 || len(page.Entries) != 2 {
			t.Fatalf("tree page: %s (%v)", value, err)
		}
		entries = append(entries, page.Entries...)
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset != offset+len(page.Entries) {
			t.Fatalf("tree continuation does not follow returned entries: %s", value)
		}
		offset = *page.NextOffset
	}
	want := []entry{{"src/branch", 1}, {"src/branch/deep", 2}, {"src/branch/keep.txt", 2}, {"src/direct.txt", 1}, {"src/excluded", 1}, {"src/excluded/hidden.txt", 2}}
	if string(mustInspectionJSON(t, entries)) != string(mustInspectionJSON(t, want)) {
		t.Fatalf("tree order, depths, or continuation lost entries: %+v", entries)
	}
	value := inspectionCall(t, worker, "ls", map[string]any{"path": "src", "tree": true, "depth": 3})
	var deeper struct{ Entries []entry }
	if err := json.Unmarshal(value, &deeper); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range deeper.Entries {
		if item.Path == "src/branch/deep/target.txt" && item.Depth == 3 {
			found = true
		}
	}
	if !found || len(deeper.Entries) != 7 {
		t.Fatalf("increasing tree depth did not include the nested file: %s", value)
	}
	var paths []string
	for offset := 0; ; {
		code := `return await tools.find({path:"src",pattern:"**/*.txt",type:"file",exclude:["excluded"],limit:1,offset:` + string(mustInspectionJSON(t, offset)) + `});`
		value = inspectionCall(t, worker, "run_js", map[string]string{"code": code})
		var page struct {
			Entries []struct{ Path string } `json:"entries"`
			Total   int                     `json:"total"`
			Next    *int                    `json:"nextOffset"`
		}
		if err := json.Unmarshal(value, &page); err != nil || page.Total != 3 || len(page.Entries) != 1 {
			t.Fatalf("find page/exclusion: %s (%v)", value, err)
		}
		paths = append(paths, strings.TrimPrefix(page.Entries[0].Path, filepath.ToSlash(workspace)+"/"))
		if page.Next == nil {
			break
		}
		if *page.Next != offset+1 {
			t.Fatalf("find continuation does not follow returned entries: %s", value)
		}
		offset = *page.Next
	}
	if strings.Join(paths, ",") != "src/branch/deep/target.txt,src/branch/keep.txt,src/direct.txt" {
		t.Fatalf("find skipped or duplicated paths, or traversed the excluded directory: %v", paths)
	}
}

func mustInspectionJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
