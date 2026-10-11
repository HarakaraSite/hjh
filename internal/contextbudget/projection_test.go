package contextbudget

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"hjh/internal/contracts"
)

func TestProjectMeasuresPreparedWireAndKeepsToolExchangeReplay(t *testing.T) {
	state := &contracts.ProviderState{
		Provider:    "demo",
		ReplayItems: []json.RawMessage{json.RawMessage(`{"opaque":"state"}`)},
	}
	calls := []contracts.ToolCall{
		{CallID: "call-a", Name: "a", Arguments: json.RawMessage(`{}`)},
		{CallID: "call-b", Name: "b", Arguments: json.RawMessage(`{}`)},
	}
	messages := []contracts.Message{
		contracts.User("task"),
		contracts.Assistant(strings.Repeat("old response ", 80), nil),
		contracts.AssistantCalls(calls, "working", state),
		contracts.ToolResults([]contracts.ToolResult{{CallID: "call-a", Name: "a", Text: "result a"}}),
		contracts.ToolResults([]contracts.ToolResult{{CallID: "call-b", Name: "b", Text: "result b"}}),
		contracts.Assistant("final response", state),
	}
	current := entries(messages)
	keptMessages := []contracts.Message{messages[0], messages[2], messages[3], messages[4], messages[5]}
	targetWire := preparedWire(testRequest(keptMessages))
	limit := estimateTokens(len(targetWire), len(keptMessages))
	projector := New(Budget{Limit: &limit}, nil, nil)

	projection, err := projector.Project(context.Background(), testRequest(nil), current, prepareJSON)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}
	if !reflect.DeepEqual(projection.Request.Messages, keptMessages) {
		t.Fatalf("unexpected projected messages: %#v", projection.Request.Messages)
	}
	if !reflect.DeepEqual(projection.Fact.OmittedCurrent, []int{1}) {
		t.Fatalf("unexpected omitted current indices: %v", projection.Fact.OmittedCurrent)
	}
	if !reflect.DeepEqual(projection.Sources, []Source{current[0].Source, current[2].Source,
		current[3].Source, current[4].Source, current[5].Source}) {
		t.Fatalf("projection sources do not match messages: %#v", projection.Sources)
	}
	if projection.Fact.WireBytes != int64(len(projection.Request.WireBody)) ||
		projection.Fact.EstimatedTokens != estimateTokens(len(projection.Request.WireBody), len(keptMessages)) ||
		!reflect.DeepEqual(projection.Request.WireBody, preparedWire(projection.Request)) {
		t.Fatalf("fact does not describe the prepared wire: %+v", projection.Fact)
	}
	if projection.Request.Messages[1].ProviderState == nil ||
		len(projection.Request.Messages[1].ProviderState.ReplayItems) != 1 ||
		projection.Request.Messages[2].Role != "tool" || projection.Request.Messages[3].Role != "tool" {
		t.Fatalf("complete call/result batch or replay was lost: %#v", projection.Request.Messages)
	}
}

func TestProjectKeepsCurrentOmissionsMonotonic(t *testing.T) {
	messages := []contracts.Message{
		contracts.User("task"),
		contracts.Assistant(strings.Repeat("old ", 100), nil),
		contracts.Assistant("latest response", nil),
	}
	target := []contracts.Message{messages[0], messages[2]}
	limit := estimateTokens(len(preparedWire(testRequest(target))), len(target))
	readCount := 0
	reader := func(context.Context, int64) (*Turn, error) {
		readCount++
		return nil, nil
	}
	projector := New(Budget{Limit: &limit}, reader, nil)

	first, err := projector.Project(context.Background(), testRequest(nil), entries(messages), prepareJSON)
	if err != nil {
		t.Fatalf("first Project returned error: %v", err)
	}
	if !reflect.DeepEqual(first.Fact.OmittedCurrent, []int{1}) || readCount != 0 {
		t.Fatalf("current trim did not suppress history: omitted=%v reads=%d", first.Fact.OmittedCurrent, readCount)
	}
	second, err := projector.Project(context.Background(), testRequest(nil), entries(messages),
		func(contracts.ModelRequest) ([]byte, error) { return []byte("small"), nil })
	if err != nil {
		t.Fatalf("second Project returned error: %v", err)
	}
	if !reflect.DeepEqual(second.Request.Messages, target) || !reflect.DeepEqual(second.Fact.OmittedCurrent, []int{1}) {
		t.Fatalf("previously omitted current message reappeared: messages=%#v omitted=%v",
			second.Request.Messages, second.Fact.OmittedCurrent)
	}
	if second.Fact.HistoryStatus != HistoryOmitted || second.Fact.History.Reason != "current_trim_started" {
		t.Fatalf("history was not marked skipped after current trimming: %+v", second.Fact.History)
	}
}

func TestProjectPartiallySelectsRecentHistoryAndStopsOlderReads(t *testing.T) {
	recent := &Turn{Number: 5, Entries: entries([]contracts.Message{
		contracts.User("recent task"),
		contracts.Assistant(strings.Repeat("old history ", 100), nil),
		contracts.Assistant("recent final", nil),
	})}
	current := entries([]contracts.Message{contracts.User("follow up")})
	target := []contracts.Message{recent.Entries[0].Message, recent.Entries[2].Message, current[0].Message}
	limit := estimateTokens(len(preparedWire(testRequest(target))), len(target))
	var reads []int64
	reader := func(_ context.Context, before int64) (*Turn, error) {
		reads = append(reads, before)
		if before <= 0 {
			return recent, nil
		}
		return nil, errors.New("older history must not be read after a partial recent turn")
	}
	projector := New(Budget{Limit: &limit}, reader, nil)

	projection, err := projector.Project(context.Background(), testRequest(nil), current, prepareJSON)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}
	if !reflect.DeepEqual(projection.Request.Messages, target) || !reflect.DeepEqual(reads, []int64{0}) {
		t.Fatalf("unexpected partial recent history projection: messages=%#v reads=%v",
			projection.Request.Messages, reads)
	}
	if projection.Fact.HistoryStatus != HistoryPartial ||
		projection.Fact.History.Reason != "recent_turn_trimmed" ||
		len(projection.Fact.History.Turns) != 1 || projection.Fact.History.Turns[0].Status != "partial" {
		t.Fatalf("partial recent turn fact missing: %+v", projection.Fact.History)
	}
}

func TestProjectOmitsAnOlderTurnAsAWhole(t *testing.T) {
	recent := &Turn{Number: 8, Entries: entries([]contracts.Message{
		contracts.User("recent task"), contracts.Assistant("recent final", nil),
	})}
	older := &Turn{Number: 7, Entries: entries([]contracts.Message{
		contracts.User("older task"), contracts.Assistant(strings.Repeat("older ", 100), nil),
	})}
	current := entries([]contracts.Message{contracts.User("follow up")})
	target := []contracts.Message{recent.Entries[0].Message, recent.Entries[1].Message, current[0].Message}
	limit := estimateTokens(len(preparedWire(testRequest(target))), len(target))
	var reads []int64
	reader := func(_ context.Context, before int64) (*Turn, error) {
		reads = append(reads, before)
		switch before {
		case 0:
			return recent, nil
		case 8:
			return older, nil
		default:
			return nil, nil
		}
	}
	projector := New(Budget{Limit: &limit}, reader, nil)

	projection, err := projector.Project(context.Background(), testRequest(nil), current, prepareJSON)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}
	if !reflect.DeepEqual(projection.Request.Messages, target) || !reflect.DeepEqual(reads, []int64{0, 8}) {
		t.Fatalf("older turn was not omitted whole: messages=%#v reads=%v", projection.Request.Messages, reads)
	}
	if projection.Fact.HistoryStatus != HistoryPartial ||
		projection.Fact.History.Reason != "older_turn_does_not_fit" ||
		len(projection.Fact.History.Turns) != 2 || projection.Fact.History.Turns[0].Status != "omitted" {
		t.Fatalf("older-turn omission fact missing: %+v", projection.Fact.History)
	}
}

func TestProjectUnknownInputLimitSendsAllHistoryAndHistoryLimitMeasuresHistoryOnly(t *testing.T) {
	budget := Resolve(Configuration{Defaults: Values{HistoryTokens: int64Pointer(1_000_000)}},
		contracts.ModelSelection{Provider: "demo", ModelID: "model"}, Capacity{Source: "unknown"})
	latest := &Turn{Number: 4, Entries: entries([]contracts.Message{
		contracts.User("latest task"), contracts.Assistant("latest answer", nil),
	})}
	older := &Turn{Number: 3, Entries: entries([]contracts.Message{
		contracts.User("older task"), contracts.Assistant("older answer", nil),
	})}
	current := entries([]contracts.Message{contracts.User(strings.Repeat("large current ", 300))})
	prefix := entries([]contracts.Message{contracts.User(strings.Repeat("large prefix ", 300))})
	var reads []int64
	reader := func(_ context.Context, before int64) (*Turn, error) {
		reads = append(reads, before)
		switch before {
		case 0:
			return latest, nil
		case 4:
			return older, nil
		default:
			return nil, nil
		}
	}
	var historyOnlyCalls int
	prepare := func(request contracts.ModelRequest) ([]byte, error) {
		if request.Instruction == "" && len(request.Tools) == 0 {
			historyOnlyCalls++
			for _, message := range request.Messages {
				if strings.Contains(message.VisibleText(), "large current") || strings.Contains(message.VisibleText(), "large prefix") {
					t.Fatalf("history-only measurement included prefix or current messages: %#v", request.Messages)
				}
			}
		}
		return preparedWire(request), nil
	}
	projector := New(budget, reader, prefix)

	projection, err := projector.Project(context.Background(), testRequest(nil), current, prepare)
	if err != nil {
		t.Fatalf("Project returned error: %v", err)
	}
	if projection.Fact.Budget.Limit != nil || projection.Fact.Budget.Sources.ContextTokens != "unknown" ||
		projection.Fact.HistoryStatus != HistoryFull || !reflect.DeepEqual(reads, []int64{0, 4, 3}) {
		t.Fatalf("unknown input limit did not retain available history: fact=%+v reads=%v",
			projection.Fact, reads)
	}
	if historyOnlyCalls == 0 || projection.Fact.HistoryEstimatedTokens == nil ||
		projection.Fact.HistoryWireBytes == nil {
		t.Fatalf("history-only measurement was not recorded: calls=%d fact=%+v", historyOnlyCalls, projection.Fact)
	}
	if len(projection.Request.Messages) != len(prefix)+len(latest.Entries)+len(older.Entries)+len(current) {
		t.Fatalf("history, prefix, or current entries were dropped: %#v", projection.Request.Messages)
	}
}

func TestProjectReturnsExceededProjectionWithOutcomeForProtectedCurrent(t *testing.T) {
	current := entries([]contracts.Message{contracts.User(strings.Repeat("protected task ", 100))})
	limit := estimateTokens(len(preparedWire(testRequest(nil))), 0) - 1
	reads := 0
	reader := func(context.Context, int64) (*Turn, error) {
		reads++
		return nil, nil
	}
	projector := New(Budget{Limit: &limit}, reader, nil)

	projection, err := projector.Project(context.Background(), testRequest(nil), current, prepareJSON)
	var exceeded *ExceededError
	if !errors.As(err, &exceeded) || err == nil {
		t.Fatalf("expected ExceededError, got %v", err)
	}
	if exceeded.Outcome() != "context_budget_exceeded" || projection.Fact.EstimatedTokens <= limit ||
		len(projection.Request.Messages) != 1 || projection.Request.Messages[0].VisibleText() != current[0].Message.VisibleText() || reads != 0 {
		t.Fatalf("protected-current exceedance lost its fact or outcome: projection=%+v reads=%d err=%v",
			projection, reads, err)
	}
	if projection.Fact.HistoryStatus != HistoryOmitted ||
		projection.Fact.History.Reason != "protected_current_does_not_fit" {
		t.Fatalf("exceedance fact did not record skipped history: %+v", projection.Fact.History)
	}
}

func testRequest(messages []contracts.Message) contracts.ModelRequest {
	return contracts.ModelRequest{
		Model:       contracts.ModelSelection{Provider: "demo", API: "chat", ModelID: "model"},
		Instruction: "system instruction",
		Messages:    messages,
		Tools:       []contracts.ToolDefinition{{Name: "inspect", Description: "inspect"}},
	}
}

func prepareJSON(request contracts.ModelRequest) ([]byte, error) {
	return preparedWire(request), nil
}

func preparedWire(request contracts.ModelRequest) []byte {
	wire, err := json.Marshal(request)
	if err != nil {
		panic(err)
	}
	return wire
}

func entries(messages []contracts.Message) []Entry {
	result := make([]Entry, len(messages))
	for index, message := range messages {
		result[index] = Entry{
			Message: message,
			Source: Source{
				Kind:           "current",
				ExecutionID:    "execution-1",
				Turn:           1,
				MessageOrdinal: int64(index + 1),
			},
		}
	}
	return result
}
