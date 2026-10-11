package recall

import (
	"encoding/json"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestProjectUsesPublicMessagesAndSavedToolEvidence(t *testing.T) {
	steering := true
	private := "provider-private-state"
	assistantText := "finished"
	evidence := Evidence{
		Execution: Execution{ExecutionID: "source-a", SessionID: "session-a", Turn: 4, AgentName: "helper",
			Selection: contracts.OpenRouterModel("old-model"), Lifecycle: "settled", Outcome: "cancelled", Task: "edit the file"},
		Messages: []Message{
			{Ordinal: 0, JSON: contracts.JSON(contracts.User("edit the file"))},
			{Ordinal: 1, JSON: contracts.JSON(contracts.Message{Role: "user", Steering: &steering,
				Content: contracts.JSON(contracts.TextContent{Kind: "text", Text: "keep the header"})})},
			{Ordinal: 2, JSON: contracts.JSON(contracts.Message{Role: "assistant", ProviderState: &contracts.ProviderState{
				Provider: "old-provider", ReplayItems: []json.RawMessage{contracts.JSON(private)}},
				Content: contracts.JSON([]contracts.ToolCall{{Kind: "tool_call", CallID: "call-1", Name: "read_file",
					Arguments: contracts.JSON(map[string]string{"path": "notes.txt"})}})})},
			{Ordinal: 3, JSON: contracts.JSON(contracts.ToolResults([]contracts.ToolResult{{CallID: "call-1", Name: "read_file",
				Text: "file body", Outcome: "success"}}))},
			{Ordinal: 4, JSON: contracts.JSON(contracts.Assistant(assistantText, nil))},
		},
		Records: []Record{
			record("source-a", 1, "user_message", map[string]any{"conversation": map[string]any{"entityKind": "steering"}, "locator": msgLocator("source-a", 1)}),
			record("source-a", 2, "model_result", map[string]any{"kind": "model_result", "text": assistantText,
				"request": map[string]any{"modelStep": 2}}),
			record("source-a", 3, "tool_call", map[string]any{"kind": "model_tool", "phase": "started", "id": "call-1",
				"name": "read_file", "input": map[string]string{"path": "notes.txt"}, "conversation": map[string]any{"entityKind": "tool"}}),
			record("source-a", 4, "tool_result", map[string]any{"kind": "model_tool", "phase": "completed", "id": "call-1",
				"name": "read_file", "conversation": map[string]any{"entityKind": "tool"}, "locator": msgLocator("source-a", 3)}),
			record("source-a", 5, "assistant_message", map[string]any{"kind": "assistant_message", "phase": "completed",
				"text": assistantText, "request": map[string]any{"modelStep": 2}, "conversation": map[string]any{"entityKind": "assistant"},
				"locator": msgLocator("source-a", 4)}),
			record("source-a", 6, "effect_observation", map[string]any{"kind": "host", "phase": "completed", "id": "inner-1",
				"parent": "call-1", "name": "read_file", "conversation": map[string]any{"entityKind": "tool"}}),
		},
	}

	projection, err := Project(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Observations) != 3 {
		t.Fatalf("observations = %#v, want steering, tool exchange, assistant", projection.Observations)
	}
	if projection.Observations[0].Kind != "steering" || projection.Observations[0].Text != "keep the header" {
		t.Fatalf("applied steering = %#v", projection.Observations[0])
	}
	exchange := projection.Observations[1]
	if exchange.Kind != "tool_exchange" || exchange.Call == nil || exchange.Call.Name != "read_file" ||
		exchange.Result == nil || exchange.Result.Text != "file body" || exchange.Status != "success" {
		t.Fatalf("tool exchange = %#v", exchange)
	}
	if len(exchange.Activity) != 0 {
		t.Fatalf("completed outer result was expanded with nested activity: result=%#v activity=%#v observations=%#v", exchange.Result, exchange.Activity, projection.Observations)
	}
	if projection.Observations[2].Kind != "assistant" || projection.Observations[2].Text != assistantText {
		t.Fatalf("assistant observation = %#v", projection.Observations[2])
	}
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), private) || strings.Contains(string(encoded), "ReplayItems") {
		t.Fatalf("provider replay state entered projection: %s", encoded)
	}
}

func TestProjectKeepsNestedToolActivityAsMetadataAndUsesOuterResult(t *testing.T) {
	call1 := contracts.ToolCall{CallID: "outer-1", Name: "run_js", Arguments: contracts.JSON(map[string]string{"source": "read_file(...)"})}
	call2 := contracts.ToolCall{CallID: "outer-2", Name: "inspect", Arguments: contracts.JSON(map[string]string{"target": "other"})}
	assistant := contracts.AssistantCalls([]contracts.ToolCall{call1, call2}, "the assistant's explanation", nil)
	outerStart := contracts.Observation{ID: "outer-1", Kind: "model_tool", Name: "run_js", Phase: "started",
		Input: contracts.JSON(map[string]string{"source": "read_file(...)"})}
	innerStart := contracts.Observation{ID: "inner-1", Parent: "outer-1", Kind: "tool", Name: "read_file", Phase: "started",
		Input: contracts.JSON(map[string]string{"path": "notes.txt", "private": "must not be projected"})}
	innerEnd := contracts.Observation{ID: "inner-1", Parent: "outer-1", Kind: "tool", Name: "read_file", Phase: "completed",
		Result: contracts.JSON("inner file contents must not be projected")}
	nestedStart := contracts.Observation{ID: "nested-1", Parent: "inner-1", Kind: "host", Name: "stat", Phase: "started",
		Input: contracts.JSON(map[string]string{"path": "notes.txt"})}
	evidence := Evidence{
		Execution: Execution{ExecutionID: "stopped", SessionID: "s", Lifecycle: "settled", Outcome: "cancelled", Task: "inspect a file"},
		Messages:  []Message{{Ordinal: 2, JSON: contracts.JSON(assistant)}},
		Records: []Record{
			record("stopped", 1, "assistant_message", map[string]any{"conversation": map[string]any{"entityKind": "assistant"},
				"locator": msgLocator("stopped", 2)}),
			{ID: "outer-start", ExecutionID: "stopped", Ordinal: 2, Kind: "tool_call", Payload: contracts.JSON(outerStart)},
			{ID: "inner-start", ExecutionID: "stopped", Ordinal: 3, Kind: "tool_call", Payload: contracts.JSON(innerStart)},
			{ID: "inner-end", ExecutionID: "stopped", Ordinal: 4, Kind: "tool_result", Payload: contracts.JSON(innerEnd)},
			{ID: "nested-start", ExecutionID: "stopped", Ordinal: 5, Kind: "effect_observation", Payload: contracts.JSON(nestedStart)},
		},
	}

	incomplete, err := Project(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(incomplete.Observations) != 2 {
		t.Fatalf("incomplete outer call produced standalone nested exchanges: %#v", incomplete.Observations)
	}
	first, second := incomplete.Observations[0], incomplete.Observations[1]
	if first.Kind != "tool_exchange" || first.Call == nil || first.Call.CallID != "outer-1" || first.Status != "incomplete" ||
		first.Text != "the assistant's explanation" || len(first.Activity) != 2 {
		t.Fatalf("outer incomplete exchange = %#v", first)
	}
	if first.Activity[0].CallID != "inner-1" || first.Activity[0].ParentCallID != "outer-1" || first.Activity[0].Status != "completed" ||
		first.Activity[1].CallID != "nested-1" || first.Activity[1].ParentCallID != "inner-1" || first.Activity[1].Status != "started" {
		t.Fatalf("nested activity metadata = %#v", first.Activity)
	}
	if second.Kind != "tool_exchange" || second.Call == nil || second.Call.CallID != "outer-2" || second.Text != "" {
		t.Fatalf("second exchange duplicated assistant text or disappeared: %#v", second)
	}
	encoded, err := json.Marshal(incomplete)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must not be projected") || strings.Contains(string(encoded), "inner file contents") {
		t.Fatalf("nested call input/result leaked into activity projection: %s", encoded)
	}

	completedEvidence := evidence
	completedEvidence.Messages = append(completedEvidence.Messages, Message{Ordinal: 3, JSON: contracts.JSON(contracts.ToolResults([]contracts.ToolResult{{
		CallID: "outer-1", Name: "run_js", Text: "public completed outer result", Outcome: "success",
	}}))})
	completed, err := Project(completedEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Observations) != 2 || completed.Observations[0].Result == nil ||
		completed.Observations[0].Result.Text != "public completed outer result" || len(completed.Observations[0].Activity) != 0 {
		t.Fatalf("completed outer public result did not take precedence: %#v", completed.Observations)
	}
}

func TestProjectFallbackMessageDoesNotRepeatAssistantTextAcrossCalls(t *testing.T) {
	projection, err := Project(Evidence{
		Execution: Execution{ExecutionID: "source", SessionID: "s", Lifecycle: "settled", Outcome: "cancelled", Task: "task"},
		Messages: []Message{{Ordinal: 4, JSON: contracts.JSON(contracts.AssistantCalls([]contracts.ToolCall{
			{CallID: "call-a", Name: "read_file"}, {CallID: "call-b", Name: "write_file"},
		}, "one assistant explanation", nil))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Observations) != 2 || projection.Observations[0].Call == nil || projection.Observations[0].Call.CallID != "call-a" ||
		projection.Observations[0].Text != "one assistant explanation" || projection.Observations[1].Call == nil ||
		projection.Observations[1].Call.CallID != "call-b" || projection.Observations[1].Text != "" {
		t.Fatalf("fallback multi-call projection duplicated or lost evidence: %#v", projection.Observations)
	}
}

func TestProjectKeepsUnfinishedToolAndAssistantPartialAsIncomplete(t *testing.T) {
	evidence := Evidence{
		Execution: Execution{ExecutionID: "stopped", SessionID: "s", Lifecycle: "settled", Outcome: "cancelled", Task: "continue"},
		Messages: []Message{{Ordinal: 1, JSON: contracts.JSON(contracts.AssistantCalls([]contracts.ToolCall{{CallID: "pending", Name: "write_file",
			Arguments: contracts.JSON(map[string]string{"path": "draft.txt"})}}, "", nil))}},
		Records: []Record{record("stopped", 2, "assistant_message", map[string]any{
			"kind": "assistant_message", "phase": "incomplete", "payload": map[string]string{"text": "half answer"},
			"request": map[string]any{"modelStep": 2}, "conversation": map[string]any{"entityKind": "assistant"},
		})},
	}
	projection, err := Project(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Observations) != 2 {
		t.Fatalf("observations = %#v", projection.Observations)
	}
	if projection.Observations[0].Kind != "tool_exchange" || projection.Observations[0].Status != "incomplete" || projection.Observations[0].Result != nil {
		t.Fatalf("unfinished call was treated as complete: %#v", projection.Observations[0])
	}
	if projection.Observations[1].Kind != "assistant_partial" || projection.Observations[1].Text != "half answer" || projection.Observations[1].Status != "incomplete" {
		t.Fatalf("terminal partial text = %#v", projection.Observations[1])
	}
}

func TestProjectFlattensDurableRecallMessage(t *testing.T) {
	first, err := Project(Evidence{Execution: Execution{ExecutionID: "source-a", SessionID: "s", Turn: 1,
		AgentName: "a", Selection: contracts.OpenRouterModel("provider-a-model"), Lifecycle: "settled", Outcome: "cancelled", Task: "first task"},
		Messages: []Message{{Ordinal: 0, JSON: contracts.JSON(contracts.Assistant("first answer", nil))}},
		Records: []Record{record("source-a", 1, "assistant_message", map[string]any{
			"kind": "assistant_message", "phase": "incomplete", "text": "first answer", "request": map[string]any{"modelStep": 1},
			"conversation": map[string]any{"entityKind": "assistant"}, "locator": msgLocator("source-a", 0),
		})}})
	if err != nil {
		t.Fatal(err)
	}
	text, err := MessageText(first)
	if err != nil {
		t.Fatal(err)
	}
	reference := contracts.User(text)
	currentAssistant := contracts.Assistant("second answer", nil)
	second, err := Project(Evidence{Execution: Execution{ExecutionID: "source-b", SessionID: "s", Turn: 2,
		AgentName: "b", Selection: contracts.ModelSelection{Provider: "provider-b", ModelID: "provider-b-model"},
		Lifecycle: "settled", Outcome: "failed", Task: "second task"},
		Messages: []Message{{Ordinal: 1, JSON: contracts.JSON(reference)}, {Ordinal: 2, JSON: contracts.JSON(currentAssistant)}},
		Records: []Record{
			record("source-b", 1, "user_message", map[string]any{"recall": contracts.RecallMarker{SourceExecutionID: "source-a",
				RelationRecordID: "relation-a-b", OperationID: "op-a", Generation: 1},
				"conversation": map[string]any{"entityKind": "recall"}, "locator": msgLocator("source-b", 1)}),
			record("source-b", 2, "assistant_message", map[string]any{"kind": "assistant_message", "phase": "completed", "text": "second answer",
				"request": map[string]any{"modelStep": 1}, "conversation": map[string]any{"entityKind": "assistant"}, "locator": msgLocator("source-b", 2)}),
		}})
	if err != nil {
		t.Fatal(err)
	}
	if second.Task != "second task" || len(second.InheritedSources) != 1 || second.InheritedSources[0].SourceExecutionID != "source-a" {
		t.Fatalf("flat inherited metadata = %#v; task=%q", second.InheritedSources, second.Task)
	}
	if len(second.Observations) != 2 || second.Observations[0].SourceLocator.ExecutionID != "source-a" ||
		second.Observations[0].Text != "first answer" || second.Observations[1].SourceLocator.ExecutionID != "source-b" {
		t.Fatalf("flat observations = %#v", second.Observations)
	}
	for _, observation := range second.Observations {
		if observation.Kind == "recall" {
			t.Fatalf("reference message was nested as an observation: %#v", observation)
		}
	}
}

func record(executionID string, ordinal int64, kind string, payload any) Record {
	return Record{ID: executionID + ":r:" + jsonNumber(ordinal), ExecutionID: executionID,
		Ordinal: ordinal, Kind: kind, Payload: contracts.JSON(payload)}
}

func msgLocator(executionID string, ordinal int64) map[string]any {
	return map[string]any{"executionId": executionID, "messageOrdinal": ordinal}
}

func jsonNumber(value int64) string { return strings.TrimSpace(string(contracts.JSON(value))) }
