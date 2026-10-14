package corerun

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/conversation"
	"hjh/internal/executiondata"
	"hjh/internal/history"
)

func TestConcurrentSessionViewReadsShareOneRestorationUntilLastRead(t *testing.T) {
	manager := New(nil, "")
	defer manager.Close()

	restoreStarted := make(chan struct{})
	releaseRestore := make(chan struct{})
	var restoreCalls atomic.Int32
	var restoredView *conversation.View
	restore := func(ctx context.Context, sessionID string) (*conversation.View, *executiondata.Subscription, error) {
		restoreCalls.Add(1)
		close(restoreStarted)
		<-releaseRestore
		view := conversation.NewSessionView(sessionID)
		if err := view.LoadCut(ctx, nil, nil, nil); err != nil {
			return nil, nil, err
		}
		restoredView = view
		return view, nil, nil
	}
	type result struct {
		snapshot api.ConversationSnapshot
		err      error
	}
	results := make(chan result, 2)
	go func() {
		snapshot, err := manager.sessionViewSnapshot(context.Background(), "session-1", restore)
		results <- result{snapshot: snapshot, err: err}
	}()
	select {
	case <-restoreStarted:
	case <-time.After(time.Second):
		t.Fatal("first Session view restore did not start")
	}
	go func() {
		snapshot, err := manager.sessionViewSnapshot(context.Background(), "session-1", restore)
		results <- result{snapshot: snapshot, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		flight := manager.sessionViews["session-1"]
		refs := 0
		if flight != nil {
			refs = flight.refs
		}
		manager.mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("overlapping Session reads did not share a flight; refs=%d", refs)
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseRestore)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("shared Session restore errors: %v / %v", first.err, second.err)
	}
	if restoreCalls.Load() != 1 || first.snapshot.ViewID == "" || first.snapshot.ViewID != second.snapshot.ViewID {
		t.Fatalf("overlapping reads did not share one materialized view: calls=%d first=%+v second=%+v", restoreCalls.Load(), first.snapshot, second.snapshot)
	}
	manager.mu.Lock()
	retained := manager.sessionViews["session-1"] != nil
	manager.mu.Unlock()
	if retained {
		t.Fatal("completed Session view remained cached after its last read")
	}
	if _, err := restoredView.Snapshot(context.Background()); !errors.Is(err, conversation.ErrClosed) {
		t.Fatalf("completed Session view was not released: %v", err)
	}
}

func TestSessionViewLoaderCannotCompleteAfterSessionDeletion(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace})
	manager := New(service, workspace)
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
		if err := service.Close(); err != nil {
			t.Error(err)
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.CreateSession(t.Context(), history.SessionInput{ID: "view-deletion", WorkspaceRoot: workspace,
		AgentChoice: json.RawMessage(`{}`), ActiveModel: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var restoredView *conversation.View
	restore := func(ctx context.Context, sessionID string) (*conversation.View, *executiondata.Subscription, error) {
		close(started)
		<-release
		view := conversation.NewSessionView(sessionID)
		if err := view.LoadCut(ctx, nil, nil, nil); err != nil {
			return nil, nil, err
		}
		restoredView = view
		return view, nil, nil
	}
	result := make(chan error, 1)
	go func() {
		_, err := manager.sessionViewSnapshot(t.Context(), "view-deletion", restore)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Session view restoration did not start")
	}
	if err := manager.DeleteSession(t.Context(), "view-deletion"); err != nil {
		t.Fatalf("delete Session during view restoration: %v", err)
	}
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, history.ErrNotFound) {
			t.Fatalf("view restoration returned after Session deletion: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Session view restoration did not settle after deletion")
	}
	if _, err := restoredView.Snapshot(t.Context()); !errors.Is(err, conversation.ErrClosed) {
		t.Fatalf("invalidated Session view was retained: %v", err)
	}
}
