package history

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func openConversationFixture(t *testing.T) (*Store, Execution) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	model := json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`)
	session, err := store.CreateSession(ctx, SessionInput{
		WorkspaceRoot: t.TempDir(), AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, BeginExecutionInput{
		ExecutionID: "conversation-root", Task: "root task", SessionID: session.ID,
		Model: model, Build: json.RawMessage(`{"fixture":"test"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, execution
}

func TestAssistantProgressDoesNotPersistEveryBodyPrefixAndTerminalTransfersLatest(t *testing.T) {
	for _, updates := range []int{2, 18} {
		t.Run(fmt.Sprintf("%d updates", updates), func(t *testing.T) {
			ctx := context.Background()
			store, execution := openConversationFixture(t)
			request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", RequestOrdinal: 1, ModelStep: 1,
				PhysicalRequestID: "physical-request"}
			for i := 1; i <= updates; i++ {
				text := fmt.Sprintf("partial-%d", i)
				if i == updates {
					text = "fixed final body"
				}
				facts, err := store.SaveAssistantProgress(ctx, AssistantProgressInput{
					RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: uint64(i),
					Request: request, Text: text,
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(facts) != 1 || facts[0].Conversation.LocalKey != "parent/1/answer" ||
					facts[0].Conversation.FirstSequence != 1 || facts[0].Conversation.Sequence != uint64(i) {
					t.Fatalf("progress identity at update %d = %#v", i, facts)
				}
			}

			beforeTerminal, err := store.Records(ctx, execution.ID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(beforeTerminal) != 1 {
				t.Fatalf("permanent records before terminal = %d, want only one marker", len(beforeTerminal))
			}
			if bytes.Contains(beforeTerminal[0].Payload, []byte("fixed final body")) ||
				bytes.Contains(beforeTerminal[0].Payload, []byte("partial-")) {
				t.Fatalf("stream body was permanently copied into its start marker: %s", beforeTerminal[0].Payload)
			}

			terminal := json.RawMessage(`{"ok":false,"task":"root task","outcome":"cancelled","stopReason":"cancelled","error":"cancelled"}`)
			terminalSequence := uint64(updates + 1)
			facts, err := store.EndExecutionWithConversation(ctx, execution.ID, OutcomeCancelled, terminal,
				"2026-10-11T00:00:00Z", execution.ID, terminalSequence)
			if err != nil {
				t.Fatal(err)
			}
			var incomplete []contracts.ExecutionFact
			for _, fact := range facts {
				if fact.Conversation.EntityKind == "assistant" {
					incomplete = append(incomplete, fact)
				}
			}
			if len(incomplete) != 1 || incomplete[0].Phase != "incomplete" || incomplete[0].Text != "fixed final body" ||
				incomplete[0].Conversation.FirstSequence != 1 || incomplete[0].Conversation.Sequence != terminalSequence {
				t.Fatalf("terminal transfer fact = %#v", incomplete)
			}
			afterTerminal, err := store.Records(ctx, execution.ID, 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(afterTerminal) != 3 {
				t.Fatalf("permanent records after %d updates = %d, want marker, incomplete body, terminal", updates, len(afterTerminal))
			}
			bodyCopies := 0
			for _, record := range afterTerminal {
				bodyCopies += strings.Count(string(record.Payload), "fixed final body")
			}
			if bodyCopies != 1 {
				t.Fatalf("final body appears %d times in permanent records", bodyCopies)
			}

			cut, err := store.CaptureConversationCut(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			if cut.ThroughSequence == nil || *cut.ThroughSequence != terminalSequence || len(cut.Executions) != 1 || len(cut.Executions[0].States) != 0 {
				t.Fatalf("terminal cut state = %#v", cut)
			}
			result, err := store.TerminalResult(ctx, execution.ID)
			if err != nil {
				t.Fatal(err)
			}
			expectedTerminal, err := contracts.EncodeCanonicalJSON(terminal)
			if err != nil {
				t.Fatal(err)
			}
			if result.Task != "root task" || !bytes.Equal(result.RuntimeOutcome, expectedTerminal) || result.Outcome != OutcomeCancelled {
				t.Fatalf("terminal result = %#v", result)
			}
		})
	}
}

func TestTerminalTransfersThinkingKindAndCompleteStableKey(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", RequestOrdinal: 1, ModelStep: 1}
	key := "rs_1/summary/2"
	if _, err := store.SaveAssistantProgress(ctx, AssistantProgressInput{
		RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 1, Request: request,
		Thinking: []contracts.ThinkingItem{{Key: key, Text: "current public summary", Kind: "summary"}},
	}); err != nil {
		t.Fatal(err)
	}
	facts, err := store.EndExecutionWithConversation(ctx, execution.ID, OutcomeCancelled,
		json.RawMessage(`{"ok":false,"task":"root task","outcome":"cancelled"}`), "2026-10-11T00:00:00Z", execution.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	var terminalThinking *contracts.ExecutionFact
	for index := range facts {
		if facts[index].Conversation.EntityKind == "thinking" {
			terminalThinking = &facts[index]
		}
	}
	if terminalThinking == nil || terminalThinking.Phase != "incomplete" || terminalThinking.ThinkingKind != "summary" ||
		len(terminalThinking.Thinking) != 1 || terminalThinking.Thinking[0].Key != key || terminalThinking.Thinking[0].Kind != "summary" {
		t.Fatalf("terminal thinking fact lost its public kind or full key: %#v", terminalThinking)
	}

	cut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var replay *contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		if fact.Conversation.EntityKind == "thinking" {
			copy := fact
			replay = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replay == nil || replay.ThinkingKind != "summary" || len(replay.Thinking) != 1 ||
		replay.Thinking[0].Key != key || replay.Thinking[0].Kind != "summary" || replay.Text != "current public summary" {
		t.Fatalf("terminal replay lost thinking kind or stable key: %#v", replay)
	}
}

func TestAssistantResultAndActiveStateClearShareTransaction(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", RequestOrdinal: 1, ModelStep: 1}
	for sequence, text := range []string{"partial", "latest partial"} {
		if _, err := store.SaveAssistantProgress(ctx, AssistantProgressInput{
			RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: uint64(sequence + 1),
			Request: request, Text: text,
		}); err != nil {
			t.Fatal(err)
		}
	}
	fact := contracts.ExecutionFact{Kind: "assistant_message", Phase: "completed", Role: "assistant", Text: "final body",
		Request: request, Conversation: contracts.ConversationMetadata{EntityKind: "assistant", LocalKey: "parent/1/answer"}}
	_, saved, err := store.AppendConversationMessage(ctx, ConversationMessageInput{
		RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 3,
		Message: json.RawMessage(`{"role":"assistant","content":{"kind":"text","text":"final body"}}`),
		Facts:   []contracts.ExecutionFact{fact}, FinalizeRequest: &request,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].Conversation.FirstSequence != 1 || saved[0].Conversation.Sequence != 3 ||
		saved[0].Text != "final body" || saved[0].Locator.RecordID == "" {
		t.Fatalf("saved final fact = %#v", saved)
	}
	cut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut.Executions) != 1 || len(cut.Executions[0].States) != 0 || cut.ThroughSequence == nil || *cut.ThroughSequence != 3 {
		t.Fatalf("cut after final result = %#v", cut)
	}
	records, err := store.Records(ctx, execution.ID, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("permanent records = %d, want marker and final result", len(records))
	}
	metadata := conversationMetadata(records[1].Payload)
	if metadata == nil || metadata.LocalKey != "parent/1/answer" || metadata.FirstSequence != 1 || metadata.Sequence != 3 {
		t.Fatalf("final payload metadata = %#v", metadata)
	}
}

func TestExecutionTerminalKeepsStartEntityPosition(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	start := contracts.ConversationMetadata{RootExecutionID: execution.ID, Sequence: 1,
		EntityKind: "execution", LocalKey: "execution", FirstSequence: 1, ItemIndex: 1}
	if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
		Kind: "execution_admission", Payload: json.RawMessage(`{"kind":"execution","phase":"started"}`), Conversation: &start,
	}); err != nil {
		t.Fatal(err)
	}
	facts, err := store.EndExecutionWithConversation(ctx, execution.ID, OutcomeCompleted,
		json.RawMessage(`{"ok":true,"outcome":"final"}`), "2026-10-11T00:00:00Z", execution.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	var terminal *contracts.ExecutionFact
	for index := range facts {
		if facts[index].Conversation.EntityKind == "execution" {
			terminal = &facts[index]
		}
	}
	if terminal == nil || terminal.Conversation.Sequence != 2 || terminal.Conversation.FirstSequence != 1 || terminal.Conversation.ItemIndex != 1 {
		t.Fatalf("terminal execution position = %#v", terminal)
	}
}

func TestConversationCutExposesChildOnlyAfterSavedStart(t *testing.T) {
	ctx := context.Background()
	store, parent := openConversationFixture(t)
	child, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{
		ExecutionID: "conversation-child-before-start", Task: "child task", ParentExecutionID: parent.ID,
		SpawnCallID: "spawn-call", Model: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`),
		Build: json.RawMessage(`{"fixture":"test"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	cut, err := store.CaptureConversationCut(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut.Executions) != 1 || cut.Executions[0].ExecutionID != parent.ID {
		t.Fatalf("unsaved child appeared in conversation cut: %+v", cut.Executions)
	}
	if _, err := store.AppendOccurrence(ctx, child.ID, OccurrenceInput{Kind: "execution_admission",
		Payload: contracts.JSON(map[string]any{"executionId": child.ID, "kind": "execution", "name": "run", "phase": "started"}),
	}); err != nil {
		t.Fatal(err)
	}
	cut, err = store.CaptureConversationCut(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut.Executions) != 2 || cut.Executions[1].ExecutionID != child.ID {
		t.Fatalf("saved child start missing from conversation cut: %+v", cut.Executions)
	}
}

func TestLegacyConversationWalkUsesSemanticAndStateOrdinalsWithoutSequence(t *testing.T) {
	ctx := context.Background()
	store, parent := openConversationFixture(t)
	child, err := store.BeginChildExecution(ctx, BeginChildExecutionInput{
		ExecutionID: "conversation-child", Task: "child task", ParentExecutionID: parent.ID, SpawnCallID: "spawn-call",
		Model: json.RawMessage(`{"provider":"fixture","modelId":"fixture/model"}`),
		Build: json.RawMessage(`{"fixture":"test"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	marker, err := store.AppendOccurrence(ctx, parent.ID, OccurrenceInput{Kind: "assistant_message",
		Payload: json.RawMessage(`{"kind":"assistant_message","phase":"started","request":{"lane":"parent","requestOrdinal":1,"modelStep":1}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, parent.ID, OccurrenceInput{Kind: "tool_call",
		Payload: json.RawMessage(`{"callId":"spawn-call","name":"run_agent","phase":"started"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAssistantText(ctx, parent.ID, "parent", 1, 1, marker.Ordinal,
		json.RawMessage(`{"kind":"assistant_message","phase":"progress","request":{"lane":"parent","requestOrdinal":1,"modelStep":1},"payload":{"text":"latest active body"}}`),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, child.ID, OccurrenceInput{Kind: "execution_admission",
		Payload: contracts.JSON(map[string]any{"executionId": child.ID, "kind": "execution", "name": "run", "phase": "started"}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendOccurrence(ctx, child.ID, OccurrenceInput{Kind: "assistant_message",
		Payload: json.RawMessage(`{"kind":"assistant_message","phase":"completed","role":"assistant","text":"child answer","request":{"lane":"parent","requestOrdinal":1,"modelStep":1}}`),
	}); err != nil {
		t.Fatal(err)
	}

	cut, err := store.CaptureConversationCut(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cut.ThroughSequence != nil {
		t.Fatalf("legacy cut fabricated through sequence %d", *cut.ThroughSequence)
	}
	var facts []contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		facts = append(facts, fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var rootAssistantFirst, activeFirst, spawnIndex, childTaskIndex, childStartParent string
	var rootAssistantSequence, activeSequence, childTaskSequence uint64
	for index, fact := range facts {
		if fact.ExecutionID == parent.ID && fact.Conversation.EntityKind == "assistant" {
			if fact.Text == "" {
				rootAssistantFirst = fmt.Sprint(fact.Conversation.FirstSequence)
				rootAssistantSequence = fact.Conversation.Sequence
			} else if fact.Text == "latest active body" {
				activeFirst = fmt.Sprint(fact.Conversation.FirstSequence)
				activeSequence = fact.Conversation.Sequence
			}
		}
		if fact.ExecutionID == parent.ID && fact.CallID == "spawn-call" {
			spawnIndex = fmt.Sprint(index)
		}
		if fact.ExecutionID == child.ID && fact.Kind == "task" {
			childTaskIndex = fmt.Sprint(index)
			childTaskSequence = fact.Conversation.Sequence
			if fact.ParentCallID != "spawn-call" {
				t.Fatalf("child task parent call = %q", fact.ParentCallID)
			}
		}
		if fact.ExecutionID == child.ID && fact.Kind == "execution_admission" {
			childStartParent = fact.ParentCallID
		}
	}
	if rootAssistantFirst == "" || rootAssistantFirst != activeFirst || rootAssistantSequence == 0 || activeSequence == 0 {
		t.Fatalf("legacy assistant marker/state positions = %q/%d and %q/%d", rootAssistantFirst, rootAssistantSequence, activeFirst, activeSequence)
	}
	if spawnIndex == "" || childTaskIndex == "" || childTaskSequence == 0 || childStartParent != "spawn-call" {
		t.Fatalf("legacy child causal fields: spawn index %q child task index %q sequence %d start parent %q", spawnIndex, childTaskIndex, childTaskSequence, childStartParent)
	}
	var spawn, childTask int
	_, _ = fmt.Sscan(spawnIndex, &spawn)
	_, _ = fmt.Sscan(childTaskIndex, &childTask)
	if childTask <= spawn {
		t.Fatalf("child task at index %d precedes spawn at %d", childTask, spawn)
	}
}

func TestAssistantResultCommitsFinalAnswerThinkingAndStateClearTogether(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	request := contracts.RequestIdentity{ExecutionID: execution.ID, Lane: "parent", RequestOrdinal: 2, ModelStep: 2,
		PhysicalRequestID: "physical-request-2"}
	for _, update := range []struct {
		sequence uint64
		text     string
	}{
		{sequence: 1, text: "draft answer"},
		{sequence: 2, text: "latest streamed answer"},
	} {
		facts, err := store.SaveAssistantProgress(ctx, AssistantProgressInput{
			RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: update.sequence, Request: request,
			Text: update.text, Thinking: []contracts.ThinkingItem{
				{Key: "rs_1/reasoning/0", Text: "draft thought", Kind: "reasoning"},
				{Key: "rs_1/summary/0", Text: "draft summary", Kind: "summary"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(facts) != 3 {
			t.Fatalf("progress facts at sequence %d = %d, want answer, reasoning and summary", update.sequence, len(facts))
		}
		for index, expected := range []struct {
			key, kind, text string
		}{{"rs_1/reasoning/0", "reasoning", "draft thought"}, {"rs_1/summary/0", "summary", "draft summary"}} {
			fact := facts[index+1]
			if fact.ThinkingKind != expected.kind || len(fact.Thinking) != 1 || fact.Thinking[0].Key != expected.key ||
				fact.Thinking[0].Kind != expected.kind || fact.Text != expected.text ||
				fact.Conversation.FirstSequence != 1 || fact.Conversation.LocalKey != "parent/2/"+expected.key {
				t.Fatalf("progress thinking identity at sequence %d = %#v", update.sequence, fact)
			}
		}
	}
	activeCut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	activeThinking := map[string]contracts.ExecutionFact{}
	if err := store.WalkConversationCut(ctx, activeCut, func(fact contracts.ExecutionFact) error {
		if fact.Conversation.EntityKind == "thinking" {
			activeThinking[fact.Text] = fact
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		key, kind, text string
	}{{"rs_1/reasoning/0", "reasoning", "draft thought"}, {"rs_1/summary/0", "summary", "draft summary"}} {
		fact, ok := activeThinking[expected.text]
		if !ok || fact.Phase != "streaming" || fact.ThinkingKind != expected.kind || len(fact.Thinking) != 1 ||
			fact.Thinking[0].Key != expected.key || fact.Thinking[0].Kind != expected.kind ||
			fact.Conversation.FirstSequence != 1 || fact.Conversation.LocalKey != "parent/2/"+expected.key {
			t.Fatalf("latest-state thinking replay = %#v", fact)
		}
	}
	resultPayload := contracts.JSON(map[string]any{
		"id": "physical-request-2-result", "parent": "physical-request-2", "kind": "model_result",
		"phase": "end", "modelStep": 2,
		"result": map[string]any{"text": "settled answer", "finishReason": "stop", "calls": []any{}},
	})
	result, err := store.SaveAssistantResult(ctx, AssistantResultInput{
		RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 3, Request: request,
		Payload: resultPayload, Text: "settled answer",
		Thinking: []contracts.ThinkingItem{
			{Key: "rs_1/reasoning/0", Text: "settled thought", Kind: "reasoning"},
			{Key: "rs_1/summary/0", Text: "settled summary", Kind: "summary"},
		}, Phase: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	facts := result.Facts
	if len(facts) != 3 || facts[0].Text != "settled answer" || facts[0].Phase != "completed" ||
		facts[0].Conversation.LocalKey != "parent/2/answer" || facts[0].Conversation.FirstSequence != 1 || facts[0].Conversation.Sequence != 3 {
		t.Fatalf("saved result facts = %#v", facts)
	}
	for index, expected := range []struct {
		key, kind, text string
	}{{"rs_1/reasoning/0", "reasoning", "settled thought"}, {"rs_1/summary/0", "summary", "settled summary"}} {
		fact := facts[index+1]
		if fact.Text != expected.text || fact.ThinkingKind != expected.kind || len(fact.Thinking) != 1 ||
			fact.Thinking[0].Key != expected.key || fact.Thinking[0].Kind != expected.kind ||
			fact.Conversation.EntityKind != "thinking" || fact.Conversation.LocalKey != "parent/2/"+expected.key ||
			fact.Conversation.FirstSequence != 1 || fact.Conversation.Sequence != 3 {
			t.Fatalf("saved result thinking fact = %#v", fact)
		}
	}

	cut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut.Executions) != 1 || len(cut.Executions[0].States) != 0 {
		t.Fatalf("state remained after committed model result: %#v", cut.Executions)
	}
	var walked []contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		walked = append(walked, fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var answer *contracts.ExecutionFact
	thoughts := map[string]contracts.ExecutionFact{}
	for index := range walked {
		fact := &walked[index]
		if fact.Conversation.EntityKind == "assistant" && fact.Text == "settled answer" {
			answer = fact
		}
		if fact.Conversation.EntityKind == "thinking" {
			thoughts[fact.Text] = *fact
		}
	}
	if answer == nil || answer.Phase != "completed" || answer.Request.ModelStep != 2 || answer.Conversation.FirstSequence != 1 {
		t.Fatalf("result replay answer = %#v", answer)
	}
	for _, expected := range []struct {
		key, kind, text string
	}{{"rs_1/reasoning/0", "reasoning", "settled thought"}, {"rs_1/summary/0", "summary", "settled summary"}} {
		thought, ok := thoughts[expected.text]
		if !ok || thought.Phase != "completed" || thought.ThinkingKind != expected.kind || len(thought.Thinking) != 1 ||
			thought.Thinking[0].Key != expected.key || thought.Thinking[0].Kind != expected.kind ||
			thought.Conversation.LocalKey != "parent/2/"+expected.key {
			t.Fatalf("result replay thinking = %#v", thought)
		}
	}
	assistantMessage := json.RawMessage(`{"role":"assistant","content":{"kind":"text","text":"settled answer"}}`)
	messageFact := contracts.ExecutionFact{Kind: "assistant_message", Phase: "completed", Role: "assistant", Text: "settled answer",
		Request: request, Thinking: []contracts.ThinkingItem{
			{Key: "rs_1/reasoning/0", Text: "settled thought", Kind: "reasoning"},
			{Key: "rs_1/summary/0", Text: "settled summary", Kind: "summary"},
		},
		Conversation: contracts.ConversationMetadata{EntityKind: "assistant", LocalKey: "parent/2/answer"}}
	_, appended, err := store.AppendConversationMessage(ctx, ConversationMessageInput{
		RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 4, Message: assistantMessage,
		Facts: []contracts.ExecutionFact{messageFact}, FinalizeRequest: &request,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(appended) != 3 || appended[0].Conversation.FirstSequence != 1 || appended[1].Conversation.FirstSequence != 1 ||
		appended[2].Conversation.FirstSequence != 1 || appended[0].Conversation.LocalKey != answer.Conversation.LocalKey ||
		appended[1].Conversation.LocalKey != "parent/2/rs_1/reasoning/0" || appended[2].Conversation.LocalKey != "parent/2/rs_1/summary/0" ||
		appended[1].ThinkingKind != "reasoning" || appended[1].Thinking[0].Kind != "reasoning" ||
		appended[2].ThinkingKind != "summary" || appended[2].Thinking[0].Kind != "summary" {
		t.Fatalf("message append changed result entity identity: %#v", appended)
	}
}

func TestLegacySourcePayloadsSupplyAssistantIdentityAndPhase(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	progressPayload := contracts.JSON(contracts.Observation{ExecutionID: execution.ID, ID: "request-2-text-1",
		Parent: "request-2", Kind: "assistant_text", Name: "legacy-provider", Phase: "progress",
		Result: contracts.JSON(map[string]string{"text": "legacy settled answer"}), ModelStep: 2})
	progress, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "assistant_message", Payload: progressPayload})
	if err != nil {
		t.Fatal(err)
	}
	statePayload := json.RawMessage(`{"kind":"assistant_message","observedAt":"2026-10-11T00:00:00Z","payload":{"text":"legacy settled answer"}}`)
	if err := store.SetAssistantText(ctx, execution.ID, "parent", 2, 2, progress.Ordinal, statePayload); err != nil {
		t.Fatal(err)
	}
	resultPayload := contracts.JSON(contracts.Observation{ExecutionID: execution.ID, ID: "request-2-result",
		Parent: "request-2", Kind: "model_result", Name: "legacy-provider", Phase: "end",
		Result: contracts.JSON(map[string]any{"text": "legacy settled answer", "calls": []any{}}), ModelStep: 2})
	resultRecord, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "model_result", Payload: resultPayload})
	if err != nil {
		t.Fatal(err)
	}

	activeCut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var activeState *contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, activeCut, func(fact contracts.ExecutionFact) error {
		if fact.Text == "legacy settled answer" && fact.Locator.RecordID == "" {
			copy := fact
			activeState = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if activeState == nil || activeState.Phase != "streaming" || activeState.Request.ModelStep != 2 ||
		activeState.Request.RequestOrdinal != 2 || activeState.Conversation.LocalKey != "parent/2/answer" {
		t.Fatalf("legacy active state identity = %#v", activeState)
	}

	// HEAD's EndExecution copied this exact state event into an assistant_message
	// semantic row, then removed the state row. Reproduce that stored source shape.
	transfer, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "assistant_message", Payload: statePayload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "DELETE FROM assistant_text_states WHERE execution_id=?", execution.ID); err != nil {
		t.Fatal(err)
	}
	settledCut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var resultFact, transferredFact *contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, settledCut, func(fact contracts.ExecutionFact) error {
		if fact.Locator.RecordID == resultRecord.ID {
			copy := fact
			resultFact = &copy
		}
		if fact.Locator.RecordID == transfer.ID {
			copy := fact
			transferredFact = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for label, fact := range map[string]*contracts.ExecutionFact{"model result": resultFact, "terminal transfer": transferredFact} {
		if fact == nil || fact.Phase != "completed" || fact.Request.ModelStep != 2 || fact.Request.RequestOrdinal != 2 ||
			fact.Conversation.LocalKey != "parent/2/answer" || fact.Role != "assistant" {
			t.Fatalf("legacy %s identity/phase = %#v", label, fact)
		}
	}
}

func TestLegacySavedAssistantMessageMergesWithItsModelResult(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	steps := []struct {
		step int
		text string
	}{
		{step: 1, text: "first answer"},
		{step: 2, text: "second answer"},
	}
	for _, item := range steps {
		resultPayload := contracts.JSON(contracts.Observation{ExecutionID: execution.ID,
			ID: fmt.Sprintf("request-%d-result", item.step), Parent: fmt.Sprintf("request-%d", item.step),
			Kind: "model_result", Name: "legacy-provider", Phase: "end", ModelStep: item.step,
			Result: contracts.JSON(map[string]any{"text": item.text, "calls": []any{}})})
		if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{Kind: "model_result", Payload: resultPayload}); err != nil {
			t.Fatal(err)
		}
		message, err := contracts.EncodeJSON(contracts.Message{Role: "assistant",
			Content: contracts.JSON(contracts.TextContent{Kind: "text", Text: item.text})})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendMessage(ctx, execution.ID, message); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendOccurrence(ctx, execution.ID, OccurrenceInput{
			Kind: "assistant_message", Payload: message, Content: message,
		}); err != nil {
			t.Fatal(err)
		}
	}

	cut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	byText := map[string][]contracts.ExecutionFact{}
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		if fact.Kind == "assistant_message" && fact.Text != "" {
			byText[fact.Text] = append(byText[fact.Text], fact)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, item := range steps {
		facts := byText[item.text]
		if len(facts) != 2 {
			t.Fatalf("assistant facts for %q = %#v, want model result and saved message", item.text, facts)
		}
		first, second := facts[0], facts[1]
		wantKey := fmt.Sprintf("parent/%d/answer", item.step)
		for _, fact := range facts {
			if fact.Role != "assistant" || fact.Phase != "completed" || fact.Request.ModelStep != item.step ||
				fact.Request.RequestOrdinal != item.step || fact.Conversation.EntityKind != "assistant" ||
				fact.Conversation.LocalKey != wantKey || fact.Conversation.FirstSequence == 0 {
				t.Fatalf("legacy step %d assistant identity/status = %#v", item.step, fact)
			}
		}
		if first.Conversation.FirstSequence != second.Conversation.FirstSequence ||
			first.Conversation.ItemIndex != second.Conversation.ItemIndex ||
			first.RootExecutionOrdinal != second.RootExecutionOrdinal || first.Locator.RecordID == second.Locator.RecordID {
			t.Fatalf("legacy step %d result/message position or locator differs: result=%+v message=%+v", item.step, first, second)
		}
		keys[first.Conversation.LocalKey] = true
	}
	if len(keys) != len(steps) {
		t.Fatalf("legacy assistant entity keys = %#v, want one per model step", keys)
	}
}
