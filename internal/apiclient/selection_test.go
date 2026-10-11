package apiclient

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

func TestSelectionSendsNextRunInputsAndReadsResolvedValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/selection" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		for key, expected := range map[string]string{
			"sessionId": "session-1", "workspace": "/core/workspace 日本語", "role": "reviewer",
			"providerId": "chatgpt", "modelId": "custom/model", "effort": "custom effort", "authProfile": "account-2",
		} {
			if actual := r.URL.Query().Get(key); actual != expected {
				t.Errorf("%s = %q, want %q", key, actual, expected)
			}
		}
		writeJSONForTest(w, http.StatusOK, api.SelectionPreview{Role: "reviewer", Selection: contracts.ModelSelection{
			Provider: "chatgpt", API: "responses", ModelID: "custom/model", Effort: "custom effort", AuthProfile: "account-2",
		}})
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	preview, err := client.Selection(t.Context(), api.SelectionRequest{
		SessionID: "session-1", Workspace: "/core/workspace 日本語", Role: "reviewer",
		ProviderID: "chatgpt", ModelID: "custom/model", Effort: "custom effort", AuthProfile: "account-2",
	})
	if err != nil || preview.Role != "reviewer" || preview.Selection.AuthProfile != "account-2" || preview.Selection.Effort != "custom effort" {
		t.Fatalf("Selection = %+v, %v", preview, err)
	}
}
