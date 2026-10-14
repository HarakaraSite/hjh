package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
)

func TestRoleChoiceAdoptionResumeAndCurrentConfig(t *testing.T) {
	store, workspace := delegationStore(t)
	configPath := filepath.Join(workspace, ".hjh", "extensions", "config.json")
	if err := extensions.InstallDefaults(filepath.Dir(configPath)); err != nil {
		t.Fatal(err)
	}
	seenRole := "default"
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		var want []string
		if seenRole == "reviewer" {
			want = []string{"run_js", "read_file", "ls", "find", "grep", "wc", "git_inspect"}
		} else {
			want = []string{"run_js", "run_agent", "spawn_agent", "agent_status", "steer_agent", "collect_agent", "cancel_agent", "read_file", "write_file", "edit_file", "command", "http", "web_fetch", "web_search", "ls", "find", "grep", "wc", "git_inspect"}
		}
		var actual []string
		for _, tool := range req.Tools {
			actual = append(actual, tool.Name)
		}
		if !reflect.DeepEqual(actual, want) {
			t.Errorf("selected definitions %v, want %v", actual, want)
		}
		if seenRole == "reviewer" && strings.Contains(req.Instruction, "api.agents.run") {
			t.Error("reviewer advertised unavailable delegation")
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	ctx := context.Background()
	first, err := Run(ctx, store, provider, RunOptions{Task: "first", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("first %+v: %v", first, err)
	}
	seenRole = "reviewer"
	second, err := Run(ctx, store, provider, RunOptions{Task: "review", SessionID: first.SessionID, Role: "reviewer"})
	if err != nil || !second.Result.OK {
		t.Fatalf("review %+v: %v", second, err)
	}
	session, err := store.Session(ctx, first.SessionID)
	if err != nil || session.Agent != "reviewer" {
		t.Fatalf("adopted role %+v: %v", session, err)
	}
	failure := delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		return contracts.ModelResult{}, errors.New("observed provider failure")
	})
	failed, err := Run(ctx, store, failure, RunOptions{Task: "failure", SessionID: first.SessionID, Role: "default"})
	if err == nil || mustExecution(t, store, failed.ExecutionID).AgentName != "default" {
		t.Fatalf("failed role %+v: %v", failed, err)
	}
	cancelledProvider := delegationProvider(func(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		<-ctx.Done()
		return contracts.ModelResult{}, ctx.Err()
	})
	workCtx, cancel := context.WithCancel(ctx)
	cancelled, err := Run(workCtx, store, cancelledProvider, RunOptions{Task: "cancel", SessionID: first.SessionID, Role: "default", Progress: func(o contracts.Observation) error {
		if o.Kind == "execution" && o.Phase == "started" {
			cancel()
		}
		return nil
	}})
	if err == nil || cancelled.Result.OK {
		t.Fatalf("cancel %+v: %v", cancelled, err)
	}
	session, err = store.Session(ctx, first.SessionID)
	if err != nil || session.Agent != "reviewer" || session.TurnCount != 2 {
		t.Fatalf("failure/cancel changed role %+v: %v", session, err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["roles"].(map[string]any)["reviewer"].(map[string]any)["instruction"] = "current-reviewer-instruction"
	if err = os.WriteFile(configPath, contracts.JSON(config), 0644); err != nil {
		t.Fatal(err)
	}
	current := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		if !strings.Contains(req.Instruction, "current-reviewer-instruction") {
			t.Error("resume used historical instruction")
		}
		return provider(ctx, req, obs)
	})
	resumed, err := Run(ctx, store, current, RunOptions{Task: "continue", SessionID: first.SessionID})
	if err != nil || !resumed.Result.OK || mustExecution(t, store, resumed.ExecutionID).AgentName != "reviewer" {
		t.Fatalf("resume %+v: %v", resumed, err)
	}
	configuration, err := store.Configuration(ctx, mustExecution(t, store, resumed.ExecutionID).ConfigurationID)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Agent struct {
			Name  string
			Tools []string
		}
		Tools []contracts.ToolDefinition
	}
	if err = json.Unmarshal(configuration.Snapshot, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Agent.Name != "reviewer" || !reflect.DeepEqual(saved.Agent.Tools, []string{"run_js", "read_file", "ls", "find", "grep", "wc", "git_inspect"}) || len(saved.Tools) != 0 {
		t.Fatalf("saved selection: %s", configuration.Snapshot)
	}
}

func TestRoleToollessRunUsesNoImplicitRuntimeTool(t *testing.T) {
	store, workspace := delegationStore(t)
	configPath := filepath.Join(workspace, "extensions", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"providers":{"test":{"api":"responses","baseURL":"http://unused","auth":{"kind":"none"},"defaultModel":"test/model"}},"model":{"provider":"test"},"defaultRole":"text","roles":{"text":{"instruction":"text-only","tools":[]}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		if len(req.Tools) != 0 || strings.Contains(req.Instruction, "run_js") {
			t.Fatalf("implicit tools: %+v / %s", req.Tools, req.Instruction)
		}
		return contracts.ModelResult{Text: "text answer"}, nil
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "answer", Workspace: workspace, ConfigPath: configPath, ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("tool-less run %+v: %v", result, err)
	}
	configuration, err := store.Configuration(context.Background(), mustExecution(t, store, result.ExecutionID).ConfigurationID)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct{ Tools []any }
	if err = json.Unmarshal(configuration.Snapshot, &saved); err != nil || len(saved.Tools) != 0 {
		t.Fatalf("implicit saved tool %s: %v", configuration.Snapshot, err)
	}
}
