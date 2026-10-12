package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
	"hjh/internal/httpapi"
	"hjh/internal/provider/protocol"
)

type connectedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *connectedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *connectedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func awaitConnected(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("connected run did not reach the expected state")
}

func connectedCore(t *testing.T, release <-chan struct{}) (string, *corerun.Manager, *history.Store, <-chan api.StartRunRequest) {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"README \"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"summary\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, protocol.New(protocol.Config{ProviderID: "connected-fixture", API: "chat-completions", BaseURL: provider.URL}),
		app.RunOptions{Workspace: workspace, MaxSteps: 3, FromTurn: 7})
	manager := corerun.New(service, workspace)
	handler := httpapi.New(service, manager)
	requests := make(chan api.StartRunRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			var request api.StartRunRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			requests <- request
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		_ = handler.Close()
		server.Close()
		_ = store.Close()
		provider.Close()
	})
	return server.URL, manager, store, requests
}

func TestConnectedRunSharesActiveSessionAndPreservesCLIOutput(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			endpoint, manager, store, requests := connectedCore(t, release)
			t.Cleanup(unblock)
			local := t.TempDir()
			t.Chdir(local)
			var stdout, stderr connectedBuffer
			args := []string{"run", "--connect", endpoint, "--model", "test/model", "--role", "default", "--max-steps", "0", "--from-turn", "0"}
			if stream {
				args = append(args, "--stream")
			}
			done := make(chan int, 1)
			go func() {
				done <- run(args, io.NopCloser(strings.NewReader(" READMEを要約して\n")), &stdout, &stderr)
			}()
			awaitConnected(t, func() bool {
				return len(manager.Status().ActiveRuns) == 1 && strings.Contains(stderr.String(), "Session:")
			})
			active := manager.Status().ActiveRuns[0]
			if got, want := stderr.String(), "Session: "+active.SessionID[:8]; !strings.Contains(got, want) {
				t.Fatalf("connected run Session display omitted short ID %q: %s", want, got)
			}
			var coreOut, coreErr bytes.Buffer
			if code := runCore(context.Background(), []string{"status", "--connect", endpoint}, &coreOut, &coreErr); code != 0 {
				t.Fatalf("Core status exit %d: %s", code, coreErr.String())
			}
			if !strings.Contains(coreOut.String(), "Session "+active.SessionID[:8]) {
				t.Fatalf("Core status omitted short active Session ID: %s", coreOut.String())
			}
			client, err := apiclient.New(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			state, err := client.ActiveSessionRun(context.Background(), manager.Core().CoreID, active.SessionID)
			if err != nil || state == nil || state.RunID != active.RunID {
				t.Fatalf("TUI active-run lookup = %+v, %v", state, err)
			}
			snapshot := make(chan apiclient.RunEvent, 8)
			attached := client.AttachRun(manager.Core().CoreID, active.RunID, func(event apiclient.RunEvent) {
				select {
				case snapshot <- event:
				default:
				}
			})
			awaitConnected(t, func() bool {
				select {
				case event := <-snapshot:
					if event.Conversation != nil {
						for _, entity := range event.Conversation.Entities {
							if entity.Kind == "assistant" && entity.Text == "README " {
								return true
							}
						}
					}
					if event.Update != nil {
						for _, entity := range event.Update.Upserts {
							if entity.Kind == "assistant" && entity.Text == "README " {
								return true
							}
						}
					}
				default:
				}
				return false
			})
			if stream {
				awaitConnected(t, func() bool { return stdout.String() == "README " })
			} else if stdout.String() != "" {
				t.Fatalf("non-stream stdout before completion = %q", stdout.String())
			}
			request := <-requests
			if request.Workspace != "" || request.Task != "READMEを要約して" || request.ModelID != "test/model" || request.Role != "default" || request.MaxSteps == nil || *request.MaxSteps != 0 || request.FromTurn == nil || *request.FromTurn != 0 {
				t.Fatalf("Core request = %+v", request)
			}
			unblock()
			select {
			case code := <-done:
				if code != 0 {
					t.Fatalf("exit %d: %s", code, stderr.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("CLI did not finish")
			}
			if state, err := attached.Wait(); err != nil || state.Result == nil || !state.Result.OK {
				t.Fatalf("attached completion = %+v, %v", state, err)
			}
			if stdout.String() != "README summary\n" {
				t.Fatalf("stdout = %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "Execution:") || !strings.Contains(stderr.String(), "Run:") {
				t.Fatalf("missing reconnect IDs: %s", stderr.String())
			}
			session, err := store.Session(context.Background(), active.SessionID)
			if err != nil || session.TurnCount != 1 {
				t.Fatalf("saved Session = %+v, %v", session, err)
			}
			files, err := os.ReadDir(local)
			if err != nil || len(files) != 0 {
				t.Fatalf("connected CLI created local state: %v, %v", files, err)
			}
			stdout.buffer.Reset()
			stderr.buffer.Reset()
			if code := run([]string{"run", "--connect", endpoint, "--session", active.SessionID[:8], "--workspace", "ignored-on-resume", "次の依頼"}, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 {
				t.Fatalf("resume exit %d: %s", code, stderr.String())
			}
			request = <-requests
			if request.MaxSteps != nil || request.FromTurn != nil || request.SessionID != active.SessionID || request.Workspace != filepath.Join(manager.Core().Workspace, "ignored-on-resume") {
				t.Fatalf("resume/default request = %+v", request)
			}
			session, err = store.Session(context.Background(), active.SessionID)
			if err != nil || session.TurnCount != 2 || session.WorkspaceRoot != manager.Core().Workspace {
				t.Fatalf("continued Session = %+v, %v", session, err)
			}
			executions, err := store.Executions(context.Background(), active.SessionID)
			if err != nil || len(executions) != 2 {
				t.Fatalf("continued executions = %d, %v", len(executions), err)
			}
			if executions[0].MaxSteps != 0 || executions[1].MaxSteps != 3 {
				t.Fatalf("explicit zero/default Core step limit = %d/%d", executions[0].MaxSteps, executions[1].MaxSteps)
			}
		})
	}
}

func TestConnectedRunInterruptWaitsForSavedCancellation(t *testing.T) {
	release := make(chan struct{})
	endpoint, manager, store, _ := connectedCore(t, release)
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr connectedBuffer
	done := make(chan int, 1)
	go func() {
		done <- runConnected(ctx, endpoint, api.StartRunRequest{Task: "interrupt this work"}, true, &stdout, &stderr)
	}()
	awaitConnected(t, func() bool { return stdout.String() == "README " })
	active := manager.Status().ActiveRuns[0]
	cancel()
	select {
	case code := <-done:
		if code != 130 {
			t.Fatalf("interrupt exit %d: %s", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupt did not finish")
	}
	if len(manager.Status().ActiveRuns) != 0 {
		t.Fatal("CLI returned before Core completion")
	}
	execution, err := store.Execution(context.Background(), active.ExecutionID)
	if err != nil || execution.SettledAt == nil || execution.Outcome != "cancelled" {
		t.Fatalf("cancel not saved before return: %+v, %v", execution, err)
	}
	if stdout.String() != "README \n" {
		t.Fatalf("cancelled partial stdout = %q", stdout.String())
	}
}

func TestConnectedRunStartupFlagsExplainCoreOwnership(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"run", "--connect", "http://127.0.0.1:1", "--db", "elsewhere.sqlite3", "task"}, io.NopCloser(strings.NewReader("")), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "belongs to Core startup") {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
}

func TestConnectedRunFailureKeepsStdoutEmpty(t *testing.T) {
	release := make(chan struct{})
	endpoint, _, _, _ := connectedCore(t, release)
	t.Cleanup(func() { close(release) })
	var stdout, stderr bytes.Buffer
	code := runConnected(context.Background(), endpoint, api.StartRunRequest{Task: "work", ProviderID: "unknown-provider"}, false, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown-provider") {
		t.Fatalf("failure exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}
