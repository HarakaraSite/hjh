package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/credentials"
)

func TestModelsCLIUsesRegisteredCredentialState(t *testing.T) {
	t.Run("explicit-root", func(t *testing.T) { testModelsCLIUsesRegisteredCredentialState(t, true) })
	t.Run("default-config-root", func(t *testing.T) { testModelsCLIUsesRegisteredCredentialState(t, false) })
}

func testModelsCLIUsesRegisteredCredentialState(t *testing.T, explicitRoot bool) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	workspace := t.TempDir()
	state := ""
	if explicitRoot {
		state = filepath.Join(workspace, "private-state")
	}
	path := filepath.Join(workspace, "config.json")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cli-test-registered" {
			t.Error("CLI catalog did not use selected credential state")
		}
		io.WriteString(w, `{"data":[{"id":"cli-model"}]}`)
	}))
	defer upstream.Close()
	config := map[string]any{"model": map[string]string{"provider": "test", "modelId": "cli-model"},
		"providers":   map[string]any{"test": map[string]any{"api": "responses", "baseURL": upstream.URL, "auth": map[string]string{"kind": "api-key", "profile": "key"}, "catalog": map[string]string{"path": "models", "kind": "openai"}}},
		"credentials": map[string]any{"key": map[string]any{}}}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	manager := credentials.NewManager(credentials.Options{StateDir: state})
	_, err := manager.RegisterAPIKey(t.Context(), func(context.Context) (credentials.Target, error) {
		return credentials.Target{ConfigPath: path, ProviderID: "test", Name: "key", Kind: credentials.KindAPIKey}, nil
	}, "cli-register", "cli-test-registered")
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"models", "--workspace", workspace, "--config", path, "--provider", "test"}
	if explicitRoot {
		args = append(args, "--credential-state-dir", state)
	}
	code := run(args, io.NopCloser(strings.NewReader("")), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "cli-model") {
		t.Fatalf("CLI exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), "cli-test-registered") {
		t.Fatal("CLI exposed credential")
	}
}
