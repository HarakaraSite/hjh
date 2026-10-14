package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/extensions"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

func installLifecycleHooks(t *testing.T, workspace, source string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hooks.js")
	if err := os.WriteFile(path, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLifecycleContextParentChildrenAndNextTurn(t *testing.T) {
	store, workspace := delegationStore(t)
	source := `let stage = 0;
export function runtimeStart({runtime}) { stage++; return {context:['LIFECYCLE_REV_A '+runtime.role+' '+runtime.executionId]}; }
export function beforeTurn({task,context}) {
  if(stage!==1 || !context.systemInstruction.includes('LIFECYCLE_REV_A')) throw new Error('start context not applied');
  stage++; return {context:['TURN_CONTEXT '+task]};
}
export async function afterTurn({runtime,outcome,settlement},api) {
  stage++; await api.writeFile({path:runtime.executionId+'-settlement.json',text:JSON.stringify({stage,outcome,settlement,runtime})});
}
export async function runtimeStop({runtime,reason},api) {
  stage++; await api.writeFile({path:runtime.executionId+'-stop.json',text:JSON.stringify({stage,reason})});
}`
	path := installLifecycleHooks(t, workspace, source)
	var mu sync.Mutex
	requests := map[string]contracts.ModelRequest{}
	steps := map[string]int{}
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := ""
		for _, message := range req.Messages {
			if message.Role == "user" {
				task = message.VisibleText()
			}
		}
		mu.Lock()
		requests[task] = req
		steps[task]++
		step := steps[task]
		mu.Unlock()
		if task == "root lifecycle" && step == 1 {
			return scriptedJS(`const children=await Promise.all([api.agents.spawn({task:'child A',tools:[]}),api.agents.spawn({task:'child B',tools:[]})]); return await Promise.all(children.map(c=>api.agents.collect({executionId:c.executionId})));`), nil
		}
		return contracts.ModelResult{Text: "lifecycle answer " + task}, nil
	})
	first, err := Run(context.Background(), store, provider, RunOptions{Task: "root lifecycle", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("first run %+v: %v", first, err)
	}
	children, err := store.Children(context.Background(), first.ExecutionID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children %+v: %v", children, err)
	}
	root, err := store.Execution(context.Background(), first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, execution := range append([]history.Execution{root}, children...) {
		role := "root"
		adoption := "canonical"
		if execution.ParentExecutionID != "" {
			role, adoption = "child", "non_canonical"
		}
		req := requests[execution.Task]
		if !strings.Contains(req.Instruction, "LIFECYCLE_REV_A "+role+" "+execution.ID) || !strings.Contains(req.Instruction, "TURN_CONTEXT "+execution.Task) {
			t.Fatalf("missing execution-local context for %s: %s", execution.Task, req.Instruction)
		}
		assertInstructionMatchesStoredLifecycle(t, store, execution.ID, req.Instruction)
		data, err := os.ReadFile(filepath.Join(workspace, execution.ID+"-settlement.json"))
		if err != nil {
			t.Fatal(err)
		}
		var settled struct {
			Stage      int                      `json:"stage"`
			Runtime    contracts.HookRuntime    `json:"runtime"`
			Settlement contracts.HookSettlement `json:"settlement"`
		}
		if json.Unmarshal(data, &settled) != nil || settled.Stage != 3 || settled.Runtime.Role != role ||
			settled.Settlement.Adoption != adoption || settled.Settlement.TerminalRecordID != execution.TerminalRecordID {
			t.Fatalf("hook ran before settlement or wrong module state: %s", data)
		}
		stop, err := os.ReadFile(filepath.Join(workspace, execution.ID+"-stop.json"))
		if err != nil || !strings.Contains(string(stop), `"stage":4`) || !strings.Contains(string(stop), `"reason":"completed"`) {
			t.Fatalf("stop stage/result %s: %v", stop, err)
		}
		records, err := store.Records(context.Background(), execution.ID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		var terminal, after, stopOrdinal int64
		for _, record := range records {
			if record.Kind == "resource_revision" && string(record.Content) == source {
				t.Fatal("lifecycle hook implementation was automatically archived")
			}
			if record.ID == execution.TerminalRecordID {
				terminal = record.Ordinal
			}
			var event contracts.Observation
			_ = json.Unmarshal(record.Payload, &event)
			if event.Kind == "hook" && event.Phase == "completed" {
				if event.Name == "afterTurn" {
					after = record.Ordinal
				}
				if event.Name == "runtimeStop" {
					stopOrdinal = record.Ordinal
				}
			}
		}
		if terminal == 0 || after <= terminal || stopOrdinal <= after {
			t.Fatalf("hook order terminal=%d after=%d stop=%d", terminal, after, stopOrdinal)
		}
	}
	// A resumed turn captures edited source and adds only this Worker's context.
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(source, "LIFECYCLE_REV_A", "LIFECYCLE_REV_B")), 0644); err != nil {
		t.Fatal(err)
	}
	second, err := Run(context.Background(), store, provider, RunOptions{Task: "next lifecycle", SessionID: first.SessionID})
	if err != nil || !second.Result.OK {
		t.Fatalf("next turn %+v: %v", second, err)
	}
	if req := requests["next lifecycle"]; !strings.Contains(req.Instruction, "LIFECYCLE_REV_B root "+second.ExecutionID) || strings.Contains(req.Instruction, "LIFECYCLE_REV_A") || strings.Contains(req.Instruction, "TURN_CONTEXT root lifecycle") {
		t.Fatalf("old context accumulated: %s", req.Instruction)
	}
	conversation, err := store.Conversation(context.Background(), first.SessionID, 1, 1)
	if err != nil || len(conversation) == 0 {
		t.Fatalf("first turn was lost: %v", err)
	}
}

func assertInstructionMatchesStoredLifecycle(t *testing.T, store *history.Store, executionID, instruction string) {
	t.Helper()
	execution, err := store.Execution(context.Background(), executionID)
	if err != nil {
		t.Fatal(err)
	}
	config, err := store.Configuration(context.Background(), execution.ConfigurationID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		SystemInstruction string `json:"systemInstruction"`
	}
	if err := json.Unmarshal(config.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	want := snapshot.SystemInstruction
	records, err := store.Records(context.Background(), executionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var requestInstruction string
	for _, record := range records {
		var event contracts.Observation
		_ = json.Unmarshal(record.Payload, &event)
		if event.Kind == "hook" && event.Phase == "completed" && (event.Name == "runtimeStart" || event.Name == "beforeTurn") {
			var result contracts.HookContextAddition
			if json.Unmarshal(event.Result, &result) == nil && len(result.Context) != 0 {
				want += "\n\n" + strings.Join(result.Context, "\n")
			}
		}
		if record.Kind == "model_request" && record.Content != nil {
			var internalRequest contracts.ModelRequest
			if json.Unmarshal(record.Content, &internalRequest) == nil && internalRequest.Instruction != "" {
				requestInstruction = internalRequest.Instruction
				continue
			}
			var request struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if json.Unmarshal(record.Content, &request) == nil {
				for _, message := range request.Messages {
					if message.Role == "system" {
						requestInstruction = message.Content
					}
				}
			}
		}
	}
	if want != instruction || requestInstruction != want {
		t.Fatalf("request differs from saved configuration + hook contributions: archive=%t request=%t", want == instruction, requestInstruction == want)
	}
}

func TestLifecycleCleanupFailurePreservesAnswerAndReplayNotice(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export function afterTurn(){throw new Error('afterTurn verification failure')}
export async function runtimeStop({runtime},api){await api.writeFile({path:'stop-'+runtime.executionId,text:'STOP_COMPLETED'});}`)
	owner := executiondata.NewOwner()
	var mu sync.Mutex
	var live []contracts.ExecutionFact
	_, subscription, err := executiondata.CaptureAndSubscribe(owner, func(string, uint64) (struct{}, error) { return struct{}{}, nil }, func(c contracts.ExecutionCommit) { mu.Lock(); live = append(live, c.Facts...); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	result, err := Run(context.Background(), store, delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		return contracts.ModelResult{Text: "SAVED_ANSWER"}, nil
	}), RunOptions{Task: "cleanup failure", Workspace: workspace, ExecutionDataOwner: owner})
	if err != nil || !result.Result.OK || result.Result.FinalText != "SAVED_ANSWER" {
		t.Fatalf("cleanup overwrote result %+v: %v", result, err)
	}
	execution, err := store.Execution(context.Background(), result.ExecutionID)
	if err != nil || execution.Outcome != "completed" || execution.Adoption != "canonical" {
		t.Fatalf("settlement %+v: %v", execution, err)
	}
	if body, err := os.ReadFile(filepath.Join(workspace, "stop-"+result.ExecutionID)); err != nil || string(body) != "STOP_COMPLETED" {
		t.Fatalf("runtimeStop did not run: %s %v", body, err)
	}
	cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var replay []contracts.ExecutionFact
	if err := store.WalkConversationCut(context.Background(), cut, func(f contracts.ExecutionFact) error { replay = append(replay, f); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, facts := range [][]contracts.ExecutionFact{live, replay} {
		found := false
		for _, fact := range facts {
			if fact.Conversation.EntityKind == "notice" && strings.Contains(fact.Text, "afterTurn verification failure") {
				found = true
			}
		}
		if !found {
			t.Fatal("cleanup failure notice missing from live or replay")
		}
	}
}

func TestLifecycleProviderCancellationRunsCleanup(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function afterTurn({settlement},api){await api.writeFile({path:'cancel-outcome',text:settlement.outcome})}
export async function runtimeStop({reason},api){await api.writeFile({path:'cancel-stop',text:reason})}`)
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := delegationProvider(func(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		close(started)
		<-ctx.Done()
		return contracts.ModelResult{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, store, provider, RunOptions{Task: "cancel model", Workspace: workspace})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not finish")
	}
	for _, name := range []string{"cancel-outcome", "cancel-stop"} {
		body, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil || string(body) != "cancelled" {
			t.Fatalf("cancel hook %s: %s %v", name, body, err)
		}
	}
}

func TestLifecycleCancelledJSRecordsSkippedCleanup(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export function afterTurn(){} export function runtimeStop(){}`)
	started := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var result RunResult
	done := make(chan error, 1)
	go func() {
		var err error
		result, err = Run(ctx, store, delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
			return scriptedJS(`await tools.command({program:'sleep',args:['30']});`), nil
		}), RunOptions{Task: "cancel JS", Workspace: workspace, Progress: func(o contracts.Observation) error {
			if o.Kind == "host" && o.Name == "command" && o.Phase == "started" {
				started <- struct{}{}
			}
			return nil
		}})
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("JS cancellation did not join")
	}
	records, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, record := range records {
		var o contracts.Observation
		_ = json.Unmarshal(record.Payload, &o)
		if o.Kind == "hook" && o.Phase == "skipped" && o.Error == "worker_unavailable" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("unavailable cleanup not recorded: %d", count)
	}
}

func TestLifecycleContextIsIncludedInInputBudget(t *testing.T) {
	store, workspace := delegationStore(t)
	path := budgetConfig(t, workspace, 6000)
	installLifecycleHooks(t, workspace, `export function beforeTurn(){return {context:['HOOK_BUDGET_'+ 'x'.repeat(40000)]}}`)
	called := false
	provider := budgetTestProvider{adapter: chatFixtureProvider("http://unused", "", "hook-budget", protocol.Options{}), generate: func(contracts.ModelRequest) contracts.ModelResult {
		called = true
		return contracts.ModelResult{Text: "must not be requested"}
	}}
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "hook budget", Workspace: workspace, ConfigPath: path})
	var exceeded *contextbudget.ExceededError
	if !errors.As(err, &exceeded) || called {
		t.Fatalf("hook context bypassed budget: %+v called=%v err=%v", result, called, err)
	}
	if result.Result.Diagnostic == nil || result.Result.Diagnostic.Code != contracts.FailureContextBudget {
		t.Fatalf("protected context overflow lost diagnostic: %+v", result.Result)
	}
	records, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, record := range records {
		if record.Kind == "context_budget" {
			var payload contracts.ContextBudgetStatus
			if err := json.Unmarshal(record.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			var fact contextbudget.Fact
			if err := json.Unmarshal(payload.Fact, &fact); err != nil {
				t.Fatal(err)
			}
			found = payload.Exceeded && payload.DetailAvailable && fact.Budget.Limit != nil &&
				fact.EstimatedTokens > *fact.Budget.Limit && payload.Summary.EstimatedTokens == fact.EstimatedTokens && record.Content == nil
		}
	}
	if !found {
		t.Fatal("budget failure did not retain calculation metadata without duplicating WireBody")
	}
}

func TestLifecycleStartupFailureIsVisibleAndStopsModel(t *testing.T) {
	for _, phase := range []string{"runtimeStart", "beforeTurn"} {
		t.Run(phase, func(t *testing.T) {
			store, workspace := delegationStore(t)
			installLifecycleHooks(t, workspace, `export function `+phase+`(){throw new Error('START_HOOK_FAILED')}
export async function runtimeStop({runtime},api){await api.writeFile({path:'startup-stop-'+runtime.executionId,text:'STOP_COMPLETED'})}`)
			called := false
			result, err := Run(context.Background(), store, delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
				called = true
				return contracts.ModelResult{Text: "unexpected model call"}, nil
			}), RunOptions{Task: "startup hook failure", Workspace: workspace})
			if err == nil || called || result.Result.OK {
				t.Fatalf("start failure was ignored: %+v called=%v err=%v", result, called, err)
			}
			cut, err := store.CaptureConversationCut(context.Background(), result.ExecutionID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			if err := store.WalkConversationCut(context.Background(), cut, func(f contracts.ExecutionFact) error {
				if f.Conversation.EntityKind == "notice" && f.Name == "hook/"+phase && strings.Contains(f.Text, "START_HOOK_FAILED") {
					found = true
				}
				return nil
			}); err != nil || !found {
				t.Fatalf("start hook notice missing from conversation: found=%v err=%v", found, err)
			}
			if body, err := os.ReadFile(filepath.Join(workspace, "startup-stop-"+result.ExecutionID)); err != nil || string(body) != "STOP_COMPLETED" {
				t.Fatalf("startup failure lost runtimeStop: %s %v", body, err)
			}
		})
	}
}

func TestLifecycleAfterTurnUsesSavedOutcomeWhenAdoptionConflicts(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function afterTurn({runtime,outcome,settlement},api){await api.writeFile({path:runtime.executionId+'-after.json',text:JSON.stringify({outcome,settlement})})}`)
	var sessionID string
	firstProvider := delegationProvider(func(ctx context.Context, _ contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		// A concurrent direct-run producer commits the captured Session first.
		_, err := Run(ctx, store, delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
			return contracts.ModelResult{Text: "concurrent answer"}, nil
		}), RunOptions{Task: "concurrent task", SessionID: sessionID})
		if err != nil {
			return contracts.ModelResult{}, err
		}
		return contracts.ModelResult{Text: "first saved answer"}, nil
	})
	result, err := Run(context.Background(), store, firstProvider, RunOptions{Task: "first task", Workspace: workspace, OnSession: func(id string) { sessionID = id }})
	if !errors.Is(err, history.ErrRevisionConflict) {
		t.Fatalf("missing captured revision conflict: %+v %v", result, err)
	}
	terminal, err := store.TerminalResult(context.Background(), result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(workspace, result.ExecutionID+"-after.json"))
	if err != nil {
		t.Fatal(err)
	}
	var after struct {
		Outcome    json.RawMessage          `json:"outcome"`
		Settlement contracts.HookSettlement `json:"settlement"`
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	savedJSON, _ := contracts.EncodeCanonicalJSON(terminal.RuntimeOutcome)
	hookJSON, _ := contracts.EncodeCanonicalJSON(after.Outcome)
	if string(savedJSON) != string(hookJSON) || after.Settlement.Adoption != "non_canonical" || after.Settlement.AdoptionError == "" {
		t.Fatalf("afterTurn changed saved outcome or lost adoption failure: %s", body)
	}
}

func TestLifecycleEmptyHookExportsAreRecorded(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, "// No lifecycle or tool hooks are defined.\n")
	result, err := Run(context.Background(), store, delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		return contracts.ModelResult{Text: "no hooks"}, nil
	}), RunOptions{Task: "no hooks", Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	records, err := store.Records(context.Background(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		var observation contracts.Observation
		_ = json.Unmarshal(record.Payload, &observation)
		if observation.Kind == "hook" && observation.Name == "handlers" && observation.Phase == "configured" {
			found = strings.Contains(string(observation.Result), `"handlers":[]`)
		}
	}
	if !found {
		t.Fatal("empty module exports were indistinguishable from unevaluated hooks")
	}
}
