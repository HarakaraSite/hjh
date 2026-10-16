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
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "hooks", "compaction.js"))
	if err != nil {
		t.Fatal(err)
	}
	installLifecycleHooks(t, workspace, string(source))
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
			}
		})
	}
}

func TestJSCompactionWithinTurnThenCompletedTurnReferencesAndResume(t *testing.T) {
	store, workspace := delegationStore(t)
	installCompaction(t, workspace, map[string]any{"inputTokens": 4000})
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
		if mainCalls <= 5 && (!strings.Contains(encoded, "SUMMARY_NOTE_1") || !strings.Contains(encoded, "SUMMARY_NOTE_2") || !strings.Contains(encoded, firstExecution) || !strings.Contains(encoded, "final message 5") || strings.Contains(encoded, "TURN1_FINAL_NOTE ")) {
			t.Errorf("completed turn projection or reference lost: %s", encoded)
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
	if summaries != 2 {
		t.Fatalf("old coverage was summarized again: %d calls", summaries)
	}
	var memory struct {
		Summaries []struct {
			Sources []struct {
				ExecutionID  string `json:"executionId"`
				FinalMessage *int64 `json:"finalMessage"`
			}
		}
	}
	if err := json.Unmarshal(service.options.ContextMemory.begin(first.SessionID).get("compaction-v1"), &memory); err != nil {
		t.Fatal(err)
	}
	if len(memory.Summaries) != 2 || memory.Summaries[0].Sources[0].FinalMessage != nil || memory.Summaries[1].Sources[0].FinalMessage == nil || *memory.Summaries[1].Sources[0].FinalMessage != 5 {
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
