package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

func requireLatestRequestBody(t *testing.T, store *history.Store, executionID string) {
	t.Helper()
	records, err := store.Records(context.Background(), executionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	bodyCount := 0
	for _, record := range records {
		if record.Kind != "model_request" {
			continue
		}
		var metadata struct {
			SchemaVersion int `json:"schemaVersion"`
			WireBytes     int `json:"wireBytes"`
		}
		if err := json.Unmarshal(record.Payload, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata.SchemaVersion != 1 || record.Content == nil {
			continue
		}
		if len(record.Content) != metadata.WireBytes {
			t.Fatalf("execution %s latest request metadata says %d bytes but read %d", executionID, metadata.WireBytes, len(record.Content))
		}
		bodyCount++
	}
	if bodyCount != 1 {
		t.Fatalf("execution %s exposes %d prepared request bodies, want one", executionID, bodyCount)
	}
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
	requireLatestRequestBody(t, store, result.ExecutionID)
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
		requireLatestRequestBody(t, store, child.ID)
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
		for _, r := range records {
			if r.Kind == "resource_revision" && string(r.Content) == source {
				t.Fatalf("%s automatically archived extension source", id)
			}
		}
	}
}

func TestDelegationAppliesChildModelAndToolOverridesToRequestAndSnapshot(t *testing.T) {
	store, workspace := delegationStore(t)
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte("captured child tool"), 0644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(workspace, ".hjh", "extensions")
	if err := extensions.InstallDefaults(dir); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.json")
	original, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]json.RawMessage
	if err = json.Unmarshal(original, &config); err != nil {
		t.Fatal(err)
	}
	var roles map[string]json.RawMessage
	if err = json.Unmarshal(config["roles"], &roles); err != nil {
		t.Fatal(err)
	}
	var reviewer map[string]json.RawMessage
	if err = json.Unmarshal(roles["reviewer"], &reviewer); err != nil {
		t.Fatal(err)
	}
	reviewer["tools"] = json.RawMessage(`["run_js"]`)
	reviewer["model"] = json.RawMessage(`{"modelId":"role-child-model","effort":"high"}`)
	roles["reviewer"], err = json.Marshal(reviewer)
	if err != nil {
		t.Fatal(err)
	}
	config["roles"], err = json.Marshal(roles)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(configPath, configured, 0644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	requested := map[string]contracts.ModelRequest{}
	provider := delegationProvider(func(ctx context.Context, req contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		task := req.Messages[0].VisibleText()
		if task == "parent" {
			if len(req.Messages) == 1 {
				return scriptedJS(`return await Promise.all([
        api.agents.run({role:"reviewer",task:"role"}),
        api.agents.run({role:"reviewer",task:"override",model:{provider:"opencode-go-responses",effort:"low"},tools:["run_js","read_file"]}),
        api.agents.run({role:"reviewer",task:"empty",tools:[]})
      ]);`), nil
			}
			return contracts.ModelResult{Text: "parent complete"}, nil
		}

		mu.Lock()
		requested[task] = req
		mu.Unlock()
		switch task {
		case "role":
			if req.Model.Provider != "openrouter-responses" || req.Model.ModelID != "role-child-model" || req.Model.Effort != "high" {
				t.Errorf("role child model did not inherit the parent selection before role defaults: %+v", req.Model)
			}
			if !sameNames(toolDefinitionNames(req.Tools), []string{"run_js"}) {
				t.Errorf("role child tools %+v", toolDefinitionNames(req.Tools))
			}
			return contracts.ModelResult{Text: "role finished"}, nil
		case "empty":
			if req.Model.Provider != "openrouter-responses" || req.Model.ModelID != "role-child-model" || req.Model.Effort != "high" {
				t.Errorf("empty-tools child model %+v", req.Model)
			}
			if len(req.Tools) != 0 {
				t.Errorf("explicit empty tools list still reached the model: %+v", req.Tools)
			}
			return contracts.ModelResult{Text: "empty finished"}, nil
		case "override":
			if len(req.Messages) == 1 {
				if req.Model.Provider != "opencode-go-responses" || req.Model.ModelID != "gpt-6-luna" || req.Model.Effort != "low" {
					t.Errorf("explicit provider override did not use its default model and requested effort: %+v", req.Model)
				}
				if !sameNames(toolDefinitionNames(req.Tools), []string{"run_js", "read_file"}) {
					t.Errorf("explicit tool override %+v", toolDefinitionNames(req.Tools))
				}
				return scriptedJS(`// read the selected child tool
        return await tools.read_file({path:"input.txt"});`), nil
			}
			if !strings.Contains(string(req.Messages[len(req.Messages)-1].Content), "captured child tool") {
				t.Errorf("selected read_file tool was not available to the child's JavaScript: %+v", req.Messages)
			}
			return contracts.ModelResult{Text: "override finished"}, nil
		default:
			return contracts.ModelResult{}, fmt.Errorf("unexpected task %q", task)
		}
	})
	result, err := Run(context.Background(), store, provider, RunOptions{Task: "parent", Workspace: workspace,
		ConfigPath: configPath, ModelID: "parent-selected-model", Effort: "medium"})
	if err != nil || !result.Result.OK {
		t.Fatalf("run %+v: %v", result, err)
	}
	children, err := store.Children(context.Background(), result.ExecutionID)
	if err != nil || len(children) != 3 {
		t.Fatalf("children %+v: %v", children, err)
	}
	for _, child := range children {
		var model contracts.ModelSelection
		if err := json.Unmarshal(child.Model, &model); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		req, ok := requested[child.Task]
		mu.Unlock()
		if !ok {
			t.Fatalf("no provider request was recorded for child %q", child.Task)
		}
		if !sameNames(toolDefinitionNames(req.Tools), snapshotToolNames(t, store, child.ConfigurationID)) {
			t.Errorf("request tools and saved child composition diverged for %q: request=%+v", child.Task, toolDefinitionNames(req.Tools))
		}
		if model != req.Model {
			t.Errorf("saved child model differs from its actual request for %q: saved=%+v request=%+v", child.Task, model, req.Model)
		}
		requireExecutionContextInstruction(t, req.Instruction, req.Model, "reviewer", workspace)
		requireSavedExecutionContext(t, store, child.ConfigurationID, req.Instruction, req.Model, "reviewer", workspace)
	}

	for _, child := range children {
		records, err := store.Records(context.Background(), child.ID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Kind == "resource_revision" && string(record.Content) == string(configured) {
				t.Fatalf("child %q automatically archived raw config", child.Task)
			}
		}
	}
	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(configured) || string(current) == string(original) {
		t.Fatal("child tool overrides rewrote the config file or did not preserve the configured bytes")
	}
}

func toolDefinitionNames(definitions []contracts.ToolDefinition) []string {
	names := make([]string, len(definitions))
	for i, definition := range definitions {
		names[i] = definition.Name
	}
	return names
}

func snapshotToolNames(t *testing.T, store *history.Store, configurationID string) []string {
	t.Helper()
	configuration, err := store.Configuration(context.Background(), configurationID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Agent struct {
			Tools []string `json:"tools"`
		} `json:"agent"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(configuration.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tools) != 0 {
		t.Fatalf("saved child configuration archived tool definitions: %+v", snapshot.Tools)
	}
	return snapshot.Agent.Tools
}

func sameNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
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
