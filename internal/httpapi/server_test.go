package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
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
	handler.ServeHTTP(coreResponse, httptest.NewRequest(http.MethodGet, "/api/v1/core", nil))
	if coreResponse.Code != http.StatusOK {
		t.Fatalf("GET /core status = %d, body = %s", coreResponse.Code, coreResponse.Body.String())
	}
	var core api.Core
	if err := json.Unmarshal(coreResponse.Body.Bytes(), &core); err != nil {
		t.Fatal(err)
	}
	if core.CoreID == "" || core.APIVersion != "v1" || core.Workspace != workspace {
		t.Fatalf("GET /core returned %+v", core)
	}

	request := api.StartRunRequest{CoreID: core.CoreID, RunID: "run-preparation-failure", Task: "do not contact a provider",
		Workspace: workspace, ProviderID: "missing-provider"}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, httptest.NewRequest(http.MethodPost, "/api/v1/runs", bytes.NewReader(body)))
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
	eventsURL := fmt.Sprintf("/api/v1/runs/%s/events?coreId=%s", request.RunID, core.CoreID)
	handler.ServeHTTP(eventsResponse, httptest.NewRequest(http.MethodGet, eventsURL, nil))
	if eventsResponse.Code != http.StatusOK || !strings.HasPrefix(eventsResponse.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("GET run events status/content type = %d/%q: %s", eventsResponse.Code, eventsResponse.Header().Get("Content-Type"), eventsResponse.Body.String())
	}
	stream := eventsResponse.Body.String()
	if !strings.Contains(stream, "event: run.snapshot\n") || !strings.Contains(stream, "event: run.terminal\n") || !strings.Contains(stream, `"phase":"terminal"`) {
		t.Fatalf("late event subscription did not return terminal snapshot and event: %s", stream)
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/providers", nil))
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
