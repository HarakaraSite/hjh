package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestRequestModelUsesParentSelectionWithoutPersistingItsTranscript(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  const result=await api.model.generate({instruction:'MODEL_SUMMARY_INSTRUCTION_ONLY',messages:[{role:'user',content:{kind:'text',text:'MODEL_SUMMARY_INPUT_ONLY'}}]});
  if(result.text!=='MODEL_SUMMARY_OUTPUT_ONLY') throw new Error('summary result missing');
  await api.context.memory.set({key:'summary',value:result.text});
}`)
	calls := 0
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, observe contracts.Observer) (contracts.ModelResult, error) {
		calls++
		if request.Instruction == "MODEL_SUMMARY_INSTRUCTION_ONLY" {
			if request.Model.ModelID != "test/model" || request.Model.Effort != "high" || request.Model.Provider == "" || len(request.Tools) != 0 || len(request.Messages) != 1 {
				t.Errorf("selection or empty tools not inherited correctly: %+v", request)
			}
			if err := observe(contracts.Observation{Kind: "assistant_text", Text: "MODEL_SUMMARY_OUTPUT_ONLY", Thinking: []contracts.ThinkingItem{{Text: "SUMMARY_THINKING_ONLY"}}}); err != nil {
				return contracts.ModelResult{}, err
			}
			return contracts.ModelResult{Text: "MODEL_SUMMARY_OUTPUT_ONLY", Usage: contracts.JSON(map[string]int{"prompt_tokens": 15})}, nil
		}
		return contracts.ModelResult{Text: "normal answer"}, nil
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	var notified []contracts.ContextBudgetStatus
	result, err := service.Run(t.Context(), RunRequest{Task: "normal task", ModelID: "test/model", Effort: "high",
		OnContextBudget: func(status contracts.ContextBudgetStatus) { notified = append(notified, status) }})
	if err != nil || !result.Result.OK || calls != 2 {
		t.Fatalf("model API: %+v %v calls %d", result, err, calls)
	}
	if len(notified) != 1 || notified[0].ExecutionID != result.ExecutionID || notified[0].ModelStep != 1 {
		t.Fatalf("summary call mixed into budget notifications: %+v", notified)
	}
	children, err := store.Children(t.Context(), result.ExecutionID)
	if err != nil || len(children) != 0 {
		t.Fatalf("summary created child: %v %v", children, err)
	}
	records, err := store.Records(t.Context(), result.ExecutionID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	usageFound := false
	for _, record := range records {
		body := string(record.Payload) + string(record.Content)
		if strings.Contains(body, "MODEL_SUMMARY_") || strings.Contains(body, "SUMMARY_THINKING_ONLY") {
			t.Fatalf("summary request was archived: %s", body)
		}
		if strings.Contains(body, "context.model.generate") && strings.Contains(body, `"prompt_tokens":15`) {
			usageFound = true
		}
	}
	if !usageFound {
		t.Fatal("summary usage metadata was not retained")
	}
	if value := service.options.ContextMemory.begin(result.SessionID).get("summary"); string(value) != `"MODEL_SUMMARY_OUTPUT_ONLY"` {
		t.Fatalf("summary memory not published: %s", value)
	}
}

func TestContextMemoryPublishesOnlyAdoptedTurnsAndResetsWithService(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  const n=(await api.context.memory.get({key:'n'}) ?? 0)+1;
  await api.context.memory.set({key:'n',value:n});
  const view=await api.context.get();
  await api.context.replace({messages:view.messages,instruction:view.instruction+'\nMEMORY_N='+n});
}`)
	want := 1
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if !strings.Contains(request.Instruction, "MEMORY_N="+strconv.Itoa(want)) {
			t.Errorf("wrong memory state, want %d: %s", want, request.Instruction)
		}
		if request.Messages[len(request.Messages)-1].VisibleText() == "fail" {
			return contracts.ModelResult{}, errors.New("normal request failed")
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	first, err := service.Run(t.Context(), RunRequest{Task: "first", ModelID: "test/model"})
	if err != nil {
		t.Fatal(err)
	}
	want = 2
	if _, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "second"}); err != nil {
		t.Fatal(err)
	}
	want = 3
	if _, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "fail"}); err == nil {
		t.Fatal("expected request failure")
	}
	if _, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "after failure"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	want = 1
	restarted := NewService(store, provider, RunOptions{Workspace: workspace})
	defer restarted.Close()
	if _, err := restarted.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "after Core restart"}); err != nil {
		t.Fatal(err)
	}
	if err := restarted.DeleteSession(t.Context(), first.SessionID); err != nil {
		t.Fatal(err)
	}
	if got := restarted.options.ContextMemory.begin(first.SessionID).get("n"); string(got) != "null" {
		t.Fatalf("deleted Session retained memory: %s", got)
	}
}

func TestRequestModelCancellationDoesNotPublishSessionMemory(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  await api.context.memory.set({key:'cancelled',value:true});
  await api.model.generate({instruction:'await cancellation',messages:[]});
}`)
	started := make(chan struct{})
	provider := delegationProvider(func(ctx context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if request.Instruction != "await cancellation" {
			t.Error("normal provider called")
		}
		close(started)
		<-ctx.Done()
		return contracts.ModelResult{}, ctx.Err()
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan RunResult, 1)
	go func() {
		result, _ := service.Run(ctx, RunRequest{Task: "cancel summary", ModelID: "test/model"})
		done <- result
	}()
	<-started
	cancel()
	result := <-done
	if result.Result.Outcome != "cancelled" {
		t.Fatalf("cancel outcome lost: %+v", result)
	}
	if value := service.options.ContextMemory.begin(result.SessionID).get("cancelled"); string(value) != "null" {
		t.Fatalf("cancelled memory published: %s", value)
	}
}

func TestRequestModelIncompleteSummaryDoesNotPublishMemory(t *testing.T) {
	store, workspace := delegationStore(t)
	installLifecycleHooks(t, workspace, `export async function projectRequest(_,api) {
  const result=await api.model.generate({instruction:'incomplete summary',messages:[]});
  await api.context.memory.set({key:'summary',value:result.text});
}`)
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if request.Instruction != "incomplete summary" {
			t.Error("normal provider called after incomplete summary")
		}
		return contracts.ModelResult{Text: "partial summary", FinishReason: "length"}, nil
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	result, err := service.Run(t.Context(), RunRequest{Task: "incomplete", ModelID: "test/model"})
	if err == nil || !strings.Contains(err.Error(), "finish_reason=length") {
		t.Fatalf("incomplete summary accepted: %+v %v", result, err)
	}
	if value := service.options.ContextMemory.begin(result.SessionID).get("summary"); string(value) != "null" {
		t.Fatalf("incomplete summary published: %s", value)
	}
}

func installCompaction(t *testing.T, workspace string, budget map[string]any) {
	installCompactionWithPolicy(t, workspace, budget, nil)
}

func installCompactionWithPolicy(t *testing.T, workspace string, budget map[string]any, policy map[string]int64) {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "hooks", "compaction.js"))
	if err != nil {
		t.Fatal(err)
	}
	hook := string(source)
	if len(policy) != 0 {
		var fields []string
		for _, key := range []string{"triggerTokens", "targetTokens"} {
			if value, ok := policy[key]; ok {
				fields = append(fields, key+": "+strconv.FormatInt(value, 10))
			}
		}
		hook = strings.Replace(hook, "  triggerTokens: null,\n  targetTokens: null,", "  "+strings.Join(fields, ",\n  ")+",", 1)
	}
	installLifecycleHooks(t, workspace, hook)
	path := filepath.Join(workspace, ".hjh", "extensions", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	config["defaultRole"] = "compaction-test"
	config["contextBudget"] = map[string]any{"defaults": budget}
	config["roles"] = map[string]any{"compaction-test": map[string]any{"instruction": "Read files as requested and answer concisely.", "tools": []string{"read_file"}}}
	if err := os.WriteFile(path, contracts.JSON(config), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestJSCompactionUsesEffectiveBudgetOrUnknownDefault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget map[string]any
		limit  int64
		source string
	}{
		{"unknown", map[string]any{}, 800000, "hook"},
		{"model-context", map[string]any{"contextTokens": 128000}, 115200, "config/defaults"},
		{"large-input", map[string]any{"inputTokens": 1000000}, 1000000, "config/defaults"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, workspace := delegationStore(t)
			installCompaction(t, workspace, tc.budget)
			calls := 0
			provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
				calls++
				if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
					t.Error("short request was summarized")
				}
				return contracts.ModelResult{Text: "done"}, nil
			})
			result, err := Run(t.Context(), store, provider, RunOptions{Workspace: workspace, Task: "short request", ModelID: "test/model"})
			if err != nil || !result.Result.OK || calls != 1 {
				t.Fatalf("request: %+v %v; calls %d", result, err, calls)
			}
			_, fact := budgetFact(t, store, result.SessionID)
			if fact.Budget.Limit == nil || *fact.Budget.Limit != tc.limit || fact.Budget.LimitSource != tc.source {
				t.Fatalf("effective budget: %+v; want limit %d source %s", fact.Budget, tc.limit, tc.source)
			}
		})
	}
}

func TestJSCompactionTriggersAboveNinetyPercentOfBudget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rawCharacters int
		wantSummaries int
	}{
		{"between-eighty-and-ninety-percent", 21000, 0},
		{"above-ninety-percent", 24000, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, workspace := delegationStore(t)
			const limit = 10000
			installCompaction(t, workspace, map[string]any{"inputTokens": limit})
			raw := strings.Repeat("a", tc.rawCharacters)
			mainCalls, summaries := 0, 0
			provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
				if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
					summaries++
					return contracts.ModelResult{Text: "COMPACTED_HISTORY_NOTE"}, nil
				}
				mainCalls++
				if mainCalls == 1 {
					return contracts.ModelResult{Text: raw}, nil
				}
				encoded := string(contracts.JSON(request.Messages))
				if tc.wantSummaries == 0 && !strings.Contains(encoded, raw) {
					t.Error("original history was not retained below the trigger")
				}
				if tc.wantSummaries == 1 && (!strings.Contains(encoded, "COMPACTED_HISTORY_NOTE") || strings.Contains(encoded, raw)) {
					t.Error("history was not summarized above the trigger")
				}
				return contracts.ModelResult{Text: "done"}, nil
			})
			service := NewService(store, provider, RunOptions{Workspace: workspace})
			defer service.Close()
			first, err := service.Run(t.Context(), RunRequest{Task: "produce history", ModelID: "test/model"})
			if err != nil || !first.Result.OK {
				t.Fatalf("seed history: %+v %v", first, err)
			}
			second, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "continue"})
			if err != nil || !second.Result.OK || summaries != tc.wantSummaries {
				t.Fatalf("threshold: %+v %v; summaries %d, want %d", second, err, summaries, tc.wantSummaries)
			}
			if tc.wantSummaries == 0 {
				_, fact := budgetFact(t, store, first.SessionID)
				if fact.EstimatedTokens <= limit*8/10 || fact.EstimatedTokens > limit*9/10 {
					t.Fatalf("retained input was not between 80%% and 90%%: %d", fact.EstimatedTokens)
				}
			} else {
				var memory struct {
					LastCompaction struct {
						TargetTokens int64 `json:"targetTokens"`
						AfterTokens  int64 `json:"afterTokens"`
						TargetMet    bool  `json:"targetMet"`
					} `json:"lastCompaction"`
				}
				if err := json.Unmarshal(service.options.ContextMemory.begin(first.SessionID).get("compaction-v2"), &memory); err != nil {
					t.Fatal(err)
				}
				if memory.LastCompaction.TargetTokens != limit/3 || !memory.LastCompaction.TargetMet || memory.LastCompaction.AfterTokens > limit/3 {
					t.Fatalf("final input missed one-third target: %+v", memory.LastCompaction)
				}
			}
		})
	}
}

func TestJSCompactionSummarizesLargeCurrentExchangeWithoutJSONExpansion(t *testing.T) {
	store, workspace := delegationStore(t)
	const limit = 14787
	const target = limit / 3
	const trigger = 6000
	installCompactionWithPolicy(t, workspace, map[string]any{"inputTokens": limit},
		map[string]int64{"triggerTokens": trigger, "targetTokens": target})
	old := strings.Repeat("OLD_JSON_LINE {\"quoted\":\"value\"} \\\\path\n", 200)
	latest := "LATEST_RECENT_TOOL_RESULT for E\n" + strings.Repeat("LATEST_DETAIL ", 900)
	for name, content := range map[string]string{"old.txt": old, "latest.txt": latest} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	summaryCalls, mainCalls := 0, 0
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
			summaryCalls++
			if len(request.Tools) != 0 || len(request.Messages) != 1 {
				return contracts.ModelResult{}, errors.New("summary request did not use the tool-free single-message API")
			}
			prompt := request.Messages[0].VisibleText()
			if !strings.Contains(prompt, "Current task (E)") || !strings.Contains(prompt, "inspect E current work") ||
				!strings.Contains(prompt, "Recent retained E state") || !strings.Contains(prompt, "LATEST_RECENT_TOOL_RESULT") ||
				strings.Count(prompt, "inspect E current work") != 1 {
				return contracts.ModelResult{}, errors.New("summary input lost current context or duplicated it")
			}
			if summaryCalls == 1 && (!strings.Contains(prompt, "Older current exchanges to summarize") ||
				!strings.Contains(prompt, "OLD_JSON_LINE") || !strings.Contains(prompt, "path\nOLD_JSON_LINE")) {
				return contracts.ModelResult{}, errors.New("raw newlines were unnecessarily JSON-escaped in summary input")
			}
			if strings.Contains(request.Instruction, "approximately 0 characters") ||
				!strings.Contains(request.Instruction, "approximately 1200 characters") {
				return contracts.ModelResult{}, errors.New("oversized required E state produced a zero or undersized summary hint")
			}
			return contracts.ModelResult{Text: "WITHIN_TURN_COMPACT_SUMMARY"}, nil
		}
		mainCalls++
		if mainCalls == 1 {
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "old", Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": "old.txt"})}}}, nil
		}
		if mainCalls == 2 {
			if !strings.Contains(string(contracts.JSON(request.Messages)), "OLD_JSON_LINE") {
				return contracts.ModelResult{}, errors.New("first current exchange was missing before compaction")
			}
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: "latest", Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": "latest.txt"})}}}, nil
		}
		encoded := string(contracts.JSON(request.Messages))
		if summaryCalls == 0 || !strings.Contains(encoded, "WITHIN_TURN_COMPACT_SUMMARY") ||
			strings.Contains(encoded, "OLD_JSON_LINE") || !strings.Contains(encoded, "LATEST_DETAIL") {
			return contracts.ModelResult{}, errors.New("compacted request did not preserve the summary and latest E state")
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	result, err := service.Run(t.Context(), RunRequest{Task: "inspect E current work", ModelID: "test/model"})
	if err != nil || !result.Result.OK || summaryCalls < 1 || summaryCalls > 2 || mainCalls != 3 {
		t.Fatalf("within-turn compaction: %+v %v; summaries=%d main=%d", result, err, summaryCalls, mainCalls)
	}
	var memory struct {
		LastCompaction struct {
			TargetTokens int64 `json:"targetTokens"`
			AfterTokens  int64 `json:"afterTokens"`
			TargetMet    bool  `json:"targetMet"`
		} `json:"lastCompaction"`
	}
	if err := json.Unmarshal(service.options.ContextMemory.begin(result.SessionID).get("compaction-v2"), &memory); err != nil {
		t.Fatal(err)
	}
	if memory.LastCompaction.TargetTokens != target || memory.LastCompaction.TargetMet || memory.LastCompaction.AfterTokens > limit {
		t.Fatalf("protected E state did not continue with target miss recorded: %+v", memory.LastCompaction)
	}
}

func TestJSCompactionRestoredHistoryUsesTargetBeforeNormalTrigger(t *testing.T) {
	store, workspace := delegationStore(t)
	const limit = 10000
	const target = 2000
	installCompactionWithPolicy(t, workspace,
		map[string]any{"contextTokens": 128000, "inputTokens": limit},
		map[string]int64{"triggerTokens": 9000, "targetTokens": target})
	raw := strings.Repeat("RESTORE_RAW_FACT ", 420)
	seedProvider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
			return contracts.ModelResult{}, errors.New("same-Core continuation unexpectedly summarized")
		}
		if request.Messages[len(request.Messages)-1].VisibleText() == "seed restoration history" {
			return contracts.ModelResult{Text: raw}, nil
		}
		if !strings.Contains(string(contracts.JSON(request.Messages)), raw) {
			return contracts.ModelResult{}, errors.New("same-Core request lost history below the normal trigger")
		}
		return contracts.ModelResult{Text: "same-Core continuation"}, nil
	})
	service := NewService(store, seedProvider, RunOptions{Workspace: workspace})
	first, err := service.Run(t.Context(), RunRequest{Task: "seed restoration history", ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("seed history: %+v %v", first, err)
	}
	if _, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "same Core normal continuation"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}

	summaryCalls, mainCalls := 0, 0
	var firstSummaryInput string
	restartedProvider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
			summaryCalls++
			encoded := string(contracts.JSON(request.Messages))
			if summaryCalls == 1 {
				firstSummaryInput = encoded
			}
			if !strings.Contains(encoded, "Current task (E)") || !strings.Contains(encoded, "restart continuation") {
				return contracts.ModelResult{}, errors.New("restored summary omitted E or its recent source")
			}
			return contracts.ModelResult{Text: "RESTORED_COMPACTED_FACTS"}, nil
		}
		mainCalls++
		encoded := string(contracts.JSON(request.Messages))
		if !strings.Contains(encoded, "RESTORED_COMPACTED_FACTS") || strings.Contains(encoded, raw) {
			return contracts.ModelResult{}, errors.New("restored request did not use the compacted history")
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	restarted := NewService(store, restartedProvider, RunOptions{Workspace: workspace})
	defer restarted.Close()
	result, err := restarted.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "restart continuation"})
	if err != nil || !result.Result.OK || summaryCalls != 1 || mainCalls != 1 {
		t.Fatalf("restored continuation: %+v %v; summaries=%d main=%d", result, err, summaryCalls, mainCalls)
	}
	if !strings.Contains(firstSummaryInput, "RESTORE_RAW_FACT") {
		t.Fatal("restored summary omitted its in-budget history source")
	}
	var memory struct {
		Summaries      []any `json:"summaries"`
		LastCompaction struct {
			BeforeTokens int64 `json:"beforeTokens"`
			TargetTokens int64 `json:"targetTokens"`
			AfterTokens  int64 `json:"afterTokens"`
			TargetMet    bool  `json:"targetMet"`
			Restored     bool  `json:"restored"`
		} `json:"lastCompaction"`
	}
	if err := json.Unmarshal(restarted.options.ContextMemory.begin(first.SessionID).get("compaction-v2"), &memory); err != nil {
		t.Fatal(err)
	}
	if len(memory.Summaries) != 1 || !memory.LastCompaction.Restored || !memory.LastCompaction.TargetMet ||
		memory.LastCompaction.TargetTokens != target || memory.LastCompaction.AfterTokens > target || memory.LastCompaction.BeforeTokens <= target {
		t.Fatalf("restored target state: %+v", memory)
	}
	_, fact := budgetFact(t, store, first.SessionID)
	if fact.Budget.Limit == nil || *fact.Budget.Limit != limit {
		t.Fatalf("configured input limit was not used: %+v", fact.Budget)
	}
}

func TestJSCompactionSameCoreOverflowSummarizesOlderCompletedTurn(t *testing.T) {
	store, workspace := delegationStore(t)
	const limit = 10000
	const target = limit / 3
	const trigger = 9000
	installCompactionWithPolicy(t, workspace,
		map[string]any{"inputTokens": limit},
		map[string]int64{"triggerTokens": trigger, "targetTokens": target})

	cTask := "complete C history"
	dTask := "complete D history"
	eTask := "E_CURRENT_TASK continue the active work " + strings.Repeat("E_DEPENDENCY ", 220)
	followupTask := "continue after E"
	cRaw := "C_REQUIRED_FACT " + strings.Repeat("C_SOURCE_FACT ", 1300)
	dRaw := "D_REQUIRED_FACT " + strings.Repeat("D_SOURCE_FACT ", 780)
	const firstNote = "C_CONTINUATION_NOTE"
	const mergedNote = "C_AND_D_CONTINUATION_NOTE"
	summaryCalls := 0
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
			summaryCalls++
			encoded := string(contracts.JSON(request.Messages))
			switch summaryCalls {
			case 1:
				if !strings.Contains(encoded, "C_REQUIRED_FACT") || strings.Contains(encoded, "D_REQUIRED_FACT") {
					return contracts.ModelResult{}, errors.New("first summary did not isolate the overflowing older C turn")
				}
				return contracts.ModelResult{Text: firstNote}, nil
			case 2:
				if !strings.Contains(encoded, firstNote) || !strings.Contains(encoded, "D_REQUIRED_FACT") ||
					strings.Contains(encoded, "C_REQUIRED_FACT") || !strings.Contains(encoded, "E_CURRENT_TASK") {
					return contracts.ModelResult{}, errors.New("second summary did not integrate the first note with pending D and E")
				}
				return contracts.ModelResult{Text: mergedNote}, nil
			default:
				return contracts.ModelResult{}, errors.New("same-Core overflow used more than two summary calls")
			}
		}

		encoded := string(contracts.JSON(request.Messages))
		latest := request.Messages[len(request.Messages)-1].VisibleText()
		switch latest {
		case cTask:
			return contracts.ModelResult{Text: cRaw}, nil
		case dTask:
			if summaryCalls != 0 || !strings.Contains(encoded, "C_REQUIRED_FACT") {
				return contracts.ModelResult{}, errors.New("D request compacted or lost C before crossing the trigger")
			}
			return contracts.ModelResult{Text: dRaw}, nil
		case eTask:
			if summaryCalls != 2 || !strings.Contains(encoded, mergedNote) ||
				strings.Contains(encoded, "C_REQUIRED_FACT") || strings.Contains(encoded, "D_REQUIRED_FACT") {
				return contracts.ModelResult{}, errors.New("E request did not receive the integrated C and D continuation note")
			}
			return contracts.ModelResult{Text: "E_RESULT"}, nil
		case followupTask:
			if !strings.Contains(encoded, mergedNote) || strings.Contains(encoded, "C_REQUIRED_FACT") || strings.Contains(encoded, "D_REQUIRED_FACT") {
				return contracts.ModelResult{}, errors.New("next turn lost the integrated C and D continuation note")
			}
			return contracts.ModelResult{Text: "follow-up complete"}, nil
		default:
			return contracts.ModelResult{}, errors.New("unexpected request in same-Core overflow test")
		}
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	c, err := service.Run(t.Context(), RunRequest{Task: cTask, ModelID: "test/model"})
	if err != nil || !c.Result.OK {
		t.Fatalf("complete C: %+v %v", c, err)
	}
	d, err := service.Run(t.Context(), RunRequest{SessionID: c.SessionID, Task: dTask})
	if err != nil || !d.Result.OK || summaryCalls != 0 {
		t.Fatalf("complete D below trigger: %+v %v; summaries=%d", d, err, summaryCalls)
	}
	e, err := service.Run(t.Context(), RunRequest{SessionID: c.SessionID, Task: eTask})
	if err != nil || !e.Result.OK || summaryCalls != 2 {
		t.Fatalf("compact E after same-Core overflow: %+v %v; summaries=%d", e, err, summaryCalls)
	}
	var memory struct {
		Summaries []struct {
			Text    string `json:"text"`
			Sources []struct {
				ExecutionID string `json:"executionId"`
			} `json:"sources"`
		} `json:"summaries"`
		Covered        map[string]json.RawMessage `json:"covered"`
		LastCompaction struct {
			BeforeTokens int64 `json:"beforeTokens"`
			AfterTokens  int64 `json:"afterTokens"`
			TargetTokens int64 `json:"targetTokens"`
			SummaryCalls int   `json:"summaryCalls"`
			TargetMet    bool  `json:"targetMet"`
		} `json:"lastCompaction"`
	}
	if err := json.Unmarshal(service.options.ContextMemory.begin(c.SessionID).get("compaction-v2"), &memory); err != nil {
		t.Fatal(err)
	}
	if len(memory.Summaries) != 1 || memory.Summaries[0].Text != mergedNote ||
		memory.LastCompaction.BeforeTokens <= limit || memory.LastCompaction.AfterTokens > target ||
		memory.LastCompaction.TargetTokens != target || memory.LastCompaction.SummaryCalls != 2 || !memory.LastCompaction.TargetMet {
		t.Fatalf("overflow compaction state: %+v", memory.LastCompaction)
	}
	seenSources := map[string]bool{}
	for _, source := range memory.Summaries[0].Sources {
		seenSources[source.ExecutionID] = true
	}
	if !seenSources[c.ExecutionID] || !seenSources[d.ExecutionID] ||
		len(memory.Covered[c.ExecutionID]) == 0 || len(memory.Covered[d.ExecutionID]) == 0 {
		t.Fatalf("C and D source state was not adopted with the merged note: %+v", memory)
	}
	if _, err := service.Run(t.Context(), RunRequest{SessionID: c.SessionID, Task: followupTask}); err != nil {
		t.Fatal(err)
	}
	if summaryCalls != 2 {
		t.Fatalf("unexpected follow-up re-summarization: calls=%d", summaryCalls)
	}
}

func TestJSCompactionUsesSecondSmallerContinuationCandidate(t *testing.T) {
	store, workspace := delegationStore(t)
	const limit = 6000
	const trigger = 2500
	const target = 1800
	installCompactionWithPolicy(t, workspace,
		map[string]any{"inputTokens": limit},
		map[string]int64{"triggerTokens": trigger, "targetTokens": target})
	raw := strings.Repeat("CANDIDATE_SOURCE_FACT ", 330)
	summaryCalls, mainCalls, secondSawFirst := 0, 0, false
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
			summaryCalls++
			if summaryCalls == 1 {
				return contracts.ModelResult{Text: "FIRST_LONG_CANDIDATE " + strings.Repeat("preserve continuation details ", 130)}, nil
			}
			secondSawFirst = strings.Contains(string(contracts.JSON(request.Messages)), "FIRST_LONG_CANDIDATE")
			return contracts.ModelResult{Text: "SECOND_SMALL_CANDIDATE"}, nil
		}
		mainCalls++
		if request.Messages[len(request.Messages)-1].VisibleText() == "seed candidate history" {
			return contracts.ModelResult{Text: raw}, nil
		}
		if mainCalls == 2 {
			encoded := string(contracts.JSON(request.Messages))
			if strings.Contains(encoded, "FIRST_LONG_CANDIDATE") || !strings.Contains(encoded, "SECOND_SMALL_CANDIDATE") {
				return contracts.ModelResult{}, errors.New("larger first candidate was selected")
			}
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	first, err := service.Run(t.Context(), RunRequest{Task: "seed candidate history", ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("seed history: %+v %v", first, err)
	}
	second, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "continue candidate history"})
	if err != nil || !second.Result.OK {
		t.Fatalf("candidate compaction: %+v %v", second, err)
	}
	if summaryCalls != 2 || !secondSawFirst {
		t.Fatalf("bounded summary input: calls=%d secondSawFirst=%v", summaryCalls, secondSawFirst)
	}
	var memory struct {
		Summaries []struct {
			Text    string `json:"text"`
			Sources []struct {
				ExecutionID string `json:"executionId"`
			} `json:"sources"`
		} `json:"summaries"`
		LastCompaction struct {
			SummaryCalls      int    `json:"summaryCalls"`
			TargetTokens      int64  `json:"targetTokens"`
			AfterTokens       int64  `json:"afterTokens"`
			TargetMet         bool   `json:"targetMet"`
			SelectedCandidate string `json:"selectedCandidate"`
		} `json:"lastCompaction"`
	}
	if err := json.Unmarshal(service.options.ContextMemory.begin(first.SessionID).get("compaction-v2"), &memory); err != nil {
		t.Fatal(err)
	}
	if len(memory.Summaries) != 1 || memory.Summaries[0].Text != "SECOND_SMALL_CANDIDATE" ||
		len(memory.Summaries[0].Sources) == 0 || memory.Summaries[0].Sources[0].ExecutionID != first.ExecutionID ||
		memory.LastCompaction.SummaryCalls != 2 || memory.LastCompaction.SelectedCandidate != "second" ||
		!memory.LastCompaction.TargetMet || memory.LastCompaction.TargetTokens != target || memory.LastCompaction.AfterTokens > target {
		t.Fatalf("selected candidate state mismatch: %+v", memory)
	}
}

func TestJSCompactionKeepsFirstCandidateWhenSecondDoesNotShrink(t *testing.T) {
	store, workspace := delegationStore(t)
	const limit = 6000
	const trigger = 2500
	const target = 1800
	installCompactionWithPolicy(t, workspace,
		map[string]any{"inputTokens": limit},
		map[string]int64{"triggerTokens": trigger, "targetTokens": target})
	raw := strings.Repeat("FIRST_CANDIDATE_SOURCE_FACT ", 330)
	firstText := "FIRST_CONTINUATION_CANDIDATE " + strings.Repeat("preserve required continuation detail ", 45)
	summaryCalls, mainCalls, secondSawFirst := 0, 0, false
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
			summaryCalls++
			if summaryCalls == 1 {
				return contracts.ModelResult{Text: firstText}, nil
			}
			secondSawFirst = strings.Contains(string(contracts.JSON(request.Messages)), firstText)
			return contracts.ModelResult{Text: firstText}, nil
		}
		mainCalls++
		if request.Messages[len(request.Messages)-1].VisibleText() == "seed first-candidate history" {
			return contracts.ModelResult{Text: raw}, nil
		}
		encoded := string(contracts.JSON(request.Messages))
		if mainCalls == 2 && (!strings.Contains(encoded, firstText) || strings.Contains(encoded, "FIRST_CANDIDATE_SOURCE_FACT")) {
			return contracts.ModelResult{}, errors.New("first continuation candidate was not kept")
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	first, err := service.Run(t.Context(), RunRequest{Task: "seed first-candidate history", ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("seed history: %+v %v", first, err)
	}
	result, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "continue with first candidate"})
	if err != nil || !result.Result.OK {
		t.Fatalf("first candidate continuation: %+v %v", result, err)
	}
	if summaryCalls != 2 || !secondSawFirst {
		t.Fatalf("bounded second summary: calls=%d secondSawFirst=%v", summaryCalls, secondSawFirst)
	}
	var memory struct {
		Summaries []struct {
			Text    string `json:"text"`
			Sources []struct {
				ExecutionID string `json:"executionId"`
			} `json:"sources"`
		} `json:"summaries"`
		LastCompaction struct {
			SummaryCalls      int    `json:"summaryCalls"`
			TargetTokens      int64  `json:"targetTokens"`
			AfterTokens       int64  `json:"afterTokens"`
			TargetMet         bool   `json:"targetMet"`
			SelectedCandidate string `json:"selectedCandidate"`
		} `json:"lastCompaction"`
	}
	if err := json.Unmarshal(service.options.ContextMemory.begin(first.SessionID).get("compaction-v2"), &memory); err != nil {
		t.Fatal(err)
	}
	if len(memory.Summaries) != 1 || memory.Summaries[0].Text != firstText ||
		len(memory.Summaries[0].Sources) == 0 || memory.Summaries[0].Sources[0].ExecutionID != first.ExecutionID ||
		memory.LastCompaction.SummaryCalls != 2 || memory.LastCompaction.SelectedCandidate != "first" ||
		memory.LastCompaction.TargetMet || memory.LastCompaction.TargetTokens != target || memory.LastCompaction.AfterTokens > limit {
		t.Fatalf("first candidate state mismatch: %+v", memory)
	}
}

func TestJSCompactionWithinTurnThenCompletedTurnReferencesAndResume(t *testing.T) {
	store, workspace := delegationStore(t)
	installCompactionWithPolicy(t, workspace, map[string]any{"inputTokens": 8000},
		map[string]int64{"triggerTokens": 3000, "targetTokens": 2500})
	a, b := strings.Repeat("FIRST_A ", 650), strings.Repeat("SECOND_B ", 600)
	for name, text := range map[string]string{"a.txt": a, "b.txt": b} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mainCalls, summaries := 0, 0
	var firstExecution string
	provider := delegationProvider(func(_ context.Context, request contracts.ModelRequest, _ contracts.Observer) (contracts.ModelResult, error) {
		if strings.HasPrefix(request.Instruction, "Summarize this conversation") {
			summaries++
			if summaries > 2 {
				encoded := string(contracts.JSON(request.Messages))
				if strings.Contains(encoded, a) {
					t.Error("summary input reintroduced a covered completed-turn message")
				}
			}
			if len(request.Tools) != 0 || request.Model.ModelID != "test/model" {
				t.Errorf("wrong summary connection: %+v", request)
			}
			return contracts.ModelResult{Text: "SUMMARY_NOTE_" + strconv.Itoa(summaries) + ": File reading completed; preserve the user's task and continue verification."}, nil
		}
		mainCalls++
		if mainCalls <= 2 {
			name := "a.txt"
			if mainCalls == 2 {
				name = "b.txt"
			}
			return contracts.ModelResult{Calls: []contracts.ToolCall{{CallID: name, Name: "read_file", Arguments: contracts.JSON(map[string]string{"path": name})}}}, nil
		}
		encoded := string(contracts.JSON(request.Messages))
		if mainCalls == 3 {
			if !strings.Contains(encoded, "SUMMARY_NOTE_1") || strings.Contains(encoded, a) || !strings.Contains(encoded, b) || !strings.Contains(encoded, "in progress") {
				t.Errorf("turn-internal projection incorrect: %s", encoded)
			}
			return contracts.ModelResult{Text: "TURN1_FINAL_NOTE " + strings.Repeat("final facts ", 650)}, nil
		}
		if mainCalls == 4 && (!strings.Contains(encoded, "SUMMARY_NOTE_3") || strings.Contains(encoded, "SUMMARY_NOTE_1") || strings.Contains(encoded, "SUMMARY_NOTE_2") || !strings.Contains(encoded, firstExecution) || !strings.Contains(encoded, "final message 5") || strings.Contains(encoded, "TURN1_FINAL_NOTE ")) {
			t.Errorf("completed turn projection or consolidated reference lost: %s", encoded)
		}
		if mainCalls == 6 && strings.Contains(encoded, "SUMMARY_NOTE_") {
			t.Error("from-turn boundary reused an older summary")
		}
		return contracts.ModelResult{Text: "done"}, nil
	})
	service := NewService(store, provider, RunOptions{Workspace: workspace})
	defer service.Close()
	first, err := service.Run(t.Context(), RunRequest{Task: "read both", ModelID: "test/model"})
	if err != nil || !first.Result.OK {
		t.Fatalf("first compaction: %+v %v", first, err)
	}
	firstExecution = first.ExecutionID
	second, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "continue"})
	if err != nil || !second.Result.OK {
		t.Fatalf("next-turn compaction: %+v %v", second, err)
	}
	if _, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "continue again"}); err != nil {
		t.Fatal(err)
	}
	if summaries < 2 || summaries > 4 {
		t.Fatalf("unexpected summary calls across two compactions: %d", summaries)
	}
	var memory struct {
		Summaries []struct {
			Sources []struct {
				ExecutionID  string `json:"executionId"`
				FinalMessage *int64 `json:"finalMessage"`
			}
		}
		LastCompaction struct {
			SummaryCalls int `json:"summaryCalls"`
		} `json:"lastCompaction"`
	}
	if err := json.Unmarshal(service.options.ContextMemory.begin(first.SessionID).get("compaction-v2"), &memory); err != nil {
		t.Fatal(err)
	}
	if len(memory.Summaries) != 1 || memory.LastCompaction.SummaryCalls > 2 || memory.Summaries[0].Sources[0].FinalMessage == nil || *memory.Summaries[0].Sources[0].FinalMessage != 5 {
		t.Fatalf("references: %+v", memory)
	}
	stored, err := store.Messages(t.Context(), first.ExecutionID, 5, 5)
	if err != nil || len(stored) != 1 || !strings.Contains(string(stored[0].JSON), "TURN1_FINAL_NOTE") {
		t.Fatalf("final locator did not resolve: %+v %v", stored, err)
	}
	conversation, err := store.Conversation(t.Context(), first.SessionID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var canonical strings.Builder
	for _, message := range conversation {
		canonical.Write(message.JSON)
	}
	if !strings.Contains(canonical.String(), a) || !strings.Contains(canonical.String(), b) || strings.Contains(canonical.String(), "SUMMARY_NOTE_") {
		t.Fatal("raw conversation was modified")
	}
	children, err := store.Children(t.Context(), first.ExecutionID)
	if err != nil || len(children) != 0 {
		t.Fatalf("compaction created children: %+v %v", children, err)
	}
	fromTurn := int64(3)
	if _, err := service.Run(t.Context(), RunRequest{SessionID: first.SessionID, Task: "use only recent turns", FromTurn: &fromTurn}); err != nil {
		t.Fatal(err)
	}
}
