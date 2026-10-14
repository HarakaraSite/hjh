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
	if !state.LatestBudget.DetailAvailable {
		t.Fatal("latest SessionContext budget did not include its full detail")
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
	path := budgetConfig(t, workspace, 17000)
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
	if len(fact.OmittedCurrent) != 2 || *fact.Budget.Limit != 17000 {
		t.Fatalf("budget: %+v", fact)
	}
	assertBudgetRecordRetention(t, store, first.ExecutionID)
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

func assertBudgetRecordRetention(t *testing.T, store *history.Store, executionID string) {
	t.Helper()
	records, err := store.Records(context.Background(), executionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	var budgets []history.Record
	for _, record := range records {
		if record.Kind == "context_budget" {
			budgets = append(budgets, record)
		}
	}
	if len(budgets) < 2 {
		t.Fatalf("budget records = %d, want multiple model steps", len(budgets))
	}
	detailCount := 0
	for i, record := range budgets {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		var detailAvailable bool
		if err := json.Unmarshal(payload["detailAvailable"], &detailAvailable); err != nil {
			t.Fatalf("budget record %d detailAvailable: %v", record.Ordinal, err)
		}
		var summary contracts.ContextBudgetSummary
		if err := json.Unmarshal(payload["summary"], &summary); err != nil {
			t.Fatalf("budget record %d summary: %v", record.Ordinal, err)
		}
		var summaryFields map[string]json.RawMessage
		if err := json.Unmarshal(payload["summary"], &summaryFields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"sources", "omittedCurrent", "omittedCurrentSources", "history", "turns"} {
			if _, exists := summaryFields[key]; exists {
				t.Fatalf("budget record %d summary retained %s detail", record.Ordinal, key)
			}
		}
		var budgetFields map[string]json.RawMessage
		if err := json.Unmarshal(summaryFields["budget"], &budgetFields); err != nil {
			t.Fatalf("budget record %d budget: %v", record.Ordinal, err)
		}
		var configurationSources map[string]string
		if err := json.Unmarshal(budgetFields["sources"], &configurationSources); err != nil {
			t.Fatalf("budget record %d budget sources must be scalar origins: %v", record.Ordinal, err)
		}
		if !detailAvailable {
			if _, exists := payload["fact"]; exists {
				t.Fatalf("historical budget record %d retained full Fact", record.Ordinal)
			}
			for _, key := range []string{"sources", "omittedCurrent", "omittedCurrentSources", "history"} {
				if _, exists := payload[key]; exists {
					t.Fatalf("historical budget record %d retained %s detail", record.Ordinal, key)
				}
			}
			continue
		}
		detailCount++
		if i != len(budgets)-1 {
			t.Fatalf("non-latest budget record %d has full detail", record.Ordinal)
		}
		var fact contextbudget.Fact
		if err := json.Unmarshal(payload["fact"], &fact); err != nil {
			t.Fatalf("latest budget record %d Fact: %v", record.Ordinal, err)
		}
		if summary.SelectedMessageCount != len(fact.Sources) || summary.OmittedCurrentMessageCount != len(fact.OmittedCurrent) {
			t.Fatalf("summary counts differ from latest detail: summary=%+v fact=%+v", summary, fact)
		}
	}
	if detailCount != 1 {
		t.Fatalf("full budget detail records = %d, want one", detailCount)
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
	path := budgetConfig(t, workspace, 12000)
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
