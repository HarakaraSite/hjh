package jsruntime_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestDirectoryMetadataDotEntriesAndSymlinks(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "a-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".hidden.txt", "a-dir/child.txt", "z.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("a-dir", filepath.Join(workspace, "link")); err != nil {
		t.Fatal(err)
	}
	modified := time.Unix(1700000000, 123456789)
	if err := os.Chtimes(filepath.Join(workspace, ".hidden.txt"), modified, modified); err != nil {
		t.Fatal(err)
	}
	selected := []string{"run_js", "ls", "find"}
	worker := inspectionWorker(t, workspace, &selected)
	type entry struct {
		Name, Path, Type, ModifiedAt string
		Bytes                        int64
		Depth                        int
	}
	value := inspectionCall(t, worker, "ls", map[string]any{"tree": true})
	var listing struct {
		Path          string
		Tree          bool
		Entries       []entry
		Offset, Total int
		NextOffset    *int
	}
	if err := json.Unmarshal(value, &listing); err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{".hidden.txt", "a-dir", "a-dir/child.txt", "link", "z.txt"}
	if listing.Path != "." || !listing.Tree || listing.Offset != 0 || listing.Total != len(wantPaths) || listing.NextOffset != nil {
		t.Fatalf("default listing and symlink traversal: %s", value)
	}
	for i, item := range listing.Entries {
		if item.Path != wantPaths[i] || item.Name != filepath.Base(item.Path) {
			t.Fatalf("sorted depth-first paths: %s", value)
		}
		info, err := os.Lstat(filepath.Join(workspace, item.Path))
		if err != nil {
			t.Fatal(err)
		}
		if item.Bytes != info.Size() || item.ModifiedAt != info.ModTime().Format(time.RFC3339Nano) {
			t.Fatalf("lstat metadata for %s: %+v, %v", item.Path, item, info)
		}
	}
	if listing.Entries[0].Type != "file" || listing.Entries[1].Type != "directory" || listing.Entries[2].Depth != 2 || listing.Entries[3].Type != "symlink" || listing.Entries[3].Depth != 1 {
		t.Fatalf("entry types/depth: %s", value)
	}
	value = inspectionCall(t, worker, "run_js", map[string]string{"code": `return {listing: await tools.ls({path:` + string(mustInspectionJSON(t, workspace)) + `}), found: await tools.find({})};`})
	var absolute struct {
		Listing struct {
			Path    string
			Entries []entry
		}
		Found struct {
			Entries []entry
			Total   int
		}
	}
	if err := json.Unmarshal(value, &absolute); err != nil || absolute.Listing.Path != workspace || len(absolute.Listing.Entries) != 4 || absolute.Listing.Entries[0].Path != filepath.Join(workspace, ".hidden.txt") || absolute.Found.Total != 5 {
		t.Fatalf("absolute paths, dot entries, or symlink traversal: %s (%v)", value, err)
	}
	for _, item := range absolute.Found.Entries {
		if !filepath.IsAbs(item.Path) || strings.Contains(item.Path, "link/") {
			t.Fatalf("find must return absolute paths without descending symlinks: %s", value)
		}
	}
	value = inspectionCall(t, worker, "find", map[string]any{"path": "a-dir/child.txt", "pattern": "*.txt"})
	if !strings.Contains(string(value), filepath.Join(workspace, "a-dir/child.txt")) {
		t.Fatalf("file root discovery: %s", value)
	}
}

func TestFindGlobSegmentsClassesAndEscapes(t *testing.T) {
	workspace := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "é.txt", "literal*.txt", "literal?.txt", "literal[.txt", "nested/a.txt", "nested/deep/c.txt", "skip/a.txt"} {
		filename := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	selected := []string{"run_js", "find"}
	worker := inspectionWorker(t, workspace, &selected)
	for _, test := range []struct {
		pattern string
		want    []string
	}{
		{"a.txt", []string{"a.txt", "nested/a.txt"}},
		{"**/[a-c].txt", []string{"a.txt", "b.txt", "nested/a.txt", "nested/deep/c.txt"}},
		{"[^a-z].txt", []string{"é.txt"}},
		{"?.txt", []string{"a.txt", "b.txt", "nested/a.txt", "nested/deep/c.txt", "é.txt"}},
		{"nested/?.txt", []string{"nested/a.txt"}},
		{"nested/**/*.txt", []string{"nested/a.txt", "nested/deep/c.txt"}},
		{"literal\\*.txt", []string{"literal*.txt"}},
		{"literal\\?.txt", []string{"literal?.txt"}},
		{"literal[\\[].txt", []string{"literal[.txt"}},
		{"[a-é].txt", []string{"a.txt", "b.txt", "nested/a.txt", "nested/deep/c.txt", "é.txt"}},
	} {
		t.Run(test.pattern, func(t *testing.T) {
			value := inspectionCall(t, worker, "find", map[string]any{"pattern": test.pattern, "exclude": []string{"skip"}, "type": "file"})
			var found struct {
				Path    string
				Entries []struct{ Path, Type string }
				Total   int
			}
			if err := json.Unmarshal(value, &found); err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, item := range found.Entries {
				if !filepath.IsAbs(item.Path) || item.Type != "file" {
					t.Fatalf("find result: %s", value)
				}
				paths = append(paths, strings.TrimPrefix(item.Path, workspace+"/"))
			}
			if found.Path != "." || found.Total != len(test.want) || strings.Join(paths, ",") != strings.Join(test.want, ",") {
				t.Fatalf("glob %q: %s, want %v", test.pattern, value, test.want)
			}
		})
	}
	value := inspectionCall(t, worker, "find", map[string]any{"pattern": "nested/**", "exclude": []string{"nested/deep"}})
	var found struct{ Entries []struct{ Path string } }
	if err := json.Unmarshal(value, &found); err != nil || len(found.Entries) != 2 || found.Entries[0].Path != filepath.Join(workspace, "nested") || found.Entries[1].Path != filepath.Join(workspace, "nested/a.txt") {
		t.Fatalf("zero-segment ** or path exclusion pruning: %s (%v)", value, err)
	}
}
