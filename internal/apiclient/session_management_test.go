package apiclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/api"
)

func TestAttachRunUsesSavedIdentityWithoutStartPost(t *testing.T) {
	state := api.RunState{CoreID: "core-attach", RunID: "run-attach", Phase: "terminal", SessionID: "session-attach", Result: &api.RunResult{OK: true}}
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			http.Error(w, "unexpected POST", 500)
			return
		}
		if r.URL.Query().Get("coreId") != state.CoreID {
			t.Error("lost Core identity")
		}
		switch r.URL.Path {
		case "/api/v2/runs/run-attach":
			writeJSONForTest(w, 200, state)
		case "/api/v2/runs/run-attach/events":
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSEForTest(w, w.(http.Flusher), "run.snapshot", state)
			writeSSEForTest(w, w.(http.Flusher), "run.snapshot", state)
			writeSSEForTest(w, w.(http.Flusher), "run.terminal", state)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	handle := client.AttachRun(state.CoreID, state.RunID, nil)
	done := make(chan struct{})
	var actual api.RunState
	var err error
	go func() { actual, err = handle.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("attach did not settle")
	}
	if err != nil || actual.RunID != state.RunID || actual.SessionID != state.SessionID || posts.Load() != 0 {
		t.Fatalf("attach=%+v err=%v posts=%d", actual, err, posts.Load())
	}
}

func TestSessionManagementAndShutdownWaitForCoreStop(t *testing.T) {
	accepted := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/sessions/saved/rename":
			var body struct {
				Title string `json:"title"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Title != "日本語の作業 名前" || r.Method != http.MethodPost {
				t.Error("rename request")
			}
			writeJSONForTest(w, 200, api.Session{ID: "saved", Title: &body.Title})
		case "/api/v2/sessions/saved/active-run":
			if r.URL.Query().Get("coreId") != "core-1" {
				t.Error("active lookup identity")
			}
			writeJSONForTest(w, 200, api.RunState{CoreID: "core-1", RunID: "same-run", Phase: "running"})
		case "/api/v2/core/shutdown":
			var body struct {
				CoreID string `json:"coreId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.CoreID != "core-1" {
				t.Error("shutdown identity")
			}
			writeJSONForTest(w, 202, map[string]bool{"ok": true})
			close(accepted)
		case "/api/v2/core":
			coreID := "core-1"
			select {
			case <-stopped:
				coreID = "next-core"
			default:
			}
			writeJSONForTest(w, 200, api.Core{CoreID: coreID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, _ := New(server.URL)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := client.RenameSession(ctx, "saved", "日本語の作業 名前")
	if err != nil || session.Title == nil || *session.Title != "日本語の作業 名前" {
		t.Fatalf("rename=%+v %v", session, err)
	}
	state, err := client.ActiveSessionRun(ctx, "core-1", "saved")
	if err != nil || state == nil || state.RunID != "same-run" {
		t.Fatalf("active=%+v %v", state, err)
	}
	done := make(chan error, 1)
	go func() { done <- client.ShutdownCore(ctx, "core-1") }()
	<-accepted
	select {
	case err := <-done:
		t.Fatalf("ack returned before Core stopped: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(stopped)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
