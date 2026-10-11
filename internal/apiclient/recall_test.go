package apiclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

func TestRecallClientMethodsUseSessionRoutesAndCoreIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/sessions/session-1/context":
			if r.URL.Query().Get("coreId") != "core-1" {
				t.Errorf("context coreId = %q", r.URL.Query().Get("coreId"))
			}
			writeJSONForTest(w, http.StatusOK, api.SessionContext{CoreID: "core-1", SessionID: "session-1", RecallRevision: 3})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/sessions/session-1/recall":
			var request api.RecallOperationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode recall request: %v", err)
			}
			if request != (api.RecallOperationRequest{CoreID: "core-1", OperationID: "operation-1", Action: "prepare", Selector: "latest"}) {
				t.Errorf("recall request = %+v", request)
			}
			writeJSONForTest(w, http.StatusOK, api.RecallOperationResult{Receipt: contracts.RecallReceipt{
				CoreID: "core-1", SessionID: "session-1", OperationID: "operation-1", Action: "prepare", Result: "prepared"}, ManagedByThisCore: true})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/sessions/session-1/recall/operations/operation-1":
			if r.URL.Query().Get("coreId") != "core-1" {
				t.Errorf("receipt coreId = %q", r.URL.Query().Get("coreId"))
			}
			writeJSONForTest(w, http.StatusOK, api.RecallOperationResult{Receipt: contracts.RecallReceipt{
				CoreID: "core-1", SessionID: "session-1", OperationID: "operation-1", Action: "prepare", Result: "prepared"}, ManagedByThisCore: true})
		default:
			t.Errorf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	view, err := client.SessionContext(t.Context(), "core-1", "session-1")
	if err != nil || view.RecallRevision != 3 || view.SessionID != "session-1" {
		t.Fatalf("SessionContext = %+v, %v", view, err)
	}
	request := api.RecallOperationRequest{CoreID: "core-1", OperationID: "operation-1", Action: "prepare", Selector: "latest"}
	result, err := client.Recall(t.Context(), "session-1", request)
	if err != nil || result.Receipt.Result != "prepared" || !result.ManagedByThisCore {
		t.Fatalf("Recall = %+v, %v", result, err)
	}
	result, err = client.RecallOperation(t.Context(), "core-1", "session-1", "operation-1")
	if err != nil || result.Receipt.OperationID != "operation-1" {
		t.Fatalf("RecallOperation = %+v, %v", result, err)
	}
}
