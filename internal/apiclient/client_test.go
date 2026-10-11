package apiclient

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hjh/internal/api"
)

func TestCatalogAndLoginUseTheResolvedSessionScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopeQuery := r.URL.Query()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/roles":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" {
				t.Errorf("roles scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, api.RoleCatalog{DefaultRole: "default"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/providers":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" {
				t.Errorf("providers scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, []api.ProviderDefinition{{ID: "chatgpt"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/models":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" || scopeQuery.Get("providerId") != "chatgpt" || scopeQuery.Get("authProfile") != "account-2" {
				t.Errorf("models scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, []api.ModelInfo{{ID: "model-1", Name: "Model 1", Efforts: []string{"low", "high"}, EffortSource: "catalog"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/auth/status":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" || scopeQuery.Get("providerId") != "chatgpt" {
				t.Errorf("auth status scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, api.Status{Connected: true})
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

	scope := Scope{SessionID: "session-1", Workspace: "/core/saved"}
	if _, err = client.Roles(t.Context(), scope); err != nil {
		t.Fatalf("Roles: %v", err)
	}
	if _, err = client.Providers(t.Context(), scope); err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if models, modelsErr := client.ModelsWithAuth(t.Context(), scope, "chatgpt", "account-2"); modelsErr != nil || len(models.Models) != 1 || models.Models[0].EffortSource != "catalog" || len(models.Models[0].Efforts) != 2 || models.Models[0].Efforts[1] != "high" {
		t.Fatalf("ModelsWithAuth: %#v, %v", models, modelsErr)
	}
	if status, statusErr := client.LoginStatus(t.Context(), scope, "chatgpt"); statusErr != nil || !status.Connected {
		t.Fatalf("LoginStatus = %#v, %v", status, statusErr)
	}
}

func TestSessionViewUsesUnifiedConversationSnapshotRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/sessions/session-1/view" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
			return
		}
		writeJSONForTest(w, http.StatusOK, api.ConversationSnapshot{ScopeID: "session-1", ViewID: "view-1", Revision: 2,
			RootCuts: []api.ConversationCut{{RootExecutionID: "root-1", RootExecutionOrdinal: 4, Executions: []api.ConversationExecutionHead{{
				ExecutionID: "root-1", SemanticOrdinal: 7, MessageOrdinal: 1,
			}}}}, Order: []string{"cv:task"}, Entities: map[string]api.ConversationEntity{
				"cv:task": {ID: "cv:task", Kind: "task", ExecutionID: "root-1", Text: "task", Status: "completed",
					Position: api.ConversationPosition{RootExecutionOrdinal: 4, FirstSequence: 1}},
			}})
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	snapshot, err := client.SessionView(t.Context(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ScopeID != "session-1" || snapshot.ViewID != "view-1" || snapshot.Revision != 2 || len(snapshot.RootCuts) != 1 || snapshot.RootCuts[0].Executions[0].SemanticOrdinal != 7 {
		t.Fatalf("SessionView decoded a different snapshot contract: %+v", snapshot)
	}
	if entity := snapshot.Entities["cv:task"]; entity.Text != "task" || entity.Position.RootExecutionOrdinal != 4 {
		t.Fatalf("SessionView lost stable entity data: %+v", snapshot.Entities)
	}
}
