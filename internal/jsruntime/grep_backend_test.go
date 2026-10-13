package jsruntime_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepPrefersRGAndWorksWithOnlyGNUGrepOnPATH(t *testing.T) {
	grepPath, err := exec.LookPath("grep")
	if err != nil {
		t.Fatal(err)
	}
	rgPath, rgErr := exec.LookPath("rg")
	for _, backend := range []string{"rg", "grep"} {
		t.Run(backend, func(t *testing.T) {
			if backend == "rg" && rgErr != nil {
				t.Skip("rg is not installed; GNU fallback is checked separately")
			}
			bin := t.TempDir()
			if err := os.Symlink(grepPath, filepath.Join(bin, "grep")); err != nil {
				t.Fatal(err)
			}
			if backend == "rg" {
				if err := os.Symlink(rgPath, filepath.Join(bin, "rg")); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			workspace := t.TempDir()
			for _, dir := range []string{".git", "src"} {
				if err := os.Mkdir(filepath.Join(workspace, dir), 0755); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				".gitignore":                "ignored.txt\n",
				"ignored.txt":               "Target\n",
				".hidden.txt":               "Target\n",
				"src/name\n:with-colon.txt": "first\nTarget\nthird\nTarget again\n",
				"src/binary.txt":            "Target\x00binary\n",
			}
			for name, text := range files {
				if err := os.WriteFile(filepath.Join(workspace, name), []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			selected := []string{"grep", "run_js"}
			worker := inspectionWorker(t, workspace, &selected)
			value := inspectionCall(t, worker, "grep", map[string]any{"pattern": "Target", "output": "files"})
			var names struct {
				Backend       string   `json:"backend"`
				IgnoreApplied bool     `json:"ignoreApplied"`
				Files         []string `json:"files"`
				Matches       int      `json:"matches"`
			}
			if err := json.Unmarshal(value, &names); err != nil {
				t.Fatal(err)
			}
			wantFiles, wantMatches := 2, 3
			if backend == "grep" {
				wantFiles, wantMatches = 3, 4
			}
			if names.Backend != backend || names.IgnoreApplied != (backend == "rg") || len(names.Files) != wantFiles || names.Matches != wantMatches {
				t.Fatalf("backend selection/ignore/file search: %s", value)
			}
			value = inspectionCall(t, worker, "run_js", map[string]string{"code": `
// Search with context, page through it, and check empty results.
const first=await tools.grep({path:"src",pattern:"target",patternKind:"literal",caseSensitive:false,context:1,glob:["*.txt","!binary.txt"],limit:2});
const next=await tools.grep({path:"src",pattern:"target",patternKind:"literal",caseSensitive:false,context:1,glob:["*.txt","!binary.txt"],offset:first.nextOffset});
const none=await tools.grep({path:"src",pattern:"no such text"});
return {first,next,none};`})
			var paged struct {
				First struct {
					Lines []struct {
						Path  string
						Line  int
						Text  string
						Match bool
					}
					Total, Matches, NextOffset int
				}
				Next struct{ Lines []any }
				None struct{ Matches, Total int }
			}
			if err := json.Unmarshal(value, &paged); err != nil {
				t.Fatal(err)
			}
			if paged.First.Matches != 2 || paged.First.Total != 4 || paged.First.NextOffset != 2 || len(paged.Next.Lines) != 2 || paged.None.Matches != 0 || paged.None.Total != 0 {
				t.Fatalf("context/paging/empty result: %s", value)
			}
			for i, line := range paged.First.Lines {
				if !strings.Contains(line.Path, "name\n:with-colon.txt") || line.Line != i+1 || line.Match != (i == 1) || !strings.HasSuffix(line.Text, "\n") {
					t.Fatalf("filename/line/context parsing: %s", value)
				}
			}
			// Native filename bytes must survive the command-to-JSON boundary.
			if backend == "grep" {
				name := string([]byte{'b', 'a', 'd', 0xff})
				if err := os.WriteFile(filepath.Join(workspace, name), []byte("unique-byte-path\n"), 0644); err != nil {
					t.Fatal(err)
				}
				value = inspectionCall(t, worker, "grep", map[string]any{"pattern": "unique-byte-path", "output": "files"})
				var encoded struct {
					Files []struct {
						Base64 string `json:"base64"`
					}
				}
				if err := json.Unmarshal(value, &encoded); err != nil || len(encoded.Files) != 1 {
					t.Fatalf("non-UTF-8 filename: %s, %v", value, err)
				}
				decoded, err := base64.StdEncoding.DecodeString(encoded.Files[0].Base64)
				if err != nil || !strings.HasSuffix(string(decoded), name) {
					t.Fatalf("filename bytes were lost: %s, %v", value, err)
				}
			}
		})
	}
}
