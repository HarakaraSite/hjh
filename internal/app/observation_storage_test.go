package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestLifecycleDefinitionsRemainAvailableButAreNotArchived(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `
function check(context) {
  if (!context.tools.some(t=>t.name === 'read_file') ||
      !context.tools.every(t=>typeof t.description === 'string' && t.inputSchema.type === 'object')) {
    throw new Error('runtime hook lost tool definitions');
  }
}
export function runtimeStart({context}) {check(context);}
export function beforeTurn({context}) {check(context);}
export function afterTurn({context}) {check(context);}
export function runtimeStop({context}) {check(context);}
`)
	var selected []string
	live := map[string][]string{}
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		for _, tool := range request.Tools {
			selected = append(selected, tool.Name)
		}
		return contracts.ModelResult{Text: "definitions available"}, nil
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Task: "verify lifecycle definitions", Workspace: workspace,
		ModelID: "test/model", Progress: func(observation contracts.Observation) error {
			if observation.Kind != "hook" || observation.Phase != "started" {
				return nil
			}
			var input contracts.LifecycleHookInput
			if err := json.Unmarshal(observation.Input, &input); err != nil {
				return err
			}
			for _, tool := range input.Context.Tools {
				live[observation.Name] = append(live[observation.Name], tool.Name)
			}
			return nil
		}})
	if err != nil || !result.Result.OK {
		t.Fatalf("lifecycle run failed: %v", err)
	}
	if len(selected) == 0 {
		t.Fatal("model did not receive definitions")
	}
	configuration, err := store.Configuration(t.Context(), mustExecution(t, store, result.ExecutionID).ConfigurationID)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(configuration.Snapshot, &saved); err != nil {
		t.Fatal(err)
	}
	if _, present := saved["tools"]; present {
		t.Fatal("configuration archived tool definitions")
	}
	records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string][]string{}
	for _, record := range records {
		var observation contracts.Observation
		if err := json.Unmarshal(record.Payload, &observation); err != nil {
			t.Fatal(err)
		}
		if observation.Kind != "hook" || observation.Phase != "started" {
			continue
		}
		var input struct {
			Context struct {
				Tools     json.RawMessage `json:"tools"`
				ToolNames []string        `json:"toolNames"`
			} `json:"context"`
		}
		if err := json.Unmarshal(observation.Input, &input); err != nil {
			t.Fatal(err)
		}
		if len(input.Context.Tools) != 0 || strings.Contains(string(observation.Input), `"inputSchema"`) {
			t.Fatal("lifecycle observation archived tool definitions")
		}
		stored[observation.Name] = input.Context.ToolNames
	}
	for _, name := range []string{"runtimeStart", "beforeTurn", "afterTurn", "runtimeStop"} {
		if !reflect.DeepEqual(live[name], selected) || !reflect.DeepEqual(stored[name], selected) {
			t.Fatalf("%s runtime/live/stored tool selections differ", name)
		}
	}
}
