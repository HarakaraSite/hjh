package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"hjh/internal/agent"
	"hjh/internal/contracts"
)

func TestBearerCredentialIsNotForwardedAcrossRedirectOrigin(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			const apiKey = "redirect-secret"
			targetAuthorization := make(chan string, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetAuthorization <- r.Header.Get("Authorization")
				writeProtocolChatCompletion(w, "redirected")
			}))
			defer target.Close()
			targetURL, err := url.Parse(target.URL)
			if err != nil {
				t.Fatal(err)
			}

			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
					t.Errorf("source Authorization = %q", got)
				}
				http.Redirect(w, r, target.URL+"/chat/completions", status)
			}))
			defer source.Close()
			sourceURL, err := url.Parse(source.URL)
			if err != nil {
				t.Fatal(err)
			}
			if sourceURL.Hostname() != targetURL.Hostname() || sourceURL.Port() == targetURL.Port() {
				t.Fatalf("test servers do not share a hostname across ports: source=%s target=%s", source.URL, target.URL)
			}

			provider := New(Config{
				ProviderID: "redirect-test", API: "chat-completions", BaseURL: source.URL,
				Path: "/chat/completions", APIKey: apiKey,
			})
			result, err := provider.Generate(context.Background(), contracts.ModelRequest{
				Model:    contracts.ModelSelection{Provider: "redirect-test", API: "chat-completions", ModelID: "test"},
				Messages: []contracts.Message{contracts.User("redirect")},
			}, nil)
			if err != nil || result.Text != "redirected" {
				t.Fatalf("Generate result=%+v err=%v", result, err)
			}
			if got := <-targetAuthorization; got != "" {
				t.Fatalf("redirect target received Authorization: %q", got)
			}
		})
	}
}

func TestResolveTokenRefreshesOnEachRequestInOneAgentRun(t *testing.T) {
	var resolveCalls atomic.Int32
	var requestCalls atomic.Int32
	authorizations := make(chan string, 2)
	secondRequestBody := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCalls.Add(1)
		authorizations <- r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		if n == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w,
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_refresh\",\"type\":\"function\",\"function\":{\"name\":\"test_tool\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"+
					"data: [DONE]\n\n")
			return
		}
		secondRequestBody <- body
		writeProtocolChatCompletion(w, "run completed")
	}))
	defer server.Close()

	provider := New(Config{
		ProviderID: "rotating-credential-test", API: "chat-completions", BaseURL: server.URL,
		Path: "/chat/completions",
		ResolveToken: func(context.Context) (string, error) {
			return fmt.Sprintf("jwt-token-%d", resolveCalls.Add(1)), nil
		},
	})
	var observations []contracts.Observation
	result, err := agent.Run(context.Background(), agent.Input{
		Task:  "use the tool and finish",
		Model: contracts.ModelSelection{Provider: "rotating-credential-test", API: "chat-completions", ModelID: "test"},
		Tools: []contracts.ToolDefinition{{Name: "test_tool", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}, provider, rotatingCredentialTestExecutor{}, func(contracts.Message) error { return nil }, func(o contracts.Observation) error {
		observations = append(observations, o)
		return nil
	}, nil)
	if err != nil || !result.OK || result.FinalText != "run completed" {
		t.Fatalf("agent run result=%+v err=%v", result, err)
	}
	if got := resolveCalls.Load(); got != 2 {
		t.Fatalf("credential resolver called %d times, want once for each of two requests", got)
	}
	if got := requestCalls.Load(); got != 2 {
		t.Fatalf("HTTP request count = %d, want 2", got)
	}
	if first, second := <-authorizations, <-authorizations; first != "Bearer jwt-token-1" || second != "Bearer jwt-token-2" {
		t.Fatalf("Authorization headers = %q, %q", first, second)
	}
	if body := string(<-secondRequestBody); !strings.Contains(body, `"tool_call_id":"call_refresh"`) || !strings.Contains(body, "tool result") {
		t.Fatalf("second request omitted tool continuation: %s", body)
	}
	for _, observation := range observations {
		encoded, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "jwt-token-") || strings.Contains(string(encoded), "Authorization") {
			t.Fatalf("credential entered provider observation: %s", encoded)
		}
	}
}

func TestGenerateRedactsResolvedTokenFromProviderError(t *testing.T) {
	const (
		initialToken = "eyJhbGciOiJub25lIn0.initial-access-token"
		updatedToken = "eyJhbGciOiJub25lIn0.updated-access-token"
	)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		if strings.Contains(string(body), initialToken) || strings.Contains(string(body), updatedToken) {
			t.Errorf("credential entered request body: %s", body)
		}
		if requests.Add(1) == 1 {
			writeProtocolChatCompletion(w, "first request succeeded")
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"rejected %s"}}`, updatedToken)
	}))
	defer server.Close()

	provider := New(Config{
		ProviderID: "oauth-credential-test", API: "chat-completions", BaseURL: server.URL,
		Path: "/chat/completions", APIKey: initialToken,
		ResolveToken: func(context.Context) (string, error) {
			if requests.Load() == 0 {
				return initialToken, nil
			}
			return updatedToken, nil
		},
	})
	var observations []contracts.Observation
	request := contracts.ModelRequest{Model: contracts.ModelSelection{ModelID: "test"}, Messages: []contracts.Message{contracts.User("test")}}
	first, err := provider.Generate(context.Background(), request, func(o contracts.Observation) error {
		observations = append(observations, o)
		return nil
	})
	if err != nil || first.Text != "first request succeeded" {
		t.Fatalf("first authenticated request result=%+v err=%v", first, err)
	}
	_, err = provider.Generate(context.Background(), request, func(o contracts.Observation) error {
		observations = append(observations, o)
		return nil
	})
	if err == nil || strings.Contains(err.Error(), initialToken) || strings.Contains(err.Error(), updatedToken) {
		t.Fatalf("resolved token was not redacted from provider error: %v", err)
	}
	for _, observation := range observations {
		encoded, err := json.Marshal(observation)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), initialToken) || strings.Contains(string(encoded), updatedToken) {
			t.Fatalf("resolved token entered provider observation: %#v", observation)
		}
	}
}

type rotatingCredentialTestExecutor struct{}

func (rotatingCredentialTestExecutor) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`"tool result"`), nil
}

func writeProtocolChatCompletion(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(map[string]any{
		"id": "chat_test", "model": "test",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}, "finish_reason": "stop"}},
	}))
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
}
