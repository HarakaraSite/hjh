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
	"testing"

	"hjh/internal/history"
)

func TestStandaloneRunResumesWithShortSessionIDAndSessionsJSONKeepsFullID(t *testing.T) {
	workspace := t.TempDir()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()

	configPath := filepath.Join(workspace, "config.json")
	config := map[string]any{
		"defaultRole": "default",
		"roles": map[string]any{"default": map[string]any{
			"revision": "1", "description": "test", "instruction": "Answer the request.", "tools": []string{},
		}},
		"model": map[string]string{"provider": "test", "modelId": "test/model"},
		"providers": map[string]any{"test": map[string]any{
			"displayName": "Test", "api": "chat-completions", "baseURL": provider.URL,
			"path": "/chat/completions", "auth": map[string]string{"kind": "none"},
			"defaultModel": "test/model", "models": []any{map[string]string{"id": "test/model", "name": "Test"}},
		}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(workspace, "history.sqlite3")
	baseArgs := []string{"run", "--workspace", workspace, "--db", dbPath, "--config", configPath,
		"--provider", "test", "--model", "test/model", "--role", "default", "--max-steps", "1"}
	var stdout, stderr bytes.Buffer
	firstArgs := append(append([]string(nil), baseArgs...), "first request")
	if code := run(firstArgs, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 {
		t.Fatalf("first run exit %d: %s", code, stderr.String())
	}
	store, err := history.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := store.ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("saved sessions = %d, %v", len(sessions), err)
	}
	sessionID := sessions[0].ID
	if len(sessionID) < 8 {
		t.Fatalf("generated Session ID is too short for the CLI selector: %q", sessionID)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Session: "+sessionID[:8]) {
		t.Fatalf("first run did not display the short Session ID: %s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	resumeArgs := append(append([]string(nil), baseArgs...), "--session", sessionID[:8], "continue request")
	if code := run(resumeArgs, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 {
		t.Fatalf("short-ID resume exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Session: "+sessionID[:8]) {
		t.Fatalf("resumed run did not display the short Session ID: %s", stderr.String())
	}
	store, err = history.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Session(context.Background(), sessionID)
	if err != nil || saved.ID != sessionID || saved.TurnCount != 2 {
		t.Fatalf("resume did not continue the complete saved Session: %+v, %v", saved, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"sessions", "--workspace", workspace, "--db", dbPath}, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 {
		t.Fatalf("human sessions exit %d: %s", code, stderr.String())
	}
	for _, expected := range []string{sessionID[:8], workspace, "2 turns", "updated "} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("human sessions output omits %q: %s", expected, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), sessionID) {
		t.Fatalf("human sessions output exposed the full Session ID: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"sessions", "--workspace", workspace, "--db", dbPath, "--json"}, io.NopCloser(strings.NewReader("")), &stdout, &stderr); code != 0 {
		t.Fatalf("JSON sessions exit %d: %s", code, stderr.String())
	}
	var structured []history.Session
	if err := json.Unmarshal(stdout.Bytes(), &structured); err != nil || len(structured) != 1 || structured[0].ID != sessionID {
		t.Fatalf("JSON sessions did not preserve the full ID: %s (%v)", stdout.String(), err)
	}
}
