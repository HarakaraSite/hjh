package corelaunch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"hjh/internal/api"
)

func TestEnsureReusesAvailableCoreBeforeBinaryOrFilesystemLookup(t *testing.T) {
	server := coreServer(t, api.Core{CoreID: "existing-core", APIVersion: "v2", Workspace: "/existing"})
	defer server.Close()
	missingDir := filepath.Join(t.TempDir(), "not-created")
	var progress strings.Builder

	core, err := Ensure(context.Background(), Options{
		Endpoint: server.URL,
		Binary:   filepath.Join(missingDir, "missing-hjh"),
		DBPath:   filepath.Join(missingDir, "history.sqlite3"),
	}, &progress)
	if err != nil {
		t.Fatalf("Ensure returned error: %v", err)
	}
	if core.CoreID != "existing-core" || core.Workspace != "/existing" {
		t.Fatalf("Ensure returned Core %+v", core)
	}
	if _, err := os.Stat(missingDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reuse touched launch directory: stat error = %v", err)
	}
	if got := progress.String(); !strings.Contains(got, "existing Core") {
		t.Fatalf("reuse progress = %q", got)
	}
}

func TestEnsureLaunchesWithOptionsAndReturnsWhenCoreIsReady(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(root, "args")
	pidPath := filepath.Join(root, "pid")
	binary := writeExecutable(t, root, fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$@\" > %s\nprintf '%%s\\n' \"$$\" > %s\nexec sleep 30\n", shellQuote(argsPath), shellQuote(pidPath)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/core" {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(argsPath); errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Core is starting", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(api.Core{CoreID: "started-core", APIVersion: "v2", Workspace: "/effective"})
	}))
	defer server.Close()

	workspace := filepath.Join(root, "workspace")
	dbPath := filepath.Join(root, "database", "history.sqlite3")
	configPath := filepath.Join(root, "extension config.json")
	credentialFile := filepath.Join(root, "credentials", "key.json")
	credentialStateDir := filepath.Join(root, "state")
	favoritesDir := filepath.Join(root, "favorites")
	logPath := filepath.Join(filepath.Dir(dbPath), "core.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("prior log\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var progress strings.Builder
	core, err := Ensure(ctx, Options{
		Endpoint:           server.URL,
		Binary:             binary,
		Workspace:          workspace,
		DBPath:             dbPath,
		ConfigPath:         configPath,
		CredentialFile:     credentialFile,
		CredentialStateDir: credentialStateDir,
		ModelFavoritesDir:  favoritesDir,
	}, &progress)
	if err != nil {
		cancel()
		t.Fatalf("Ensure returned error: %v\nprogress: %s", err, progress.String())
	}
	pid := waitPID(t, pidPath)
	if core.CoreID != "started-core" {
		cancel()
		t.Fatalf("Ensure returned Core %+v", core)
	}

	gotArgs, err := readLines(argsPath)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	parsed, err := url.Parse(server.URL)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	wantArgs := []string{
		"serve", "--listen", net.JoinHostPort(parsed.Hostname(), parsed.Port()), "--workspace", workspace,
		"--db", dbPath, "--config", configPath, "--credential-file", credentialFile,
		"--credential-state-dir", credentialStateDir, "--model-favorites-dir", favoritesDir,
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		cancel()
		t.Fatalf("child args = %#v, want %#v", gotArgs, wantArgs)
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		cancel()
		t.Fatalf("get child process group: %v", err)
	}
	if pgid != pid {
		cancel()
		t.Fatalf("child process group is %d, want its own session %d", pgid, pid)
	}
	if err = processIsAlive(pid); err != nil {
		cancel()
		t.Fatalf("child process is not alive after readiness: %v", err)
	}
	if got, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(got), "prior log\n") {
		cancel()
		t.Fatalf("Core log was not preserved for append: %q, %v", got, err)
	}
	if got := progress.String(); !strings.Contains(got, "Starting Core") || !strings.Contains(got, "Core is ready") {
		cancel()
		t.Fatalf("startup progress = %q", got)
	}

	// Cancelling the UI context after readiness leaves the independent Core
	// process running for another TUI.
	cancel()
	if err = processIsAlive(pid); err != nil {
		t.Fatalf("Core stopped after caller context cancellation: %v", err)
	}
}

func TestEnsureCancellationStopsWaitingButLeavesStartedCoreRunning(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "pid")
	binary := writeExecutable(t, root, fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$$\" > %s\nexec sleep 30\n", shellQuote(pidPath)))
	server := unavailableCoreServer()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Ensure(ctx, Options{Endpoint: server.URL, Binary: binary, Workspace: root}, nil)
		done <- err
	}()
	pid := waitPID(t, pidPath)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Ensure error = %v, want context.Canceled", err)
		}
		if !strings.Contains(err.Error(), "core.log") {
			t.Fatalf("cancellation error lacks Core log path: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ensure did not return after context cancellation")
	}
	if err := processIsAlive(pid); err != nil {
		t.Fatalf("Core stopped when startup wait was cancelled: %v", err)
	}
	stopProcess(t, pid)
}

func TestEnsureReportsFailedLaunchAndLogPath(t *testing.T) {
	root := t.TempDir()
	binary := writeExecutable(t, root, "#!/bin/sh\nprintf 'child failed\\n' >&2\nexit 7\n")
	server := unavailableCoreServer()
	defer server.Close()
	workspace := filepath.Join(root, "workspace")
	logPath := filepath.Join(workspace, ".hjh", "core.log")
	var progress strings.Builder

	_, err := Ensure(context.Background(), Options{Endpoint: server.URL, Binary: binary, Workspace: workspace}, &progress)
	if err == nil {
		t.Fatal("Ensure returned nil error for an exited child")
	}
	if !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), logPath) {
		t.Fatalf("Ensure error = %v, want exit status and log path", err)
	}
	if got, readErr := os.ReadFile(logPath); readErr != nil || !strings.Contains(string(got), "child failed") {
		t.Fatalf("Core log = %q, read error %v", got, readErr)
	}
	if !strings.Contains(progress.String(), logPath) {
		t.Fatalf("failure progress lacks Core log path: %q", progress.String())
	}
}

func TestEnsureReportsChildExitWhenListenPortCloses(t *testing.T) {
	root := t.TempDir()
	pidPath := filepath.Join(root, "pid")
	releasePath := filepath.Join(root, "release")
	binary := writeExecutable(t, root, fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$$\" > %s\nwhile [ ! -e %s ]; do sleep 0.01; done\nexit 7\n", shellQuote(pidPath), shellQuote(releasePath)))
	server := unavailableCoreServer()
	defer server.Close()
	done := make(chan error, 1)
	go func() {
		_, err := Ensure(context.Background(), Options{Endpoint: server.URL, Binary: binary, Workspace: root}, nil)
		done <- err
	}()
	pid := waitPID(t, pidPath)
	server.Close()
	if err := os.WriteFile(releasePath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "core.log") {
			t.Fatalf("Ensure error = %v, want child exit status and log path", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Ensure kept waiting after the Core listen port closed (pid %d)", pid)
	}
}

func TestEnsureWaitsForWinningListenerToServeCoreAfterLosingChildExits(t *testing.T) {
	root := t.TempDir()
	exitedPath := filepath.Join(root, "loser-exited")
	binary := writeExecutable(t, root, fmt.Sprintf("#!/bin/sh\n: > %s\nexit 9\n", shellQuote(exitedPath)))
	ready := make(chan struct{})
	blockedRequests := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/core" {
			http.NotFound(w, r)
			return
		}
		if _, err := os.Stat(exitedPath); errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Core is not ready", http.StatusServiceUnavailable)
			return
		}
		select {
		case blockedRequests <- struct{}{}:
		default:
		}
		select {
		case <-ready:
			_ = json.NewEncoder(w).Encode(api.Core{CoreID: "concurrent-winner", APIVersion: "v2", Workspace: root})
		case <-r.Context().Done():
		}
	}))
	defer server.Close()

	type result struct {
		core api.Core
		err  error
	}
	done := make(chan result, 1)
	go func() {
		core, err := Ensure(context.Background(), Options{Endpoint: server.URL, Binary: binary, Workspace: root}, nil)
		done <- result{core: core, err: err}
	}()

	// Two blocked probes account for the probe already in flight when the
	// losing child exits and the final re-probe. A third proves Ensure kept
	// waiting while the winner's TCP listener remained open without HTTP.
	for range 3 {
		select {
		case result := <-done:
			t.Fatalf("Ensure returned before the listening Core became ready: core=%+v err=%v", result.core, result.err)
		case <-blockedRequests:
		case <-time.After(5 * time.Second):
			t.Fatal("Ensure did not keep probing the listening Core")
		}
	}
	close(ready)
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("Ensure returned error after the winner became ready: %v", result.err)
		}
		if result.core.CoreID != "concurrent-winner" {
			t.Fatalf("Ensure returned Core %+v", result.core)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ensure did not return after the listening Core became ready")
	}
}

func coreServer(t *testing.T, core api.Core) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/core" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(core)
	}))
}

func unavailableCoreServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Core is not ready", http.StatusServiceUnavailable)
	}))
}

func writeExecutable(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "helper")
	if err := os.WriteFile(path, []byte(contents), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	return lines, nil
}

func waitPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pid := readPIDValue(path); pid > 0 {
			t.Cleanup(func() { stopProcess(t, pid) })
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child did not write pid file %s", path)
	return 0
}

func readPIDValue(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

func processIsAlive(pid int) error {
	return syscall.Kill(pid, 0)
}

func stopProcess(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("stop helper process %d: %v", pid, err)
	}
}
