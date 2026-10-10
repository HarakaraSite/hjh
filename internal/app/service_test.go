package app

import (
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

	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

func TestServiceRunsResumesAndExposesSavedEvidence(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	var requestBodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
			return
		}
		requestBodies = append(requestBodies, string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]any{"content": "確認できました。"}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := NewService(store, chatFixtureProvider(server.URL, "test-secret", "service-fixture", protocol.Options{}), RunOptions{Workspace: workspace, ModelID: "test/default", MaxSteps: 1, FromTurn: 2})
	observedModel := ""
	zeroSteps, firstTurn := 0, int64(0)
	callbackSession := ""
	first, err := service.Run(ctx, RunRequest{Task: "first", ModelID: "test/selected", Workspace: workspace, NewSessionID: "api-preallocated", MaxSteps: &zeroSteps, FromTurn: &firstTurn, OnSession: func(id string) {
		callbackSession = id
		if _, callbackErr := service.Session(ctx, id); callbackErr != nil {
			t.Errorf("session callback ran before the Session could be read: %v", callbackErr)
		}
	}, Progress: func(o contracts.Observation) error {
		if o.Kind == "execution" && o.Phase == "started" {
			var input struct {
				Model contracts.ModelSelection `json:"model"`
			}
			if err := json.Unmarshal(o.Input, &input); err != nil {
				return err
			}
			observedModel = input.Model.ModelID
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID != "api-preallocated" || callbackSession != first.SessionID {
		t.Fatalf("preallocated Session/callback = %q/%q", first.SessionID, callbackSession)
	}
	if observedModel != "test/selected" {
		t.Fatalf("admitted model %q", observedModel)
	}
	session, err := service.Session(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var active contracts.ModelSelection
	if err = json.Unmarshal(session.ActiveModel, &active); err != nil {
		t.Fatal(err)
	}
	if active.ModelID != "test/selected" {
		t.Fatalf("saved model: %s", session.ActiveModel)
	}
	second, err := service.Run(ctx, RunRequest{Task: "second", SessionID: first.SessionID, ModelID: "test/next", Workspace: workspace, FromTurn: &firstTurn})
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != first.SessionID {
		t.Fatal("resume changed Session")
	}
	if len(requestBodies) != 2 || !strings.Contains(requestBodies[1], "first") {
		t.Fatalf("explicit fromTurn=0 did not restore older conversation: requests=%q", requestBodies)
	}
	storedExecutions, err := service.Executions(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedExecutions) != 2 || storedExecutions[0].MaxSteps != 0 || storedExecutions[1].MaxSteps != 1 {
		t.Fatalf("run maxSteps overrides = %+v", storedExecutions)
	}
	sessions, err := service.Sessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions %v: %v", sessions, err)
	}
	conversation, err := service.Conversation(ctx, first.SessionID, 2, 2)
	if err != nil || len(conversation) == 0 || conversation[0].TurnNumber != 2 {
		t.Fatalf("conversation %v: %v", conversation, err)
	}
	executions, err := service.Executions(ctx, first.SessionID)
	if err != nil || len(executions) != 2 {
		t.Fatalf("executions %v: %v", executions, err)
	}
	currentWorkspace := t.TempDir()
	fresh, err := service.Run(ctx, RunRequest{Task: "new after choosing another workspace", ModelID: "test/next", Workspace: currentWorkspace})
	if err != nil {
		t.Fatal(err)
	}
	freshSession, err := service.Session(ctx, fresh.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if freshSession.WorkspaceRoot != currentWorkspace {
		t.Fatalf("new Session workspace = %q, want %q", freshSession.WorkspaceRoot, currentWorkspace)
	}
	if _, err := os.Stat(filepath.Join(currentWorkspace, ".hjh", "extensions", "config.json")); err != nil {
		t.Fatalf("new Session did not capture extensions in displayed workspace: %v", err)
	}

	inspected, err := service.Inspect(ctx, second.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Execution.ID != second.ExecutionID || len(inspected.Messages) == 0 || len(inspected.Records) == 0 || !json.Valid(inspected.Context) || !json.Valid(inspected.Configuration) {
		t.Fatalf("incomplete inspection: %+v", inspected)
	}
}
