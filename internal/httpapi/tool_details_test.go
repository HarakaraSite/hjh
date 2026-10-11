package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/apiclient"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

type fileToolProvider struct{ direct bool }

func (fileToolProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return json.Marshal(request)
}

func (p fileToolProvider) Generate(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	if len(request.Messages) == 1 {
		if p.direct {
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "read-source", Name: "read_file",
				Arguments: contracts.JSON(map[string]string{"path": "README.md"})}}}, nil
		}
		return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "read-source", Name: "run_js",
			Arguments: contracts.JSON(map[string]string{"code": `return await tools.read_file({path:"README.md"});`})}}}, nil
	}
	return contracts.ModelResult{Text: "最終回答は読みやすく表示する。"}, nil
}

// The observed regression printed one README repeatedly for nested run_js and
// read_file results. The public view must stay compact while locator reads keep
// the complete result available over the actual HTTP client path.
func TestToolViewKeepsResultInSeparateHTTPDetail(t *testing.T) {
	t.Run("direct model call", func(t *testing.T) { checkToolHTTPDetail(t, true) })
	t.Run("nested JS call", func(t *testing.T) { checkToolHTTPDetail(t, false) })
}

func checkToolHTTPDetail(t *testing.T, direct bool) {
	ctx := context.Background()
	workspace := t.TempDir()
	body := strings.Repeat("保存したREADME本文です。\n", 300)
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	result, err := app.Run(ctx, store, fileToolProvider{direct: direct}, app.RunOptions{Task: "read README", Workspace: workspace, ModelID: "test/model"})
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace})
	manager := corerun.New(service, workspace)
	defer manager.Close()
	handler := New(service, manager)
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := apiclient.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	view, err := client.SessionView(ctx, result.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	var readFound, finalFound bool
	var tools []string
	for _, entity := range view.Entities {
		if entity.Kind == "assistant" && entity.Text == result.Result.FinalText {
			finalFound = true
		}
		if entity.Kind != "tool" {
			continue
		}
		tools = append(tools, entity.Name)
		if entity.Text != "" {
			t.Fatalf("tool %s copied full result into ordinary conversation", entity.Name)
		}
		if entity.Name != "read_file" {
			continue
		}
		readFound = true
		if entity.Preview != "README.md" || entity.Detail == nil {
			t.Fatalf("read_file lost its activity preview or saved detail locator: %+v", entity)
		}
		detail, err := client.Record(ctx, entity.Detail.ExecutionID, entity.Detail.Ordinal)
		if err != nil {
			t.Fatal(err)
		}
		var saved struct {
			Result struct {
				Text string `json:"text"`
			} `json:"result"`
			Content []contracts.ToolResult `json:"content"`
		}
		if err := json.Unmarshal(detail.Payload, &saved); err != nil {
			t.Fatal(err)
		}
		text := saved.Result.Text
		if direct && len(saved.Content) == 1 {
			var result struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal([]byte(saved.Content[0].Text), &result); err != nil {
				t.Fatal(err)
			}
			text = result.Text
		}
		if text != body {
			t.Fatal("separate HTTP tool detail did not retain complete README")
		}
	}
	if !readFound || !finalFound {
		t.Fatalf("conversation omitted read_file or final answer: read=%v final=%v", readFound, finalFound)
	}
	wantCount := 2 // model run_js and its actual nested read_file
	if direct {
		wantCount = 1
	}
	if len(tools) != wantCount {
		t.Fatalf("model declaration and runtime duplicated the invocation: %v", tools)
	}
	for _, entity := range view.Entities {
		if entity.Kind == "tool" && entity.Name == "read_file" {
			if direct && entity.Detail.CallID != "read-source" {
				t.Fatalf("direct runtime lost model call identity: %+v", entity)
			}
			if !direct && (entity.ParentEntityID == "" || view.Entities[entity.ParentEntityID].Name != "run_js") {
				t.Fatalf("nested tool lost actual model call parent: %+v", entity)
			}
		}
	}
}
