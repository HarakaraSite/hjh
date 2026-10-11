package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

func TestSelectionAndModelsRoutesCarryEffectiveInputsAndEfforts(t *testing.T) {
	t.Setenv("HJH_I08_DEFAULT_TOKEN", "default-token")
	t.Setenv("HJH_I08_SELECTED_TOKEN", "selected-token")
	workspace := t.TempDir()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer selected-token" {
			t.Errorf("catalog authorization = %q, want selected profile", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"models":[{"slug":"gpt-6.1-sol","display_name":"GPT 6.1 Sol","visibility":"list","supported_reasoning_levels":[{"description":"Low","effort":"low"},{"description":"High","effort":"high"}]}]}`)
	}))
	defer catalog.Close()
	configPath := filepath.Join(workspace, "providers.json")
	config := fmt.Sprintf(`{
"defaultRole":"default",
"model":{"provider":"static","modelId":"configured","effort":"auto","authProfile":"default"},
"credentials":{"default":{"env":"HJH_I08_DEFAULT_TOKEN"},"selected":{"env":"HJH_I08_SELECTED_TOKEN"}},
"providers":{
  "static":{"api":"responses","auth":{"kind":"api-key","profile":"default"},"defaultModel":"static-model","defaultEffort":"auto","models":[{"id":"static-model","name":"Static","efforts":["minimal","high"]}]},
  "account":{"api":"responses","baseURL":"%s","auth":{"kind":"api-key","profile":"default"},"defaultModel":"gpt-6.1-sol","catalog":{"kind":"openai-account","path":"/models"}}
},"roles":{"default":{"tools":[]}}}`, catalog.URL)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace, ConfigPath: configPath})
	manager := corerun.New(service, workspace)
	handler := New(service, manager)
	defer func() {
		_ = handler.Close()
		_ = service.Close()
		_ = store.Close()
	}()

	query := url.Values{"workspace": {workspace}, "providerId": {"static"}, "modelId": {"unlisted/model"}, "effort": {"custom"}, "authProfile": {"selected"}}
	selectionResponse := httptest.NewRecorder()
	handler.ServeHTTP(selectionResponse, httptest.NewRequest(http.MethodGet, "/api/v2/selection?"+query.Encode(), nil))
	if selectionResponse.Code != http.StatusOK {
		t.Fatalf("GET /selection status = %d, body=%s", selectionResponse.Code, selectionResponse.Body.String())
	}
	var preview api.SelectionPreview
	if err = json.Unmarshal(selectionResponse.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Role != "default" || preview.Selection.Provider != "static" || preview.Selection.API != "responses" ||
		preview.Selection.ModelID != "unlisted/model" || preview.Selection.Effort != "custom" || preview.Selection.AuthProfile != "selected" {
		t.Fatalf("selection response = %+v", preview)
	}
	sessions, err := service.Sessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("selection preview created Sessions %v: %v", sessions, err)
	}

	modelQuery := url.Values{"workspace": {workspace}, "providerId": {"account"}, "authProfile": {"selected"}}
	modelsResponse := httptest.NewRecorder()
	handler.ServeHTTP(modelsResponse, httptest.NewRequest(http.MethodGet, "/api/v2/models?"+modelQuery.Encode(), nil))
	if modelsResponse.Code != http.StatusOK {
		t.Fatalf("GET /models status = %d, body=%s", modelsResponse.Code, modelsResponse.Body.String())
	}
	var models []api.ModelInfo
	if err = json.Unmarshal(modelsResponse.Body.Bytes(), &models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "gpt-6.1-sol" || models[0].EffortSource != "catalog" || !reflect.DeepEqual(models[0].Efforts, []string{"low", "high"}) {
		t.Fatalf("models response = %+v", models)
	}

	staticQuery := url.Values{"workspace": {workspace}, "providerId": {"static"}}
	staticResponse := httptest.NewRecorder()
	handler.ServeHTTP(staticResponse, httptest.NewRequest(http.MethodGet, "/api/v2/models?"+staticQuery.Encode(), nil))
	if staticResponse.Code != http.StatusOK {
		t.Fatalf("GET /models static status = %d, body=%s", staticResponse.Code, staticResponse.Body.String())
	}
	var staticModels []api.ModelInfo
	if err = json.Unmarshal(staticResponse.Body.Bytes(), &staticModels); err != nil {
		t.Fatal(err)
	}
	if len(staticModels) != 1 || staticModels[0].EffortSource != "config" || !reflect.DeepEqual(staticModels[0].Efforts, []string{"minimal", "high"}) {
		t.Fatalf("static models response = %+v", staticModels)
	}
}
