package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

// The root updates its registered key before spawning two children. Those
// children and the next root step must use the root's captured binding, while
// a new run resolves the updated key.
func TestRunPinsRegisteredCredentialThroughParallelChildren(t *testing.T) {
	runCredentialBindingThroughChildren(t, credentials.KindAPIKey)
}

func TestRunPinsOAuthAccountThroughParallelChildren(t *testing.T) {
	runCredentialBindingThroughChildren(t, credentials.KindChatGPTOAuth)
}

func runCredentialBindingThroughChildren(t *testing.T, kind string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	workspace := t.TempDir()
	configDir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(configDir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	manager := credentials.NewManager(credentials.Options{StateDir: filepath.Join(workspace, "credentials-state")})
	defer manager.Close()
	target := credentials.Target{ConfigPath: configPath, ProviderID: "binding-test", Name: "key", Kind: kind}
	const secondAccount = "88888888-8888-4888-8888-888888888888"
	if kind == credentials.KindChatGPTOAuth {
		target.Profile.OAuthFile = filepath.Join(workspace, "oauth.json")
		for id, token := range map[string]string{"primary": "test-key-before", secondAccount: "test-key-after"} {
			path := target.Profile.OAuthFile
			if id != "primary" {
				path += ".accounts/" + id + ".json"
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			record := map[string]any{"issuer": "https://auth.openai.com", "subject": id, "email": id + "@test.invalid", "client_id": "client-" + id,
				"ext_agent_host_id": "test-host", "id_token": "test-id", "access_token": token, "refresh_token": "test-refresh-" + id,
				"token_type": "Bearer", "expires_at": time.Now().Add(time.Hour), "scopes": []string{"chatgpt.tokens.use.direct"}, "saved_at": time.Now()}
			data, _ := json.Marshal(record)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		metadata, _ := json.Marshal(map[string]any{"schemaVersion": 1, "revision": 1, "selectedAccountId": "primary", "accountIds": []string{"primary", secondAccount}})
		if err := os.WriteFile(target.Profile.OAuthFile+".accounts.json", metadata, 0600); err != nil {
			t.Fatal(err)
		}
	}
	resolve := func(context.Context) (credentials.Target, error) { return target, nil }
	if kind == credentials.KindAPIKey {
		if _, err := manager.RegisterAPIKey(ctx, resolve, "first", "test-key-before"); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var observed []string
	rootSteps := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		observed = append(observed, r.Header.Get("Authorization"))
		first := request.Model == "root" && rootSteps == 0
		if request.Model == "root" {
			rootSteps++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		var output []any
		if first {
			var switchErr error
			if kind == credentials.KindAPIKey {
				_, switchErr = manager.RegisterAPIKey(ctx, resolve, "second", "test-key-after")
			} else {
				_, switchErr = manager.SelectAccount(ctx, resolve, secondAccount)
			}
			if switchErr != nil {
				t.Error(switchErr)
				return
			}
			code := `const results=await Promise.all([api.agents.run({role:"reviewer",task:"first child"}),api.agents.run({role:"reviewer",task:"second child"})]); return results.map(x=>x.finalText);`
			args, _ := json.Marshal(map[string]string{"code": code})
			output = []any{map[string]any{"type": "function_call", "id": "fc_binding", "call_id": "binding_call", "name": "run_js", "namespace": "hjh", "arguments": string(args), "status": "completed"}}
		} else {
			output = []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "BINDING_OK"}}}}
		}
		writeI04SSE(w, map[string]any{"type": "response.completed", "response": map[string]any{"id": "response_binding", "model": request.Model, "status": "completed", "output": output}})
	}))
	defer server.Close()
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["credentials"] = map[string]any{"key": target.Profile}
	if kind == credentials.KindChatGPTOAuth {
		cfg["credentials"].(map[string]any)["alias"] = target.Profile
	}
	cfg["providers"] = map[string]any{"binding-test": map[string]any{"api": "responses", "baseURL": server.URL, "path": "/responses", "auth": map[string]string{"kind": kind, "profile": "key"}, "defaultModel": "root", "adapter": map[string]bool{"namespaceTools": true, "store": false}}}
	cfg["model"] = map[string]any{"provider": "binding-test", "modelId": "root"}
	cfg["roles"].(map[string]any)["reviewer"].(map[string]any)["model"] = map[string]string{"modelId": "child"}
	if kind == credentials.KindChatGPTOAuth {
		cfg["roles"].(map[string]any)["reviewer"].(map[string]any)["model"].(map[string]string)["authProfile"] = "alias"
	}
	data, _ = json.Marshal(cfg)
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	options := RunOptions{Workspace: workspace, ConfigPath: configPath, Credentials: manager, Task: "use two children"}
	result, err := Run(ctx, store, nil, options)
	if err != nil || !result.Result.OK {
		t.Fatalf("root/children failed: %v", err)
	}
	children, err := store.Children(ctx, result.ExecutionID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children count=%d err=%v", len(children), err)
	}
	options.Task = "next run"
	result, err = Run(ctx, store, nil, options)
	if err != nil || !result.Result.OK {
		t.Fatalf("next run failed: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 5 {
		t.Fatalf("model requests=%d, want root+2 children+root final+new run", len(observed))
	}
	for i, token := range observed {
		want := "Bearer test-key-before"
		if i == 4 {
			want = "Bearer test-key-after"
		}
		if token != want {
			t.Fatalf("request %d did not use its run binding", i)
		}
	}
	// No key material belongs in execution captures or recorded model payloads.
	execution, err := store.Execution(ctx, result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if string(execution.Model) != string(contracts.JSON(contracts.ModelSelection{Provider: "binding-test", API: "responses", AuthProfile: "key", ModelID: "root"})) {
		// Compare semantically because the resolver may explicitly retain auto effort.
		var model contracts.ModelSelection
		if err := json.Unmarshal(execution.Model, &model); err != nil || model.AuthProfile != "key" {
			t.Fatal("saved model profile missing")
		}
	}
}
