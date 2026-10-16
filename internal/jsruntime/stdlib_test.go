package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/host"
)

func newStdlibWorker(t *testing.T, options Options) *Worker {
	t.Helper()
	w, err := New(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w
}

func stdlibSnapshot(t *testing.T, module, hooks string) extensions.Snapshot {
	t.Helper()
	dir := t.TempDir()
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	if module != "" {
		if err := os.WriteFile(filepath.Join(dir, "stdlib-candidate.js"), []byte(module), 0644); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(dir, "config.json")
		raw, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]json.RawMessage
		if err = json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		var tools []extensions.Tool
		if err = json.Unmarshal(config["tools"], &tools); err != nil {
			t.Fatal(err)
		}
		tools = append(tools, extensions.Tool{Name: "stdlib_candidate", Module: "stdlib-candidate.js", Export: "candidate", Description: "candidate", InputSchema: json.RawMessage(`{"type":"object"}`)})
		config["tools"] = contracts.JSON(tools)
		var roles map[string]extensions.Role
		if err = json.Unmarshal(config["roles"], &roles); err != nil {
			t.Fatal(err)
		}
		role := roles["default"]
		role.Tools = append(role.Tools, "stdlib_candidate")
		roles["default"] = role
		config["roles"] = contracts.JSON(roles)
		if err = os.WriteFile(configPath, contracts.JSON(config), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if hooks != "" {
		if err := os.WriteFile(filepath.Join(dir, "hooks.js"), []byte(hooks), 0644); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func stdlibRun(t *testing.T, w *Worker, code string) json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := w.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": code}))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStdlibWorkerIOAndState(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	t.Setenv("HJH_STDLIB_ORDINARY", "ordinary-value")
	t.Setenv("HJH_STDLIB_CREDENTIAL", "test-credential-do-not-expose")
	t.Setenv("OPENROUTER_API_KEY", "test-existing-credential-do-not-expose")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected Authorization on ordinary fetch")
		}
		_, _ = w.Write([]byte("local-http-body"))
	}))
	defer server.Close()
	var requests atomic.Int32
	client := &http.Client{Transport: stdlibRoundTripper(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return http.DefaultTransport.RoundTrip(r)
	})}
	h := &host.Host{Workspace: workspace, HTTPClient: client, CommandEnv: map[string]string{"not": "mutated"}}
	var observations []contracts.Observation
	snapshot := stdlibSnapshot(t, `import { basename } from 'node:path';
import { promises as fs } from 'node:fs';
let calls=0;
export async function candidate({path}) {
 await new Promise(resolve=>setTimeout(resolve,2));
 return {calls:++calls,name:basename(path),text:await fs.readFile(path,'utf8')};
}`, `import { join } from 'node:path';
export async function beforeTurn() {
 await new Promise(resolve=>setTimeout(resolve,2));
 return join('hook','timer');
}`)
	w := newStdlibWorker(t, Options{Snapshot: snapshot, Workspace: workspace, Host: h, CredentialEnvNames: []string{"HJH_STDLIB_CREDENTIAL"}, Observer: func(o contracts.Observation) error { observations = append(observations, o); return nil }})
	filename := filepath.Join(workspace, "note.txt")
	code := fmt.Sprintf(`const fs=await import('node:fs'); const path=await import('path'); const cp=await import('node:child_process');
await new Promise(resolve=>setTimeout(resolve,3));
await fs.promises.writeFile(%q,'hello'); await fs.promises.writeFile(%q,'outside');
const response=await fetch(%q); const body=await response.text();
const command=await cp.execFile('sh',['-c','printf "%%s|%%s|%%s|%%s" "$PWD" "$HJH_STDLIB_ORDINARY" "${HJH_STDLIB_CREDENTIAL-unset}" "${OPENROUTER_API_KEY-unset}"; cat; printf stderr >&2'],{input:'stdin'});
const toolCommand=await tools.command({program:'sh',args:['-c','printf "%%s|%%s|%%s" "$HJH_STDLIB_ORDINARY" "${HJH_STDLIB_CREDENTIAL-unset}" "${OPENROUTER_API_KEY-unset}"']});
const nested=await tools.stdlib_candidate({path:%q});
globalThis.stdlibState={body,command,toolCommand,nested};
return {text:await fs.promises.readFile(%q,'utf8'),outside:await fs.promises.readFile(%q,'utf8'),resolved:path.resolve(process.cwd(),'note.txt'),cwd:process.cwd(),hasCredential:Object.hasOwn(process.env,'HJH_STDLIB_CREDENTIAL'),hasExistingCredential:Object.hasOwn(process.env,'OPENROUTER_API_KEY'),state:globalThis.stdlibState};`, filename, outside, server.URL, filename, filename, outside)
	result := stdlibRun(t, w, code)
	var got struct {
		Text, Outside, Resolved, Cwd         string
		HasCredential, HasExistingCredential bool
		State                                struct {
			Body        string
			Command     struct{ Stdout, Stderr string }
			ToolCommand struct {
				ExitCode       int
				Stdout, Stderr string
			}
			Nested struct {
				Calls      int
				Name, Text string
			}
		}
	}
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "hello" || got.Outside != "outside" || got.Resolved != filename || got.Cwd != workspace || got.HasCredential || got.HasExistingCredential {
		t.Fatalf("file/process values: %s", result)
	}
	if got.State.Body != "local-http-body" || got.State.Command.Stdout != workspace+"|ordinary-value|unset|unsetstdin" || got.State.Command.Stderr != "stderr" || got.State.ToolCommand.Stdout != "ordinary-value|unset|unset" || got.State.Nested.Calls != 1 || got.State.Nested.Text != "hello" {
		t.Fatalf("I/O results: %s", result)
	}
	if requests.Load() != 1 {
		t.Fatalf("fetch did not use host client: %d", requests.Load())
	}
	if len(h.CommandEnv) != 1 || h.CommandEnv["not"] != "mutated" {
		t.Fatal("Worker mutated supplied Host")
	}
	if next := stdlibRun(t, w, fmt.Sprintf(`const nested=await tools.stdlib_candidate({path:%q});return {calls:nested.calls,body:globalThis.stdlibState.body};`, filename)); string(next) != `{"calls":2,"body":"local-http-body"}` {
		t.Fatalf("next-call state: %s", next)
	}
	hook, err := w.Hook(t.Context(), "beforeTurn", json.RawMessage(`{}`))
	if err != nil || string(hook) != `"hook/timer"` {
		t.Fatalf("stdlib hook: %s %v", hook, err)
	}
	var outer, hookRecorded, nestedRecorded bool
	for _, o := range observations {
		if o.Kind == "host" {
			t.Fatalf("individual stdlib I/O observed: %+v", o)
		}
		outer = outer || (o.Kind == "tool" && o.Name == "run_js" && o.Phase == "completed")
		hookRecorded = hookRecorded || (o.Kind == "hook" && o.Name == "beforeTurn" && o.Phase == "completed")
		nestedRecorded = nestedRecorded || (o.Kind == "tool" && o.Name == "stdlib_candidate" && o.Phase == "completed")
	}
	if !outer || !hookRecorded || !nestedRecorded {
		t.Fatal("tool/hook observations lost")
	}
}

type stdlibRoundTripper func(*http.Request) (*http.Response, error)

func (f stdlibRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStdlibDrainsMixedWorkBeforeNextCall(t *testing.T) {
	workspace := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("drained"))
	}))
	defer server.Close()
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace})
	filename := filepath.Join(workspace, "drained.txt")
	code := fmt.Sprintf(`globalThis.events=[];
const fs=await import('fs'); const cp=await import('child_process');
fs.promises.writeFile(%q,'write-drained').then(()=>events.push('file'));
tools.write_file({path:'nested/tool-drained.txt',text:'before'}).then(()=>tools.edit_file({path:'nested/tool-drained.txt',edits:[{oldText:'before',newText:'after'}]})).then(()=>events.push('file-tools'));
fetch(%q).then(r=>r.text()).then(text=>events.push(text));
cp.execFile('sh',['-c','sleep 0.02; printf command']).then(r=>events.push(r.stdout));
tools.http({url:%q}).then(()=>{events.push('http-tool');setTimeout(()=>events.push('http-tool-timer'),10);});
setTimeout(()=>{events.push('timer');tools.read_file({path:%q}).then(()=>events.push('timer-tool'));},30);
let n=0;const id=setInterval(()=>{if(++n===2){clearInterval(id);events.push('interval');}},5);
queueMicrotask(()=>events.push('microtask'));
return 'started';`, filename, server.URL, server.URL, filename)
	if got := stdlibRun(t, w, code); string(got) != `"started"` {
		t.Fatalf("root value changed: %s", got)
	}
	raw, err := os.ReadFile(filename)
	if err != nil || string(raw) != "write-drained" {
		t.Fatalf("unawaited write not drained: %s %v", raw, err)
	}
	toolRaw, err := os.ReadFile(filepath.Join(workspace, "nested/tool-drained.txt"))
	if err != nil || string(toolRaw) != "after" {
		t.Fatalf("unawaited file-tool composition not drained: %s %v", toolRaw, err)
	}
	next := stdlibRun(t, w, `return events.slice().sort();`)
	if string(next) != `["command","drained","file","file-tools","http-tool","http-tool-timer","interval","microtask","timer","timer-tool"]` {
		t.Fatalf("work crossed call boundary or was lost: %s", next)
	}
}

func TestStdlibCancellationRemainsAttachedThroughDrain(t *testing.T) {
	for _, kind := range []string{"timer", "fetch", "command"} {
		t.Run(kind, func(t *testing.T) {
			started := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { started <- struct{}{}; <-r.Context().Done() }))
			defer server.Close()
			w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: t.TempDir()})
			code := `setInterval(()=>{},1000);return 'root-settled';`
			if kind == "fetch" {
				code = fmt.Sprintf(`fetch(%q);return 'root-settled';`, server.URL)
			}
			if kind == "command" {
				code = `const cp=await import('child_process');cp.execFile('sleep',['60']);return 'root-settled';`
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := w.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": code})); done <- err }()
			if kind == "fetch" {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("fetch did not start")
				}
			} else {
				time.Sleep(30 * time.Millisecond)
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel lost: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("drain ignored request cancellation")
			}
			if w.Available() {
				t.Fatal("cancelled Worker available")
			}
		})
	}
}

func TestStdlibRawRelativeFSUsesHostCwd(t *testing.T) {
	// Raw stdlib fs uses the host process cwd; process.cwd() is the workspace.
	// Standard tools resolve their own paths to workspace-absolute paths.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace})
	// The relative file is read-only repository source; no global chdir or
	// relative writes are needed to show which directory fs actually uses.
	expected, err := os.ReadFile(filepath.Join(cwd, "bootstrap.go"))
	if err != nil {
		t.Fatal(err)
	}
	result := stdlibRun(t, w, `const fs=await import('fs');return {raw:fs.readFileSync('bootstrap.go','utf8'),workspace:process.cwd()};`)
	var got struct{ Raw, Workspace string }
	if err = json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	if got.Raw != string(expected) || got.Workspace != workspace {
		t.Fatalf("relative fs evidence changed: %s", result)
	}
	t.Logf("raw fs read bootstrap.go from host cwd=%s, process.cwd()=%s; use absolute paths", cwd, got.Workspace)
}

func TestStdlibOuterRejectionStopsIOAndResetsCapturedVM(t *testing.T) {
	workspace := t.TempDir()
	initPath := filepath.Join(workspace, "module-loads.txt")
	pidPath := filepath.Join(workspace, "process.pid")
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			<-started
			_, _ = w.Write([]byte("ready"))
			return
		}
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	module := fmt.Sprintf("import * as fs from 'fs'; fs.appendFileSync(%q,'loaded\\n'); let calls=0; export function candidate(){return {calls:++calls,revision:'captured'};}", initPath)
	snapshot := stdlibSnapshot(t, module, "")
	var observations []contracts.Observation
	w := newStdlibWorker(t, Options{Snapshot: snapshot, Workspace: workspace, Observer: func(o contracts.Observation) error { observations = append(observations, o); return nil }})
	capturedHandlers := strings.Join(w.HookHandlers(), ",")
	if got := stdlibRun(t, w, "globalThis.beforeFailure=true; return await tools.stdlib_candidate({});"); string(got) != `{"calls":1,"revision":"captured"}` {
		t.Fatalf("initial state: %s", got)
	}
	// Editing deployed source must not affect recreation from the same capture.
	if err := os.WriteFile(filepath.Join(snapshot.Root, "stdlib-candidate.js"), []byte("throw new Error('edited module must not load');"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot.Root, "hooks.js"), []byte("export function runtimeStop(){throw new Error('edited hook');}"), 0644); err != nil {
		t.Fatal(err)
	}
	code := fmt.Sprintf(`const fs=await import('fs'); const cp=await import('child_process');
 setInterval(()=>{globalThis.intervalLeaked=true;},5);
 fetch(%q).then(r=>r.text());
 await (await fetch(%q)).text();
 cp.execFile('sh',['-c',%q]);
 while(!fs.existsSync(%q)) await new Promise(resolve=>setTimeout(resolve,2));
 function outerFailureFrame(){throw new Error('outer-rejection-marker');} outerFailureFrame();`, server.URL+"/hang", server.URL+"/ready", fmt.Sprintf("printf '%%s' \"$$\" > %s; exec sleep 60", pidPath), pidPath)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := w.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": code}))
	if err == nil || !strings.Contains(err.Error(), "outer-rejection-marker") || !strings.Contains(err.Error(), "outerFailureFrame") || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("outer exception/stack lost: %v", err)
	}
	if !w.Available() {
		t.Fatal("rejected Worker is unavailable")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("stdlib HTTP was not cancelled")
	}
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for proc.Signal(syscall.Signal(0)) == nil {
		if time.Now().After(deadline) {
			t.Fatal("stdlib direct child was not stopped and reaped")
		}
		time.Sleep(time.Millisecond)
	}
	if got, err := os.ReadFile(initPath); err != nil || string(got) != "loaded\n" {
		t.Fatalf("failed call was automatically retried: %q %v", got, err)
	}
	next := stdlibRun(t, w, `return {state:typeof beforeFailure,leak:typeof intervalLeaked,module:await tools.stdlib_candidate({})};`)
	if string(next) != `{"state":"undefined","leak":"undefined","module":{"calls":1,"revision":"captured"}}` {
		t.Fatalf("VM not reset from capture: %s", next)
	}
	if got, err := os.ReadFile(initPath); err != nil || string(got) != "loaded\nloaded\n" {
		t.Fatalf("module top-level did not rerun: %q %v", got, err)
	}
	if got := strings.Join(w.HookHandlers(), ","); got != capturedHandlers {
		t.Fatalf("captured handlers changed: %s", got)
	}
	var failed, completed bool
	for _, o := range observations {
		if o.Kind == "tool" && o.Name == "run_js" {
			failed = failed || o.Phase == "failed"
			completed = completed || o.Phase == "completed"
		}
	}
	if !failed || !completed {
		t.Fatalf("outer observations lost: %+v", observations)
	}
}

func TestStdlibHookFailureReopensFreshCapturedVM(t *testing.T) {
	snapshot := stdlibSnapshot(t, "", `let calls=0; export async function beforeTurn({fail}){const call=++calls; if(fail){setInterval(()=>{},5);throw new Error('hook-reset-marker');} await new Promise(resolve=>setTimeout(resolve,2));return call;}`)
	w := newStdlibWorker(t, Options{Snapshot: snapshot, Workspace: t.TempDir()})
	if got, err := w.Hook(t.Context(), "beforeTurn", contracts.JSON(map[string]bool{"fail": false})); err != nil || string(got) != "1" {
		t.Fatalf("initial hook: %s %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(snapshot.Root, "hooks.js"), []byte("throw new Error('edited hook must not load');"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := w.Hook(ctx, "beforeTurn", contracts.JSON(map[string]bool{"fail": true})); err == nil || !strings.Contains(err.Error(), "hook-reset-marker") || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hook exception: %v", err)
	}
	if !w.Available() {
		t.Fatal("hook rejection retired Worker")
	}
	if got, err := w.Hook(t.Context(), "beforeTurn", contracts.JSON(map[string]bool{"fail": false})); err != nil || string(got) != "1" {
		t.Fatalf("next hook not fresh/captured: %s %v", got, err)
	}
}

func TestStdlibCaughtErrorRetainsStateAndDrains(t *testing.T) {
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "let calls=0;export function candidate(){return ++calls;}", ""), Workspace: t.TempDir()})
	got := stdlibRun(t, w, `globalThis.retained=1;await tools.stdlib_candidate({});try{await tools.read_file({path:'missing-caught-file'});}catch(error){retained++;}setTimeout(()=>{retained++;},5);return retained;`)
	if string(got) != "2" {
		t.Fatalf("caught error result: %s", got)
	}
	next := stdlibRun(t, w, `return {state:retained,module:await tools.stdlib_candidate({})};`)
	if string(next) != `{"state":3,"module":2}` {
		t.Fatalf("caught error reset VM or skipped drain: %s", next)
	}
}
