package apiclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hjh/internal/api"
)

func TestCatalogAndLoginUseTheResolvedSessionScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopeQuery := r.URL.Query()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/roles":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" {
				t.Errorf("roles scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, api.RoleCatalog{DefaultRole: "default"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/providers":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" {
				t.Errorf("providers scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, []api.ProviderDefinition{{ID: "chatgpt"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" || scopeQuery.Get("providerId") != "chatgpt" {
				t.Errorf("models scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, []api.ModelInfo{{ID: "model-1", Name: "Model 1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/auth/status":
			if scopeQuery.Get("sessionId") != "session-1" || scopeQuery.Get("workspace") != "/core/saved" || scopeQuery.Get("providerId") != "chatgpt" {
				t.Errorf("auth status scope = %s", scopeQuery.Encode())
			}
			writeJSONForTest(w, http.StatusOK, api.Status{Connected: true})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/login":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode login request: %v", err)
			}
			if request["sessionId"] != "session-1" || request["workspace"] != "/core/saved" || request["providerId"] != "chatgpt" {
				t.Errorf("login request = %#v", request)
			}
			writeJSONForTest(w, http.StatusOK, api.Login{AuthorizationURL: "https://auth.example/authorize", Workspace: "/core/saved", ProviderID: "chatgpt", AuthProfile: "profile-ref"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/complete":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode completion request: %v", err)
			}
			if request["workspace"] != "/core/saved" || request["providerId"] != "chatgpt" || request["callbackUrl"] != "http://127.0.0.1/callback?code=sample" {
				t.Errorf("completion request = %#v", request)
			}
			if _, exists := request["authProfile"]; exists {
				t.Errorf("completion body included a field outside the resolved API request: %#v", request)
			}
			writeJSONForTest(w, http.StatusOK, api.Status{Connected: true, Account: "account@example.invalid"})
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
	if _, err = client.Models(t.Context(), scope, "chatgpt"); err != nil {
		t.Fatalf("Models: %v", err)
	}
	if status, statusErr := client.LoginStatus(t.Context(), scope, "chatgpt"); statusErr != nil || !status.Connected {
		t.Fatalf("LoginStatus = %#v, %v", status, statusErr)
	}
	login, err := client.BeginLogin(t.Context(), scope, "chatgpt")
	if err != nil {
		t.Fatalf("BeginLogin: %v", err)
	}
	if login.Workspace != "/core/saved" || login.ProviderID != "chatgpt" || login.AuthProfile != "profile-ref" {
		t.Fatalf("resolved login = %#v", login)
	}
	status, err := client.CompleteLogin(t.Context(), login, "http://127.0.0.1/callback?code=sample")
	if err != nil || !status.Connected {
		t.Fatalf("CompleteLogin = %#v, %v", status, err)
	}
}
