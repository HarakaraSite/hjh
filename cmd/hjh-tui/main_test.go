package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestVersionRunsBeforeCLIStartup(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "hjh ") || !strings.Contains(stdout.String(), "binary sha256:") {
		t.Fatalf("version output = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("version wrote to stderr: %q", stderr.String())
	}
}

func TestDefaultStartupUsesLocalCoreAndCurrentWorkspace(t *testing.T) {
	startup, options, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if !startup.StartCore || startup.Endpoint != "http://127.0.0.1:8765" || options.Workspace != "" || startup.Workspace != cwd {
		t.Fatalf("default startup = %+v; TUI workspace = %q", startup, options.Workspace)
	}
}

func TestExplicitEndpointRequiresExplicitStartup(t *testing.T) {
	connect, options, err := parseOptions([]string{"--endpoint", "http://core.example:8765"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if connect.StartCore || options.Workspace != "" {
		t.Fatalf("explicit connection gained local startup or workspace: %+v, %+v", connect, options)
	}
	start, _, err := parseOptions([]string{"--endpoint", "http://127.0.0.1:18876", "--start-core"}, io.Discard)
	if err != nil || !start.StartCore {
		t.Fatalf("explicit local startup = %+v, %v", start, err)
	}
	disabled, _, err := parseOptions([]string{"--start-core=false"}, io.Discard)
	if err != nil || disabled.StartCore || disabled.Endpoint != "http://127.0.0.1:8765" {
		t.Fatalf("disabled default startup = %+v, %v", disabled, err)
	}
}

func TestOptionalRunValuesPreserveAbsentAndExplicitZero(t *testing.T) {
	_, absent, err := parseOptions([]string{"--endpoint", "http://127.0.0.1:7483"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if absent.MaxSteps != nil || absent.FromTurn != nil {
		t.Fatalf("omitted options became explicit values: maxSteps=%v fromTurn=%v", absent.MaxSteps, absent.FromTurn)
	}
	_, zero, err := parseOptions([]string{"--endpoint", "http://127.0.0.1:7483", "--max-steps=0", "--from-turn=0"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if zero.MaxSteps == nil || *zero.MaxSteps != 0 || zero.FromTurn == nil || *zero.FromTurn != 0 {
		t.Fatalf("explicit zero was not retained: maxSteps=%v fromTurn=%v", zero.MaxSteps, zero.FromTurn)
	}
}

func TestContinueFlagSelectsLatestWithoutAnExplicitSession(t *testing.T) {
	_, options, err := parseOptions([]string{"--continue", "--workspace", "/work"}, io.Discard)
	if err != nil || !options.Continue || options.SessionID != "" || options.Workspace != "/work" {
		t.Fatalf("continue options = %+v, %v", options, err)
	}
	if _, _, err := parseOptions([]string{"--continue", "--session", "12345678"}, io.Discard); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("explicit and latest Session choices conflict: %v", err)
	}
}
