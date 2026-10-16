package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/extensions"
	"hjh/internal/host"
	"hjh/internal/providers"
)

type webSearchTransport func(*http.Request) (*http.Response, error)

func (f webSearchTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWebSearchProviderErrorReachesFollowingModelRequest(t *testing.T) {
	store, workspace := delegationStore(t)
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HJH_TEST_EXA_ERROR_KEY", "test-error-key")
	config["credentials"].(map[string]any)["exa-api-key"] = map[string]string{"env": "HJH_TEST_EXA_ERROR_KEY"}
	config["roles"].(map[string]any)["default"].(map[string]any)["tools"] = []string{"web_search", "web_fetch"}
	if err := os.WriteFile(path, contracts.JSON(config), 0644); err != nil {
		t.Fatal(err)
	}
	// The failure body observed in the affected user sessions, including Exa's
	// documented public error fields. It must reach the model, not just host history.
	const reason = "HIPAA compliance is available for enterprise customers. Please contact us for more information."
	client := &http.Client{Transport: webSearchTransport(func(*http.Request) (*http.Response, error) {
		body := contracts.JSON(map[string]string{"requestId": "observed-request", "error": reason, "tag": "FEATURE_DISABLED"})
		return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	step := 0
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		if step == 1 {
			for _, tool := range req.Tools {
				if tool.Name == "web_search" {
					var schema struct {
						Properties map[string]any `json:"properties"`
					}
					if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
						t.Fatal(err)
					}
					if _, exists := schema.Properties["compliance"]; exists {
						t.Error("enterprise compliance is still advertised to the model")
					}
				}
			}
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "failed-search", Name: "web_search", Arguments: contracts.JSON(map[string]string{"query": "official documentation"})}}}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		for _, detail := range []string{"403", reason, "FEATURE_DISABLED", "observed-request"} {
			if last.Role != "tool" || !strings.Contains(string(last.Content), detail) {
				t.Errorf("provider error detail %q missing from following model input", detail)
			}
		}
		return contracts.ModelResult{Text: "The provider explained the failure."}, nil
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, ModelID: "test/model", Task: "explain search failure", HTTPClient: client, CredentialStateDir: t.TempDir()})
	if err != nil || !result.Result.OK || step != 2 {
		t.Fatalf("provider error run %+v, steps %d: %v", result, step, err)
	}
}

func TestWebSearchDirectGeneratedAndChildReturnCompleteResultsWithoutCredentialExposure(t *testing.T) {
	store, workspace := delegationStore(t)
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	const key = "search-secret-must-stay-in-Go-transport"
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	t.Setenv("EXA_API_KEY", "")
	keyPath := filepath.Join(configDir, "hjh", "credentials", "exa-api-key")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		t.Fatal(err)
	}
	config["roles"].(map[string]any)["default"].(map[string]any)["tools"] = []string{"run_js", "spawn_agent", "collect_agent", "web_search"}
	if err := os.WriteFile(path, contracts.JSON(config), 0644); err != nil {
		t.Fatal(err)
	}
	modelKeyPath := filepath.Join(workspace, "model-key")
	if err := os.WriteFile(modelKeyPath, []byte("separate-model-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	bodies := map[string]map[string]any{}
	client := &http.Client{Transport: webSearchTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.String() != "https://api.exa.ai/search" || r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("Content-Type") != "application/json" {
			t.Error("Exa method, destination or profile authentication differs from the selected tool")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		query := body["query"].(string)
		contents, _ := body["contents"].(map[string]any)
		highlights, _ := contents["highlights"].(map[string]any)
		if contents["text"] != false || highlights["maxCharacters"] != float64(1000) {
			t.Error("search request did not keep page text disabled and excerpts bounded")
		}
		mu.Lock()
		bodies[query] = body
		mu.Unlock()
		result := contracts.JSON(map[string]any{"requestId": query, "results": []any{
			map[string]any{"url": "https://example.com/first", "title": query, "highlights": []string{"source evidence"}, "future_result_field": "PRESERVED"},
			map[string]any{"url": "https://example.com/second", "highlights": []string{"complete source excerpt"}}},
			"output": map[string]any{"content": "synthesized evidence"}, "future_response_field": "PRESERVED"})
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(result)))}, nil
	})}
	steps := map[string]int{}
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				task = m.VisibleText()
			}
			if m.Role == "tool" && (!strings.Contains(string(m.Content), "future_result_field") || !strings.Contains(string(m.Content), "future_response_field") || !strings.Contains(string(m.Content), "synthesized evidence")) {
				t.Error("complete Exa JSON was not available in the following model request")
			}
		}
		if !strings.Contains(req.Instruction, "web_search:") || !strings.Contains(req.Instruction, "Cite direct source URLs") || strings.Contains(string(contracts.JSON(req)), key) {
			t.Error("search guidelines missing or credential exposed in model input")
		}
		mu.Lock()
		steps[task]++
		step := steps[task]
		mu.Unlock()
		for _, tool := range req.Tools {
			if tool.Name == "web_search" {
				var schema struct{ Properties map[string]json.RawMessage }
				if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				if schema.Properties["contents"] != nil {
					t.Fatal("page content controls are still advertised to the model")
				}
			}
		}
		if task == "parent search" && step == 1 {
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "direct-search", Name: "web_search", Arguments: contracts.JSON(map[string]string{"query": "direct"})}}}, nil
		}
		if task == "parent search" && step == 2 {
			return scriptedJS(`const search=await tools.web_search({query:'generated',type:'fast',numResults:2,includeDomains:['example.com'],startPublishedDate:'2026-01-01T00:00:00Z',userLocation:{country:'JP'},contents:{text:{maxCharacters:8000},highlights:{dynamic:true,verbosity:'medium'}},futureOption:{accepted:true},stream:true});const child=await api.agents.spawn({task:'child search',tools:['web_search']});return {search,child:await api.agents.collect({executionId:child.executionId})};`), nil
		}
		if task == "child search" && step == 1 {
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "child-search", Name: "web_search", Arguments: contracts.JSON(map[string]any{"query": "child", "contents": nil, "outputSchema": map[string]string{"type": "text"}})}}}, nil
		}
		return contracts.ModelResult{Text: "search complete: future_result_field future_response_field synthesized evidence"}, nil
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, ModelID: "test/model", Task: "parent search",
		HTTPClient: client, CredentialFile: modelKeyPath, CredentialStateDir: filepath.Join(workspace, "credentials")})
	if err != nil || !result.Result.OK {
		t.Fatalf("search %+v: %v", result, err)
	}
	if len(bodies) != 3 || bodies["direct"]["numResults"] != float64(10) || bodies["child"]["numResults"] != float64(10) || bodies["generated"]["numResults"] != float64(2) || bodies["direct"]["type"] != "auto" || bodies["direct"]["stream"] != false || bodies["generated"]["stream"] != false || bodies["generated"]["futureOption"].(map[string]any)["accepted"] != true {
		t.Fatalf("search defaults/options were changed: %+v", bodies)
	}
	children, err := store.Children(context.Background(), result.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children %+v: %v", children, err)
	}
	for _, eid := range []string{result.ExecutionID, children[0].ID} {
		records, err := store.Records(context.Background(), eid, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		requestSaved, responseSaved := false, false
		for _, r := range records {
			if strings.Contains(string(r.Payload), key) || strings.Contains(string(r.Content), key) {
				t.Fatal("credential exposed in stored search history")
			}
			if r.Kind == "effect_observation" && strings.Contains(string(r.Payload), "requestProvider") {
				requestSaved = requestSaved || strings.Contains(string(r.Payload), "exa-api-key")
				var observation contracts.Observation
				if err := json.Unmarshal(r.Payload, &observation); err != nil {
					t.Fatal(err)
				}
				if observation.Phase == "completed" {
					var summary host.ProviderObservation
					if err := json.Unmarshal(observation.Result, &summary); err != nil {
						t.Fatal(err)
					}
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(observation.Result, &fields); err != nil || len(fields) != 2 {
						t.Fatal("provider observation must contain only status and responseBytes")
					}
					responseSaved = summary.Status == 200 && summary.ResponseBytes > 0
					if strings.Contains(string(observation.Result), "future_response_field") {
						t.Fatal("raw response duplicated in host summary")
					}
				}
			}
		}
		if !requestSaved || !responseSaved {
			t.Fatal("public search request/response not preserved in ordinary host history")
		}
	}
}

func TestWebSearchCancellationJoinsCredentialRequest(t *testing.T) {
	store, workspace := delegationStore(t)
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	const key = "cancelled-search-transport-only-key"
	t.Setenv("HJH_TEST_EXA_CANCEL_KEY", key)
	config["credentials"].(map[string]any)["exa-api-key"] = map[string]string{"env": "HJH_TEST_EXA_CANCEL_KEY"}
	if err := os.WriteFile(path, contracts.JSON(config), 0644); err != nil {
		t.Fatal(err)
	}
	entered, stopped := make(chan struct{}), make(chan struct{})
	client := &http.Client{Transport: webSearchTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("search credential did not reach transport")
		}
		close(entered)
		<-r.Context().Done()
		close(stopped)
		return nil, r.Context().Err()
	})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var result RunResult
	done := make(chan error, 1)
	credentialStateDir := t.TempDir()
	go func() {
		var err error
		result, err = Run(ctx, store, delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
			return scriptedJS("// Search until cancelled.\nif(process.env.HJH_TEST_EXA_CANCEL_KEY)throw new Error('credential reached JS');return await tools.web_search({query:'cancel'});"), nil
		}), RunOptions{Workspace: workspace, ConfigPath: path, ModelID: "test/model", Task: "cancel search", HTTPClient: client, CredentialStateDir: credentialStateDir})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("search ended before transport: %+v: %v", result, err)
	case <-time.After(3 * time.Second):
		t.Fatal("JS search did not reach credential transport")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || result.Result.Outcome != "cancelled" {
			t.Fatalf("search lost cancellation: %+v: %v", result, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled JS search did not join")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("search returned before the credential transport stopped")
	}
	records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if strings.Contains(string(record.Payload), key) || strings.Contains(string(record.Content), key) {
			t.Fatal("cancelled search exposed its credential in history")
		}
	}
}

func TestToolRequestCancellationAndCredentialTransportError(t *testing.T) {
	const key = "request-token-only-for-transport"
	t.Setenv("HJH_TEST_SEARCH_KEY", key)
	entered := make(chan struct{})
	factory := newConnectionFactory(RunOptions{CredentialStateDir: t.TempDir(), HTTPClient: &http.Client{Transport: webSearchTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("request credential not resolved")
		}
		close(entered)
		<-r.Context().Done()
		return nil, errors.Join(r.Context().Err(), errors.New("invalid credential value: "+key))
	})}})
	defer factory.close()
	registry := &providers.Registry{Config: providers.Config{Credentials: map[string]credentials.Profile{"search": {Env: "HJH_TEST_SEARCH_KEY"}}}}
	request := factory.toolRequestProvider(registry, filepath.Join(t.TempDir(), "config.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := request(ctx, host.ProviderRequestArgs{AuthProfile: "search", Endpoint: "https://example.com/search", Method: "POST", Body: contracts.JSON(map[string]string{"query": "cancel"})})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("credential request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "[redacted]") {
			t.Fatal("transport error lost cancellation or exposed the credential")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled search transport did not finish")
	}
}
