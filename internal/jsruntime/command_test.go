package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestCommandToolExecutionAndErrors(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("HJH_COMMAND_ORDINARY", "ordinary-value")
	t.Setenv("HJH_COMMAND_CREDENTIAL", "credential-must-not-reach-command")
	var observations []contracts.Observation
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace,
		CredentialEnvNames: []string{"HJH_COMMAND_CREDENTIAL"},
		Observer:           func(o contracts.Observation) error { observations = append(observations, o); return nil }})
	for _, generated := range []bool{false, true} {
		t.Run(fmt.Sprintf("generated=%t", generated), func(t *testing.T) {
			call := func(args any) (json.RawMessage, error) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if generated {
					return w.Call(ctx, "run_js", contracts.JSON(map[string]string{"code": "return await tools.command(" + string(contracts.JSON(args)) + ");"}))
				}
				return w.Call(ctx, "command", contracts.JSON(args))
			}
			for _, exitCode := range []int{0, 7} {
				value, err := call(map[string]any{"program": "sh", "args": []string{"-c", fmt.Sprintf(`printf '%%s|%%s|%%s|' "$PWD" "$HJH_COMMAND_ORDINARY" "${HJH_COMMAND_CREDENTIAL-unset}"; cat; printf diagnostic >&2; exit %d`, exitCode)}, "stdin": "input 日本語"})
				if err != nil {
					t.Fatal(err)
				}
				var got struct {
					ExitCode       int
					Stdout, Stderr string
				}
				if err := json.Unmarshal(value, &got); err != nil || got.ExitCode != exitCode || got.Stdout != workspace+"|ordinary-value|unset|input 日本語" || got.Stderr != "diagnostic" {
					t.Fatalf("command result: %s (%v)", value, err)
				}
			}
			missing := filepath.Join(workspace, "missing-program")
			if value, err := call(map[string]string{"program": missing}); err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("launch failure became an exit result: %s %v", value, err)
			}
			if got := stdlibRun(t, w, `return (await tools.command({program:'printf',args:['recovered']})).stdout;`); string(got) != `"recovered"` {
				t.Fatalf("command recovery: %s", got)
			}
		})
	}
	var completed bool
	for _, o := range observations {
		if o.Kind == "host" {
			t.Fatalf("stdlib command emitted host observation: %+v", o)
		}
		completed = completed || (o.Kind == "tool" && o.Name == "command" && o.Phase == "completed")
	}
	if !completed {
		t.Fatal("command tool observation missing")
	}
}

func TestProcessHelperBinaryOutputAndPATH(t *testing.T) {
	workspace, bin := t.TempDir(), t.TempDir()
	if err := os.Symlink("/bin/sh", filepath.Join(bin, "test-shell")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	module := `import { runProcess, resolveExecutable } from './process.js';
export async function candidate(args, api) {
 const program = await resolveExecutable('test-shell');
 const results = [];
 for (const code of [0,7]) {
   const result = await runProcess(program, ['-c', "printf '\\000\\377'; printf '\\376' >&2; exit " + code], {encoding:'buffer'});
   results.push({exitCode:result.exitCode, binary:result.stdout instanceof Uint8Array && result.stderr instanceof Uint8Array, stdout:Array.from(result.stdout), stderr:Array.from(result.stderr)});
 }
 return {program, missing:await resolveExecutable('missing-program'), results,
   obsoleteAPI: 'command' in api || 'resolveExecutable' in api};
}`
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, module, ""), Workspace: workspace})
	value := stdlibRun(t, w, `return await tools.stdlib_candidate({});`)
	want := fmt.Sprintf(`{"program":%q,"missing":null,"results":[{"exitCode":0,"binary":true,"stdout":[0,255],"stderr":[254]},{"exitCode":7,"binary":true,"stdout":[0,255],"stderr":[254]}],"obsoleteAPI":false}`, filepath.Join(bin, "test-shell"))
	if string(value) != want {
		t.Fatalf("binary output or PATH: %s; want %s", value, want)
	}
}

func TestGrepBackendSelectionUsesConfiguredPATH(t *testing.T) {
	// Resolve the real installed programs before constraining the Worker PATH.
	programs := map[string]string{}
	for _, name := range []string{"rg", "grep"} {
		if program, err := exec.LookPath(name); err == nil {
			programs[name] = program
		}
	}
	for _, backend := range []string{"rg", "grep"} {
		t.Run(backend, func(t *testing.T) {
			if programs[backend] == "" {
				t.Skip(backend + " not installed")
			}
			workspace, bin := t.TempDir(), t.TempDir()
			for name, program := range programs {
				if backend == "grep" && name == "rg" {
					// An ordinary non-executable file must not select a backend.
					if err := os.WriteFile(filepath.Join(bin, name), []byte("not executable"), 0644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.Symlink(program, filepath.Join(bin, name)); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("ordinary needle\n"), 0644); err != nil {
				t.Fatal(err)
			}
			w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			value, err := w.Call(ctx, "grep", contracts.JSON(map[string]string{"pattern": "needle", "path": "note.txt"}))
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Backend string
				Matches int
				Lines   []struct {
					Path, Text string
					Line       int
				}
			}
			if err := json.Unmarshal(value, &got); err != nil || got.Backend != backend || got.Matches != 1 || len(got.Lines) != 1 || got.Lines[0].Path != "note.txt" || got.Lines[0].Line != 1 || got.Lines[0].Text != "ordinary needle\n" {
				t.Fatalf("real grep selection: %s (%v)", value, err)
			}
		})
	}
}

// The subprocess runs this test only: no shell or descendants are involved.
func TestCommandCancellationChild(t *testing.T) {
	filename := os.Getenv("HJH_COMMAND_TEST_CHILD")
	if filename == "" {
		return
	}
	if err := os.WriteFile(filename, []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Second)
}

func TestCommandCancellationStopsDirectExecutable(t *testing.T) {
	workspace := t.TempDir()
	filename := filepath.Join(workspace, "child.pid")
	t.Setenv("HJH_COMMAND_TEST_CHILD", filename)
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	w := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := w.Call(ctx, "command", contracts.JSON(map[string]any{"program": program, "args": []string{"-test.run=^TestCommandCancellationChild$"}}))
		done <- err
	}()
	var pid int
	defer func() {
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for pid == 0 {
		if raw, err := os.ReadFile(filename); err == nil {
			pid, _ = strconv.Atoi(string(raw))
		}
		if time.Now().After(deadline) {
			t.Fatal("direct command did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("direct command cancellation did not finish")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("direct executable still present after cancellation: %v", err)
	}
	pid = 0
	if w.Available() {
		t.Fatal("cancelled worker available")
	}
	next := newStdlibWorker(t, Options{Snapshot: stdlibSnapshot(t, "", ""), Workspace: workspace})
	if got := stdlibRun(t, next, `return (await tools.command({program:'printf',args:['next-worker']})).stdout;`); string(got) != `"next-worker"` {
		t.Fatalf("next worker command: %s", got)
	}
}
