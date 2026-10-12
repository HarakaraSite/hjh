package jsruntime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"hjh/internal/jsruntime"
)

type testAgentRunner func(context.Context, contracts.AgentRunRequest) (contracts.AgentRunResult, error)

func (run testAgentRunner) RunAgent(ctx context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
	return run(ctx, request)
}

type testAgentController struct {
	spawn   func(context.Context, contracts.AgentRunRequest) (contracts.AgentSpawnResult, error)
	status  func(context.Context, contracts.AgentQuery) (contracts.AgentStatus, error)
	steer   func(context.Context, contracts.AgentSteerRequest) (contracts.ChildInstructionReceipt, error)
	collect func(context.Context, contracts.AgentQuery) (contracts.AgentRunResult, error)
	cancel  func(context.Context, contracts.AgentQuery) (contracts.AgentRunResult, error)
}

func (c testAgentController) SpawnAgent(ctx context.Context, request contracts.AgentRunRequest) (contracts.AgentSpawnResult, error) {
	return c.spawn(ctx, request)
}
func (c testAgentController) StatusAgent(ctx context.Context, query contracts.AgentQuery) (contracts.AgentStatus, error) {
	return c.status(ctx, query)
}
func (c testAgentController) SteerAgent(ctx context.Context, request contracts.AgentSteerRequest) (contracts.ChildInstructionReceipt, error) {
	return c.steer(ctx, request)
}
func (c testAgentController) CollectAgent(ctx context.Context, query contracts.AgentQuery) (contracts.AgentRunResult, error) {
	return c.collect(ctx, query)
}
func (c testAgentController) CancelAgent(ctx context.Context, query contracts.AgentQuery) (contracts.AgentRunResult, error) {
	return c.cancel(ctx, query)
}

func TestAgentControlBridgeUsesSelectedToolsAndDedicatedCallIDs(t *testing.T) {
	dir := makeExtensions(t)
	configPath := filepath.Join(dir, "config.json")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config extensions.Config
	if err = json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	config.Roles["async"] = extensions.Role{Revision: "1", Tools: []string{"run_js", "spawn_agent", "agent_status", "steer_agent", "collect_agent", "cancel_agent"}}
	configBytes, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(dir, "async")
	if err != nil {
		t.Fatal(err)
	}
	var received contracts.AgentRunRequest
	var receivedSteer contracts.AgentSteerRequest
	var queries []contracts.AgentQuery
	controller := testAgentController{
		spawn: func(_ context.Context, request contracts.AgentRunRequest) (contracts.AgentSpawnResult, error) {
			received = request
			return contracts.AgentSpawnResult{ExecutionID: "child-1", State: "starting"}, nil
		},
		status: func(_ context.Context, query contracts.AgentQuery) (contracts.AgentStatus, error) {
			queries = append(queries, query)
			return contracts.AgentStatus{ExecutionID: query.ExecutionID, State: "running", Role: "reviewer",
				Model: contracts.ModelSelection{Provider: "provider-a", ModelID: "model-a", Effort: "low"}, Tools: []string{"run_js"},
				Progress: contracts.AgentProgress{Phase: "model", ModelStep: 2}}, nil
		},
		steer: func(_ context.Context, request contracts.AgentSteerRequest) (contracts.ChildInstructionReceipt, error) {
			receivedSteer = request
			return contracts.ChildInstructionReceipt{InstructionID: "instruction-1", ExecutionID: request.ExecutionID,
				ParentExecutionID: "parent-1", CallID: request.CallID, Sequence: 1, Status: "accepted", ContentDigest: "sha256:content"}, nil
		},
		collect: func(_ context.Context, query contracts.AgentQuery) (contracts.AgentRunResult, error) {
			queries = append(queries, query)
			return contracts.AgentRunResult{ExecutionID: query.ExecutionID, Outcome: "completed", FinalText: "finished"}, nil
		},
		cancel: func(_ context.Context, query contracts.AgentQuery) (contracts.AgentRunResult, error) {
			queries = append(queries, query)
			return contracts.AgentRunResult{ExecutionID: query.ExecutionID, Outcome: "cancelled"}, nil
		},
	}
	var observations []contracts.Observation
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, AgentControl: controller,
		Observer: func(observation contracts.Observation) error {
			observations = append(observations, observation)
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	result, err := worker.Call(context.Background(), "run_js", json.RawMessage(`{"code":"const spawned=await api.agents.spawn({task:'inspect',role:'reviewer',model:{provider:'provider-b',modelId:'model-b',effort:'medium'},tools:[]}); const steered=await api.agents.steer({executionId:spawned.executionId,text:'include the deadline'}); const status=await api.agents.status({executionId:spawned.executionId}); const collected=await api.agents.collect({executionId:spawned.executionId}); const cancelled=await api.agents.cancel({executionId:'child-2'}); return {methods:Object.keys(api.agents).sort(),spawned,steered,status,collected,cancelled};"}`))
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Methods   []string                          `json:"methods"`
		Spawned   contracts.AgentSpawnResult        `json:"spawned"`
		Steered   contracts.ChildInstructionReceipt `json:"steered"`
		Status    contracts.AgentStatus             `json:"status"`
		Collected contracts.AgentRunResult          `json:"collected"`
		Cancelled contracts.AgentRunResult          `json:"cancelled"`
	}
	if err = json.Unmarshal(result, &value); err != nil {
		t.Fatal(err)
	}
	if strings.Join(value.Methods, ",") != "cancel,collect,spawn,status,steer" || value.Spawned.ExecutionID != "child-1" ||
		value.Steered.Status != "accepted" || value.Steered.CallID == "" ||
		value.Status.State != "running" || value.Status.Progress.ModelStep != 2 || value.Collected.FinalText != "finished" ||
		value.Cancelled.Outcome != "cancelled" {
		t.Fatalf("agent control bridge returned unexpected values: %s", result)
	}
	if received.Task != "inspect" || received.Role != "reviewer" || received.Model == nil || received.Model.ModelID != "model-b" ||
		received.Tools == nil || len(*received.Tools) != 0 || received.CallID == "" {
		t.Fatalf("spawn did not preserve selected inputs and call ID: %+v", received)
	}
	if receivedSteer.ExecutionID != "child-1" || receivedSteer.Text != "include the deadline" || receivedSteer.CallID == "" {
		t.Fatalf("steer did not preserve child, text, and call ID: %+v", receivedSteer)
	}
	if len(queries) != 3 {
		t.Fatalf("controller received %d queries, want status, collect, cancel", len(queries))
	}
	for i, query := range queries {
		if query.CallID == "" || (i < 2 && query.ExecutionID != "child-1") {
			t.Fatalf("controller query %d has incomplete identifiers: %+v", i, query)
		}
	}
	wantOperations := map[string]bool{"agents.spawn": false, "agents.status": false, "agents.steer": false, "agents.collect": false, "agents.cancel": false}
	starts := map[string]string{}
	for _, observation := range observations {
		if observation.Kind != "agent" {
			continue
		}
		if _, ok := wantOperations[observation.Name]; !ok {
			continue
		}
		if observation.Phase == "started" {
			starts[observation.ID] = observation.Name
		} else if starts[observation.ID] == observation.Name {
			wantOperations[observation.Name] = true
		}
	}
	for operation, seen := range wantOperations {
		if !seen {
			t.Errorf("operation %s lacks matching agent start and completion observations: %+v", operation, observations)
		}
	}
}

func TestDirectRunAgentRecordsActualCompletedResult(t *testing.T) {
	snapshot, err := extensions.CaptureDir(makeExtensions(t))
	if err != nil {
		t.Fatal(err)
	}
	var observed []contracts.Observation
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{
		Snapshot: snapshot,
		Observer: func(o contracts.Observation) error { observed = append(observed, o); return nil },
		Agents: testAgentRunner(func(_ context.Context, r contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
			return contracts.AgentRunResult{ExecutionID: "direct-child", Outcome: "completed", FinalText: "found it"}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	value, err := worker.Call(context.Background(), "run_agent", json.RawMessage(`{"role":"reviewer","task":"inspect source"}`))
	if err != nil {
		t.Fatal(err)
	}
	var start, end *contracts.Observation
	for i := range observed {
		o := &observed[i]
		if o.Kind != "tool" || o.Name != "run_agent" {
			continue
		}
		if o.Phase == "started" {
			start = o
		} else {
			end = o
		}
	}
	if start == nil || end == nil || start.ID != end.ID || end.Phase != "completed" || end.Error != "" || string(end.Result) != string(value) {
		t.Fatalf("direct successful tool has incorrect terminal record: value=%s observations=%+v", value, observed)
	}
}

func TestAgentRunReturnsResultAndUsesDedicatedCallID(t *testing.T) {
	snapshot, err := extensions.CaptureDir(makeExtensions(t))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var observations []contracts.Observation
	var received contracts.AgentRunRequest
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{
		Snapshot: snapshot,
		Observer: func(observation contracts.Observation) error {
			mu.Lock()
			observations = append(observations, observation)
			mu.Unlock()
			return nil
		},
		Agents: testAgentRunner(func(_ context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
			received = request
			return contracts.AgentRunResult{ExecutionID: "child-123", Outcome: "completed", FinalText: "found it"}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	result, err := worker.Call(context.Background(), "run_js", json.RawMessage(`{"code":"return await api.agents.run({role:'reviewer',task:'inspect source',model:{provider:'provider-b',modelId:'model-b',effort:'low'},tools:['run_js','read_file']});"}`))
	if err != nil {
		t.Fatal(err)
	}
	var child contracts.AgentRunResult
	if err = json.Unmarshal(result, &child); err != nil {
		t.Fatal(err)
	}
	if child.ExecutionID != "child-123" || child.Outcome != "completed" || child.FinalText != "found it" {
		t.Fatalf("api.agents.run returned unexpected result: %s", result)
	}
	if received.Task != "inspect source" || received.Role != "reviewer" || received.Model == nil ||
		received.Model.Provider != "provider-b" || received.Model.ModelID != "model-b" || received.Model.Effort != "low" ||
		received.Tools == nil || len(*received.Tools) != 2 || (*received.Tools)[0] != "run_js" || (*received.Tools)[1] != "read_file" || received.CallID == "" {
		t.Fatalf("AgentRunner received incomplete request: %+v", received)
	}

	mu.Lock()
	defer mu.Unlock()
	var start, end *contracts.Observation
	for i := range observations {
		observation := &observations[i]
		if observation.Kind == "agent" && observation.Name == "agents.run" && observation.ID == received.CallID {
			if observation.Phase == "started" {
				start = observation
			} else {
				end = observation
			}
		}
	}
	if start == nil || end == nil || end.Phase != "completed" {
		t.Fatalf("agent operation was not observed with the dedicated call ID: %+v", observations)
	}
	var delegationStart, delegationEnd *contracts.Observation
	for i := range observations {
		observation := &observations[i]
		if observation.Kind == "tool" && observation.Name == "run_agent" {
			if observation.Phase == "started" {
				delegationStart = observation
			} else {
				delegationEnd = observation
			}
		}
	}
	if delegationStart == nil || delegationEnd == nil || delegationEnd.ID != delegationStart.ID || delegationEnd.Phase != "completed" || end.Parent != delegationStart.ID {
		t.Fatalf("api.agents.run did not use the selected run_agent tool path: %+v", observations)
	}
}

func TestUnawaitedAgentIsCancelledAndJoinedOnSettlement(t *testing.T) {
	for _, mode := range []string{"normal", "rejection", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			dir := makeExtensions(t)
			if err := os.WriteFile(filepath.Join(dir, "hooks.js"), delegatingHookSource(), 0644); err != nil {
				t.Fatal(err)
			}
			snapshot, err := extensions.CaptureDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			returned := make(chan struct{})
			var mu sync.Mutex
			var observations []contracts.Observation
			worker, err := jsruntime.New(context.Background(), jsruntime.Options{
				Snapshot: snapshot,
				Observer: func(observation contracts.Observation) error {
					mu.Lock()
					observations = append(observations, observation)
					mu.Unlock()
					return nil
				},
				Agents: testAgentRunner(func(ctx context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
					close(started)
					<-ctx.Done()
					defer close(returned)
					return contracts.AgentRunResult{ExecutionID: "child-" + request.CallID, Outcome: "cancelled"}, ctx.Err()
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer worker.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callResult := make(chan error, 1)
			go func() {
				code := `api.agents.run({task:'inspect'}); return 'done';`
				if mode == "rejection" {
					code = `api.agents.run({task:'inspect'}); throw new Error('root failed');`
				} else if mode == "cancellation" {
					code = `api.agents.run({task:'inspect'}); await new Promise(() => {});`
				}
				_, callErr := worker.Call(ctx, "run_js", json.RawMessage(`{"code":`+jsJSON(t, code)+`}`))
				callResult <- callErr
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("child runner did not start")
			}
			if mode == "cancellation" {
				cancel()
			}
			select {
			case callErr := <-callResult:
				if mode == "normal" && callErr != nil {
					t.Fatalf("normal root settlement returned error: %v", callErr)
				}
				if mode == "rejection" && (callErr == nil || !strings.Contains(callErr.Error(), "root failed")) {
					t.Fatalf("root rejection was not preserved: %v", callErr)
				}
				if mode == "cancellation" && !errors.Is(callErr, context.Canceled) {
					t.Fatalf("root cancellation was not preserved: %v", callErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Worker.Call did not join its child runner")
			}
			select {
			case <-returned:
			default:
				t.Fatal("Worker.Call returned before the child runner returned")
			}

			mu.Lock()
			var childStart, childEnd, rootEnd *contracts.Observation
			for i := range observations {
				observation := &observations[i]
				if observation.Name == "agents.run" && observation.Kind == "agent" {
					if observation.Phase == "started" {
						childStart = observation
					} else {
						childEnd = observation
					}
				}
				if observation.Name == "run_js" && observation.Kind == "tool" && observation.Phase != "started" {
					rootEnd = observation
				}
			}
			mu.Unlock()
			if childStart == nil || childEnd == nil || childEnd.Phase != "cancelled" {
				t.Fatalf("unawaited child was not recorded as cancelled: %+v", observations)
			}
			var childResult contracts.AgentRunResult
			if err = json.Unmarshal(childEnd.Result, &childResult); err != nil || childResult.ExecutionID == "" || childResult.Outcome != "cancelled" {
				t.Fatalf("cancelled child result was not retained in its observation: %s (%v)", childEnd.Result, err)
			}
			if rootEnd != nil && childEnd.Time.After(rootEnd.Time) {
				t.Fatal("root tool ended before the child operation was joined")
			}
			var wrapperStart, wrapperEnd, hookStart, hookEnd *contracts.Observation
			for i := range observations {
				observation := &observations[i]
				if observation.Kind == "tool" && observation.Name == "run_agent" {
					if observation.Phase == "started" {
						wrapperStart = observation
					} else {
						wrapperEnd = observation
					}
				}
				if observation.Kind == "hook" && observation.Name == "beforeTool" {
					if observation.Phase == "started" {
						hookStart = observation
					} else if hookStart != nil && observation.ID == hookStart.ID {
						hookEnd = observation
					}
				}
			}
			if wrapperStart == nil || wrapperEnd == nil || wrapperEnd.ID != wrapperStart.ID || wrapperEnd.Phase != "cancelled" {
				t.Fatalf("unawaited run_agent tool observation was not owner-closed as cancelled: %+v", observations)
			}
			if hookStart == nil || hookEnd == nil || hookEnd.ID != hookStart.ID || hookEnd.Phase != "cancelled" {
				t.Fatalf("unawaited delegation hook observation was not owner-closed as cancelled: %+v", observations)
			}
			if hookEnd.Time.After(wrapperEnd.Time) {
				t.Fatal("run_agent observation ended before its active beforeTool hook")
			}
		})
	}
}

func TestLateDelegationFromBeforeToolIsClosedBeforeNextCall(t *testing.T) {
	dir := makeExtensions(t)
	if err := os.WriteFile(filepath.Join(dir, "hooks.js"), delayedDelegationHookSource(), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err = os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var observations []contracts.Observation
	agentRequests := make(chan contracts.AgentRunRequest, 4)
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{
		Snapshot:  snapshot,
		Workspace: workspace,
		Observer: func(observation contracts.Observation) error {
			mu.Lock()
			observations = append(observations, observation)
			mu.Unlock()
			return nil
		},
		Agents: testAgentRunner(func(ctx context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
			agentRequests <- request
			<-ctx.Done()
			return contracts.AgentRunResult{ExecutionID: "child-" + request.CallID, Outcome: "cancelled"}, ctx.Err()
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	result, err := worker.Call(context.Background(), "run_js", json.RawMessage(`{"code":"api.agents.run({task:'late child'}); return 'outer result';"}`))
	if err != nil || string(result) != `"outer result"` {
		t.Fatalf("outer result was not preserved: %s (%v)", result, err)
	}
	mu.Lock()
	starts := map[string]contracts.Observation{}
	ends := map[string]contracts.Observation{}
	for _, observation := range observations {
		if observation.Phase == "started" {
			starts[observation.ID] = observation
		} else {
			ends[observation.ID] = observation
		}
	}
	var runAgentID, hookID string
	for id, observation := range starts {
		if observation.Kind == "tool" && observation.Name == "run_agent" {
			runAgentID = id
		}
	}
	for id, observation := range starts {
		if observation.Kind == "hook" && observation.Name == "beforeTool" && observation.Parent == runAgentID {
			hookID = id
		}
	}
	for _, id := range []string{runAgentID, hookID} {
		if id == "" {
			t.Errorf("delegation tool/hook start was not observed: %+v", observations)
			continue
		}
		end, ok := ends[id]
		if !ok {
			t.Errorf("delegation observation %s was not terminal before the next call: %+v", id, observations)
		} else if id == runAgentID && end.Phase != "cancelled" {
			t.Errorf("run_agent observation was not owner-closed as cancelled before the next call: %+v", observations)
		} else if id == hookID && end.Phase != "completed" {
			t.Errorf("settled beforeTool observation did not retain its completed outcome: %+v", observations)
		}
	}
	mu.Unlock()
	var firstRequest *contracts.AgentRunRequest
	select {
	case request := <-agentRequests:
		firstRequest = &request
	default:
	}

	second, secondErr := worker.Call(context.Background(), "read_file", json.RawMessage(`{"path":"source.txt"}`))
	if secondErr != nil || !strings.Contains(string(second), `"text":"source"`) {
		t.Fatalf("the next Worker.Call failed: %s (%v)", second, secondErr)
	}
	var leakedRequest *contracts.AgentRunRequest
	select {
	case request := <-agentRequests:
		leakedRequest = &request
	default:
	}
	if firstRequest == nil {
		t.Errorf("the pending beforeTool continuation started its child only after the next Worker.Call: leaked request=%+v", leakedRequest)
	} else if firstRequest.Task != "late child" {
		t.Errorf("beforeTool started an unexpected child: %+v", firstRequest)
	}
	if leakedRequest != nil {
		t.Errorf("the previous Worker.Call started a child during the next call: %+v", leakedRequest)
	}
}

func TestDelegationAfterToolHookIsClosedByOwner(t *testing.T) {
	dir := makeExtensions(t)
	if err := os.WriteFile(filepath.Join(dir, "hooks.js"), stalledDelegationAfterToolHookSource(), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err = os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	hostStarted := make(chan struct{})
	releaseHost := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHost) }) }
	defer release()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(hostStarted)
		<-releaseHost
		_, _ = w.Write([]byte("ready"))
	}))
	defer server.Close()
	var mu sync.Mutex
	var observations []contracts.Observation
	agentRequests := make(chan contracts.AgentRunRequest, 2)
	afterToolStarted := make(chan struct{}, 1)
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{
		Snapshot:  snapshot,
		Workspace: workspace,
		Observer: func(observation contracts.Observation) error {
			mu.Lock()
			observations = append(observations, observation)
			mu.Unlock()
			if observation.Kind == "hook" && observation.Name == "afterTool" && observation.Phase == "started" && strings.Contains(string(observation.Input), `"name":"run_agent"`) {
				select {
				case afterToolStarted <- struct{}{}:
				default:
				}
			}
			return nil
		},
		Agents: testAgentRunner(func(_ context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
			agentRequests <- request
			return contracts.AgentRunResult{ExecutionID: "child-" + request.CallID, Outcome: "completed"}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	code := `api.agents.run({task:'afterTool child'}); await tools.http({url:` + jsJSON(t, server.URL) + `}); return 'outer result';`
	arguments, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	callResult := make(chan callResultForTest, 1)
	go func() {
		result, callErr := worker.Call(context.Background(), "run_js", arguments)
		callResult <- callResultForTest{value: result, err: callErr}
	}()
	select {
	case <-hostStarted:
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("outer host request did not start")
	}
	select {
	case <-afterToolStarted:
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("run_agent afterTool did not start after child completion")
	}
	release()
	select {
	case result := <-callResult:
		if result.err != nil || string(result.value) != `"outer result"` {
			t.Fatalf("outer result was not preserved: %s (%v)", result.value, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Worker.Call did not finish after the outer host request was released")
	}
	mu.Lock()
	starts := map[string]contracts.Observation{}
	ends := map[string]contracts.Observation{}
	for _, observation := range observations {
		if observation.Phase == "started" {
			starts[observation.ID] = observation
		} else {
			ends[observation.ID] = observation
		}
	}
	var runAgentID, afterToolID string
	var childEnd *contracts.Observation
	for id, observation := range starts {
		if observation.Kind == "tool" && observation.Name == "run_agent" {
			runAgentID = id
		}
	}
	for id, observation := range ends {
		if start, ok := starts[id]; ok && start.Kind == "agent" && start.Name == "agents.run" {
			copy := observation
			childEnd = &copy
		}
	}
	for id, observation := range starts {
		if observation.Kind == "hook" && observation.Name == "afterTool" && observation.Parent == runAgentID {
			afterToolID = id
		}
	}
	for _, id := range []string{runAgentID, afterToolID} {
		if id == "" {
			t.Errorf("delegation tool/afterTool hook start was not observed: %+v", observations)
			continue
		}
		if end, ok := ends[id]; !ok || end.Phase != "cancelled" {
			t.Errorf("unfinished delegation observation %s was not owner-closed as cancelled before the next call: %+v", id, observations)
		}
	}
	if childEnd == nil || childEnd.Phase != "completed" {
		t.Errorf("owner closure did not preserve the completed child result: %+v", observations)
	}
	mu.Unlock()
	select {
	case request := <-agentRequests:
		if request.Task != "afterTool child" {
			t.Fatalf("afterTool started an unexpected child: %+v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unawaited run_agent did not start")
	}

	second, secondErr := worker.Call(context.Background(), "read_file", json.RawMessage(`{"path":"source.txt"}`))
	if secondErr != nil || !strings.Contains(string(second), `"text":"source"`) {
		t.Fatalf("the next Worker.Call failed: %s (%v)", second, secondErr)
	}
	select {
	case request := <-agentRequests:
		t.Errorf("the previous Worker.Call started another child during the next call: %+v", request)
	default:
	}

}

func TestUnstartedDelegationBeforeToolIsClosedByOwner(t *testing.T) {
	dir := makeExtensions(t)
	if err := os.WriteFile(filepath.Join(dir, "hooks.js"), stalledDelegationBeforeToolHookSource(), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err = os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var observations []contracts.Observation
	agentRequests := make(chan contracts.AgentRunRequest, 1)
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{
		Snapshot:  snapshot,
		Workspace: workspace,
		Observer: func(observation contracts.Observation) error {
			mu.Lock()
			observations = append(observations, observation)
			mu.Unlock()
			return nil
		},
		Agents: testAgentRunner(func(_ context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
			agentRequests <- request
			return contracts.AgentRunResult{ExecutionID: "child-" + request.CallID, Outcome: "completed"}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	result, err := worker.Call(context.Background(), "run_js", json.RawMessage(`{"code":"api.agents.run({task:'unstarted child'}); return 'outer result';"}`))
	if err != nil || string(result) != `"outer result"` {
		t.Fatalf("outer result was not preserved: %s (%v)", result, err)
	}
	mu.Lock()
	starts := map[string]contracts.Observation{}
	ends := map[string]contracts.Observation{}
	for _, observation := range observations {
		if observation.Phase == "started" {
			starts[observation.ID] = observation
		} else {
			ends[observation.ID] = observation
		}
	}
	var runAgentID, hookID string
	for id, observation := range starts {
		if observation.Kind == "tool" && observation.Name == "run_agent" {
			runAgentID = id
		}
	}
	for id, observation := range starts {
		if observation.Kind == "hook" && observation.Name == "beforeTool" && observation.Parent == runAgentID {
			hookID = id
		}
	}
	for _, id := range []string{runAgentID, hookID} {
		if id == "" {
			t.Errorf("unstarted delegation tool/hook start was not observed: %+v", observations)
			continue
		}
		if end, ok := ends[id]; !ok || end.Phase != "cancelled" {
			t.Errorf("unstarted delegation observation %s was not owner-closed as cancelled: %+v", id, observations)
		}
	}
	mu.Unlock()
	select {
	case request := <-agentRequests:
		t.Errorf("unsettled beforeTool unexpectedly started a child: %+v", request)
	default:
	}

	second, secondErr := worker.Call(context.Background(), "read_file", json.RawMessage(`{"path":"source.txt"}`))
	if secondErr != nil || !strings.Contains(string(second), `"text":"source"`) {
		t.Fatalf("the next Worker.Call failed: %s (%v)", second, secondErr)
	}
	select {
	case request := <-agentRequests:
		t.Errorf("the previous Worker.Call started a child during the next call: %+v", request)
	default:
	}
}

func TestAgentRunsCanCompleteConcurrentlyWithPromiseAll(t *testing.T) {
	snapshot, err := extensions.CaptureDir(makeExtensions(t))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan contracts.AgentRunRequest, 2)
	release := make(chan struct{})
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Agents: testAgentRunner(func(ctx context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
		started <- request
		select {
		case <-release:
			return contracts.AgentRunResult{ExecutionID: "child-" + request.Task, Outcome: "completed"}, nil
		case <-ctx.Done():
			return contracts.AgentRunResult{ExecutionID: "child-" + request.Task, Outcome: "cancelled"}, ctx.Err()
		}
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	resultCh := make(chan callResultForTest, 1)
	go func() {
		result, callErr := worker.Call(context.Background(), "run_js", json.RawMessage(`{"code":"return await Promise.all([api.agents.run({task:'alpha'}), api.agents.run({task:'beta'})]);"}`))
		resultCh <- callResultForTest{value: result, err: callErr}
	}()
	requests := make([]contracts.AgentRunRequest, 0, 2)
	for range 2 {
		select {
		case request := <-started:
			requests = append(requests, request)
		case <-time.After(3 * time.Second):
			t.Fatal("Promise.all did not start both child runners concurrently")
		}
	}
	if requests[0].CallID == requests[1].CallID || requests[0].Task == requests[1].Task {
		t.Fatalf("child requests did not retain separate task and call identities: %+v", requests)
	}
	close(release)
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !strings.Contains(string(result.value), `"child-alpha"`) || !strings.Contains(string(result.value), `"child-beta"`) {
			t.Fatalf("Promise.all did not return both child results: %s", result.value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Promise.all did not settle after both child runners completed")
	}
}

func TestSelectedToolsControlGeneratedRuntimeAPI(t *testing.T) {
	dir := makeExtensions(t)
	snapshot, err := extensions.CaptureDir(dir, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	definitions := worker.Definitions()
	if len(definitions) != 2 || definitions[0].Name != "run_js" || definitions[1].Name != "read_file" {
		t.Fatalf("Worker definitions do not match the selected reviewer tools: %+v", definitions)
	}
	result, err := worker.Call(context.Background(), "run_js", json.RawMessage(`{"code":"const nested=await tools.run_js({code:'return 42;'}); return {api:Object.keys(api).sort(),tools:Object.keys(tools).sort(),hasAgents:'agents' in api,hasRawHost:'readFile' in api || 'command' in api,nested};"}`))
	if err != nil {
		t.Fatal(err)
	}
	var exposed struct {
		API        []string `json:"api"`
		Tools      []string `json:"tools"`
		HasAgents  bool     `json:"hasAgents"`
		HasRawHost bool     `json:"hasRawHost"`
		Nested     int      `json:"nested"`
	}
	if err = json.Unmarshal(result, &exposed); err != nil {
		t.Fatal(err)
	}
	if strings.Join(exposed.API, ",") != "tools" || strings.Join(exposed.Tools, ",") != "read_file,run_js" || exposed.HasAgents || exposed.HasRawHost || exposed.Nested != 42 {
		t.Fatalf("generated JS did not receive the selected tool API: %s", result)
	}
	if _, err = worker.Call(context.Background(), "run_agent", json.RawMessage(`{"task":"unavailable"}`)); err == nil || !strings.Contains(err.Error(), "unknown tool: run_agent") {
		t.Fatalf("unselected run_agent remained callable: %v", err)
	}

	configPath := filepath.Join(dir, "config.json")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config extensions.Config
	if err = json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	config.Roles["no-js"] = extensions.Role{Revision: "1", Tools: []string{"read_file"}}
	configBytes, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0644); err != nil {
		t.Fatal(err)
	}
	withoutJS, err := extensions.CaptureDir(dir, "no-js")
	if err != nil {
		t.Fatal(err)
	}
	workerWithoutJS, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: withoutJS})
	if err != nil {
		t.Fatal(err)
	}
	defer workerWithoutJS.Close()
	if definitions = workerWithoutJS.Definitions(); len(definitions) != 1 || definitions[0].Name != "read_file" {
		t.Fatalf("unselected run_js remained in Worker definitions: %+v", definitions)
	}
	if _, err = workerWithoutJS.Call(context.Background(), "run_js", json.RawMessage(`{"code":"return 42;"}`)); err == nil || !strings.Contains(err.Error(), "unknown tool: run_js") {
		t.Fatalf("unselected run_js remained callable: %v", err)
	}
}

func TestRootSettlementStillDrainsPendingHostOperations(t *testing.T) {
	snapshot, err := extensions.CaptureDir(makeExtensions(t))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("ready"))
	}))
	defer server.Close()
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	code := `tools.http({url:` + jsJSON(t, server.URL) + `}); return "done";`
	arguments, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan callResultForTest, 1)
	go func() {
		result, callErr := worker.Call(context.Background(), "run_js", arguments)
		resultCh <- callResultForTest{value: result, err: callErr}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("pending host request did not start")
	}
	select {
	case result := <-resultCh:
		close(release)
		t.Fatalf("Worker.Call returned before the host request drained: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case result := <-resultCh:
		if result.err != nil || string(result.value) != `"done"` {
			t.Fatalf("Worker.Call returned unexpected result after host drain: %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Worker.Call did not return after the host request drained")
	}
}

type callResultForTest struct {
	value json.RawMessage
	err   error
}

func jsJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestWorkerKeepsVMAndCapturedModulesAcrossCalls(t *testing.T) {
	extensionDir := makeExtensions(t)
	var mu sync.Mutex
	var observations []contracts.Observation
	observer := func(observation contracts.Observation) error {
		mu.Lock()
		observations = append(observations, observation)
		mu.Unlock()
		return nil
	}
	snapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: t.TempDir(), Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	ctx := contracts.WithCallParent(context.Background(), "model-call-first")
	first, err := worker.Call(ctx, "run_js", json.RawMessage(`{"code":"const write = await tools.write_file({path:'result.txt', text:'alpha alpha'}); const edit = await tools.edit_file({path:'result.txt', oldText:'alpha', newText:'beta'}); const read = await tools.read_file({path:'result.txt'}); const count = await tools.count({marker:'first'}); return {write, edit, read, count};"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Edit struct {
			Replacements int `json:"replacements"`
		} `json:"edit"`
		Read struct {
			Text string `json:"text"`
		} `json:"read"`
		Count struct {
			Calls                     int    `json:"calls"`
			Revision                  string `json:"revision"`
			HookRevision              string `json:"hookRevision"`
			ImplementationHasReadFile bool   `json:"implementationHasReadFile"`
			ImplementationHasHost     bool   `json:"implementationHasHost"`
			HookHasReadFile           bool   `json:"hookHasReadFile"`
			HookHasHost               bool   `json:"hookHasHost"`
		} `json:"count"`
	}
	if err = json.Unmarshal(first, &result); err != nil {
		t.Fatal(err)
	}
	if result.Edit.Replacements != 2 || result.Read.Text != "beta beta" || result.Count.Calls != 1 || result.Count.Revision != "module-one" || result.Count.HookRevision != "hook-one" || !result.Count.ImplementationHasReadFile || !result.Count.ImplementationHasHost || !result.Count.HookHasReadFile || !result.Count.HookHasHost {
		t.Fatalf("generated JS did not compose captured tools: %s", first)
	}
	second, err := worker.Call(context.Background(), "count", json.RawMessage(`{"marker":"second"}`))
	if err != nil {
		t.Fatal(err)
	}
	var secondResult struct {
		Calls        int    `json:"calls"`
		Revision     string `json:"revision"`
		HookRevision string `json:"hookRevision"`
	}
	if err = json.Unmarshal(second, &secondResult); err != nil {
		t.Fatal(err)
	}
	if secondResult.Calls != 2 || secondResult.Revision != "module-one" || secondResult.HookRevision != "hook-one" {
		t.Fatalf("Worker.Call did not reuse its VM or hook capture: %s", second)
	}

	if err = os.WriteFile(filepath.Join(extensionDir, "count-state.js"), []byte(`export const revision = "module-two";`), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(extensionDir, "hooks.js"), hookSource("hook-two"), 0644); err != nil {
		t.Fatal(err)
	}
	frozen, err := worker.Call(context.Background(), "count", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(frozen), `"revision":"module-one"`) || !strings.Contains(string(frozen), `"hookRevision":"hook-one"`) {
		t.Fatalf("an edit changed source already captured by this Worker: %s", frozen)
	}

	newSnapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	newWorker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: newSnapshot, Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer newWorker.Close()
	updated, err := newWorker.Call(context.Background(), "count", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), `"revision":"module-two"`) || !strings.Contains(string(updated), `"hookRevision":"hook-two"`) {
		t.Fatalf("the next Worker did not capture edited source: %s", updated)
	}

	mu.Lock()
	defer mu.Unlock()
	starts := map[string]contracts.Observation{}
	ends := map[string]contracts.Observation{}
	for _, observation := range observations {
		if observation.Phase == "started" {
			starts[observation.ID] = observation
		} else {
			ends[observation.ID] = observation
		}
	}
	var runID, writeID string
	for id, observation := range starts {
		if observation.Name == "run_js" && observation.Parent == "model-call-first" {
			runID = id
		}
		if observation.Name == "write_file" && observation.Parent == runID {
			writeID = id
		}
	}
	if runID == "" {
		t.Fatal("run_js observation did not retain its provider call parent")
	}
	if writeID == "" {
		for id, observation := range starts {
			if observation.Name == "write_file" && observation.Parent == runID {
				writeID = id
			}
		}
	}
	if writeID == "" {
		t.Fatal("inner tool observation was not linked to run_js")
	}
	var hostLinked bool
	for id, observation := range starts {
		if observation.Kind == "host" && observation.Name == "writeFile" && observation.Parent == writeID {
			hostLinked = true
		}
		if _, ok := ends[id]; !ok {
			t.Errorf("observation %s (%s/%s) has no ending event", id, observation.Kind, observation.Name)
		}
	}
	if !hostLinked {
		t.Fatal("host file operation was not linked to its external tool")
	}
}

func TestLifecycleHooksShareModuleHostAndNestedToolHooks(t *testing.T) {
	extensionDir := makeExtensions(t)
	hookSource := []byte(`let lifecycleCalls = 0;
export async function runtimeStart({ runtime }, api) {
  lifecycleCalls++;
  const note = await api.readFile({ path: 'note.txt' });
  const nested = await api.tools.count({ marker: 'from-runtimeStart' });
  return { kind: 'runtimeStart', calls: lifecycleCalls, startedAt: runtime.startedAt, note: note.text, nested };
}
export async function beforeTurn({ task }) { lifecycleCalls++; return { kind: 'beforeTurn', calls: lifecycleCalls, task }; }
export async function afterTurn({ outcome }) { lifecycleCalls++; return { kind: 'afterTurn', calls: lifecycleCalls, outcome }; }
export async function runtimeStop({ reason }) { lifecycleCalls++; return { kind: 'runtimeStop', calls: lifecycleCalls, reason }; }
export async function beforeTool({ name, args }) { return name === 'count' ? { ...args, lifecycleNested: true } : args; }
export async function afterTool({ name, args, result }) { return name === 'count' ? { ...result, lifecycleNested: args.lifecycleNested, lifecycleAfter: true } : result; }
export async function helper() { return 'not a handler'; }
`)
	if err := os.WriteFile(filepath.Join(extensionDir, "hooks.js"), hookSource, 0644); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "note.txt"), []byte("hook host read"), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	var observations []contracts.Observation
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{
		Snapshot: snapshot, Workspace: workspace, ObserverParent: "execution-1",
		Observer: func(observation contracts.Observation) error {
			observations = append(observations, observation)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	wantHandlers := []string{"runtimeStart", "beforeTurn", "beforeTool", "afterTool", "afterTurn", "runtimeStop"}
	handlers := worker.HookHandlers()
	if strings.Join(handlers, ",") != strings.Join(wantHandlers, ",") {
		t.Fatalf("HookHandlers() = %v, want %v", handlers, wantHandlers)
	}
	handlers[0] = "changed"
	if got := worker.HookHandlers(); strings.Join(got, ",") != strings.Join(wantHandlers, ",") {
		t.Fatalf("HookHandlers() exposed mutable Worker state: %v", got)
	}
	if !worker.Available() {
		t.Fatal("new Worker is not available")
	}

	startRaw, err := worker.Hook(context.Background(), "runtimeStart", json.RawMessage(`{"runtime":{"startedAt":"2026-10-12T09:30:00+09:00","timeZone":"Asia/Tokyo"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var start struct {
		Kind      string `json:"kind"`
		Calls     int    `json:"calls"`
		StartedAt string `json:"startedAt"`
		Note      string `json:"note"`
		Nested    struct {
			Calls           int    `json:"calls"`
			Marker          string `json:"marker"`
			LifecycleNested bool   `json:"lifecycleNested"`
			LifecycleAfter  bool   `json:"lifecycleAfter"`
		} `json:"nested"`
	}
	if err = json.Unmarshal(startRaw, &start); err != nil {
		t.Fatal(err)
	}
	if start.Kind != "runtimeStart" || start.Calls != 1 || start.StartedAt != "2026-10-12T09:30:00+09:00" || start.Note != "hook host read" ||
		start.Nested.Calls != 1 || start.Nested.Marker != "from-runtimeStart" || !start.Nested.LifecycleNested || !start.Nested.LifecycleAfter {
		t.Fatalf("runtimeStart did not preserve module state, host IO, and normal nested tool hooks: %s", startRaw)
	}

	for _, call := range []struct {
		name  string
		input string
		kind  string
		count int
	}{
		{name: "beforeTurn", input: `{"task":"do work"}`, kind: "beforeTurn", count: 2},
		{name: "afterTurn", input: `{"outcome":"completed"}`, kind: "afterTurn", count: 3},
		{name: "runtimeStop", input: `{"reason":"completed"}`, kind: "runtimeStop", count: 4},
	} {
		value, callErr := worker.Hook(context.Background(), call.name, json.RawMessage(call.input))
		if callErr != nil {
			t.Fatalf("Hook(%s): %v", call.name, callErr)
		}
		var result struct {
			Kind  string `json:"kind"`
			Calls int    `json:"calls"`
		}
		if callErr = json.Unmarshal(value, &result); callErr != nil {
			t.Fatal(callErr)
		}
		if result.Kind != call.kind || result.Calls != call.count {
			t.Errorf("Hook(%s) = %s, want kind=%s calls=%d", call.name, value, call.kind, call.count)
		}
	}

	starts := map[string]contracts.Observation{}
	ends := map[string]contracts.Observation{}
	for _, observation := range observations {
		if observation.Phase == "started" {
			starts[observation.ID] = observation
		} else {
			ends[observation.ID] = observation
		}
	}
	var runtimeStartID, countID string
	lifecycleStarts := map[string]int{}
	toolHookStarts := map[string]int{}
	toolHookParents := map[string][]string{}
	for id, observation := range starts {
		if observation.Kind == "hook" && (observation.Name == "runtimeStart" || observation.Name == "beforeTurn" || observation.Name == "afterTurn" || observation.Name == "runtimeStop") {
			lifecycleStarts[observation.Name]++
			if observation.Parent != "execution-1" {
				t.Errorf("lifecycle hook %s has unexpected parent: %+v", observation.Name, observation)
			}
			if observation.Name == "runtimeStart" {
				runtimeStartID = id
			}
		}
		if observation.Kind == "tool" && observation.Name == "count" {
			countID = id
		}
		if observation.Kind == "hook" && (observation.Name == "beforeTool" || observation.Name == "afterTool") {
			toolHookStarts[observation.Name]++
			toolHookParents[observation.Name] = append(toolHookParents[observation.Name], observation.Parent)
		}
		if _, ok := ends[id]; !ok {
			t.Errorf("observation %s (%s/%s) has no ending event", id, observation.Kind, observation.Name)
		}
	}
	for _, name := range []string{"runtimeStart", "beforeTurn", "afterTurn", "runtimeStop"} {
		if lifecycleStarts[name] != 1 {
			t.Errorf("lifecycle observation %s started %d times, want exactly once", name, lifecycleStarts[name])
		}
	}
	if toolHookStarts["beforeTool"] != 1 || toolHookStarts["afterTool"] != 1 {
		t.Errorf("nested count did not receive exactly one before/after hook: %v", toolHookStarts)
	}
	if countID != "" {
		for _, name := range []string{"beforeTool", "afterTool"} {
			if len(toolHookParents[name]) != 1 || toolHookParents[name][0] != countID {
				t.Errorf("tool hook %s parents = %v, want count observation %s", name, toolHookParents[name], countID)
			}
		}
	}
	if runtimeStartID == "" || countID == "" || starts[countID].Parent != runtimeStartID {
		t.Errorf("nested count is not directly parented by the runtimeStart hook: runtimeStart=%q count=%+v", runtimeStartID, starts[countID])
	}
}

func TestAbsentLifecycleHookReturnsNullWithoutObservation(t *testing.T) {
	snapshot, err := extensions.CaptureDir(makeExtensions(t))
	if err != nil {
		t.Fatal(err)
	}
	var observations []contracts.Observation
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot,
		Observer: func(observation contracts.Observation) error {
			observations = append(observations, observation)
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	if got := strings.Join(worker.HookHandlers(), ","); got != "beforeTool,afterTool" {
		t.Fatalf("HookHandlers() = %q, want only callable supported exports", got)
	}
	result, err := worker.Hook(context.Background(), "beforeTurn", json.RawMessage(`{"task":"unused"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "null" || len(observations) != 0 {
		t.Fatalf("absent hook result = %s observations=%+v, want null with no observation", result, observations)
	}
}

func TestLifecycleHookFailureRetainsJavaScriptStack(t *testing.T) {
	extensionDir := makeExtensions(t)
	if err := os.WriteFile(filepath.Join(extensionDir, "hooks.js"), []byte(`function lifecycleFailureFrame() { throw new Error('lifecycle-hook-marker'); }
export async function beforeTurn() { lifecycleFailureFrame(); }`), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	var observations []contracts.Observation
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot,
		Observer: func(observation contracts.Observation) error {
			observations = append(observations, observation)
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	_, err = worker.Hook(context.Background(), "beforeTurn", json.RawMessage(`{"task":"do work"}`))
	if err == nil || !strings.Contains(err.Error(), "lifecycle-hook-marker") || !strings.Contains(err.Error(), "lifecycleFailureFrame") {
		t.Fatalf("lifecycle hook error lost its message or captured stack: %v", err)
	}
	if !worker.Available() {
		t.Fatal("a rejected hook made the Worker unavailable")
	}
	var failed bool
	for _, observation := range observations {
		if observation.Kind == "hook" && observation.Name == "beforeTurn" && observation.Phase == "failed" &&
			strings.Contains(observation.Error, "lifecycle-hook-marker") {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("lifecycle hook failure was not observed: %+v", observations)
	}
}

func TestLifecycleHookCancellationMakesWorkerUnavailable(t *testing.T) {
	extensionDir := makeExtensions(t)
	if err := os.WriteFile(filepath.Join(extensionDir, "hooks.js"), []byte(`export async function runtimeStart() { while (true) {} }`), 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot,
		Observer: func(observation contracts.Observation) error {
			if observation.Kind == "hook" && observation.Name == "runtimeStart" && observation.Phase == "started" {
				started <- struct{}{}
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, hookErr := worker.Hook(ctx, "runtimeStart", json.RawMessage(`{}`))
		result <- hookErr
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("lifecycle hook did not start")
	}
	cancel()
	if err = waitCancelled(t, result); err != nil {
		t.Fatal(err)
	}
	if worker.Available() {
		t.Fatal("Worker remained available after an active hook was cancelled")
	}
	if err = worker.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerReturnsModuleInitializationError(t *testing.T) {
	extensionDir := makeExtensions(t)
	if err := os.WriteFile(filepath.Join(extensionDir, "broken.js"), []byte(`function moduleInitializationFrame() {throw new Error("module-init-marker");} moduleInitializationFrame();`), 0644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(extensionDir, "config.json")
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config extensions.Config
	if err = json.Unmarshal(configBytes, &config); err != nil {
		t.Fatal(err)
	}
	config.Tools[0].Module = "broken.js"
	configBytes, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configBytes, 0644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot})
	if err == nil {
		_ = worker.Close()
		t.Fatal("New succeeded although an imported module threw during initialization")
	}
	if !strings.Contains(err.Error(), "module-init-marker") {
		t.Fatalf("module initialization error lost its original cause: %v", err)
	}
	if !strings.Contains(err.Error(), "moduleInitializationFrame") {
		t.Fatalf("module initialization error lost its original stack: %v", err)
	}
}

func TestRootRejectionCancelsAndJoinsPendingHostCall(t *testing.T) {
	extensionDir := makeExtensions(t)
	snapshot, err := extensions.CaptureDir(extensionDir)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	var observations []contracts.Observation
	observer := func(observation contracts.Observation) error {
		observations = append(observations, observation)
		if observation.Kind == "host" && observation.Name == "readFile" && observation.Phase == "started" {
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				return errors.New("HTTP request did not start before the failing read")
			}
		}
		return nil
	}
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	missingPath := "root-rejection-missing-file.txt"
	code := fmt.Sprintf(`await Promise.all([tools.http({url:%q}), tools.read_file({path:%q})]); return "unreachable";`, server.URL, missingPath)
	arguments, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	_, callErr := worker.Call(ctx, "run_js", arguments)
	if callErr == nil || !strings.Contains(callErr.Error(), missingPath) {
		t.Fatalf("Worker.Call did not preserve the root file error: %v", callErr)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request context remained active after the root rejection")
	}
	var httpID string
	var httpEnded bool
	for _, observation := range observations {
		if observation.Kind == "host" && observation.Name == "http" && observation.Phase == "started" {
			httpID = observation.ID
		}
		if observation.Kind == "host" && observation.Name == "http" && observation.Phase == "cancelled" {
			httpEnded = observation.ID == httpID
		}
	}
	if httpID == "" || !httpEnded {
		t.Fatalf("pending HTTP host observation was not joined with a cancelled end: %+v", observations)
	}
}

func TestWorkerCancellationStopsWorkAndAllowsNextWorker(t *testing.T) {
	t.Run("javascript", func(t *testing.T) {
		extensionDir := makeExtensions(t)
		snapshot, err := extensions.CaptureDir(extensionDir)
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{}, 1)
		observer := func(observation contracts.Observation) error {
			if observation.Kind == "tool" && observation.Name == "run_js" && observation.Phase == "started" {
				select {
				case started <- struct{}{}:
				default:
				}
			}
			return nil
		}
		worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Observer: observer})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, callErr := worker.Call(ctx, "run_js", json.RawMessage(`{"code":"globalThis.loopStarted = true; while (true) {}"}`))
			result <- callErr
		}()
		<-started
		time.Sleep(25 * time.Millisecond)
		cancel()
		if err = waitCancelled(t, result); err != nil {
			t.Fatal(err)
		}
		if err = worker.Close(); err != nil {
			t.Fatal(err)
		}
		assertNextWorkerRuns(t, snapshot)
	})

	t.Run("http", func(t *testing.T) {
		extensionDir := makeExtensions(t)
		snapshot, err := extensions.CaptureDir(extensionDir)
		if err != nil {
			t.Fatal(err)
		}
		started := make(chan struct{})
		stopped := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(stopped)
		}))
		defer server.Close()
		worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, callErr := worker.Call(ctx, "http", json.RawMessage(fmt.Sprintf(`{"url":%q}`, server.URL)))
			result <- callErr
		}()
		<-started
		cancel()
		if err = waitCancelled(t, result); err != nil {
			t.Fatal(err)
		}
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			t.Fatal("HTTP request context remained active after Worker cancellation")
		}
		if err = worker.Close(); err != nil {
			t.Fatal(err)
		}
		assertNextWorkerRuns(t, snapshot)
	})

	t.Run("command", func(t *testing.T) {
		extensionDir := makeExtensions(t)
		snapshot, err := extensions.CaptureDir(extensionDir)
		if err != nil {
			t.Fatal(err)
		}
		workspace := t.TempDir()
		worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot, Workspace: workspace})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			_, callErr := worker.Call(ctx, "command", json.RawMessage(`{"program":"sh","args":["-c","echo started > started; exec sleep 30"]}`))
			result <- callErr
		}()
		deadline := time.Now().Add(3 * time.Second)
		for {
			if _, statErr := os.Stat(filepath.Join(workspace, "started")); statErr == nil {
				break
			}
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("command did not start in its configured workspace")
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		if err = waitCancelled(t, result); err != nil {
			t.Fatal(err)
		}
		if err = worker.Close(); err != nil {
			t.Fatal(err)
		}
		assertNextWorkerRuns(t, snapshot)
	})
}

func makeExtensions(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config extensions.Config
	if err = json.Unmarshal(contents, &config); err != nil {
		t.Fatal(err)
	}
	config.Tools = append(config.Tools, extensions.Tool{
		Name: "count", Description: "Return module state", InputSchema: json.RawMessage(`{"type":"object"}`),
		Module: "count.js", Export: "count",
	})
	defaultRole := config.Roles["default"]
	defaultRole.Tools = append(defaultRole.Tools, "count")
	config.Roles["default"] = defaultRole
	contents, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, contents, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "count.js"), []byte(`import { revision } from "./count-state.js"; let calls = 0; export async function count(args, api) { calls++; return { calls, revision, marker: args.marker, implementationHasReadFile: typeof api.tools.read_file === "function", implementationHasHost: typeof api.readFile === "function" }; }`), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "count-state.js"), []byte(`export const revision = "module-one";`), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "hooks.js"), hookSource("hook-one"), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func hookSource(revision string) []byte {
	return []byte(fmt.Sprintf(`export async function beforeTool({ name, args }, api) { return name === "count" ? { ...args, hookRevision: %q, hookHasReadFile: typeof api.tools.read_file === "function", hookHasHost: typeof api.readFile === "function" } : args; }
export async function afterTool({ name, result }, api) { return name === "count" ? { ...result, hookRevision: %q, hookHasReadFile: typeof api.tools.read_file === "function", hookHasHost: typeof api.readFile === "function" } : result; }
`, revision, revision))
}

func delegatingHookSource() []byte {
	return []byte(`export async function beforeTool({ name, args }, api) {
  if (name === 'run_agent') await api.agents.run({ task: 'hook child' });
  return args;
}
`)
}

func delayedDelegationHookSource() []byte {
	return []byte(`export async function beforeTool({ name, args }, api) {
  if (name === 'run_agent') await api.command({ program: 'sh', args: ['-c', 'sleep 0.05'] });
  return args;
}
`)
}

func stalledDelegationAfterToolHookSource() []byte {
	return []byte(`export async function afterTool({ name, result }, api) {
  if (name === 'run_agent') await new Promise(() => {});
  return result;
}
`)
}

func stalledDelegationBeforeToolHookSource() []byte {
	return []byte(`export async function beforeTool({ name, args }, api) {
  if (name === 'run_agent') await new Promise(() => {});
  return args;
}
`)
}

func waitCancelled(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("call returned %v, want context cancellation", err)
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("cancelled call did not finish")
	}
}

func assertNextWorkerRuns(t *testing.T, snapshot extensions.Snapshot) {
	t.Helper()
	worker, err := jsruntime.New(context.Background(), jsruntime.Options{Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	result, err := worker.Call(context.Background(), "run_js", json.RawMessage(`{"code":"return 42;"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != "42" {
		t.Fatalf("next Worker returned %s, want 42", result)
	}
}
