package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

type delegationProvider func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error)

func (f delegationProvider) Prepare(request contracts.ModelRequest) ([]byte, error) {
	return contracts.EncodeJSON(request)
}

func (f delegationProvider) Generate(ctx context.Context, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
	return f(ctx, request, observe)
}
func scriptedJS(code string) contracts.ModelResult {
	return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "script-call", Name: "run_js", Arguments: contracts.JSON(map[string]any{"code": code})}}}
}
func delegationStore(t *testing.T) (*history.Store, string) {
	t.Helper()
	workspace := t.TempDir()
	s, err := history.Open(filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, workspace
}

func TestDelegationParallelResultsEvidenceAndResume(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	owner := executiondata.NewOwner()
	var mu sync.Mutex
	var rootResult string
	var liveFacts []contracts.ExecutionFact
	_, subscription, err := executiondata.CaptureAndSubscribe(owner,
		func(string, uint64) (struct{}, error) { return struct{}{}, nil },
		func(commit contracts.ExecutionCommit) {
			mu.Lock()
			liveFacts = append(liveFacts, commit.Facts...)
			mu.Unlock()
		})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	provider := delegationProvider(func(ctx context.Context, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		task := request.Messages[0].VisibleText()
		if len(request.Messages) == 1 {
			if task == "parent" {
				return scriptedJS(`return await Promise.all([api.agents.run({task:"child A"}),api.agents.run({task:"child B"})]);`), nil
			}
			return scriptedJS(`return await tools.read_file({path:"input.txt"});`), nil
		}
		if task == "parent" {
			mu.Lock()
			rootResult = string(request.Messages[len(request.Messages)-1].Content)
			mu.Unlock()
		}
		return contracts.ModelResult{Text: task + " source result"}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "parent", Workspace: workspace, ModelID: "test/model",
		ExecutionDataOwner: owner})
	if err != nil || !result.Result.OK {
		t.Fatalf("run %+v: %v", result, err)
	}
	children, err := store.Children(ctx, result.ExecutionID)
	if err != nil || len(children) != 2 {
		t.Fatalf("children %+v: %v", children, err)
	}
	cut, err := store.CaptureConversationCut(ctx, result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	var replayFacts []contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		replayFacts = append(replayFacts, fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	liveFacts = append([]contracts.ExecutionFact(nil), liveFacts...)
	mu.Unlock()
	parentRecords, err := store.Records(ctx, result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	spawns := map[string]bool{}
	for _, child := range children {
		if child.SessionID != "" || child.SessionCorrelation != result.SessionID || child.Adoption != "non_canonical" || child.Outcome != "completed" || child.BaseMessageCount != 0 {
			t.Fatalf("child %+v", child)
		}
		if spawns[child.SpawnCallID] {
			t.Fatal("two children shared a spawn call")
		}
		spawns[child.SpawnCallID] = true
		if !strings.Contains(rootResult, child.ID) || !strings.Contains(rootResult, child.Task+" source result") {
			t.Fatalf("parent did not use child result: %s", rootResult)
		}
		var terminalLinked bool
		for _, r := range parentRecords {
			for _, link := range r.Relations {
				if link.TargetRecordID == child.TerminalRecordID && link.Resolved {
					terminalLinked = true
				}
			}
		}
		if !terminalLinked {
			t.Fatal("parent result lacks child terminal relation")
		}
		records, e := store.Records(ctx, child.ID, 0, -1)
		if e != nil {
			t.Fatal(e)
		}
		var admissionLinked bool
		for _, r := range records {
			if r.Kind == "execution_admission" {
				for _, link := range r.Relations {
					if link.Relation == "parent_call" && link.Resolved && strings.HasPrefix(link.TargetRecordID, result.ExecutionID) {
						admissionLinked = true
					}
				}
			}
		}
		if !admissionLinked {
			t.Fatal("child admission lacks parent call relation")
		}
		var liveAnswer, replayAnswer *contracts.ExecutionFact
		for index := range liveFacts {
			fact := &liveFacts[index]
			if fact.ExecutionID == child.ID && fact.Conversation.EntityKind == "assistant" && fact.Text == child.Task+" source result" {
				copy := *fact
				liveAnswer = &copy
				break
			}
		}
		for index := range replayFacts {
			fact := &replayFacts[index]
			if fact.ExecutionID == child.ID && fact.Conversation.EntityKind == "assistant" && fact.Text == child.Task+" source result" {
				copy := *fact
				replayAnswer = &copy
				break
			}
		}
		if liveAnswer == nil || replayAnswer == nil || liveAnswer.ParentExecutionID != result.ExecutionID ||
			replayAnswer.ParentExecutionID != result.ExecutionID || liveAnswer.RootExecutionID != result.ExecutionID ||
			replayAnswer.RootExecutionID != result.ExecutionID {
			t.Fatalf("child assistant live/replay scope differs: live=%+v replay=%+v root=%q", liveAnswer, replayAnswer, result.ExecutionID)
		}
		childMessages, e := store.Messages(ctx, child.ID, 0, -1)
		if e != nil || len(childMessages) < 4 {
			t.Fatalf("child messages %+v: %v", childMessages, e)
		}
		for _, m := range childMessages {
			if strings.Contains(string(m.JSON), `"text":"parent"`) {
				t.Fatal("parent conversation leaked into child")
			}
		}
	}
	session, err := store.Session(ctx, result.SessionID)
	if err != nil || session.TurnCount != 1 || session.StateRevision != mustExecution(t, store, result.ExecutionID).BaseRevision+1 {
		t.Fatalf("Session %+v: %v", session, err)
	}
	inspection, err := NewService(store, provider, RunOptions{}).Inspect(ctx, result.ExecutionID, 9999, 9999)
	if err != nil || len(inspection.Children) != 2 {
		t.Fatalf("bounded inspection %+v: %v", inspection, err)
	}
	resumer := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		body := string(contracts.JSON(req.Messages))
		if !strings.Contains(body, children[0].ID) || !strings.Contains(body, "parent source result") {
			t.Errorf("resume omitted parent result: %s", body)
		}
		return contracts.ModelResult{Text: "resumed"}, nil
	})
	resumed, err := Run(ctx, store, resumer, RunOptions{Task: "continue", SessionID: result.SessionID})
	if err != nil || !resumed.Result.OK {
		t.Fatalf("resume %+v: %v", resumed, err)
	}
}

func TestDelegationUsesSelectedConfigAndFreshChildSource(t *testing.T) {
	store, workspace := delegationStore(t)
	dir := filepath.Join(workspace, "selected-config")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	config := `{"defaultRole":"default","roles":{"default":{"instruction":"parent-role","tools":["run_js","run_agent","stamp"]},"reviewer":{"instruction":"reviewer-old","tools":["run_js","stamp"]}},"providers":{"test":{"api":"responses","baseURL":"http://unused","auth":{"kind":"none"},"defaultModel":"config/ignored"}},"model":{"provider":"test"},"tools":[{"name":"run_agent","module":"stamp.js","export":"runAgent"},{"name":"stamp","description":"Return source marker","inputSchema":{"type":"object"},"module":"stamp.js","export":"stamp"}]}`
	configPath := filepath.Join(dir, "chosen.json")
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(dir, "stamp.js")
	oldSource, newSource := `export function stamp(){return "parent-source";} export async function runAgent(args,api){return api.agents.run(args);}`, `export function stamp(){return "child-source";} export async function runAgent(args,api){return api.agents.run(args);}`
	if err := os.WriteFile(sourcePath, []byte(oldSource), 0644); err != nil {
		t.Fatal(err)
	}
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		if req.Model.ModelID != "explicit/model" || req.Model.Effort != "low" {
			t.Errorf("model %+v", req.Model)
		}
		if len(req.Messages) == 1 {
			if req.Messages[0].VisibleText() == "parent" {
				if err := os.WriteFile(sourcePath, []byte(newSource), 0644); err != nil {
					return contracts.ModelResult{}, err
				}
				if err := os.WriteFile(configPath, []byte(strings.ReplaceAll(config, "reviewer-old", "reviewer-fresh")), 0644); err != nil {
					return contracts.ModelResult{}, err
				}
				return scriptedJS(`const parent=await tools.stamp({}); const child=await api.agents.run({role:"reviewer",task:"child"}); return {parent,child};`), nil
			}
			if !strings.Contains(req.Instruction, "reviewer-fresh") || strings.Contains(req.Instruction, "parent-role") {
				t.Errorf("child instruction: %s", req.Instruction)
			}
			if len(req.Tools) != 2 {
				t.Errorf("child selected tools: %+v", req.Tools)
			}
			return scriptedJS(`return await tools.stamp({});`), nil
		}
		last := string(req.Messages[len(req.Messages)-1].Content)
		if req.Messages[0].VisibleText() == "child" && !strings.Contains(last, "child-source") {
			t.Errorf("child source: %s", last)
		}
		if req.Messages[0].VisibleText() == "parent" && (!strings.Contains(last, "parent-source") || !strings.Contains(last, "child-source")) {
			t.Errorf("parent/child source: %s", last)
		}
		return contracts.ModelResult{Text: last}, nil
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "parent", Workspace: workspace, ConfigPath: configPath, ModelID: "explicit/model", Effort: "low"})
	if err != nil || !result.Result.OK {
		t.Fatalf("run %+v: %v", result, err)
	}
	children, err := store.Children(context.Background(), result.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children %+v: %v", children, err)
	}
	if children[0].AgentName != "reviewer" {
		t.Fatalf("child role %+v", children[0])
	}
	for id, source := range map[string]string{result.ExecutionID: oldSource, children[0].ID: newSource} {
		records, e := store.Records(context.Background(), id, 0, -1)
		if e != nil {
			t.Fatal(e)
		}
		var found bool
		for _, r := range records {
			if r.Kind == "resource_revision" && string(r.Content) == source {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s source bytes were not archived", id)
		}
	}
}

func TestDelegationFailedChildIsUsableByParent(t *testing.T) {
	store, workspace := delegationStore(t)
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		if len(req.Messages) == 1 {
			if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(`throw new Error("child module original failure");`), 0644); err != nil {
				return contracts.ModelResult{}, err
			}
			return scriptedJS(`return await api.agents.run({task:"child"});`), nil
		}
		last := string(req.Messages[len(req.Messages)-1].Content)
		if !strings.Contains(last, "failed") || !strings.Contains(last, "child module original failure") {
			t.Errorf("lost child failure: %s", last)
		}
		return contracts.ModelResult{Text: "reported child failure"}, nil
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "parent", Workspace: workspace, ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("parent %+v: %v", result, err)
	}
	children, err := store.Children(context.Background(), result.ExecutionID)
	if err != nil || len(children) != 1 || children[0].Outcome != "failed" || children[0].TerminalRecordID == "" {
		t.Fatalf("child %+v: %v", children, err)
	}
}

func TestDelegationPersistenceFailureCannotBeCaughtAsSuccess(t *testing.T) {
	store, workspace := delegationStore(t)
	db, err := sql.Open("sqlite", filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_child_end BEFORE UPDATE OF lifecycle ON executions WHEN NEW.parent_execution_id IS NOT NULL AND NEW.lifecycle='settled' BEGIN SELECT RAISE(ABORT,'controlled child settlement failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		if req.Messages[0].VisibleText() == "child" {
			return contracts.ModelResult{Text: "child completed work"}, nil
		}
		if len(req.Messages) == 1 {
			return scriptedJS(`try { return await api.agents.run({task:"child"}); } catch (_) { return "caught"; }`), nil
		}
		return contracts.ModelResult{Text: "must not become canonical"}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "parent", Workspace: workspace, ModelID: "test/model"})
	var fatal *contracts.ExecutionError
	if err == nil || !errors.As(err, &fatal) || result.Result.OK || !strings.Contains(err.Error(), "controlled child settlement failure") {
		t.Fatalf("result %+v: %v", result, err)
	}
	parent := mustExecution(t, store, result.ExecutionID)
	if parent.Outcome != "failed" || parent.Adoption != "non_canonical" {
		t.Fatalf("parent %+v", parent)
	}
	session, e := store.Session(context.Background(), result.SessionID)
	if e != nil || session.TurnCount != 0 {
		t.Fatalf("session %+v: %v", session, e)
	}
}

func TestDelegationCleanupPersistenceFailurePreservesParentException(t *testing.T) {
	store, workspace := delegationStore(t)
	db, err := sql.Open("sqlite", filepath.Join(workspace, "history.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TRIGGER reject_child_end BEFORE UPDATE OF lifecycle ON executions WHEN NEW.parent_execution_id IS NOT NULL AND NEW.lifecycle='settled' BEGIN SELECT RAISE(ABORT,'controlled child settlement failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		if req.Messages[0].VisibleText() == "child" {
			return scriptedJS(`await tools.write_file({path:"child.started",text:"ready"}); return await tools.command({program:"sleep",args:["60"]});`), nil
		}
		if len(req.Messages) == 1 {
			return scriptedJS(`api.agents.run({task:"child"}); await tools.command({program:"sh",args:["-c","while [ ! -f child.started ]; do sleep .02; done"]}); throw new Error("parent original exception");`), nil
		}
		return contracts.ModelResult{Text: "must not become canonical"}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, store, provider, RunOptions{Task: "parent", Workspace: workspace, ModelID: "test/model"})
	var fatal *contracts.ExecutionError
	if err == nil || !errors.As(err, &fatal) || result.Result.OK || (!strings.Contains(err.Error(), "controlled child settlement failure") || !strings.Contains(err.Error(), "parent original exception")) {
		t.Fatalf("result %+v: %v", result, err)
	}
	parent := mustExecution(t, store, result.ExecutionID)
	if parent.Outcome != "failed" || parent.Adoption != "non_canonical" {
		t.Fatalf("parent %+v", parent)
	}
	session, e := store.Session(context.Background(), result.SessionID)
	if e != nil || session.TurnCount != 0 {
		t.Fatalf("session %+v: %v", session, e)
	}
}

func TestDelegationParentCancellationJoinsChildren(t *testing.T) {
	store, workspace := delegationStore(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
		if req.Messages[0].VisibleText() == "child" {
			close(started)
			<-ctx.Done()
			close(finished)
			return contracts.ModelResult{}, ctx.Err()
		}
		return scriptedJS(`return await api.agents.run({task:"child"});`), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	type outcome struct {
		value RunResult
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		value, err := Run(ctx, store, provider, RunOptions{Task: "parent", Workspace: workspace, ModelID: "test/model"})
		done <- outcome{value, err}
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("child did not start")
	}
	cancel()
	select {
	case r := <-done:
		if r.err == nil || r.value.Result.OK {
			t.Fatalf("cancel %+v", r)
		}
		select {
		case <-finished:
		default:
			t.Fatal("run returned before child joined")
		}
		children, e := store.Children(context.Background(), r.value.ExecutionID)
		if e != nil || len(children) != 1 || children[0].Lifecycle != "settled" || children[0].Outcome != "cancelled" || children[0].TerminalRecordID == "" {
			t.Fatalf("child %+v: %v", children, e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not join")
	}
}

func TestDelegationUnawaitedChildSettlesBeforeNextModelStep(t *testing.T) {
	for _, tail := range []string{`return "done";`, `throw new Error("parent original exception");`} {
		t.Run(tail, func(t *testing.T) {
			store, workspace := delegationStore(t)
			dir := filepath.Join(workspace, ".hjh", "extensions")
			if err := extensions.InstallDefaults(dir); err != nil {
				t.Fatal(err)
			}
			// Keep beforeTool active around the same delegation to exercise both
			// wrapper and hook terminal records through the actual SQLite journal.
			hook := `export async function beforeTool(input,api){ if(input.name==='run_agent'){ await api.agents.run(input.args); } return input.args; }`
			if err := os.WriteFile(filepath.Join(dir, "hooks.js"), []byte(hook), 0644); err != nil {
				t.Fatal(err)
			}
			provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, obs contracts.Observer) (contracts.ModelResult, error) {
				if req.Messages[0].VisibleText() == "child" {
					return scriptedJS(`await tools.write_file({path:"child.started",text:"ready"}); return await tools.command({program:"sleep",args:["60"]});`), nil
				}
				if len(req.Messages) == 1 {
					return scriptedJS(`api.agents.run({task:"child"}); await tools.command({program:"sh",args:["-c","while [ ! -f child.started ]; do sleep .02; done"]}); ` + tail), nil
				}
				executions, e := store.Executions(context.Background(), currentSessionID(t, store))
				if e != nil {
					return contracts.ModelResult{}, e
				}
				var found bool
				for _, execution := range executions {
					if execution.ParentExecutionID != "" {
						found = true
						if execution.Lifecycle != "settled" || execution.Outcome != "cancelled" || execution.TerminalRecordID == "" {
							t.Errorf("next model request before child settlement: %+v", execution)
						}
					}
				}
				if !found {
					t.Error("child was not admitted")
				}
				for _, execution := range executions {
					if execution.ParentExecutionID != "" {
						continue
					}
					records, err := store.Records(context.Background(), execution.ID, 0, -1)
					if err != nil {
						t.Fatal(err)
					}
					active := map[string]contracts.Observation{}
					for _, record := range records {
						var observation contracts.Observation
						_ = json.Unmarshal(record.Payload, &observation)
						if observation.Kind != "tool" && observation.Kind != "hook" && observation.Kind != "agent" {
							continue
						}
						if observation.Phase == "started" {
							active[observation.ID] = observation
						} else {
							delete(active, observation.ID)
						}
					}
					if len(active) != 0 {
						t.Errorf("next model request before tool/hook termination: %+v", active)
					}
				}
				if strings.Contains(tail, "throw") && !strings.Contains(string(req.Messages[len(req.Messages)-1].Content), "parent original exception") {
					t.Error("original root JS error lost")
				}
				return contracts.ModelResult{Text: "parent handled completed call"}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := Run(ctx, store, provider, RunOptions{Task: "parent", Workspace: workspace, ModelID: "test/model"})
			if err != nil || !result.Result.OK {
				t.Fatalf("result %+v: %v", result, err)
			}
		})
	}
}
func currentSessionID(t *testing.T, store *history.Store) string {
	t.Helper()
	sessions, err := store.ListSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions %+v: %v", sessions, err)
	}
	return sessions[0].ID
}
