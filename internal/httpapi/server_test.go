package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

func TestRunPreparationFailureIsRetainedAndLateSSEGetsTerminal(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	defer func() {
		if err := handler.Close(); err != nil {
			t.Error(err)
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()

	coreResponse := httptest.NewRecorder()
	handler.ServeHTTP(coreResponse, httptest.NewRequest(http.MethodGet, "/api/v2/core", nil))
	if coreResponse.Code != http.StatusOK {
		t.Fatalf("GET /core status = %d, body = %s", coreResponse.Code, coreResponse.Body.String())
	}
	var core api.Core
	if err := json.Unmarshal(coreResponse.Body.Bytes(), &core); err != nil {
		t.Fatal(err)
	}
	if core.CoreID == "" || core.APIVersion != "v2" || core.Workspace != workspace {
		t.Fatalf("GET /core returned %+v", core)
	}

	request := api.StartRunRequest{CoreID: core.CoreID, RunID: "run-preparation-failure", Task: "do not contact a provider",
		Workspace: workspace, ProviderID: "missing-provider"}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, httptest.NewRequest(http.MethodPost, "/api/v2/runs", bytes.NewReader(body)))
	if startResponse.Code != http.StatusAccepted {
		t.Fatalf("POST /runs status = %d, body = %s", startResponse.Code, startResponse.Body.String())
	}
	var accepted api.RunState
	if err := json.Unmarshal(startResponse.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.Phase != "preparing" || accepted.RunID != request.RunID {
		t.Fatalf("POST /runs returned %+v", accepted)
	}

	deadline := time.Now().Add(3 * time.Second)
	var terminal api.RunState
	for time.Now().Before(deadline) {
		terminal, err = manager.Get(core.CoreID, request.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if terminal.Phase == "terminal" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if terminal.Phase != "terminal" || terminal.Result == nil || terminal.Result.OK || terminal.Result.Error == nil {
		t.Fatalf("preparation failure result = %+v", terminal)
	}

	eventsResponse := httptest.NewRecorder()
	eventsURL := fmt.Sprintf("/api/v2/runs/%s/events?coreId=%s", request.RunID, core.CoreID)
	handler.ServeHTTP(eventsResponse, httptest.NewRequest(http.MethodGet, eventsURL, nil))
	if eventsResponse.Code != http.StatusOK || !strings.HasPrefix(eventsResponse.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("GET run events status/content type = %d/%q: %s", eventsResponse.Code, eventsResponse.Header().Get("Content-Type"), eventsResponse.Body.String())
	}
	stream := eventsResponse.Body.String()
	if strings.Count(stream, "event: run.snapshot\n") < 2 || !strings.Contains(stream, "event: run.terminal\n") || !strings.Contains(stream, `"phase":"terminal"`) || !strings.Contains(stream, `"conversation":{`) {
		t.Fatalf("late event subscription did not return terminal snapshot and event: %s", stream)
	}
	if strings.Contains(stream, `"texts"`) || strings.Contains(stream, `"operations"`) || strings.Contains(stream, `"executions"`) {
		t.Fatalf("run SSE retained the removed parallel projection arrays: %s", stream)
	}
}

func TestProviderListingDoesNotExposeTransportHeaders(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	defer func() {
		_ = handler.Close()
		_ = store.Close()
	}()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/providers", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /providers status = %d, body = %s", response.Code, response.Body.String())
	}
	var definitions []api.ProviderDefinition
	if err := json.Unmarshal(response.Body.Bytes(), &definitions); err != nil {
		t.Fatal(err)
	}
	if len(definitions) == 0 || strings.Contains(response.Body.String(), `"headers"`) || strings.Contains(response.Body.String(), `"adapter"`) {
		t.Fatalf("provider listing lacks definitions or exposes transport settings: %s", response.Body.String())
	}
}

type snapshotProvider struct{}

func (snapshotProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}

func (snapshotProvider) Generate(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
	return contracts.ModelResult{Text: "保存済みの回答", FinishReason: "stop"}, nil
}

type heldSessionProvider struct {
	entered     chan struct{}
	canceled    chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
	cancel      bool
}

func (p *heldSessionProvider) releaseRun() { p.releaseOnce.Do(func() { close(p.release) }) }

func (p *heldSessionProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request.Messages)
}

func (p *heldSessionProvider) Generate(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	p.once.Do(func() { close(p.entered) })
	if p.cancel {
		<-ctx.Done()
		close(p.canceled)
		<-p.release
		return contracts.ModelResult{}, ctx.Err()
	}
	select {
	case <-p.release:
		return contracts.ModelResult{Text: "saved after rename", FinishReason: "stop"}, nil
	case <-ctx.Done():
		return contracts.ModelResult{}, ctx.Err()
	}
}

func TestSessionRenameAndActiveRunRoutesDuringExecution(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &heldSessionProvider{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	defer func() {
		provider.releaseRun()
		_ = handler.Close()
		_ = store.Close()
	}()

	coreID := manager.Core().CoreID
	request := api.StartRunRequest{CoreID: coreID, RunID: "management-run", Task: "continue this Session"}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/api/v2/runs", bytes.NewReader(body)))
	if start.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, body=%s", start.Code, start.Body.String())
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		state, stateErr := manager.Get(coreID, request.RunID)
		t.Fatalf("run did not reach provider; state=%+v err=%v", state, stateErr)
	}
	state, err := manager.Get(coreID, request.RunID)
	if err != nil || state.SessionID == "" {
		t.Fatalf("admitted Session state = %+v, %v", state, err)
	}
	session, err := service.Session(context.Background(), state.SessionID)
	if err != nil {
		t.Fatalf("read admitted Session: %v", err)
	}

	activeURL := "/api/v2/sessions/" + session.ID + "/active-run?coreId=" + coreID
	activeResponse := httptest.NewRecorder()
	handler.ServeHTTP(activeResponse, httptest.NewRequest(http.MethodGet, activeURL, nil))
	var active api.RunState
	if activeResponse.Code != http.StatusOK || json.Unmarshal(activeResponse.Body.Bytes(), &active) != nil ||
		active.RunID != request.RunID || active.Phase == "terminal" || active.Result != nil {
		t.Fatalf("active-run response = %d %s", activeResponse.Code, activeResponse.Body.String())
	}
	sessionResponse := httptest.NewRecorder()
	handler.ServeHTTP(sessionResponse, httptest.NewRequest(http.MethodGet, "/api/v2/sessions/"+session.ID, nil))
	var sessionWithRun api.Session
	if sessionResponse.Code != http.StatusOK || json.Unmarshal(sessionResponse.Body.Bytes(), &sessionWithRun) != nil ||
		sessionWithRun.ActiveRunID != request.RunID {
		t.Fatalf("GET Session activeRunId = %d %s", sessionResponse.Code, sessionResponse.Body.String())
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/v2/sessions", nil))
	var sessions []api.Session
	if listResponse.Code != http.StatusOK || json.Unmarshal(listResponse.Body.Bytes(), &sessions) != nil || len(sessions) != 1 || sessions[0].ActiveRunID != request.RunID {
		t.Fatalf("Session list activeRunId = %d %s", listResponse.Code, listResponse.Body.String())
	}

	renameBody := bytes.NewBufferString(`{"title":"renamed while running"}`)
	renameResponse := httptest.NewRecorder()
	handler.ServeHTTP(renameResponse, httptest.NewRequest(http.MethodPost,
		"/api/v2/sessions/"+session.ID+"/rename", renameBody))
	var renamed api.Session
	if renameResponse.Code != http.StatusOK || json.Unmarshal(renameResponse.Body.Bytes(), &renamed) != nil ||
		renamed.Title == nil || *renamed.Title != "renamed while running" || renamed.ActiveRunID != request.RunID ||
		renamed.StateRevision != session.StateRevision {
		t.Fatalf("rename response = %d %s", renameResponse.Code, renameResponse.Body.String())
	}
	provider.releaseRun()

	deadline := time.Now().Add(5 * time.Second)
	var terminal api.RunState
	for time.Now().Before(deadline) {
		terminal, err = manager.Get(coreID, request.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if terminal.Phase == "terminal" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if terminal.Phase != "terminal" || terminal.Result == nil || !terminal.Result.OK {
		t.Fatalf("run did not adopt after rename: %+v", terminal)
	}
	finishedActiveResponse := httptest.NewRecorder()
	handler.ServeHTTP(finishedActiveResponse, httptest.NewRequest(http.MethodGet, activeURL, nil))
	if finishedActiveResponse.Code != http.StatusOK || strings.TrimSpace(finishedActiveResponse.Body.String()) != "null" {
		t.Fatalf("terminal Session active-run response = %d %s", finishedActiveResponse.Code, finishedActiveResponse.Body.String())
	}
	saved, err := service.Session(context.Background(), session.ID)
	if err != nil || saved.Title == nil || *saved.Title != "renamed while running" || saved.TurnCount != 1 {
		t.Fatalf("renamed Session after run adoption = %#v, %v", saved, err)
	}
}

func TestDeleteSessionThroughClientPreservesOtherSessionAndRejectsActiveRun(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &heldSessionProvider{entered: make(chan struct{}), release: make(chan struct{})}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	defer func() {
		provider.releaseRun()
		if err := handler.Close(); err != nil {
			t.Error(err)
		}
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	}()

	for _, id := range []string{"delete-me", "keep-me"} {
		if _, err := store.CreateSession(t.Context(), history.SessionInput{ID: id, WorkspaceRoot: workspace,
			AgentChoice: json.RawMessage(`{}`), ActiveModel: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := apiclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.DeleteSession(t.Context(), "delete-me"); err != nil {
		t.Fatalf("DELETE Session: %v", err)
	}
	sessions, err := client.Sessions(t.Context())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "keep-me" {
		t.Fatalf("remaining Sessions = %+v, %v", sessions, err)
	}

	request := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: "delete-busy-run", Task: "hold this Session while deletion is requested"}
	if _, created, err := manager.Start(request); err != nil || !created {
		t.Fatalf("start run: created=%t err=%v", created, err)
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatalf("run did not reach provider: %+v", mustGetRun(t, manager, request.RunID))
	}
	active, err := manager.Get(request.CoreID, request.RunID)
	if err != nil || active.SessionID == "" || active.Phase == "terminal" {
		t.Fatalf("active run state = %+v, %v", active, err)
	}
	err = client.DeleteSession(t.Context(), active.SessionID)
	var coded *api.Error
	if !errors.As(err, &coded) || coded.Code != "session_busy" || coded.ActiveRunID != request.RunID || !strings.Contains(coded.Message, "Stop") {
		t.Fatalf("active Session deletion error = %v", err)
	}
	busyResponse := httptest.NewRecorder()
	handler.ServeHTTP(busyResponse, httptest.NewRequest(http.MethodDelete, "/api/v2/sessions/"+active.SessionID, nil))
	var busyBody api.ErrorResponse
	if busyResponse.Code != http.StatusConflict || json.Unmarshal(busyResponse.Body.Bytes(), &busyBody) != nil ||
		busyBody.Error == nil || busyBody.Error.Code != "session_busy" || busyBody.Error.ActiveRunID != request.RunID {
		t.Fatalf("busy DELETE response = %d %s", busyResponse.Code, busyResponse.Body.String())
	}
	stillRunning, err := manager.Get(request.CoreID, request.RunID)
	if err != nil || stillRunning.Phase != "running" {
		t.Fatalf("rejected deletion changed the active run: %+v, %v", stillRunning, err)
	}

	provider.releaseRun()
	deadline := time.Now().Add(5 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		state, getErr := manager.Get(request.CoreID, request.RunID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if state.Phase == "terminal" {
			completed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !completed {
		t.Fatal("run did not complete after provider release")
	}
	subscription, err := manager.Subscribe(request.CoreID, request.RunID)
	if err != nil {
		t.Fatalf("subscribe to completed run: %v", err)
	}
	defer subscription.Close()
	if err := client.DeleteSession(t.Context(), active.SessionID); err != nil {
		t.Fatalf("DELETE completed Session: %v", err)
	}
	if _, err := client.SessionView(t.Context(), active.SessionID); !errors.As(err, &coded) || coded.Code != "not_found" {
		t.Fatalf("deleted Session view remained available: %v", err)
	}
	if _, err := manager.Get(request.CoreID, request.RunID); !errors.As(err, &coded) || coded.Code != "run_not_found" {
		t.Fatalf("deleted Session run remained available from Manager: %v", err)
	}
	if _, _, err := subscription.UpdateContext(t.Context()); !errors.As(err, &coded) || coded.Code != "run_not_found" {
		t.Fatalf("deleted Session run subscription remained available: %v", err)
	}
	if _, err := subscription.Snapshot(); !errors.As(err, &coded) || coded.Code != "run_not_found" {
		t.Fatalf("deleted Session run snapshot remained available: %v", err)
	}
	sessions, err = client.Sessions(t.Context())
	if err != nil || len(sessions) != 1 || sessions[0].ID != "keep-me" {
		t.Fatalf("Sessions after deleting completed run = %+v, %v", sessions, err)
	}
}

func mustGetRun(t *testing.T, manager *corerun.Manager, runID string) api.RunState {
	t.Helper()
	state, err := manager.Get(manager.Core().CoreID, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return state
}

func TestShutdownRouteAcknowledgesBeforeJoiningAndSavesCancelledRun(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &heldSessionProvider{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{}), cancel: true}
	service := app.NewService(store, provider, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	defer func() {
		provider.releaseRun()
		_ = handler.Close()
		_ = store.Close()
	}()

	coreID := manager.Core().CoreID
	request := api.StartRunRequest{CoreID: coreID, RunID: "shutdown-run", Task: "save cancellation"}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRecorder()
	handler.ServeHTTP(start, httptest.NewRequest(http.MethodPost, "/api/v2/runs", bytes.NewReader(body)))
	if start.Code != http.StatusAccepted {
		t.Fatalf("start status = %d, body=%s", start.Code, start.Body.String())
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		state, stateErr := manager.Get(coreID, request.RunID)
		t.Fatalf("run did not reach provider; state=%+v err=%v", state, stateErr)
	}
	active, err := manager.Get(coreID, request.RunID)
	if err != nil || active.ExecutionID == "" || active.SessionID == "" {
		t.Fatalf("admitted run IDs = %+v, %v", active, err)
	}

	shutdownBody, _ := json.Marshal(struct {
		CoreID string `json:"coreId"`
	}{CoreID: coreID})
	shutdownResponse := httptest.NewRecorder()
	shutdownDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(shutdownResponse, httptest.NewRequest(http.MethodPost,
			"/api/v2/core/shutdown", bytes.NewReader(shutdownBody)))
		close(shutdownDone)
	}()
	select {
	case <-provider.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel the run owner")
	}
	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		provider.releaseRun()
		<-shutdownDone
		t.Fatal("shutdown response waited for run join")
	}
	if shutdownResponse.Code != http.StatusAccepted || !strings.Contains(shutdownResponse.Body.String(), `"ok":true`) {
		t.Fatalf("shutdown response = %d %s", shutdownResponse.Code, shutdownResponse.Body.String())
	}
	select {
	case <-handler.ShutdownRequested():
	default:
		t.Fatal("shutdown intent was not exposed after the response")
	}

	_, _, err = manager.Start(api.StartRunRequest{CoreID: coreID, RunID: "after-shutdown", Task: "must not start"})
	var coreError *api.Error
	if !errors.As(err, &coreError) || coreError.Code != "core_closing" {
		t.Fatalf("run admission after shutdown = %v", err)
	}

	provider.releaseRun()
	if err := handler.Close(); err != nil {
		t.Fatal(err)
	}
	inspection, err := service.Inspect(context.Background(), active.ExecutionID, 0, -1)
	if err != nil || inspection.Execution.Lifecycle == "active" || inspection.Execution.Outcome != "cancelled" {
		t.Fatalf("shutdown did not save run termination: %#v, %v", inspection.Execution, err)
	}
}

func TestSessionViewRouteReturnsUnifiedSavedConversation(t *testing.T) {
	workspace := t.TempDir()
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, snapshotProvider{}, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	defer func() {
		_ = handler.Close()
		_ = store.Close()
	}()
	request := api.StartRunRequest{CoreID: manager.Core().CoreID, RunID: "session-view-run", Task: "保存する依頼"}
	if _, _, err := manager.Start(request); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var state api.RunState
	for time.Now().Before(deadline) {
		state, err = manager.Get(request.CoreID, request.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase == "terminal" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if state.Phase != "terminal" || state.SessionID == "" {
		t.Fatalf("run did not create a saved Session: %+v", state)
	}
	response := httptest.NewRecorder()
	path := "/api/v2/sessions/" + state.SessionID + "/view"
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET Session view status = %d, body = %s", response.Code, response.Body.String())
	}
	var snapshot api.ConversationSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ScopeID != state.SessionID || len(snapshot.RootCuts) != 1 || findConversationEntity(snapshot, "task", "保存する依頼") == nil || findConversationEntity(snapshot, "assistant", "保存済みの回答") == nil {
		t.Fatalf("Session view did not return the unified saved conversation: %+v", snapshot)
	}
}

func findConversationEntity(snapshot api.ConversationSnapshot, kind, text string) *api.ConversationEntity {
	for _, entity := range snapshot.Entities {
		if entity.Kind == kind && entity.Text == text {
			copy := entity
			return &copy
		}
	}
	return nil
}
