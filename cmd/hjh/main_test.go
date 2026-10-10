package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRolesCommandDoesNotOpenHistoryOrRequireCredentials(t *testing.T) {
	workspace := t.TempDir()
	database := filepath.Join(workspace, "missing", "history.sqlite3")
	var stdout, stderr bytes.Buffer
	code := run([]string{"roles", "--workspace", workspace, "--db", database}, os.Stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("roles exit code = %d, stderr = %s", code, stderr.String())
	}
	var catalog struct {
		DefaultRole string `json:"defaultRole"`
		Roles       []struct {
			Name  string   `json:"name"`
			Tools []string `json:"tools"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &catalog); err != nil {
		t.Fatalf("roles output is not JSON: %v; output=%s", err, stdout.String())
	}
	if catalog.DefaultRole == "" || len(catalog.Roles) == 0 {
		t.Fatalf("roles output lacks default/catalog: %#v", catalog)
	}
	if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("roles command touched history database path: stat error = %v", err)
	}
}

func TestInputCancellationReleasesRead(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := readInput(ctx, reader); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stdin read remained blocked after cancellation")
	}
}

func TestInheritedPipeCancellationBeforeEOF(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	fd, err := syscall.Dup(int(reader.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	input := os.NewFile(uintptr(fd), "inherited-stdin")
	defer input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := readInput(ctx, input); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("inherited stdin remained blocked without EOF")
	}
}

func TestLoginCallbackReadCanBeCancelledWithoutBrowserResponse(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := readLoginLine(ctx, reader); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login callback read stayed blocked")
	}
}
