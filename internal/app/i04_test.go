package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

type i04WireCapture struct {
	api  string
	body []byte
}

func TestFactorySwitchesResponsesRootAndChatChildAndAdoptsOnlySuccessfulRoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var captureMu sync.Mutex
	var captures []i04WireCapture
	appendCapture := func(api string, body []byte) {
		captureMu.Lock()
		captures = append(captures, i04WireCapture{api: api, body: append([]byte(nil), body...)})
		captureMu.Unlock()
	}

	responsesCalls := 0
	responses := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			t.Errorf("Responses request = %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read Responses request: %v", err)
			return
		}
		appendCapture("responses", body)
		var request struct {
			Model string `json:"model"`
			Input []struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Namespace string `json:"namespace"`
				Output    string `json:"output"`
			} `json:"input"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode Responses request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch request.Model {
		case "root-responses-v1":
			responsesCalls++
			if responsesCalls == 1 {
				if len(request.Input) != 1 || !strings.Contains(string(body), "root task: ask reviewer") {
					t.Errorf("initial Responses input = %+v", request.Input)
				}
				code := `const child=await api.agents.run({role:"reviewer",task:"Review the configured connection and report a concise finding."}); return {executionId:child.executionId,outcome:child.outcome,finalText:child.finalText};`
				arguments, _ := json.Marshal(map[string]string{"code": code})
				writeI04SSE(w, map[string]any{
					"type": "response.completed",
					"response": map[string]any{
						"id": "resp_root_tool", "model": request.Model, "status": "completed",
						"usage":  map[string]int{"input_tokens": 11, "output_tokens": 7, "total_tokens": 18},
						"output": []any{map[string]any{"type": "function_call", "id": "fc_root_run_js", "call_id": "call_root_run_js", "namespace": "hjh", "name": "run_js", "arguments": string(arguments), "status": "completed"}},
					},
				})
				return
			}
			if responsesCalls != 2 {
				t.Errorf("unexpected root Responses call %d", responsesCalls)
			}
			var usedChildResult bool
			for _, item := range request.Input {
				if item.Type == "function_call_output" && item.CallID == "call_root_run_js" && item.Namespace == "hjh" && strings.Contains(item.Output, "reviewer found the configured connection") {
					usedChildResult = true
				}
			}
			if !usedChildResult {
				t.Errorf("parent's next request did not include the child result: %s", body)
			}
			writeI04SSE(w, map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp_root_final", "model": request.Model, "status": "completed",
					"usage":  map[string]int{"input_tokens": 20, "output_tokens": 8, "total_tokens": 28},
					"output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "The reviewer found the configured connection; I used that result."}}}},
				},
			})
		case "failed-responses-v3":
			writeI04SSE(w, map[string]any{
				"type": "response.incomplete",
				"response": map[string]any{
					"id": "resp_incomplete", "model": request.Model, "status": "incomplete",
					"incomplete_details": map[string]string{"reason": "max_output_tokens"},
					"output":             []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "unfinished"}}}},
				},
			})
		default:
			t.Errorf("unexpected Responses model %q", request.Model)
			writeI04SSE(w, map[string]any{"type": "error", "error": map[string]string{"message": "unexpected model"}})
		}
	}))
	defer responses.Close()

	var chatModels []string
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("Chat request = %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read Chat request: %v", err)
			return
		}
		appendCapture("chat-completions", body)
		var request = struct {
			Model    string `json:"model"`
			Messages []struct {
				Role      string `json:"role"`
				Content   any    `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}{}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode Chat request: %v", err)
			return
		}
		chatModels = append(chatModels, request.Model)
		if request.Model == "resume-chat-v2" {
			var sawFirstTask, sawParentAnswer, sawFunctionCall, sawToolResult, sawSecondTask bool
			for _, message := range request.Messages {
				content := fmt.Sprint(message.Content)
				switch message.Role {
				case "user":
					sawFirstTask = sawFirstTask || strings.Contains(content, "root task: ask reviewer")
					sawSecondTask = sawSecondTask || strings.Contains(content, "resume task: continue on Chat")
				case "assistant":
					sawParentAnswer = sawParentAnswer || strings.Contains(content, "The reviewer found the configured connection")
					for _, call := range message.ToolCalls {
						sawFunctionCall = sawFunctionCall || (call.ID == "call_root_run_js" && call.Function.Name == "run_js")
					}
				case "tool":
					sawToolResult = sawToolResult || strings.Contains(content, "reviewer found the configured connection")
				}
			}
			if !(sawFirstTask && sawParentAnswer && sawFunctionCall && sawToolResult && sawSecondTask) {
				t.Errorf("resumed Chat request lost visible conversation (first=%t answer=%t call=%t result=%t second=%t): %s", sawFirstTask, sawParentAnswer, sawFunctionCall, sawToolResult, sawSecondTask, body)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		answer := "reviewer found the configured connection"
		if request.Model == "resume-chat-v2" {
			answer = "continued on the selected Chat connection"
		}
		fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(map[string]any{
			"id": "chat_completion", "model": request.Model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": answer}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 9, "completion_tokens": 4, "total_tokens": 13},
		}))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer chat.Close()

	workspace := t.TempDir()
	configDir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(configDir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	providers := config["providers"].(map[string]any)
	providers["i04-test-responses"] = map[string]any{
		"displayName": "I-04 test Responses", "api": "responses", "baseURL": responses.URL, "path": "/responses",
		"auth": map[string]any{"kind": "none"}, "defaultModel": "root-responses-v1", "defaultEffort": "auto",
		"adapter": map[string]any{"store": false, "namespaceTools": true, "reasoningReplay": true},
	}
	providers["i04-test-chat"] = map[string]any{
		"displayName": "I-04 test Chat", "api": "chat-completions", "baseURL": chat.URL, "path": "/chat/completions",
		"auth": map[string]any{"kind": "none"}, "defaultModel": "reviewer-chat-v1", "defaultEffort": "auto",
	}
	config["model"] = map[string]any{"provider": "i04-test-responses", "modelId": "root-responses-v1", "effort": "auto"}
	roles := config["roles"].(map[string]any)
	reviewer := roles["reviewer"].(map[string]any)
	reviewer["model"] = map[string]any{"provider": "i04-test-chat", "modelId": "reviewer-chat-v1", "effort": "auto"}
	configBytes, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0644); err != nil {
		t.Fatal(err)
	}

	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first, err := Run(ctx, store, nil, RunOptions{
		Task: "root task: ask reviewer", Workspace: workspace, ConfigPath: configPath,
	})
	if err != nil || !first.Result.OK || !strings.Contains(first.Result.FinalText, "reviewer found the configured connection") {
		t.Fatalf("Responses parent with Chat child: result=%+v err=%v", first.Result, err)
	}
	children, err := store.Children(ctx, first.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("root child executions = %+v; err=%v", children, err)
	}
	child := children[0]
	var childModel contracts.ModelSelection
	if err := json.Unmarshal(child.Model, &childModel); err != nil {
		t.Fatal(err)
	}
	if child.AgentName != "reviewer" || child.SessionID != "" || child.SessionCorrelation != first.SessionID || child.Adoption != "non_canonical" || childModel.Provider != "i04-test-chat" || childModel.API != "chat-completions" || childModel.ModelID != "reviewer-chat-v1" {
		t.Fatalf("reviewer child selection/adoption = %+v model=%+v", child, childModel)
	}
	session, err := store.Session(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var initialSelection contracts.ModelSelection
	if err := json.Unmarshal(session.ActiveModel, &initialSelection); err != nil {
		t.Fatal(err)
	}
	if session.TurnCount != 1 || initialSelection.Provider != "i04-test-responses" || initialSelection.API != "responses" || initialSelection.ModelID != "root-responses-v1" {
		t.Fatalf("child changed or failed to adopt root Session selection: Session=%+v model=%+v", session, initialSelection)
	}

	second, err := Run(ctx, store, nil, RunOptions{
		Task: "resume task: continue on Chat", SessionID: first.SessionID, ConfigPath: configPath,
		ProviderID: "i04-test-chat", ModelID: "resume-chat-v2", Effort: "high",
	})
	if err != nil || !second.Result.OK || second.SessionID != first.SessionID {
		t.Fatalf("resume with provider/model/effort switch: result=%+v err=%v", second.Result, err)
	}
	session, err = store.Session(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var switchedSelection contracts.ModelSelection
	if err := json.Unmarshal(session.ActiveModel, &switchedSelection); err != nil {
		t.Fatal(err)
	}
	if session.TurnCount != 2 || session.ModelChangeCount != 2 || switchedSelection.Provider != "i04-test-chat" || switchedSelection.API != "chat-completions" || switchedSelection.ModelID != "resume-chat-v2" || switchedSelection.Effort != "high" {
		t.Fatalf("successful root switch was not adopted: Session=%+v model=%+v", session, switchedSelection)
	}

	failed, err := Run(ctx, store, nil, RunOptions{
		Task: "failed switch should not be adopted", SessionID: first.SessionID, ConfigPath: configPath,
		ProviderID: "i04-test-responses", ModelID: "failed-responses-v3", Effort: "low",
	})
	if err == nil || failed.Result.OK {
		t.Fatalf("incomplete Responses turn was accepted: result=%+v err=%v", failed.Result, err)
	}
	session, err = store.Session(ctx, first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var afterFailure contracts.ModelSelection
	if err := json.Unmarshal(session.ActiveModel, &afterFailure); err != nil {
		t.Fatal(err)
	}
	if session.TurnCount != 2 || session.ModelChangeCount != 2 || afterFailure.Provider != switchedSelection.Provider || afterFailure.ModelID != switchedSelection.ModelID || afterFailure.Effort != switchedSelection.Effort {
		t.Fatalf("incomplete root changed adopted selection: Session=%+v model=%+v", session, afterFailure)
	}

	// Each execution retains the exact latest body received by its selected transport.
	bodiesByModel := map[string][][]byte{}
	captureMu.Lock()
	for _, capture := range captures {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(capture.body, &request); err != nil {
			t.Fatalf("decode captured %s request: %v", capture.api, err)
		}
		bodiesByModel[request.Model] = append(bodiesByModel[request.Model], capture.body)
	}
	captureCount := len(captures)
	captureMu.Unlock()
	if captureCount != 5 || len(chatModels) != 2 {
		t.Fatalf("captured requests = %d (chat models %v), want root tool/final, child, resumed root, failed root", captureCount, chatModels)
	}
	expected := []struct {
		executionID string
		model       string
		requests    int
	}{
		{executionID: first.ExecutionID, model: "root-responses-v1", requests: 2},
		{executionID: child.ID, model: "reviewer-chat-v1", requests: 1},
		{executionID: second.ExecutionID, model: "resume-chat-v2", requests: 1},
		{executionID: failed.ExecutionID, model: "failed-responses-v3", requests: 1},
	}
	for _, want := range expected {
		records, err := store.Records(ctx, want.executionID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		var requestCount, bodyCount int
		var latestBody []byte
		for _, record := range records {
			switch record.Kind {
			case "model_request":
				var payload struct {
					SchemaVersion int             `json:"schemaVersion"`
					Kind          string          `json:"kind"`
					Phase         string          `json:"phase"`
					Input         json.RawMessage `json:"input"`
				}
				if err := json.Unmarshal(record.Payload, &payload); err != nil {
					t.Fatal(err)
				}
				if payload.SchemaVersion == 1 {
					requestCount++
					if record.Content != nil {
						bodyCount++
						latestBody = append([]byte(nil), record.Content...)
					}
				} else if payload.Kind == "model_request" && payload.Phase == "start" {
					if len(payload.Input) != 0 || record.Content != nil {
						t.Fatalf("physical request in %s retained WireBody", want.executionID)
					}
				}
			case "context_budget":
				if record.Content != nil {
					t.Fatalf("context budget in %s retained WireBody", want.executionID)
				}
			}
		}
		if requestCount != want.requests || bodyCount != 1 {
			t.Fatalf("execution %s saved %d logical requests and %d WireBody owners, want %d/1", want.executionID, requestCount, bodyCount, want.requests)
		}
		captured := bodiesByModel[want.model]
		if len(captured) != want.requests || !bytes.Equal(latestBody, captured[len(captured)-1]) {
			t.Fatalf("execution %s latest saved body differs from last %s HTTP request", want.executionID, want.model)
		}
	}
}

func writeI04SSE(w http.ResponseWriter, event any) {
	fmt.Fprintf(w, "data: %s\n\n", contracts.JSON(event))
}
