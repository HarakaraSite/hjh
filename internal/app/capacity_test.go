package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/providers"
)

func TestRunBudgetUsesLiveCatalogAndSharesSnapshotAcrossModels(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer catalog-secret" {
			t.Errorf("catalog destination/auth: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"catalog-only","context_length":2000000},{"id":"child","context_length":1000000}]}`)
	}))
	defer server.Close()
	workspace := t.TempDir()
	key := filepath.Join(workspace, "key")
	if err := os.WriteFile(key, []byte("catalog-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	registry, err := providers.Parse(contracts.JSON(map[string]any{"providers": map[string]any{"test": map[string]any{
		"api": "chat-completions", "baseURL": server.URL, "catalog": map[string]string{"kind": "openai", "path": "/models"}, "auth": map[string]string{"kind": "api-key", "profile": "key"}}},
		"credentials": map[string]any{"key": map[string]string{"file": key}}}))
	if err != nil {
		t.Fatal(err)
	}
	factory := newConnectionFactory(RunOptions{CredentialStateDir: filepath.Join(workspace, "state")})
	defer factory.close()
	for model, limit := range map[string]int64{"catalog-only": 1800000, "child": 900000} {
		budget, message := factory.budget(context.Background(), registry, contracts.ModelSelection{Provider: "test", AuthProfile: "key", ModelID: model}, false, filepath.Join(workspace, "config.json"))
		if message != "" || budget.Limit == nil || *budget.Limit != limit || budget.Sources.ContextTokens != "catalog/test" {
			t.Fatalf("catalog budget: %+v %s", budget, message)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("catalog snapshot loaded %d times", calls.Load())
	}
}
