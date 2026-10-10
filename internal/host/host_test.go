package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandReturnsNonzeroResultWithoutOpenRouterCredential(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "credential-must-not-reach-child")
	h, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(CommandArgs{
		Program: "sh",
		Args:    []string{"-c", `printf '%s' "$OPENROUTER_API_KEY"; printf 'diagnostic' >&2; exit 7`},
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := h.Call(context.Background(), "command", input)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(CommandResult)
	if result.ExitCode != 7 || result.Stdout != "" || result.Stderr != "diagnostic" {
		t.Fatalf("unexpected command result: %+v", result)
	}
}

func TestCommandCancellationKillsGroupAfterShellExits(t *testing.T) {
	workspace := t.TempDir()
	h, err := New(workspace)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var shellPID int
	defer func() {
		if shellPID > 0 {
			_ = syscall.Kill(-shellPID, syscall.SIGKILL)
		}
	}()
	input, err := json.Marshal(CommandArgs{
		Program: "sh",
		Args:    []string{"-c", `sleep 30 & echo $! > child.pid; echo $$ > shell.pid`},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, callErr := h.Call(ctx, "command", input)
		result <- callErr
	}()

	var childPID int
	deadline := time.Now().Add(3 * time.Second)
	for shellPID == 0 || childPID == 0 {
		if shellPID == 0 {
			if contents, readErr := os.ReadFile(filepath.Join(workspace, "shell.pid")); readErr == nil {
				shellPID, _ = strconv.Atoi(strings.TrimSpace(string(contents)))
			}
		}
		if childPID == 0 {
			if contents, readErr := os.ReadFile(filepath.Join(workspace, "child.pid")); readErr == nil {
				childPID, _ = strconv.Atoi(strings.TrimSpace(string(contents)))
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("shell did not write its process IDs")
		}
		time.Sleep(10 * time.Millisecond)
	}
	deadline = time.Now().Add(3 * time.Second)
	for syscall.Kill(shellPID, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("shell did not exit before the background child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err = <-result:
		t.Fatalf("command returned while its child still held output pipes: %v", err)
	default:
	}

	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("command returned %v after cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command did not kill its process group and finish Wait")
	}
}
