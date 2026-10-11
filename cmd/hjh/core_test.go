package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hjh/internal/apiclient"
	"hjh/internal/corecatalog"
)

func TestCoreCLIUsesServeRegistrationAndGracefulStop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	_ = reserved.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	workspace := t.TempDir()
	finished := make(chan int, 1)
	go func() { finished <- serve(ctx, workspace, "", "", "", "", "", 0, 0, address, io.Discard) }()
	client, err := apiclient.New("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	coreID := ""
	for ctx.Err() == nil {
		core, err := client.Core(ctx)
		if err == nil {
			coreID = core.CoreID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if coreID == "" {
		t.Fatal("serve did not become ready")
	}
	var stdout, stderr bytes.Buffer
	if code := runCore(ctx, []string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code %d: %s", code, stderr.String())
	}
	var list []corecatalog.Status
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil || len(list) != 1 || list[0].CoreID != coreID || list[0].State != "running" {
		t.Fatalf("list = %s (%v)", stdout.String(), err)
	}
	stdout.Reset()
	if code := runCore(ctx, []string{"status", "--core", coreID[:8], "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status code %d: %s", code, stderr.String())
	}
	var status corecatalog.Status
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil || status.Workspace != workspace || status.Sessions == nil || *status.Sessions != 0 {
		t.Fatalf("status = %s (%v)", stdout.String(), err)
	}
	stdout.Reset()
	if code := runCore(ctx, []string{"stop"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "Select a Core") {
		t.Fatalf("target-free stop code %d: %s", code, stderr.String())
	}
	if _, err := client.Core(ctx); err != nil {
		t.Fatal("target-free stop stopped the Core")
	}
	stdout.Reset()
	stderr.Reset()
	if code := runCore(ctx, []string{"stop", "--core", coreID[:8]}, &stdout, &stderr); code != 0 {
		t.Fatalf("stop code %d: %s", code, stderr.String())
	}
	select {
	case code := <-finished:
		if code != 0 {
			t.Fatalf("serve exit %d", code)
		}
	case <-ctx.Done():
		t.Fatal("stop did not wait for serve shutdown")
	}
	entries, err := corecatalog.Entries("")
	if err != nil || len(entries) != 0 {
		t.Fatalf("Core exit left registration: %v, %v", entries, err)
	}
}

func TestVersionAndHelpDoNotCreateWorkspaceState(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	for _, args := range [][]string{{"--version"}, {"--help"}, {"core", "list"}, {"history", "--help"}} {
		t.Setenv("XDG_STATE_HOME", filepath.Join(workspace, "state"))
		var stdout, stderr bytes.Buffer
		if code := run(args, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 {
			t.Fatalf("%v exit %d: %s", args, code, stderr.String())
		}
		if stdout.Len()+stderr.Len() == 0 {
			t.Fatalf("%v produced no output", args)
		}
	}
	files, err := os.ReadDir(workspace)
	if err != nil || len(files) != 0 {
		t.Fatalf("information commands created workspace state: %v, %v", files, err)
	}
}
