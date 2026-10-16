package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hjh/internal/contracts"
)

// Exercise public Worker construction and the normal agent, journal, and history
// owners. Only the provider is scripted: no candidate runtime or mock Worker.
func TestRunStdlibUncaughtErrorRecoversFreshVM(t *testing.T) {
	store, workspace := delegationStore(t)
	hookPath := installLifecycleHooks(t, workspace, `let calls = 0;
export function beforeTool({args}) {
  globalThis.recoveryModuleCalls = ++calls;
  return args;
}`)
	drainedPath := filepath.Join(workspace, "caught-timer.txt")
	const failureMarker = "UNCAUGHT_STDLIB_RECOVERY"
	const finalText = "RECOVERED_ANSWER"
	codes := []string{
		fmt.Sprintf(`const fs = await import('node:fs');
globalThis.recoveryState = 'caught-kept';
try { throw new Error('caught normally'); } catch (_) {}
setTimeout(() => {
  globalThis.recoveryTimer = 'drained';
  fs.writeFileSync(%q, 'CAUGHT_TIMER_DRAINED');
}, 5);
return 'CAUGHT_OK';`, drainedPath),
		`if (globalThis.recoveryState !== 'caught-kept' ||
    globalThis.recoveryTimer !== 'drained' || globalThis.recoveryModuleCalls !== 2) {
  throw new Error('caught error lost state or timer was not drained');
}
globalThis.recoveryState = 'discard-me';
setInterval(() => { globalThis.discardedInterval = true; }, 10000);
throw new Error('UNCAUGHT_STDLIB_RECOVERY');`,
		`if (globalThis.recoveryState !== undefined || globalThis.recoveryTimer !== undefined ||
    globalThis.discardedInterval !== undefined || globalThis.recoveryModuleCalls !== 1) {
  throw new Error('uncaught error did not reset globals and module state');
}
const path = await import('node:path');
const cp = await import('node:child_process');
const command = await cp.execFile('/bin/sh', ['-c', 'printf RAW_RECOVERED']);
if (command.stdout !== 'RAW_RECOVERED' || path.basename(process.cwd()) === '') {
  throw new Error('stdlib did not recover');
}
await tools.write_file({path:'recovered.txt', text:'RECOVERED_FILE'});
return 'RECOVERED_OK';`,
	}
	callIDs := []string{"caught-call", "uncaught-call", "recovered-call"}
	var model contracts.ModelSelection
	var observed []contracts.ToolResult
	step := 0
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		if step > len(codes)+1 {
			return contracts.ModelResult{}, fmt.Errorf("unexpected provider step %d", step)
		}
		if step == 1 {
			model = request.Model
		} else {
			if !reflect.DeepEqual(request.Model, model) {
				return contracts.ModelResult{}, fmt.Errorf("model selection changed after JS call: %+v", request.Model)
			}
			var results []contracts.ToolResult
			if err := json.Unmarshal(request.Messages[len(request.Messages)-1].Content, &results); err != nil || len(results) != 1 {
				return contracts.ModelResult{}, fmt.Errorf("step %d tool results %+v: %v", step, results, err)
			}
			got := results[0]
			if got.CallID != callIDs[step-2] || got.Name != "run_js" {
				return contracts.ModelResult{}, fmt.Errorf("step %d wrong tool identity: %+v", step, got)
			}
			if step == 3 {
				if got.Outcome != "error" || !strings.Contains(got.Text, failureMarker) {
					return contracts.ModelResult{}, fmt.Errorf("uncaught error not returned to model: %+v", got)
				}
				// Reopening must use this execution's capture, not edited disk modules.
				if err := os.WriteFile(hookPath, []byte(`export function beforeTool(){throw new Error('RELOADED_FROM_DISK')}`), 0644); err != nil {
					return contracts.ModelResult{}, err
				}
			} else {
				want := `"CAUGHT_OK"`
				if step == 4 {
					want = `"RECOVERED_OK"`
				}
				if got.Outcome != "success" || got.Text != want {
					return contracts.ModelResult{}, fmt.Errorf("step %d result: %+v, want %s", step, got, want)
				}
			}
			if step == 2 {
				if body, err := os.ReadFile(drainedPath); err != nil || string(body) != "CAUGHT_TIMER_DRAINED" {
					return contracts.ModelResult{}, fmt.Errorf("normal call returned before timer drain: %s %v", body, err)
				}
			}
			observed = append(observed, got)
		}
		if step == 4 {
			return contracts.ModelResult{Text: finalText}, nil
		}
		return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: callIDs[step-1], Name: "run_js", Arguments: contracts.JSON(map[string]string{"code": codes[step-1]})}}}, nil
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "recover after an uncaught JS error", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !result.Result.OK || result.Result.FinalText != finalText || step != 4 {
		t.Fatalf("recovery run: %+v, step %d: %v", result, step, err)
	}
	if body, err := os.ReadFile(filepath.Join(workspace, "recovered.txt")); err != nil || string(body) != "RECOVERED_FILE" {
		t.Fatalf("host tools did not recover: %s %v", body, err)
	}
	execution, err := store.Execution(t.Context(), result.ExecutionID)
	if err != nil || execution.Outcome != "completed" || execution.Adoption != "canonical" {
		t.Fatalf("recovered execution settlement: %+v %v", execution, err)
	}

	// The results delivered back to the provider must also survive persistence,
	// including the failed call, without turning the execution into a failure.
	messages, err := store.Messages(t.Context(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var saved []contracts.ToolResult
	var savedAnswer bool
	for _, message := range messages {
		var decoded contracts.Message
		if err := json.Unmarshal(message.JSON, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Role == "tool" {
			var results []contracts.ToolResult
			if err := json.Unmarshal(decoded.Content, &results); err != nil {
				t.Fatal(err)
			}
			saved = append(saved, results...)
		}
		savedAnswer = savedAnswer || (decoded.Role == "assistant" && decoded.VisibleText() == finalText)
	}
	// History canonicalizes embedded JSON, so compare decoded failure values
	// rather than the encoding's HTML escaping or object key order.
	var savedValue, observedValue any
	if err := json.Unmarshal(contracts.JSON(saved), &savedValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contracts.JSON(observed), &observedValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(savedValue, observedValue) || !savedAnswer {
		t.Fatalf("persisted recovery differs: results %+v, want %+v; answer saved %v", saved, observed, savedAnswer)
	}
	cut, err := store.CaptureConversationCut(t.Context(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var replayFailure, replayRecovery bool
	if err := store.WalkConversationCut(t.Context(), cut, func(fact contracts.ExecutionFact) error {
		if fact.Kind == "tool_result" && fact.CallID == "uncaught-call" && strings.Contains(fact.Error, failureMarker) {
			replayFailure = true
		}
		if fact.Kind == "tool_result" && fact.CallID == "recovered-call" && fact.Error == "" {
			replayRecovery = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !replayFailure || !replayRecovery {
		t.Fatalf("conversation replay lost failure/recovery: failure %v, recovery %v", replayFailure, replayRecovery)
	}
}
