package history

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"hjh/internal/contracts"
)

func TestAssistantReportsReadVisibleBodiesAndCursor(t *testing.T) {
	ctx := context.Background()
	store, execution := openConversationFixture(t)
	state := &contracts.ProviderState{Provider: "fixture", Reasoning: &contracts.Reasoning{Text: "private reasoning"}, ReplayItems: []json.RawMessage{json.RawMessage(`{"encrypted":"private replay"}`)}}
	messages := []contracts.Message{
		contracts.User("task"),
		contracts.AssistantCalls([]contracts.ToolCall{{Name: "read_file", CallID: "read"}}, "first finding", state),
		contracts.ToolResults([]contracts.ToolResult{{Name: "read_file", CallID: "read", Text: "tool body"}}),
		contracts.AssistantCalls([]contracts.ToolCall{{Name: "read_file", CallID: "read-again"}}, "", state),
		contracts.Assistant("", state),
		contracts.Assistant("final body", state),
	}
	for _, message := range messages {
		if _, err := store.AppendMessage(ctx, execution.ID, contracts.JSON(message)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.SaveAssistantProgress(ctx, AssistantProgressInput{RootExecutionID: execution.ID, ExecutionID: execution.ID, Sequence: 1, Request: contracts.RequestIdentity{RequestOrdinal: 3, ModelStep: 3, Lane: "parent"}, Text: "uncommitted stream body"}); err != nil {
		t.Fatal(err)
	}
	reports, cursor, err := store.AssistantReports(ctx, execution.ID, -1)
	if err != nil || cursor != 5 || len(reports) != 2 || reports[0].Text != "first finding" || reports[0].MessageOrdinal != 1 || reports[1].Text != "final body" || reports[1].MessageOrdinal != 5 {
		t.Fatalf("reports=%+v cursor=%d err=%v", reports, cursor, err)
	}
	for _, report := range reports {
		if report.ExecutionID != execution.ID {
			t.Fatalf("source ID=%s", report.ExecutionID)
		}
	}
	empty, next, err := store.AssistantReports(ctx, execution.ID, cursor)
	if err != nil || next != cursor || len(empty) != 0 {
		t.Fatalf("empty=%+v next=%d err=%v", empty, next, err)
	}
	ordinal, err := store.AppendMessage(ctx, execution.ID, contracts.JSON(contracts.Assistant("new finding", nil)))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		delta, next, err := store.AssistantReports(ctx, execution.ID, cursor)
		if err != nil || next != ordinal || len(delta) != 1 || delta[0].Text != "new finding" || delta[0].MessageOrdinal != ordinal {
			t.Fatalf("delta=%+v next=%d err=%v", delta, next, err)
		}
	}
}

func TestAssistantReportsConcurrentSaveDoesNotSkipBodies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, execution := openConversationFixture(t)
	var wg sync.WaitGroup
	writeErr := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 32; i++ {
			if _, err := store.AppendMessage(ctx, execution.ID, contracts.JSON(contracts.Assistant(fmt.Sprintf("report %d", i), nil))); err != nil {
				writeErr <- err
				return
			}
		}
	}()
	cursor := int64(-1)
	seen := map[int64]string{}
	for cursor < 31 {
		select {
		case err := <-writeErr:
			t.Fatal(err)
		default:
		}
		reports, next, err := store.AssistantReports(ctx, execution.ID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range reports {
			if r.MessageOrdinal <= cursor || r.MessageOrdinal > next {
				t.Fatalf("report outside cut: %+v cursor=%d next=%d", r, cursor, next)
			}
			seen[r.MessageOrdinal] = r.Text
		}
		cursor = next
	}
	wg.Wait()
	select {
	case err := <-writeErr:
		t.Fatal(err)
	default:
	}
	if len(seen) != 32 {
		t.Fatalf("saved bodies skipped: %d", len(seen))
	}
	for ordinal, text := range seen {
		if !strings.HasPrefix(text, "report ") {
			t.Fatalf("bad body at %d: %q", ordinal, text)
		}
	}
}
