package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
)

// Run through app's production Worker setup, not a directly configured runtime.
// Both the selected profile and an unused profile must contribute env metadata
// to the root and child across process.env, raw child_process, and the legacy
// command tool, without dropping the ordinary environment.
func TestRunCredentialEnvironmentRootAndChild(t *testing.T) {
	const selectedEnv = "HJH_TEST_SELECTED_CREDENTIAL"
	const unusedEnv = "HJH_TEST_UNUSED_CREDENTIAL"
	const ordinaryEnv = "HJH_TEST_ORDINARY_ENV"
	t.Setenv(selectedEnv, "dummy-selected-key")
	t.Setenv(unusedEnv, "dummy-unused-key")
	t.Setenv(ordinaryEnv, "ordinary-kept")

	store, workspace := delegationStore(t)
	configDir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(configDir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	profiles := config["credentials"].(map[string]any)
	profiles["openrouter-api-key"] = map[string]string{"env": selectedEnv}
	profiles["unused-test-profile"] = map[string]string{"env": unusedEnv}
	data, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	probe := `
const cp = await import('node:child_process');
if (Object.hasOwn(process.env, 'HJH_TEST_SELECTED_CREDENTIAL') ||
    Object.hasOwn(process.env, 'HJH_TEST_UNUSED_CREDENTIAL') ||
    process.env.HJH_TEST_ORDINARY_ENV !== 'ordinary-kept') {
  throw new Error("process.env environment differs");
}
const command = 'printf "%s|%s|%s" "${HJH_TEST_SELECTED_CREDENTIAL+present}" "${HJH_TEST_UNUSED_CREDENTIAL+present}" "$HJH_TEST_ORDINARY_ENV"';
const raw = await cp.execFile('/bin/sh', ['-c', command]);
if (raw.stdout !== "||ordinary-kept" || raw.stderr !== "") {
  throw new Error("raw child_process environment differs");
}
const legacy = await tools.command({program:"/bin/sh", args:["-c", command]});
if (legacy.exitCode !== 0 || legacy.stdout !== "||ordinary-kept" || legacy.stderr !== "") {
  throw new Error("legacy command environment differs");
}
`
	var verified atomic.Int32
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := request.Messages[0].VisibleText()
		if len(request.Messages) == 1 {
			code := probe
			if task == "root environment" {
				code += `
const child = await api.agents.run({task:"child environment"});
if (child.finalText !== "ENV_OK" || child.outcome !== "completed") {
  throw new Error("child environment probe failed");
}
`
			}
			return scriptedJS(code + `return "ENV_OK";`), nil
		}
		var results []contracts.ToolResult
		if err := json.Unmarshal(request.Messages[len(request.Messages)-1].Content, &results); err != nil {
			return contracts.ModelResult{}, err
		}
		if len(results) != 1 || results[0].Outcome != "success" {
			return contracts.ModelResult{}, fmt.Errorf("%s: environment probe did not return: %+v", task, results)
		}
		var marker string
		if err := json.Unmarshal([]byte(results[0].Text), &marker); err != nil || marker != "ENV_OK" {
			return contracts.ModelResult{}, fmt.Errorf("%s: environment probe marker missing: %s", task, results[0].Text)
		}
		verified.Add(1)
		return contracts.ModelResult{Text: "ENV_OK"}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "root environment", Workspace: workspace,
		ConfigPath: configPath, ModelID: "test/model"})
	if err != nil || !result.Result.OK || result.Result.FinalText != "ENV_OK" {
		t.Fatalf("environment run: %+v, %v", result, err)
	}
	if got := verified.Load(); got != 2 {
		t.Fatalf("verified %d executions, want root and child", got)
	}
}
