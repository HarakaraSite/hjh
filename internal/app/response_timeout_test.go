package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/extensions"
	"hjh/internal/providers"
)

func TestResponseTimeoutRetriesPreparedParentRequestWithoutRepeatingCompletedTool(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("tool result"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var requests [][]byte
	var started []time.Time
	cancelled := make(chan time.Time, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		started = append(started, time.Now())
		call := len(requests)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		switch call {
		case 1:
			sendChunk(w, map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "completed-call", "function": map[string]any{"name": "read_file", "arguments": `{"path":"input.txt"}`}}}}, "tool_calls")
		case 2:
			sendChunk(w, map[string]any{"content": "FAILED_ATTEMPT_BODY", "reasoning_content": "FAILED_ATTEMPT_THINKING", "tool_calls": []any{map[string]any{"index": 0, "id": "bad-call", "function": map[string]any{"name": "read_file", "arguments": `{"path":"input.txt"`}}}}, nil)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			cancelled <- time.Now()
			return
		case 3:
			sendChunk(w, map[string]any{"content": "REGENERATED_NOTE", "reasoning_content": "REGENERATED_THINKING", "tool_calls": []any{map[string]any{"index": 0, "id": "good-call", "function": map[string]any{"name": "read_file", "arguments": `{"path":"input.txt"}`}}}}, "tool_calls")
		default:
			sendChunk(w, map[string]any{"content": "RETRY_FINISHED"}, "stop")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	configPath, modelID := responseTimeoutLocalConfig(t, workspace, server.URL, map[string]any{"startSeconds": 0.5, "idleSeconds": 0.1})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := Run(ctx, store, nil, RunOptions{Task: "read input", Workspace: workspace, ConfigPath: configPath, ProviderID: "opencode-go-chat", ModelID: modelID})
	if err != nil || !result.Result.OK || result.Result.FinalText != "RETRY_FINISHED" {
		t.Fatalf("retry failed: %+v %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 4 {
		t.Fatalf("model requests = %d, want initial tool + timeout + retry tool + final", len(requests))
	}
	if !bytes.Equal(requests[1], requests[2]) || started[2].Sub(started[1]) < 2*time.Second {
		t.Fatalf("request retry changed input or timing: calls=%d gap=%s", len(requests), started[2].Sub(started[1]))
	}
	if bytes.Contains(requests[3], []byte("FAILED_ATTEMPT")) || !bytes.Contains(requests[3], []byte("good-call")) || !bytes.Contains(requests[3], []byte("tool result")) {
		t.Fatalf("model continuation included failed response or lost successful tool: %s", requests[3])
	}
	select {
	case stopped := <-cancelled:
		if !stopped.Before(started[2]) {
			t.Error("retry began before old server request observed cancellation")
		}
	default:
		t.Fatal("old server request was not cancelled")
	}
	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var failedKey, successKey string
	var failedThinking, goodThinking, notice bool
	toolCalls := map[string]int{}
	if err := store.WalkConversationCut(context.Background(), cut, func(f contracts.ExecutionFact) error {
		switch {
		case f.Text == "FAILED_ATTEMPT_BODY":
			if f.Phase != "superseded" {
				t.Errorf("failed attempt phase = %s", f.Phase)
			}
			failedKey = f.Conversation.LocalKey
		case f.Text == "FAILED_ATTEMPT_THINKING":
			failedThinking = f.Phase == "superseded"
		case f.Text == "REGENERATED_NOTE":
			successKey = f.Conversation.LocalKey
			if f.Request.RetryAttempt != 1 {
				t.Errorf("retry identity = %+v", f.Request)
			}
		case f.Text == "REGENERATED_THINKING":
			goodThinking = f.Request.RetryAttempt == 1 && strings.HasPrefix(f.Locator.Lane, "thinking/") && !strings.Contains(f.Locator.Lane, "retry/1")
		case f.Conversation.EntityKind == "notice" && strings.HasPrefix(f.Text, "RETRYING"):
			notice = true
		case f.Kind == "tool_call" && f.Name == "read_file":
			toolCalls[f.CallID]++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if failedKey == "" || successKey == "" || failedKey == successKey || !failedThinking || !goodThinking || !notice || toolCalls["completed-call"] != 1 || toolCalls["good-call"] != 1 || len(toolCalls) != 2 {
		t.Fatalf("attempt display/history not separated: failed=%q good=%q thinking=%t/%t notice=%t tools=%v", failedKey, successKey, failedThinking, goodThinking, notice, toolCalls)
	}
	messages, err := store.Conversation(context.Background(), result.SessionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 {
		t.Fatal("successful retry did not adopt its conversation")
	}
	for _, message := range messages {
		if bytes.Contains(contracts.JSON(message), []byte("FAILED_ATTEMPT")) {
			t.Fatal("failed response entered canonical conversation")
		}
	}
}

// Use the shipped Chat adapter and the actual captured JSON connection path.
func responseTimeoutLocalConfig(t *testing.T, workspace, endpoint string, timeout map[string]any) (string, string) {
	t.Helper()
	path, model := standardLocalProviderConfig(t, workspace, "opencode-go-chat", endpoint)
	updateResponseTimeoutConfig(t, path, func(cfg map[string]any) {
		cfg["providers"].(map[string]any)["opencode-go-chat"].(map[string]any)["responseTimeout"] = timeout
	})
	return path, model
}

func updateResponseTimeoutConfig(t *testing.T, path string, update func(map[string]any)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	update(cfg)
	if err := os.WriteFile(path, contracts.JSON(cfg), 0600); err != nil {
		t.Fatal(err)
	}
}

func timeoutCapturedConnection(t *testing.T, path string) (contracts.Provider, contracts.ModelSelection) {
	t.Helper()
	snapshot, err := extensions.Capture(path)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := capturedRegistry(snapshot, path)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := registry.Resolve(contracts.ModelSelection{}, contracts.ModelSelection{Provider: "opencode-go-chat"})
	if err != nil {
		t.Fatal(err)
	}
	factory := newConnectionFactory(RunOptions{CredentialStateDir: t.TempDir()})
	t.Cleanup(func() { _ = factory.close() })
	provider, err := factory.provider(context.Background(), registry, selection, "timeout-session", path)
	if err != nil {
		t.Fatal(err)
	}
	return provider, selection
}

func TestResponseTimeoutCapturedSettingsAndSingleHelperGeneration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout map[string]any
		want    string
		stall   bool
	}{
		{name: "omitted", timeout: nil},
		{name: "override", timeout: map[string]any{"startSeconds": 0.05}, want: contracts.FailureProviderTimeout, stall: true},
		{name: "zero-start", timeout: map[string]any{"startSeconds": 0, "idleSeconds": 0.01}},
		{name: "zero-idle", timeout: map[string]any{"startSeconds": 0.03, "idleSeconds": 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				calls.Add(1)
				if tc.stall {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.name == "zero-idle" {
					fmt.Fprint(w, ": heartbeat\n\n")
					w.(http.Flusher).Flush()
				}
				time.Sleep(100 * time.Millisecond)
				sendChunk(w, map[string]any{"content": "helper completed"}, "stop")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			path, _ := responseTimeoutLocalConfig(t, t.TempDir(), server.URL, tc.timeout)
			provider, selection := timeoutCapturedConnection(t, path)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result, err := requestModelGenerator(provider, selection)(ctx, contextModelInput{Instruction: "helper"})
			if tc.want == "" {
				if err != nil || result.Text != "helper completed" {
					t.Fatalf("result=%+v error=%v", result, err)
				}
			} else if d := contracts.FailureFor(err); d == nil || d.Code != tc.want {
				t.Fatalf("diagnostic=%+v error=%v", d, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("helper performed %d requests", calls.Load())
			}
		})
	}
}

func TestResponseTimeoutConfigurationErrorNamesProvider(t *testing.T) {
	for _, field := range []string{"startSeconds", "idleSeconds"} {
		t.Run(field, func(t *testing.T) {
			registry, err := providers.Parse(contracts.JSON(map[string]any{"providers": map[string]any{"bad-wait": map[string]any{"auth": map[string]string{"kind": "none"}, "responseTimeout": map[string]float64{field: -1}}}}))
			if err != nil {
				t.Fatal(err)
			}
			factory := newConnectionFactory(RunOptions{CredentialStateDir: t.TempDir()})
			defer factory.close()
			_, err = factory.provider(context.Background(), registry, contracts.ModelSelection{Provider: "bad-wait"}, "session", "config.json")
			if err == nil || !strings.Contains(err.Error(), `provider "bad-wait"`) || !strings.Contains(err.Error(), field) {
				t.Fatalf("configuration explanation=%v", err)
			}
		})
	}
}

func TestResponseTimeoutSharesRetryBudgetWithHTTPFailure(t *testing.T) {
	store, workspace := delegationStore(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			fmt.Fprint(w, "busy")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		sendChunk(w, map[string]any{"content": "LAST_TIMEOUT_BODY", "reasoning_content": "LAST_TIMEOUT_THINKING"}, nil)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	path, model := responseTimeoutLocalConfig(t, workspace, server.URL, map[string]any{"idleSeconds": 0.05})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, store, nil, RunOptions{Workspace: workspace, ConfigPath: path, ProviderID: "opencode-go-chat", ModelID: model, Task: "mixed failure"})
	if err == nil || calls.Load() != 2 || result.Result.Diagnostic == nil || result.Result.Diagnostic.Code != contracts.FailureProviderTimeout || result.Result.Diagnostic.RetryAttempt != 1 {
		t.Fatalf("result=%+v calls=%d error=%v", result, calls.Load(), err)
	}
	cut, err := store.CaptureConversationCut(ctx, result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	partial := map[string]bool{}
	if err := store.WalkConversationCut(ctx, cut, func(f contracts.ExecutionFact) error {
		if strings.HasPrefix(f.Text, "LAST_TIMEOUT_") {
			partial[f.Text] = f.Phase == "incomplete" && f.Request.RetryAttempt == 1
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !partial["LAST_TIMEOUT_BODY"] || !partial["LAST_TIMEOUT_THINKING"] {
		t.Fatalf("last attempt not retained incomplete: %v", partial)
	}
}

func TestResponseTimeoutChildExhaustionReturnsThroughSpawnCollectToParent(t *testing.T) {
	store, workspace := delegationStore(t)
	var childCalls, parentCalls atomic.Int32
	var childResult atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &request)
		w.Header().Set("Content-Type", "text/event-stream")
		if request.Model == "timeout-child" {
			childCalls.Add(1)
			sendChunk(w, map[string]any{"content": "CHILD_PARTIAL", "reasoning_content": "CHILD_THINKING"}, nil)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if parentCalls.Add(1) == 1 {
			sendTool(w, "spawn-collect", `const child=await tools.spawn_agent({task:"timeout child",model:{modelId:"timeout-child"}}); return await tools.collect_agent({executionId:child.executionId});`)
			return
		}
		childResult.Store(bytes.Contains(body, []byte(`failed`)) && bytes.Contains(body, []byte(`response read idle timeout`)))
		sendChunk(w, map[string]any{"content": "PARENT_COLLECTED_FAILURE"}, "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	path, model := responseTimeoutLocalConfig(t, workspace, server.URL, map[string]any{"startSeconds": 0.5, "idleSeconds": 0.05})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := Run(ctx, store, nil, RunOptions{Workspace: workspace, ConfigPath: path, ProviderID: "opencode-go-chat", ModelID: model, Task: "collect child"})
	if err != nil || !result.Result.OK || result.Result.FinalText != "PARENT_COLLECTED_FAILURE" || !childResult.Load() || childCalls.Load() != 2 || parentCalls.Load() != 2 {
		t.Fatalf("parent=%+v child calls=%d parent calls=%d collected=%t error=%v", result, childCalls.Load(), parentCalls.Load(), childResult.Load(), err)
	}
	children, err := store.Children(ctx, result.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children=%+v error=%v", children, err)
	}
	if child := children[0]; child.Lifecycle != "settled" || child.Outcome != "failed" || child.Adoption != "non_canonical" {
		t.Fatalf("child not settled failed: %+v", child)
	}
	cut, err := store.CaptureConversationCut(ctx, result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	phases := map[string]int{}
	if err := store.WalkConversationCut(ctx, cut, func(f contracts.ExecutionFact) error {
		if f.ExecutionID == children[0].ID && (f.Text == "CHILD_PARTIAL" || f.Text == "CHILD_THINKING") {
			phases[f.Phase]++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if phases["superseded"] != 2 || phases["incomplete"] != 2 {
		t.Fatalf("child attempt evidence=%v", phases)
	}
}

// Redirect the credential manager's canonical discovery request to local HTTP;
// all token refreshes and inference requests still use real net/http transports.
type timeoutDiscoveryTransport struct{ local *url.URL }

func (transport timeoutDiscoveryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	if copy.URL.Host == "auth.openai.com" {
		copy.URL.Scheme = transport.local.Scheme
		copy.URL.Host = transport.local.Host
	}
	return http.DefaultTransport.RoundTrip(copy)
}

func TestResponseTimeoutActualOAuthWrapperClassification(t *testing.T) {
	for _, mode := range []string{"own-timeout-retries", "refresh-body-timeout", "discovery-body-timeout", "outer-cancel", "actual-auth-failure"} {
		t.Run(mode, func(t *testing.T) {
			store, workspace := delegationStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			var discoveryCalls, refreshCalls, modelCalls atomic.Int32
			var localURL string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch r.URL.Path {
				case "/.well-known/openid-configuration":
					call := discoveryCalls.Add(1)
					if mode == "discovery-body-timeout" && call == 1 {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"issuer":`)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]string{"issuer": "https://auth.openai.com", "authorization_endpoint": localURL + "/authorize", "token_endpoint": localURL + "/token", "jwks_uri": localURL + "/jwks"})
				case "/token":
					call := refreshCalls.Add(1)
					if mode == "actual-auth-failure" {
						w.WriteHeader(401)
						return
					}
					if call == 1 && mode != "discovery-body-timeout" {
						if mode == "refresh-body-timeout" {
							w.Header().Set("Content-Type", "application/json")
							fmt.Fprint(w, `{"access_token":`)
							w.(http.Flusher).Flush()
						}
						if mode == "outer-cancel" {
							cancel()
						}
						<-r.Context().Done()
						return
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"access_token":"local-refreshed","refresh_token":"local-rotated","expires_in":3600,"token_type":"Bearer"}`)
				default:
					modelCalls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					sendChunk(w, map[string]any{"content": "OAUTH_RECOVERED"}, "stop")
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			localURL = server.URL
			defer server.Close()
			path, model := responseTimeoutLocalConfig(t, workspace, server.URL, map[string]any{"startSeconds": 0.1, "idleSeconds": 0.1})
			oauthFile := filepath.Join(workspace, "local-oauth.json")
			writeRecord := func(expires time.Time) {
				t.Helper()
				record := map[string]any{"issuer": "https://auth.openai.com", "subject": "local-subject", "client_id": "local-client", "access_token": "local-token", "refresh_token": "local-refresh", "expires_at": expires, "scopes": []string{"chatgpt.tokens.use.direct"}}
				if err := os.WriteFile(oauthFile, contracts.JSON(record), 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeRecord(time.Now().Add(time.Hour))
			updateResponseTimeoutConfig(t, path, func(cfg map[string]any) {
				cfg["credentials"] = map[string]any{"local-oauth": map[string]string{"oauthFile": oauthFile}}
				cfg["providers"].(map[string]any)["opencode-go-chat"].(map[string]any)["auth"] = map[string]string{"kind": credentials.KindChatGPTOAuth, "profile": "local-oauth"}
			})
			snapshot, err := extensions.Capture(path)
			if err != nil {
				t.Fatal(err)
			}
			registry, err := capturedRegistry(snapshot, path)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := registry.Resolve(contracts.ModelSelection{}, contracts.ModelSelection{Provider: "opencode-go-chat", ModelID: model})
			if err != nil {
				t.Fatal(err)
			}
			local, _ := url.Parse(server.URL)
			factory := newConnectionFactory(RunOptions{HTTPClient: &http.Client{Transport: timeoutDiscoveryTransport{local: local}}, CredentialStateDir: t.TempDir()})
			defer factory.close()
			provider, err := factory.provider(ctx, registry, selection, "local-session", path)
			if err != nil {
				t.Fatal(err)
			}
			expired := false
			var firstFailure *contracts.FailureDiagnostic
			result, err := Run(ctx, store, provider, RunOptions{Workspace: workspace, ConfigPath: path, ProviderID: "opencode-go-chat", ModelID: model, Task: "oauth wait", Progress: func(o contracts.Observation) error {
				if firstFailure == nil && o.Kind == "model_request" && o.Diagnostic != nil {
					firstFailure = contracts.CloneFailure(o.Diagnostic)
				}
				if !expired && o.Kind == "model_request" && o.Phase == "start" {
					writeRecord(time.Now().Add(-time.Hour))
					expired = true
				}
				return nil
			}})
			if mode == "own-timeout-retries" || mode == "refresh-body-timeout" || mode == "discovery-body-timeout" {
				if firstFailure == nil || firstFailure.Code != contracts.FailureProviderTimeout {
					t.Fatalf("OAuth wait diagnostic = %+v, want provider_timeout", firstFailure)
				}
				wantRefresh := int32(2)
				if mode == "discovery-body-timeout" {
					wantRefresh = 1
				}
				if err != nil || result.Result.FinalText != "OAUTH_RECOVERED" || refreshCalls.Load() != wantRefresh || modelCalls.Load() != 1 {
					t.Fatalf("own clock classification result=%+v refresh=%d inference=%d error=%v", result, refreshCalls.Load(), modelCalls.Load(), err)
				}
			} else {
				want := contracts.FailureMissingCredential
				if mode == "outer-cancel" {
					want = contracts.FailureCancelled
				}
				if err == nil || result.Result.Diagnostic == nil || result.Result.Diagnostic.Code != want || refreshCalls.Load() != 1 || modelCalls.Load() != 0 {
					t.Fatalf("classification want=%s result=%+v refresh=%d inference=%d error=%v", want, result, refreshCalls.Load(), modelCalls.Load(), err)
				}
			}
		})
	}
}
