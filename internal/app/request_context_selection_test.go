package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestProjectRequestMeasureAndSelectIncludeHistoryWithoutChangingRealSelection(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest({runtime},api) {
  if(runtime.turnNumber!==2) return;
  const current=await api.context.measure({scope:'current'});
  const whole=await api.context.measure();
  if(current.messageCount!==1 || whole.messageCount!==3 || whole.wireBytes<=current.wireBytes || whole.selection.messageCount!==3) throw new Error('saved history not measured');
  const low=await api.context.select({budget:{inputTokens:1}});
  if(low.fits) throw new Error('low budget unexpectedly fits');
  const noHistory=await api.context.select({budget:{historyTokens:1}});
  if(!noHistory.fits || noHistory.messages.length!==1 || !noHistory.omitted) throw new Error('history budget not applied');
  const normal=await api.context.select();
  if(!normal.fits || normal.messages.length!==3) throw new Error('preview changed normal selection');
}`)
	calls := 0
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		calls++
		if calls == 2 && (len(req.Messages) != 3 || req.Messages[0].VisibleText() != "first") {
			t.Errorf("history selection mutated: %+v", req.Messages)
		}
		return contracts.ModelResult{Text: "answer"}, nil
	})
	first, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, Task: "first", ModelID: "test/model"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(t.Context(), store, provider, RunOptions{SessionID: first.SessionID, Task: "second"})
	if err != nil || !second.Result.OK {
		t.Fatalf("resume: %+v %v", second, err)
	}
}

func TestProjectRequestSelectAfterSummaryAppliesBudgetAndPreservesOriginal(t *testing.T) {
	store, workspace := delegationStore(t)
	path := budgetConfig(t, workspace, 50000)
	for name, text := range map[string]string{"a.txt": strings.Repeat("OLD_RESULT_A", 500), "b.txt": strings.Repeat("LATEST_RESULT_B", 300)} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	installLifecycleHooks(t, workspace, `export async function projectRequest({modelStep},api) {
  if(modelStep!==3) return;
  const view=await api.context.get();
  const summary={role:'user',content:{kind:'text',text:'SUMMARY_FROM_JS'}};
  const messages=[view.messages[0],summary,...view.messages.slice(1)];
  const keep=[view.messages[0],summary,...view.messages.slice(-2)];
  const limit=(await api.context.measure({messages:keep})).estimatedTokens;
  const chosen=await api.context.select({messages,budget:{inputTokens:limit}});
  if(!chosen.fits || !chosen.omitted || chosen.candidate.fits || chosen.messages.length!==4) throw new Error('selection did not remove one complete old exchange');
  if((await api.context.get()).messages.length!==5) throw new Error('select changed candidate before replace');
  await api.context.replace(chosen);
  const after=await api.context.measure();
  if(after.wireBytes!==chosen.wireBytes || after.budget.inputTokens!==limit || !after.fits) throw new Error('selected budget not adopted');
}`)
	step := 0
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		if step <= 2 {
			name := "a.txt"
			if step == 2 {
				name = "b.txt"
			}
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: name, Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": name})}}}, nil
		}
		encoded := string(contracts.JSON(req.Messages))
		if len(req.Messages) != 4 || !strings.Contains(encoded, "SUMMARY_FROM_JS") || !strings.Contains(encoded, "LATEST_RESULT_B") || strings.Contains(encoded, "OLD_RESULT_A") {
			t.Errorf("wrong selected request: %s", encoded)
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, Task: "read files", ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("selected run: %+v %v", result, err)
	}
	_, fact := budgetFact(t, store, result.SessionID)
	if fact.Budget.InputTokens == nil || *fact.Budget.InputTokens >= 50000 || fact.Budget.Sources.InputTokens != "hook" || fact.EstimatedTokens != *fact.Budget.InputTokens {
		t.Fatalf("JS budget not saved: %+v", fact)
	}
	conversation, err := store.Conversation(t.Context(), result.SessionID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var full strings.Builder
	for _, saved := range conversation {
		full.Write(saved.JSON)
	}
	if !strings.Contains(full.String(), "OLD_RESULT_A") || strings.Contains(full.String(), "SUMMARY_FROM_JS") {
		t.Fatal("selection changed saved original transcript")
	}
}

func TestProjectRequestJSBudgetCanRaiseConfiguredInputLimit(t *testing.T) {
	store, workspace := delegationStore(t)
	path := budgetConfig(t, workspace, 100)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  const configured=await api.context.measure();
  if(configured.fits) throw new Error('configured budget should be too small');
  const chosen=await api.context.select({budget:{inputTokens:50000}});
  if(!chosen.fits) throw new Error('JS override not used');
  await api.context.replace(chosen);
}`)
	provider := delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		return contracts.ModelResult{Text: "done"}, nil
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, Task: "run with JS budget", ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("budget raise: %+v %v", result, err)
	}
	_, fact := budgetFact(t, store, result.SessionID)
	if fact.Budget.Limit == nil || *fact.Budget.Limit != 50000 {
		t.Fatalf("wrong final budget: %+v", fact.Budget)
	}
}
