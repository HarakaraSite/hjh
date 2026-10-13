package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/provider/protocol"
)

type budgetTestProvider struct {
	adapter  *protocol.Provider
	generate func(contracts.ModelRequest) contracts.ModelResult
}

func (p budgetTestProvider) Prepare(r contracts.ModelRequest) ([]byte, error) {
	return p.adapter.Prepare(r)
}
func (p budgetTestProvider) Generate(_ context.Context, r contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
	return p.generate(r), nil
}

func budgetConfig(t *testing.T, workspace string, limit int64) string {
	t.Helper()
	path, err := prepareConfig(workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["contextBudget"] = map[string]any{"defaults": map[string]any{"inputTokens": limit}, "models": map[string]any{
		"openrouter-responses/test/large": map[string]any{"inputTokens": 50000},
		"openrouter-responses/test/child": map[string]any{"inputTokens": 20}}}
	if err := os.WriteFile(path, contracts.JSON(config), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func budgetFact(t *testing.T, store *history.Store, session string) (contracts.SessionContext, contextbudget.Fact) {
	t.Helper()
	state, err := store.SessionContext(context.Background(), session)
	if err != nil || state.LatestBudget == nil {
		t.Fatalf("budget readback: %+v %v", state, err)
	}
	var fact contextbudget.Fact
	if err := json.Unmarshal(state.LatestBudget.Fact, &fact); err != nil {
		t.Fatal(err)
	}
	return state, fact
}

func TestCoreBudgetPreservesFullTurnAndReloadsItAfterModelChange(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	// Leave room for one 15KB read with the current default tool definitions,
	// while two reads still exceed the budget and require exchange selection.
	path := budgetConfig(t, workspace, 13000)
	a := "SAVED_A_" + strings.Repeat("a", 15000)
	b := "SAVED_B_" + strings.Repeat("b", 15000)
	for name, text := range map[string]string{"a.txt": a, "b.txt": b} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(workspace, "history.sqlite3")
	store, err := history.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	step := 0
	provider := budgetTestProvider{adapter: chatFixtureProvider("http://unused", "", "budget-fixture", protocol.Options{}), generate: func(request contracts.ModelRequest) contracts.ModelResult {
		step++
		if step <= 2 {
			name := "a.txt"
			if step == 2 {
				name = "b.txt"
			}
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: name, Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": name})}}}
		}
		if step == 3 || step == 4 {
			if strings.Contains(string(request.WireBody), a) || !strings.Contains(string(request.WireBody), b) {
				t.Error("old exchange was not removed while retaining the latest exchange")
			}
		}
		if step == 5 && (!strings.Contains(string(request.WireBody), a) || !strings.Contains(string(request.WireBody), b)) {
			t.Error("model change did not reselect the full saved turn")
		}
		return contracts.ModelResult{Text: "completed"}
	}}
	first, err := Run(ctx, store, provider, RunOptions{Workspace: workspace, ConfigPath: path, ModelID: "test/small", Task: "read both files"})
	if err != nil || !first.Result.OK {
		t.Fatalf("first: %+v %v", first, err)
	}
	full, err := store.Messages(ctx, first.ExecutionID, 0, -1)
	if err != nil || len(full) != 6 || !strings.Contains(string(full[2].JSON), a) {
		t.Fatalf("full saved transcript: %d %v", len(full), err)
	}
	_, fact := budgetFact(t, store, first.SessionID)
	if len(fact.OmittedCurrent) != 2 || *fact.Budget.Limit != 13000 {
		t.Fatalf("budget: %+v", fact)
	}
	cut, err := store.CaptureConversationCut(ctx, first.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	notice := false
	if err := store.WalkConversationCut(ctx, cut, func(f contracts.ExecutionFact) error {
		if f.Conversation.EntityKind == "notice" && f.Text != "" && f.Phase == "completed" {
			notice = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !notice {
		t.Fatal("Core notice was not retained for replay")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = history.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(ctx, store, provider, RunOptions{SessionID: first.SessionID, ConfigPath: path, Task: "continue after restart"})
	if err != nil || !second.Result.OK {
		t.Fatalf("second: %+v %v", second, err)
	}
	_, fact = budgetFact(t, store, first.SessionID)
	if fact.HistoryStatus != "partial" {
		t.Fatalf("recent selection: %+v", fact.History)
	}
	third, err := Run(ctx, store, provider, RunOptions{SessionID: first.SessionID, ConfigPath: path, ModelID: "test/large", Task: "use larger model"})
	if err != nil || !third.Result.OK {
		t.Fatalf("third: %+v %v", third, err)
	}
	_, fact = budgetFact(t, store, first.SessionID)
	if fact.HistoryStatus != "full" || *fact.Budget.Limit != 50000 {
		t.Fatalf("larger model: %+v", fact)
	}
	for _, source := range fact.Sources {
		if source.Kind == "history" && source.ExecutionID == "" {
			t.Fatal("missing saved source")
		}
	}
}

func TestCoreProtectedBudgetExceededIsSavedWithoutProviderRequest(t *testing.T) {
	store, workspace := delegationStore(t)
	path := budgetConfig(t, workspace, 20)
	called := false
	provider := budgetTestProvider{adapter: chatFixtureProvider("http://unused", "", "budget-fixture", protocol.Options{}), generate: func(contracts.ModelRequest) contracts.ModelResult {
		called = true
		return contracts.ModelResult{Text: "unexpected"}
	}}
	result, err := Run(context.Background(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, Task: "protected task"})
	if err == nil || called || result.Result.Outcome != "context_budget_exceeded" {
		t.Fatalf("failure: %+v %v called=%v", result, err, called)
	}
	state, _ := budgetFact(t, store, result.SessionID)
	if !state.LatestBudget.Exceeded || state.LatestRequest != nil {
		t.Fatalf("failure readback: %+v", state)
	}
	full, err := store.Messages(context.Background(), result.ExecutionID, 0, -1)
	if err != nil || len(full) != 1 {
		t.Fatalf("saved failed task: %d %v", len(full), err)
	}
}

func TestCoreChildResolvesIndependentModelBudget(t *testing.T) {
	store, workspace := delegationStore(t)
	path := budgetConfig(t, workspace, 9000)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["roles"].(map[string]any)["reviewer"].(map[string]any)["model"] = map[string]string{"modelId": "test/child"}
	if err := os.WriteFile(path, contracts.JSON(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	provider := budgetTestProvider{adapter: chatFixtureProvider("http://unused", "", "budget-fixture", protocol.Options{}), generate: func(r contracts.ModelRequest) contracts.ModelResult {
		calls++
		if r.Model.ModelID == "test/child" {
			t.Error("child sent above its own budget")
		}
		if calls == 1 {
			return scriptedJS(`return await api.agents.run({role:"reviewer",task:"check task"});`)
		}
		return contracts.ModelResult{Text: "parent completed"}
	}}
	result, err := Run(context.Background(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, ModelID: "test/parent", Task: "delegate"})
	if err != nil || !result.Result.OK {
		t.Fatalf("parent: %+v %v", result, err)
	}
	children, err := store.Children(context.Background(), result.ExecutionID)
	if err != nil || len(children) != 1 {
		t.Fatalf("children: %+v %v", children, err)
	}
	records, err := store.Records(context.Background(), children[0].ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range records {
		if r.Kind == "context_budget" {
			var status contracts.ContextBudgetStatus
			if err := json.Unmarshal(r.Payload, &status); err != nil {
				t.Fatal(err)
			}
			var fact contextbudget.Fact
			if err := json.Unmarshal(status.Fact, &fact); err != nil {
				t.Fatal(err)
			}
			found = fact.Budget.Limit != nil && *fact.Budget.Limit == 20 && status.Exceeded
		}
	}
	if !found {
		t.Fatal("child model budget not saved")
	}
}
