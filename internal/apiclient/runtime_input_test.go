package apiclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hjh/internal/api"
)

func TestRuntimeInputMethodsUseCapturedTargetsAndRevisionQueries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := func(value any) {
			t.Helper()
			writeJSONForTest(w, http.StatusOK, value)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs/run-1/inputs":
			var request api.SubmitInputRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode submit input: %v", err)
			}
			if request.CoreID != "core-old" || request.SessionID != "session-1" || request.InputID != "input-1" ||
				request.Kind != "follow-up" || request.Text != "next task" || request.Next == nil ||
				request.Next.ProviderID != "openai" || request.Next.ModelID != "model-2" {
				t.Errorf("submitted input lost its captured scope or selection: %+v", request)
			}
			write(api.InputReceipt{CoreID: request.CoreID, SessionID: request.SessionID, RunID: "run-1", InputID: request.InputID, Kind: request.Kind, Status: "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-1/inputs/input-1":
			if r.URL.Query().Get("coreId") != "core-old" || r.URL.Query().Get("includeText") != "true" {
				t.Errorf("input query = %s", r.URL.RawQuery)
			}
			write(api.InputReceipt{CoreID: "core-old", SessionID: "session-1", RunID: "run-1", InputID: "input-1", Kind: "follow-up", Status: "queued", Text: "next task"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/sessions/session-1/inputs" && r.URL.Query().Get("coreId") == "":
			if r.URL.Query().Get("afterRevision") != "" || r.URL.Query().Get("includeText") != "" {
				t.Errorf("initial receipt read must discover all Core epochs without expansion: %s", r.URL.RawQuery)
			}
			write(api.InputReceiptPage{CoreID: "core-new", SessionID: "session-1", Revision: 7, Receipts: []api.InputReceipt{{CoreID: "core-old", SessionID: "session-1", InputID: "input-1", Kind: "follow-up", Status: "queued"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/sessions/session-1/inputs":
			if r.URL.Query().Get("coreId") != "core-new" || r.URL.Query().Get("afterRevision") != "7" {
				t.Errorf("incremental receipt read = %s", r.URL.RawQuery)
			}
			write(api.InputReceiptPage{CoreID: "core-new", SessionID: "session-1", Revision: 8, Receipts: []api.InputReceipt{{CoreID: "core-old", SessionID: "session-1", InputID: "input-1", Kind: "follow-up", Status: "started", Revision: 8}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs/run-1/input-chain/cancel":
			var request struct {
				CoreID string `json:"coreId"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.CoreID != "core-old" {
				t.Errorf("cancel chain request = %+v, err=%v", request, err)
			}
			write(api.InputChainState{CoreID: "core-old", SessionID: "session-1", AnchorRunID: "run-1", Phase: "cancelling"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/runs/run-1/input-chain":
			if r.URL.Query().Get("coreId") != "core-old" {
				t.Errorf("input chain query = %s", r.URL.RawQuery)
			}
			write(api.InputChainState{CoreID: "core-old", SessionID: "session-1", AnchorRunID: "run-1", Phase: "settled"})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	request := api.SubmitInputRequest{
		CoreID: "core-old", SessionID: "session-1", InputID: "input-1", Kind: "follow-up", Text: "next task",
		Next: &api.NextRunOptions{ProviderID: "openai", ModelID: "model-2"},
	}
	if receipt, err := client.SubmitInput(t.Context(), "run-1", request); err != nil || receipt.Status != "queued" {
		t.Fatalf("SubmitInput = %+v, %v", receipt, err)
	}
	if receipt, err := client.Input(t.Context(), "core-old", "run-1", "input-1", true); err != nil || receipt.Text != "next task" {
		t.Fatalf("Input = %+v, %v", receipt, err)
	}
	if page, err := client.Inputs(t.Context(), "session-1", "", 0, false); err != nil || page.CoreID != "core-new" || len(page.Receipts) != 1 || page.Receipts[0].CoreID != "core-old" {
		t.Fatalf("initial Inputs = %+v, %v", page, err)
	}
	if page, err := client.Inputs(t.Context(), "session-1", "core-new", 7, false); err != nil || page.Revision != 8 || page.Receipts[0].Status != "started" {
		t.Fatalf("incremental Inputs = %+v, %v", page, err)
	}
	if state, err := client.CancelInputChain(t.Context(), "core-old", "run-1"); err != nil || state.Phase != "cancelling" {
		t.Fatalf("CancelInputChain = %+v, %v", state, err)
	}
	if state, err := client.InputChain(t.Context(), "core-old", "run-1"); err != nil || state.Phase != "settled" {
		t.Fatalf("InputChain = %+v, %v", state, err)
	}
}
