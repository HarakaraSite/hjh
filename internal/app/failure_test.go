package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/executiondata"
	"hjh/internal/provider/protocol"
	"hjh/internal/providers"
)

func TestMalformedToolFailureDiagnosticPersistsAndReplays(t *testing.T) {
	store, workspace := delegationStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]any{"content": "unfinished answer", "reasoning_content": "unfinished thinking", "tool_calls": []any{map[string]any{"index": 0, "id": "call-1", "type": "function", "function": map[string]any{"name": "command", "arguments": `{"program":"true"`}}}}, "tool_calls")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	owner := executiondata.NewOwner()
	var mu sync.Mutex
	var live []contracts.ExecutionFact
	_, subscription, err := executiondata.CaptureAndSubscribe(owner, func(string, uint64) (struct{}, error) { return struct{}{}, nil }, func(commit contracts.ExecutionCommit) {
		mu.Lock()
		live = append(live, commit.Facts...)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	result, runErr := Run(context.Background(), store, chatFixtureProvider(server.URL, "secret", "failure-test", protocol.Options{ReasoningField: "reasoning_content"}),
		RunOptions{Task: "use command", Workspace: workspace, ModelID: "test/model", ExecutionDataOwner: owner})
	if runErr == nil || result.Result.OK {
		t.Fatalf("malformed tool succeeded: %+v %v", result, runErr)
	}
	diagnostic := result.Result.Diagnostic
	if diagnostic == nil || diagnostic.Code != contracts.FailureResponse || diagnostic.ParseReason != "invalid_tool_arguments" || diagnostic.Field == "" {
		t.Fatalf("diagnostic: %+v", diagnostic)
	}
	var liveTerminal *contracts.ExecutionFact
	mu.Lock()
	for _, fact := range live {
		if fact.Kind == "host_decision" {
			copy := fact
			liveTerminal = &copy
		}
		if fact.Kind == "tool_call" {
			t.Fatal("invalid tool was executed")
		}
	}
	mu.Unlock()
	if liveTerminal == nil || !reflect.DeepEqual(liveTerminal.Diagnostic, diagnostic) {
		t.Fatalf("live terminal: %+v", liveTerminal)
	}
	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var replayTerminal *contracts.ExecutionFact
	if err := store.WalkConversationCut(context.Background(), cut, func(fact contracts.ExecutionFact) error {
		if fact.Kind == "host_decision" {
			copy := fact
			replayTerminal = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replayTerminal == nil || !reflect.DeepEqual(replayTerminal.Diagnostic, diagnostic) || replayTerminal.Error != result.Result.Error {
		t.Fatalf("replay differs: %+v", replayTerminal)
	}
	records, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var requestFailure bool
	for _, record := range records {
		var observation contracts.Observation
		_ = json.Unmarshal(record.Payload, &observation)
		if observation.Kind == "model_request" && observation.Phase == "error" {
			requestFailure = reflect.DeepEqual(observation.Diagnostic, diagnostic) && observation.Error == runErr.Error()
		}
	}
	if !requestFailure {
		t.Fatal("model request lost its diagnostic or detailed error")
	}
}

func TestCapturedOAuthCredentialMissingAtRequestIsNotTransportFailure(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "oauth.json")
	record := map[string]any{"issuer": "https://auth.openai.com", "subject": "primary", "client_id": "test-client", "ext_agent_host_id": "test-host", "id_token": "test-id", "access_token": "test-token", "refresh_token": "test-refresh", "token_type": "Bearer", "expires_at": time.Now().Add(time.Hour), "scopes": []string{"chatgpt.tokens.use.direct"}, "saved_at": time.Now()}
	if err := os.WriteFile(path, contracts.JSON(record), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("missing credential request reached provider") }))
	defer server.Close()
	registry, err := providers.Parse(contracts.JSON(providers.Config{
		Providers:   map[string]providers.Definition{"oauth": {API: "responses", BaseURL: server.URL, Path: "/responses", Auth: providers.Auth{Kind: credentials.KindChatGPTOAuth, Profile: "key"}}},
		Credentials: map[string]credentials.Profile{"key": {OAuthFile: path}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	factory := newConnectionFactory(RunOptions{CredentialStateDir: filepath.Join(workspace, "state")})
	defer factory.close()
	selection := contracts.ModelSelection{Provider: "oauth", API: "responses", AuthProfile: "key", ModelID: "test-model"}
	provider, err := factory.provider(context.Background(), registry, selection, "session", filepath.Join(workspace, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = provider.Generate(context.Background(), contracts.ModelRequest{Model: selection}, nil)
	if diagnostic := contracts.FailureFor(err); diagnostic == nil || diagnostic.Code != contracts.FailureMissingCredential {
		t.Fatalf("request credential failure = %v; diagnostic=%+v", err, diagnostic)
	}
}
