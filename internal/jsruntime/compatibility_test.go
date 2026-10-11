package jsruntime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/jsruntime"
)

// Use the same external tool/hook sources and real host operations for both
// public call routes. This test also runs against the pre-migration checkout.
func TestStandardToolCompatibilityDirectAndGenerated(t *testing.T) {
	large := bytes.Repeat([]byte("x"), 1100000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/raw":
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "互換HTTP")
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html><script>hidden-script</script><body><p>互換Web</p></body></html>")
		case "/large":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(large)
		}
	}))
	defer server.Close()
	for _, generated := range []bool{false, true} {
		t.Run(fmt.Sprintf("generated=%t", generated), func(t *testing.T) {
			dir, workspace := t.TempDir(), t.TempDir()
			if err := extensions.InstallDefaults(dir); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hooks.js"), []byte(`
export function beforeTool({name,args}) {
  return name === 'read_file' && args.path === 'alias.txt' ? {...args,path:'note.txt'} : args;
}
export function afterTool({name,result}) {
  return name === 'read_file' ? {...result,hook:'compatibility'} : result;
}`), 0644); err != nil {
				t.Fatal(err)
			}
			snapshot, err := extensions.CaptureDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			var observations []contracts.Observation
			worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: workspace,
				Observer: func(o contracts.Observation) error { observations = append(observations, o); return nil }})
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Close()
			call := func(name string, args any) json.RawMessage {
				t.Helper()
				input := contracts.JSON(args)
				if generated {
					input = contracts.JSON(map[string]string{"code": "return await tools." + name + "(" + string(input) + ");"})
					name = "run_js"
				}
				value, err := worker.Call(contracts.WithCallParent(context.Background(), "compatibility-root"), name, input)
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			text := "alpha 日本語🙂 alpha"
			var written struct {
				Path  string
				Bytes int
			}
			if err := json.Unmarshal(call("write_file", map[string]any{"path": "note.txt", "text": text}), &written); err != nil {
				t.Fatal(err)
			}
			if written.Path != "note.txt" || written.Bytes != len(text) {
				t.Fatalf("write result = %+v", written)
			}
			var edited struct {
				Path                string
				Replacements, Bytes int
			}
			if err := json.Unmarshal(call("edit_file", map[string]any{"path": "note.txt", "oldText": "alpha", "newText": "beta"}), &edited); err != nil {
				t.Fatal(err)
			}
			want := "beta 日本語🙂 beta"
			if edited.Path != "note.txt" || edited.Replacements != 2 || edited.Bytes != len(want) {
				t.Fatalf("edit result = %+v", edited)
			}
			var read struct{ Path, Text, Hook string }
			if err := json.Unmarshal(call("read_file", map[string]any{"path": "alias.txt"}), &read); err != nil {
				t.Fatal(err)
			}
			if read.Path != "note.txt" || read.Text != want || read.Hook != "compatibility" {
				t.Fatalf("read/hook result = %+v", read)
			}
			file, err := os.ReadFile(filepath.Join(workspace, "note.txt"))
			if err != nil || string(file) != want {
				t.Fatalf("file = %q, %v", file, err)
			}
			var command struct {
				ExitCode       int
				Stdout, Stderr string
			}
			if err := json.Unmarshal(call("command", map[string]any{"program": "sh", "args": []string{"-c", "cat; printf 'command-error' >&2; exit 7"}, "stdin": "command-input"}), &command); err != nil {
				t.Fatal(err)
			}
			if command.ExitCode != 7 || command.Stdout != "command-input" || command.Stderr != "command-error" {
				t.Fatalf("command = %+v", command)
			}
			var raw struct {
				Status int
				Body   string
			}
			if err := json.Unmarshal(call("http", map[string]any{"url": server.URL + "/raw"}), &raw); err != nil {
				t.Fatal(err)
			}
			if raw.Status != 503 || raw.Body != "互換HTTP" {
				t.Fatalf("HTTP = %+v", raw)
			}
			var page string
			if err := json.Unmarshal(call("web_fetch", map[string]any{"url": server.URL + "/page"}), &page); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(page, "互換Web") || strings.Contains(page, "hidden-script") {
				t.Fatalf("Web page = %q", page)
			}
			if err := json.Unmarshal(call("web_fetch", map[string]any{"url": server.URL + "/large"}), &page); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(page, "truncated: true") || !strings.Contains(page, "[body truncated]") {
				t.Fatal("Web cap not retained")
			}
			if err := json.Unmarshal(call("web_fetch", map[string]any{"url": server.URL + "/large", "save_to": "download.bin"}), &page); err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(filepath.Join(workspace, "download.bin"))
			if err != nil || !bytes.Equal(saved, large) {
				t.Fatalf("save_to bytes = %d, %v", len(saved), err)
			}
			starts, ends := map[string]contracts.Observation{}, map[string]contracts.Observation{}
			for _, o := range observations {
				if o.Phase == "started" {
					starts[o.ID] = o
				} else {
					ends[o.ID] = o
				}
			}
			for id, start := range starts {
				end, ok := ends[id]
				if !ok || end.Phase != "completed" {
					t.Fatalf("unfinished operation %s/%s: %+v", start.Kind, start.Name, end)
				}
				if start.Parent != "compatibility-root" {
					if _, ok := starts[start.Parent]; !ok {
						t.Fatalf("missing parent for %+v", start)
					}
				}
			}
		})
	}
}

func TestRunJSCompatibilityValuesAndExceptions(t *testing.T) {
	snapshot, err := extensions.CaptureDir(makeExtensions(t))
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	for _, item := range []struct{ code, want string }{
		{`return {text:'日本語🙂',items:[1,true,null]};`, `{"text":"日本語🙂","items":[1,true,null]}`},
		{`return await Promise.all([Promise.resolve('first'),tools.run_js({code:'return 42;'})]);`, `["first",42]`},
		{`return '文字列';`, `"文字列"`}, {`return 42;`, `42`}, {`return false;`, `false`}, {`return null;`, `null`}, {`return undefined;`, `null`},
	} {
		result, err := worker.Call(context.Background(), "run_js", contracts.JSON(map[string]string{"code": item.code}))
		if err != nil || string(result) != item.want {
			t.Fatalf("%s: result %s, error %v; want %s", item.code, result, err, item.want)
		}
	}
	_, err = worker.Call(context.Background(), "run_js", contracts.JSON(map[string]string{"code": `function compatibilityOriginalFrame() {throw new Error('compatibility-original-error');} compatibilityOriginalFrame();`}))
	if err == nil || !strings.Contains(err.Error(), "compatibility-original-error") {
		t.Fatalf("original exception = %v", err)
	}
	if !strings.Contains(err.Error(), "compatibilityOriginalFrame") {
		t.Fatalf("original JavaScript stack missing: %v", err)
	}
	result, err := worker.Call(context.Background(), "run_js", contracts.JSON(map[string]string{"code": `try {await tools.read_file({path:'compatibility-missing-file'});} catch(e) {return e.message.includes('compatibility-missing-file');}`}))
	if err != nil || string(result) != "true" {
		t.Fatalf("Go error caught in JS = %s, %v", result, err)
	}
}

func TestCapturedYAMLAndSemverCompatibility(t *testing.T) {
	dir := t.TempDir()
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(dir, "vendor"), os.DirFS("../../spikes/s01/s03-tools/vendor")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "modules"), 0755); err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile("../../spikes/s01/s03-tools/modules/catalog.js")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules/catalog.js"), module, 0644); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config extensions.Config
	if err := json.Unmarshal(contents, &config); err != nil {
		t.Fatal(err)
	}
	config.Tools = append(config.Tools, extensions.Tool{Name: "catalog", Description: "Read the prepared ESM catalog", InputSchema: json.RawMessage(`{"type":"object"}`), Module: "modules/catalog.js", Export: "catalog"})
	role := config.Roles["default"]
	role.Tools = append(role.Tools, "catalog")
	config.Roles["default"] = role
	if err := os.WriteFile(filepath.Join(dir, "config.json"), contracts.JSON(config), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "project: compatibility\ndependencies:\n  - name: compatible\n    candidate: 1.4.2\n    range: '^1.0.0'\n  - name: excluded\n    candidate: 2.0.0\n    range: '^1.0.0'\n")
	}))
	defer server.Close()
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	input := contracts.JSON(map[string]string{"url": server.URL, "note": "日本語ESM"})
	for _, generated := range []bool{false, true} {
		name, args := "catalog", input
		if generated {
			name = "run_js"
			args = contracts.JSON(map[string]string{"code": "return await tools.catalog(" + string(input) + ");"})
		}
		result, err := worker.Call(context.Background(), name, args)
		if err != nil {
			t.Fatal(err)
		}
		var parsed struct {
			Project, Note string
			Compatible    []struct{ Name, Version string }
		}
		if err := json.Unmarshal(result, &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Project != "compatibility" || parsed.Note != "日本語ESM" || len(parsed.Compatible) != 1 || parsed.Compatible[0].Name != "compatible" || parsed.Compatible[0].Version != "1.4.2" {
			t.Fatalf("prepared ESM result = %s", result)
		}
	}
}
