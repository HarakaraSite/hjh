package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestProviderSessionIDSeparatesChildrenAndSurvivesRetrySteeringAndResume(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	headers := map[string][]string{}
	steered := make(chan struct{})
	var steerOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []struct {
				Content json.RawMessage `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(request.Input) == 0 || len(request.Input[0].Content) == 0 {
			t.Error("request omitted the task")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var task string
		if err := json.Unmarshal(request.Input[0].Content, &task); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		headers[task] = append(headers[task], r.Header.Get("session_id"))
		attempt := len(headers[task])
		mu.Unlock()
		if task == "sync child" && attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if task == "async child" && attempt == 1 {
			select {
			case <-steered:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		output := map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}}
		var tool, code string
		if task == "parent" && attempt == 1 {
			tool = "run_js"
			code = `const sync = await tools.run_agent({role:"explorer",task:"sync child"});
const async = await tools.spawn_agent({role:"explorer",task:"async child"});
await tools.steer_agent({executionId:async.executionId,text:"Include the source."});
return {sync,async:await tools.collect_agent({executionId:async.executionId})};`
		} else if (task == "sync child" && attempt == 2) || (task == "async child" && attempt == 1) {
			tool = "read_file"
		}
		if tool != "" {
			arguments := contracts.JSON(map[string]string{"path": "input.txt"})
			if tool == "run_js" {
				arguments = contracts.JSON(map[string]string{"code": code})
			}
			output = map[string]any{"type": "function_call", "id": "fc-" + tool, "call_id": "call-" + tool,
				"namespace": "hjh", "name": tool, "arguments": string(arguments), "status": "completed"}
		}
		writeI04SSE(w, map[string]any{"type": "response.completed", "response": map[string]any{
			"id": fmt.Sprintf("resp-%s-%d", task, attempt), "status": "completed", "output": []any{output},
		}})
	}))
	defer server.Close()
	configPath, modelID := standardLocalProviderConfig(t, workspace, "openai-chatgpt", server.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	first, err := Run(ctx, store, nil, RunOptions{Task: "parent", Workspace: workspace, ConfigPath: configPath,
		ProviderID: "openai-chatgpt", ModelID: modelID, Progress: func(o contracts.Observation) error {
			if o.Kind == "tool" && o.Name == "steer_agent" && o.Phase == "completed" {
				steerOnce.Do(func() { close(steered) })
			}
			return nil
		}})
	if err != nil || !first.Result.OK {
		t.Fatalf("parent/children failed: %+v, %v", first.Result, err)
	}
	second, err := Run(ctx, store, nil, RunOptions{Task: "resume", SessionID: first.SessionID, ConfigPath: configPath})
	if err != nil || !second.Result.OK {
		t.Fatalf("resume failed: %+v, %v", second.Result, err)
	}
	children, err := store.Children(ctx, first.ExecutionID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children: %+v, %v", children, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(headers["parent"]) != 3 || len(headers["sync child"]) != 3 || len(headers["async child"]) != 2 {
		t.Fatalf("unexpected request counts: %+v", headers)
	}
	for task, ids := range headers {
		want := first.SessionID
		if task != "parent" {
			want = ""
			for _, child := range children {
				if child.Task == task {
					want = child.ID
					if want == first.SessionID || child.SessionCorrelation != first.SessionID || child.SessionID != "" {
						t.Fatalf("child identity/correlation changed: %+v", child)
					}
				}
			}
		}
		for _, id := range ids {
			if id == "" || id != want {
				t.Errorf("%s transport session = %q, want %q", task, id, want)
			}
		}
	}
}
