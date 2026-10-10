package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestPrepareResponsesEncodesNamespacedToolsAndOpaqueReplay(t *testing.T) {
	provider := New(Config{
		ProviderID:    "openai-chatgpt",
		StateIdentity: "account-identity",
		API:           "responses",
		BaseURL:       "https://example.invalid/v1",
		Path:          "/responses",
		Options: Options{
			NamespaceTools:  true,
			ReasoningReplay: true,
		},
	})
	state := &contracts.ProviderState{
		Provider: "account-identity",
		Model:    "gpt-test",
		ReplayItems: []json.RawMessage{
			json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"opaque-value","custom":{"kept":true}}`),
			json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"run_js","arguments":"{\"code\":\"return 1\"}","namespace":"hjh"}`),
		},
	}
	request := contracts.ModelRequest{
		Model:       contracts.ModelSelection{Provider: "openai-chatgpt", API: "responses", ModelID: "gpt-test", Effort: "auto"},
		Instruction: "Use tools when needed.",
		Messages: []contracts.Message{
			contracts.User("inspect the project"),
			contracts.AssistantCalls([]contracts.ToolCall{{CallID: "call_1", Name: "run_js", Arguments: json.RawMessage(`{"code":"return 1"}`)}}, "", state),
			contracts.ToolResults([]contracts.ToolResult{{CallID: "call_1", Name: "run_js", Text: "1", Outcome: "success"}}),
		},
		Tools: []contracts.ToolDefinition{{
			Name:        "run_js",
			Description: "Execute JavaScript",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}}}`),
		}},
	}
	body, err := provider.Prepare(request)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if string(wire["instructions"]) != `"Use tools when needed."` || string(wire["store"]) != "false" || string(wire["stream"]) != "true" {
		t.Fatalf("request instructions/store/stream = %s / %s / %s", wire["instructions"], wire["store"], wire["stream"])
	}
	var input []json.RawMessage
	if err := json.Unmarshal(wire["input"], &input); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if len(input) != 4 || !strings.Contains(string(input[1]), `"encrypted_content":"opaque-value"`) || !strings.Contains(string(input[2]), `"call_id":"call_1"`) {
		t.Fatalf("opaque output replay was not kept in order: %s", wire["input"])
	}
	var toolResult map[string]any
	if err := json.Unmarshal(input[3], &toolResult); err != nil {
		t.Fatalf("decode function output: %v", err)
	}
	if toolResult["type"] != "function_call_output" || toolResult["namespace"] != namespaceName || toolResult["call_id"] != "call_1" || toolResult["output"] != "1" {
		t.Fatalf("function output = %#v", toolResult)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(wire["tools"], &tools); err != nil || len(tools) != 1 {
		t.Fatalf("decode namespaced tools: %v; %s", err, wire["tools"])
	}
	if string(tools[0]["type"]) != `"namespace"` || string(tools[0]["name"]) != `"hjh"` {
		t.Fatalf("tools did not use the namespace shape: %s", wire["tools"])
	}
	var nested struct {
		Tools []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(tools[0]["tools"], &nested.Tools); err != nil || len(nested.Tools) != 1 || string(nested.Tools[0]["type"]) != `"function"` {
		t.Fatalf("nested namespace functions = %s; err=%v", tools[0]["tools"], err)
	}
}

func TestPrepareResponsesOmitsOpaqueReplayForOtherStateIdentity(t *testing.T) {
	provider := New(Config{ProviderID: "openai-chatgpt", StateIdentity: "current-account", API: "responses", BaseURL: "https://example.invalid/v1", Path: "/responses", Options: Options{ReasoningReplay: true}})
	message := contracts.Assistant("old answer", &contracts.ProviderState{
		Provider:    "different-account",
		Model:       "gpt-test",
		ReplayItems: []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"other-account-state"}`)},
	})
	body, err := provider.Prepare(contracts.ModelRequest{
		Model:    contracts.ModelSelection{ModelID: "gpt-test"},
		Messages: []contracts.Message{message},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	var wire struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if len(wire.Input) != 1 || wire.Input[0]["content"] != "old answer" {
		t.Fatalf("expected common message projection, got %#v", wire.Input)
	}
	if strings.Contains(string(body), "other-account-state") {
		t.Fatalf("replayed opaque state from a different identity: %s", body)
	}
}

func TestPrepareChatCompletionsUsesCommonMessagesAndConfiguredReasoningField(t *testing.T) {
	provider := New(Config{
		ProviderID:    "opencode-go-chat",
		StateIdentity: "go-key-identity",
		API:           "chat-completions",
		BaseURL:       "https://example.invalid/v1",
		Path:          "/chat/completions",
		Options:       Options{ReasoningReplay: true, ReasoningField: "reasoning_content"},
	})
	state := &contracts.ProviderState{
		Provider:  "go-key-identity",
		Model:     "deepseek-test",
		Reasoning: &contracts.Reasoning{Field: "reasoning_content", Text: "opaque reasoning"},
	}
	body, err := provider.Prepare(contracts.ModelRequest{
		Model:       contracts.ModelSelection{ModelID: "deepseek-test", Effort: "high"},
		Instruction: "Follow the request.",
		Messages: []contracts.Message{
			contracts.User("read this"),
			contracts.AssistantCalls([]contracts.ToolCall{{CallID: "call_a", Name: "run_js", Arguments: json.RawMessage(`{"code":"1"}`)}}, "working", state),
			contracts.ToolResults([]contracts.ToolResult{{CallID: "call_a", Name: "run_js", Text: "1", Outcome: "success"}}),
		},
		Tools: []contracts.ToolDefinition{{Name: "run_js", Description: "Run code", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	var wire struct {
		Model         string                       `json:"model"`
		Messages      []map[string]json.RawMessage `json:"messages"`
		Tools         []map[string]json.RawMessage `json:"tools"`
		Stream        bool                         `json:"stream"`
		StreamOptions map[string]bool              `json:"stream_options"`
		Reasoning     string                       `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if wire.Model != "deepseek-test" || !wire.Stream || !wire.StreamOptions["include_usage"] || wire.Reasoning != "high" {
		t.Fatalf("request header fields = %#v", wire)
	}
	if len(wire.Messages) != 4 || string(wire.Messages[0]["role"]) != `"system"` || string(wire.Messages[2]["reasoning_content"]) != `"opaque reasoning"` {
		t.Fatalf("common messages were not encoded: %s", body)
	}
	if string(wire.Messages[2]["tool_calls"]) == "" || string(wire.Messages[3]["tool_call_id"]) != `"call_a"` || len(wire.Tools) != 1 {
		t.Fatalf("tool exchange was not encoded: %s", body)
	}
}

func TestGenerateResponsesUsesPreparedBytesAndReplaysCompletedOutput(t *testing.T) {
	const apiKey = "protocol-test-secret"
	const sessionID = "root-session-id"
	var receivedBody []byte
	var receivedAuth, receivedSession string
	var receivedTenant string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %q", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		receivedBody = body
		receivedAuth = r.Header.Get("Authorization")
		receivedSession = r.Header.Get("X-OpenCode-Session")
		receivedTenant = r.Header.Get("X-Tenant")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"event: response.created\n"+
				"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n"+
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Checking the project.\"}\n\n"+
				"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":2,\"item_id\":\"fc_item\",\"delta\":\"{\\\"code\\\":\\\"return 1\\\"}\"}\n\n"+
				"data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":2,\"item_id\":\"fc_item\",\"arguments\":\"{\\\"code\\\":\\\"return 1\\\"}\",\"item\":{\"type\":\"function_call\",\"id\":\"fc_item\",\"call_id\":\"call_1\",\"name\":\"run_js\",\"namespace\":\"hjh\",\"arguments\":\"{\\\"code\\\":\\\"return 1\\\"}\"}}\n\n"+
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-test\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4,\"total_tokens\":7},\"output\":[{\"type\":\"reasoning\",\"id\":\"rs_1\",\"encrypted_content\":\"opaque-response-state\",\"future\":{\"kept\":true}},{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Checking the project.\"}]},{\"type\":\"function_call\",\"id\":\"fc_item\",\"call_id\":\"call_1\",\"name\":\"run_js\",\"namespace\":\"hjh\",\"arguments\":\"{\\\"code\\\":\\\"return 1\\\"}\",\"status\":\"completed\"}]}}\n\n")
	}))
	defer server.Close()

	provider := New(Config{
		ProviderID:    "openai-chatgpt",
		StateIdentity: "account-identity",
		API:           "responses",
		BaseURL:       server.URL + "/v1",
		Path:          "/responses",
		Headers:       map[string]string{"X-Tenant": "team-1"},
		APIKey:        apiKey,
		SessionID:     sessionID,
		SessionHeader: "X-OpenCode-Session",
		Options:       Options{NamespaceTools: true, ReasoningReplay: true},
	})
	request := contracts.ModelRequest{
		Model:       contracts.ModelSelection{Provider: "openai-chatgpt", API: "responses", ModelID: "gpt-test"},
		Instruction: "Inspect the project.",
		Messages:    []contracts.Message{contracts.User("check the code")},
		Tools:       []contracts.ToolDefinition{{Name: "run_js", Description: "Execute JavaScript", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	}
	prepared, err := provider.Prepare(request)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	request.WireBody = prepared
	var observations []contracts.Observation
	result, err := provider.Generate(context.Background(), request, func(observation contracts.Observation) error {
		observations = append(observations, observation)
		return nil
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !reflect.DeepEqual(receivedBody, prepared) {
		t.Fatalf("sent body differs from Prepare bytes\nsent: %s\nprepared: %s", receivedBody, prepared)
	}
	if receivedAuth != "Bearer "+apiKey || receivedSession != sessionID || receivedTenant != "team-1" {
		t.Fatalf("transport headers: auth=%q session=%q tenant=%q", receivedAuth, receivedSession, receivedTenant)
	}
	if result.Text != "Checking the project." || result.FinishReason != "completed" {
		t.Fatalf("result text/reason = %q / %q", result.Text, result.FinishReason)
	}
	if len(result.Calls) != 1 || result.Calls[0].CallID != "call_1" || result.Calls[0].Name != "run_js" || string(result.Calls[0].Arguments) != `{"code":"return 1"}` {
		t.Fatalf("tool calls = %#v", result.Calls)
	}
	if string(result.Usage) != `{"input_tokens":3,"output_tokens":4,"total_tokens":7}` {
		t.Fatalf("usage = %s", result.Usage)
	}
	if result.ProviderState == nil || result.ProviderState.Provider != "account-identity" || result.ProviderState.Model != "gpt-test" || len(result.ProviderState.ReplayItems) != 3 {
		t.Fatalf("provider state = %#v", result.ProviderState)
	}
	if !strings.Contains(string(result.ProviderState.ReplayItems[0]), `"encrypted_content":"opaque-response-state"`) || !strings.Contains(string(result.ProviderState.ReplayItems[0]), `"future":{"kept":true}`) {
		t.Fatalf("opaque output item was changed: %s", result.ProviderState.ReplayItems[0])
	}
	if len(observations) == 0 || !reflect.DeepEqual([]byte(observations[0].Input), prepared) {
		t.Fatalf("observed request differs from prepared bytes: %#v", observations)
	}

	followup, err := provider.Prepare(contracts.ModelRequest{
		Model: contracts.ModelSelection{ModelID: "gpt-test"},
		Messages: []contracts.Message{
			contracts.AssistantCalls(result.Calls, result.Text, result.ProviderState),
			contracts.ToolResults([]contracts.ToolResult{{CallID: "call_1", Name: "run_js", Text: "1", Outcome: "success"}}),
		},
	})
	if err != nil {
		t.Fatalf("Prepare follow-up: %v", err)
	}
	var replay struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(followup, &replay); err != nil {
		t.Fatalf("decode follow-up: %v", err)
	}
	if len(replay.Input) != 4 || !strings.Contains(string(replay.Input[0]), "opaque-response-state") || !strings.Contains(string(replay.Input[1]), `"type":"message"`) || !strings.Contains(string(replay.Input[2]), `"call_id":"call_1"`) || !strings.Contains(string(replay.Input[3]), `"type":"function_call_output"`) {
		t.Fatalf("follow-up did not replay response output and tool result: %s", followup)
	}
}

func TestGenerateChatCompletionsAssemblesTextToolCallsAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"data: {\"id\":\"chat_1\",\"model\":\"deepseek-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Reading\"}}]}\n\n"+
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_chat\",\"type\":\"function\",\"function\":{\"name\":\"run_\",\"arguments\":\"{\\\"code\\\":\"}}]}}]}\n\n"+
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"js\",\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"+
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer server.Close()
	provider := New(Config{ProviderID: "opencode-go-chat", StateIdentity: "go-key", API: "chat-completions", BaseURL: server.URL, Path: "/chat/completions"})
	result, err := provider.Generate(context.Background(), contracts.ModelRequest{
		Model:    contracts.ModelSelection{ModelID: "deepseek-test"},
		Messages: []contracts.Message{contracts.User("inspect")},
	}, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Text != "Reading" || result.FinishReason != "tool_calls" || len(result.Calls) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.Calls[0].CallID != "call_chat" || result.Calls[0].Name != "run_js" || string(result.Calls[0].Arguments) != `{"code":1}` {
		t.Fatalf("tool call = %#v", result.Calls[0])
	}
	if string(result.Usage) != `{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}` {
		t.Fatalf("usage = %s", result.Usage)
	}
}

func TestGenerateResponsesRequiresCompletedTerminalEvent(t *testing.T) {
	testCases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "failed",
			body: "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"model failed\"}}}\n\n",
			want: "server_error: model failed",
		},
		{
			name: "incomplete",
			body: "data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n",
			want: "max_output_tokens",
		},
		{
			name: "stream ended before terminal event",
			body: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n",
			want: "before response.completed",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, testCase.body)
			}))
			defer server.Close()
			provider := New(Config{ProviderID: "openai-responses", API: "responses", BaseURL: server.URL, Path: "/responses"})
			_, err := provider.Generate(context.Background(), contracts.ModelRequest{Model: contracts.ModelSelection{ModelID: "test"}}, nil)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Generate error = %v, want containing %q", err, testCase.want)
			}
		})
	}
}

func TestGenerateRedactsAPIKeyFromProviderError(t *testing.T) {
	const apiKey = "never-store-this"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"rejected %s"}}`, apiKey)
	}))
	defer server.Close()
	provider := New(Config{ProviderID: "openai-responses", API: "responses", BaseURL: server.URL, Path: "/responses", APIKey: apiKey})
	var observations []contracts.Observation
	_, err := provider.Generate(context.Background(), contracts.ModelRequest{Model: contracts.ModelSelection{ModelID: "test"}}, func(observation contracts.Observation) error {
		observations = append(observations, observation)
		return nil
	})
	if err == nil || strings.Contains(err.Error(), apiKey) {
		t.Fatalf("Generate error did not redact API key: %v", err)
	}
	for _, observation := range observations {
		if strings.Contains(observation.Error, apiKey) || strings.Contains(string(observation.Input), apiKey) {
			t.Fatalf("credential entered observation: %#v", observation)
		}
	}
}
