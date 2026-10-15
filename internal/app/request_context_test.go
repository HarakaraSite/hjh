package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"hjh/internal/contracts"
)

func TestProjectRequestChangesInputAfterToolAndReadsOriginalHistoryOnResume(t *testing.T) {
	store, workspace := delegationStore(t)
	const original = "ORIGINAL_TOOL_RESULT_ONLY"
	if err := os.WriteFile(filepath.Join(workspace, "input.txt"), []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	installLifecycleHooks(t, workspace, `let requests=0;
export async function projectRequest({modelStep,runtime},api) {
  requests++;
  if(modelStep!==requests) throw new Error('step state lost');
  const view=await api.context.get();
  const before=await api.context.measure();
  if(!before.fits || before.wireBytes<=0 || !view.budget.inputRatio) throw new Error('measurement unavailable');
  if(runtime.turnNumber===1 && modelStep===2) {
    if(!JSON.stringify(view.messages).includes('ORIGINAL_TOOL_RESULT_ONLY')) throw new Error('raw tool result unavailable');
    const messages=[{role:'user',content:{kind:'text',text:'PROJECTED_SUMMARY_ONLY'}}];
    const measured=await api.context.measure({messages});
    if(measured.wireBytes>=before.wireBytes) throw new Error('candidate not measured');
    await api.context.replace({messages,instruction:view.instruction+'\nPROJECTED_INSTRUCTION_ONLY'});
    const changed=await api.context.get();
    if(changed.messages[0].content.text!=='PROJECTED_SUMMARY_ONLY') throw new Error('replacement unreadable');
  }
  if(runtime.turnNumber===2) {
    const turn=await api.context.history();
    if(turn.number!==1 || !JSON.stringify(turn.entries).includes('ORIGINAL_TOOL_RESULT_ONLY') || JSON.stringify(turn).includes('PROJECTED_SUMMARY_ONLY')) throw new Error('history was changed');
    if(await api.context.history({beforeTurn:turn.number})!==null) throw new Error('history boundary incorrect');
    await api.context.replace({messages:[{role:'user',content:{kind:'text',text:'RESUMED_SUMMARY_ONLY'}},...view.messages]});
  }
  return view;
}`)
	step := 0
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		step++
		switch step {
		case 1:
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "read", Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": "input.txt"})}}}, nil
		case 2:
			if len(req.Messages) != 1 || req.Messages[0].VisibleText() != "PROJECTED_SUMMARY_ONLY" || !strings.Contains(req.Instruction, "PROJECTED_INSTRUCTION_ONLY") {
				t.Errorf("replacement not sent: %+v", req)
			}
		case 3:
			if len(req.Messages) != 2 || req.Messages[0].VisibleText() != "RESUMED_SUMMARY_ONLY" || req.Messages[1].VisibleText() != "continue" || strings.Contains(req.Instruction, "PROJECTED_INSTRUCTION_ONLY") {
				t.Errorf("resume input incorrect: %+v", req)
			}
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	first, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, Task: "read input", ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("first run: %+v %v", first, err)
	}
	second, err := Run(t.Context(), store, provider, RunOptions{SessionID: first.SessionID, Task: "continue"})
	if err != nil || !second.Result.OK {
		t.Fatalf("resume: %+v %v", second, err)
	}
	if step != 3 {
		t.Fatalf("model calls: %d", step)
	}
	conversation, err := store.Conversation(t.Context(), first.SessionID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	var transcript strings.Builder
	for _, message := range conversation {
		transcript.Write(message.JSON)
	}
	if !strings.Contains(transcript.String(), original) || strings.Contains(transcript.String(), "SUMMARY_ONLY") {
		t.Fatalf("canonical transcript changed: %s", transcript.String())
	}
	for _, executionID := range []string{first.ExecutionID, second.ExecutionID} {
		records, err := store.Records(t.Context(), executionID, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			var observation contracts.Observation
			_ = json.Unmarshal(record.Payload, &observation)
			if observation.Kind == "hook" || strings.HasPrefix(observation.Name, "context.") {
				if strings.Contains(string(record.Payload), original) || strings.Contains(string(record.Payload), "SUMMARY_ONLY") || strings.Contains(string(record.Content), "SUMMARY_ONLY") {
					t.Fatalf("transcript duplicated in hook/API record: %s", record.Payload)
				}
			}
		}
	}
}

func TestProjectRequestReadOnlyKeepsNormalHistorySelection(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) { await api.context.get(); await api.context.history(); await api.context.measure(); }`)
	calls := 0
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		calls++
		if calls == 2 && (len(req.Messages) != 3 || req.Messages[0].VisibleText() != "first" || req.Messages[1].VisibleText() != "answer") {
			t.Errorf("normal history missing: %+v", req.Messages)
		}
		return contracts.ModelResult{Text: "answer"}, nil
	})
	first, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, Task: "first", ModelID: "test/model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), store, provider, RunOptions{SessionID: first.SessionID, Task: "second"}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRequestParentAndChildHaveIndependentCandidates(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest({runtime},api) {
  const owner=await api.context.memory.get({key:'owner'});
  if(owner!==null && owner!==runtime.role) throw new Error('parent memory leaked into child');
  await api.context.memory.set({key:'owner',value:runtime.role});
  const view=await api.context.get();
  if(runtime.parentExecutionId && await api.context.history()!==null) throw new Error('child received parent history');
  await api.context.replace({messages:view.messages,instruction:view.instruction+'\nREQUEST_ROLE '+runtime.role});
}`)
	var mu sync.Mutex
	calls := map[string]int{}
	provider := delegationProvider(func(_ context.Context, req contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		task := req.Messages[0].VisibleText()
		mu.Lock()
		calls[task]++
		step := calls[task]
		mu.Unlock()
		role := "root"
		if task == "child" {
			role = "child"
		}
		if !strings.Contains(req.Instruction, "REQUEST_ROLE "+role) {
			t.Errorf("hook missing for %s", task)
		}
		if task == "parent" && step == 1 {
			return scriptedJS(`return await api.agents.run({task:'child',tools:[]});`), nil
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, Task: "parent", ModelID: "test/model"})
	if err != nil || !result.Result.OK {
		t.Fatalf("parent/child: %+v %v", result, err)
	}
	if calls["parent"] != 2 || calls["child"] != 1 {
		t.Fatalf("calls: %v", calls)
	}
}

func TestProjectRequestFailureDoesNotSendStagedReplacement(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  await api.context.replace({messages:[{role:'user',content:{kind:'text',text:'staged'}}]});
  throw new Error('request hook failed');
}`)
	provider := delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		t.Error("provider called after hook failed")
		return contracts.ModelResult{}, nil
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, Task: "first", ModelID: "test/model"})
	if err == nil || !strings.Contains(err.Error(), "request hook failed") || result.Result.OK {
		t.Fatalf("hook failure lost: %+v %v", result, err)
	}
	conversation, err := store.Conversation(t.Context(), result.SessionID, 1, 1)
	if err != nil || len(conversation) != 0 {
		t.Fatalf("failed run adopted: %+v %v", conversation, err)
	}
}

func TestProjectRequestReplacementUsesExistingBudgetFailure(t *testing.T) {
	store, workspace := delegationStore(t)
	path := budgetConfig(t, workspace, 17000)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  const messages=[{role:'user',content:{kind:'text',text:'x'.repeat(100000)}}];
  if((await api.context.measure({messages})).fits) throw new Error('oversized candidate incorrectly fits');
  await api.context.replace({messages});
}`)
	provider := delegationProvider(func(context.Context, contracts.ModelRequest, contracts.Observer) (contracts.ModelResult, error) {
		t.Error("provider called over budget")
		return contracts.ModelResult{}, nil
	})
	result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, ConfigPath: path, Task: "first", ModelID: "test/model"})
	if err == nil || result.Result.Outcome != "context_budget_exceeded" {
		t.Fatalf("budget failure missing: %+v %v", result, err)
	}
	_, fact := budgetFact(t, store, result.SessionID)
	if len(fact.Sources) != 1 || fact.Sources[0].Kind != "hook" || len(fact.OmittedCurrent) != 0 || fact.History.Reason != "project_request_hook" {
		t.Fatalf("replacement was trimmed: %+v", fact)
	}
}
