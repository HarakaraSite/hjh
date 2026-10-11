package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

func TestSessionContextAndRecallRoutesUseManagerOperations(t *testing.T) {
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

	call := func(method, path string, body any, into any) *httptest.ResponseRecorder {
		t.Helper()
		var encoded []byte
		if body != nil {
			var marshalErr error
			encoded, marshalErr = json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewReader(encoded)))
		if into != nil && response.Code < http.StatusBadRequest {
			if decodeErr := json.Unmarshal(response.Body.Bytes(), into); decodeErr != nil {
				t.Fatal(decodeErr)
			}
		}
		return response
	}

	coreID := manager.Core().CoreID
	runID := "recall-http-source"
	started := call(http.MethodPost, "/api/v2/runs", api.StartRunRequest{CoreID: coreID, RunID: runID, Task: "save a stopped source"}, new(api.RunState))
	if started.Code != http.StatusAccepted {
		t.Fatalf("POST /runs = %d %s", started.Code, started.Body.String())
	}
	select {
	case <-provider.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("source execution did not reach provider")
	}
	state, err := manager.Get(coreID, runID)
	if err != nil || state.SessionID == "" {
		t.Fatalf("source run state = %+v, %v", state, err)
	}
	cancelURL := fmt.Sprintf("/api/v2/runs/%s/cancel?coreId=%s", runID, coreID)
	cancelled := call(http.MethodPost, cancelURL, nil, new(api.RunState))
	if cancelled.Code != http.StatusAccepted {
		t.Fatalf("POST /runs/{id}/cancel = %d %s", cancelled.Code, cancelled.Body.String())
	}
	select {
	case <-provider.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("source provider did not observe cancellation")
	}
	provider.releaseRun()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state, err = manager.Get(coreID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if state.Phase == "terminal" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if state.Phase != "terminal" || state.ExecutionID == "" {
		t.Fatalf("source did not settle: %+v", state)
	}

	contextURL := fmt.Sprintf("/api/v2/sessions/%s/context?coreId=%s", state.SessionID, coreID)
	var before api.SessionContext
	contextResponse := call(http.MethodGet, contextURL, nil, &before)
	if contextResponse.Code != http.StatusOK || before.LatestRootExecution == nil || before.LatestRootExecution.ExecutionID != state.ExecutionID {
		t.Fatalf("GET context before recall = %d %+v %s", contextResponse.Code, before, contextResponse.Body.String())
	}

	request := api.RecallOperationRequest{CoreID: coreID, OperationID: "recall-http-one", Action: "prepare", Selector: "latest"}
	var prepared api.RecallOperationResult
	prepareResponse := call(http.MethodPost, "/api/v2/sessions/"+state.SessionID+"/recall", request, &prepared)
	if prepareResponse.Code != http.StatusOK || prepared.Receipt.Result != "prepared" || prepared.Receipt.SourceExecutionID != state.ExecutionID {
		t.Fatalf("POST recall = %d %+v %s", prepareResponse.Code, prepared, prepareResponse.Body.String())
	}
	var replayed api.RecallOperationResult
	replayResponse := call(http.MethodPost, "/api/v2/sessions/"+state.SessionID+"/recall", request, &replayed)
	if replayResponse.Code != http.StatusOK || replayed.Receipt.CompletedAt != prepared.Receipt.CompletedAt {
		t.Fatalf("same-ID POST recall = %d %+v %s", replayResponse.Code, replayed, replayResponse.Body.String())
	}
	operationURL := fmt.Sprintf("/api/v2/sessions/%s/recall/operations/%s?coreId=%s", state.SessionID, request.OperationID, coreID)
	var queried api.RecallOperationResult
	operationResponse := call(http.MethodGet, operationURL, nil, &queried)
	if operationResponse.Code != http.StatusOK || queried.Receipt.OperationID != request.OperationID || !queried.ManagedByThisCore {
		t.Fatalf("GET recall operation = %d %+v %s", operationResponse.Code, queried, operationResponse.Body.String())
	}
	var after api.SessionContext
	contextResponse = call(http.MethodGet, contextURL, nil, &after)
	if contextResponse.Code != http.StatusOK || after.PendingRecall == nil || after.PendingRecall.SourceExecutionID != state.ExecutionID {
		t.Fatalf("GET context after recall = %d %+v %s", contextResponse.Code, after, contextResponse.Body.String())
	}
}
