package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/corecatalog"
	"hjh/internal/history"
)

func TestSessionsCLIDeletesByShortIDAfterCoreStops(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	workspace := t.TempDir()
	db := filepath.Join(workspace, "history.sqlite3")
	store, err := history.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"12345678-deleted", "87654321-kept"} {
		if _, err := store.CreateSession(t.Context(), history.SessionInput{ID: id, WorkspaceRoot: workspace, AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: json.RawMessage(`{"provider":"test","modelId":"test/model"}`)}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveConfiguration(t.Context(), "config-"+id, contracts.JSON(map[string]string{"instruction": id})); err != nil {
			t.Fatal(err)
		}
		execution, err := store.BeginExecution(t.Context(), history.BeginExecutionInput{ExecutionID: "execution-" + id, SessionID: id,
			Task: "task for " + id, ConfigurationID: "config-" + id, Model: json.RawMessage(`{"provider":"test"}`), Build: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendMessage(t.Context(), execution.ID, contracts.JSON(contracts.User(execution.Task))); err != nil {
			t.Fatal(err)
		}
		if err := store.EndExecution(t.Context(), execution.ID, history.OutcomeCompleted, json.RawMessage(`{"outcome":"completed"}`), ""); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptCanonical(t.Context(), execution.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/core" {
			t.Errorf("unexpected discovery path %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(api.Core{CoreID: "running-core", APIVersion: "v2", Workspace: workspace})
	}))
	remove, err := corecatalog.Register(corecatalog.Entry{CoreID: "running-core", Endpoint: server.URL, Workspace: workspace, DBPath: db})
	if err != nil {
		t.Fatal(err)
	}
	defer remove()
	defer server.Close()
	var stdout, stderr bytes.Buffer
	args := []string{"sessions", "delete", "--db", db, "12345678"}
	if code := run(args, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "stop Core first") {
		t.Fatalf("live Core deletion exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	server.Close()
	stdout.Reset()
	stderr.Reset()
	if code := run(args, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 || stdout.String() != "Deleted Session: 12345678\n" {
		t.Fatalf("offline deletion exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"sessions", "--db", db, "--json"}, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 {
		t.Fatalf("list after deletion exit=%d stderr=%s", code, stderr.String())
	}
	var sessions []history.Session
	if err := json.Unmarshal(stdout.Bytes(), &sessions); err != nil || len(sessions) != 1 || sessions[0].ID != "87654321-kept" {
		t.Fatalf("remaining Sessions=%s, %v", stdout.String(), err)
	}
	remaining, err := history.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer remaining.Close()
	if _, err := remaining.Configuration(t.Context(), "config-12345678-deleted"); !errors.Is(err, history.ErrNotFound) {
		t.Fatalf("CLI deletion did not remove its unused configuration: %v", err)
	}
	if conversation, err := remaining.Conversation(t.Context(), "87654321-kept", 0, -1); err != nil || len(conversation) != 1 {
		t.Fatalf("CLI deletion affected remaining conversation: %+v, %v", conversation, err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(args, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "not found") {
		t.Fatalf("missing Session deletion exit=%d stderr=%s", code, stderr.String())
	}
}
