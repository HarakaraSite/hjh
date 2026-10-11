package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/history"
)

type testSteeringSource struct {
	inputs      []contracts.SteeringInput
	applied     []uint64
	closeReason string
}

func (s *testSteeringSource) Pending(context.Context, bool) ([]contracts.SteeringInput, error) {
	inputs := s.inputs
	s.inputs = nil
	return inputs, nil
}
func (s *testSteeringSource) Applied(_ contracts.SteeringInput, revision uint64) {
	s.applied = append(s.applied, revision)
}
func (s *testSteeringSource) Close(reason string) error { s.closeReason = reason; return nil }

func TestSteeringAdapterAppliesReceiptAndReusesAcceptedMessageContent(t *testing.T) {
	ctx := context.Background()
	store, workspace := delegationStore(t)
	session, err := store.CreateSession(ctx, history.SessionInput{ID: "steering-session", WorkspaceRoot: workspace,
		AgentChoice: contracts.JSON(map[string]string{"name": "default"}), ActiveModel: contracts.JSON(contracts.OpenRouterModel("test/model"))})
	if err != nil {
		t.Fatal(err)
	}
	execution, err := store.BeginExecution(ctx, history.BeginExecutionInput{ExecutionID: "steering-execution", Task: "root task", SessionID: session.ID,
		Model: contracts.JSON(contracts.OpenRouterModel("test/model")), Build: contracts.JSON(map[string]bool{"test": true})})
	if err != nil {
		t.Fatal(err)
	}
	marker := true
	encoded, err := contracts.EncodeJSON(contracts.Message{Role: "user", Content: contracts.JSON(contracts.TextContent{Kind: "text", Text: "include the deadline"}), Steering: &marker})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := store.SaveRuntimeInput(ctx, contracts.InputReceipt{CoreID: "steering-core", SessionID: session.ID, RunID: "steering-run",
		ExecutionID: execution.ID, InputID: "steering-input", Kind: "steering", Sequence: 1, Status: "accepted"}, "steering-fingerprint", encoded)
	if err != nil {
		t.Fatal(err)
	}
	source := &testSteeringSource{inputs: []contracts.SteeringInput{{Receipt: accepted}}}
	owner := executiondata.NewOwner()
	journal := newJournal(ctx, store, execution.ID, execution.ID, execution.Task, owner, nil)
	journal.rootExecutionOrdinal, err = store.ExecutionDisplayOrdinal(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	task := contracts.User(execution.Task)
	task.ExecutionFact = &contracts.ExecutionFact{Kind: "user_message", Role: "user", Text: execution.Task,
		Conversation: contracts.ConversationMetadata{EntityKind: "task", LocalKey: "task"}}
	if err := journal.appendMessage(task); err != nil {
		t.Fatal(err)
	}
	adapter := &steeringAdapter{store: store, journal: journal, source: source}
	messages, err := adapter.Apply(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].VisibleText() != "include the deadline" || messages[0].Steering == nil || !*messages[0].Steering {
		t.Fatalf("applied steering message = %+v", messages)
	}
	if len(source.applied) != 1 || source.applied[0] != 2 {
		t.Fatalf("Applied revisions = %v, want the receipt's committed revision 2", source.applied)
	}
	stored, err := store.RuntimeInput(ctx, "steering-core", "steering-input", true)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "applied" || stored.Text != "include the deadline" || stored.AppliedMessageOrdinal == nil || *stored.AppliedMessageOrdinal != 1 {
		t.Fatalf("persisted applied receipt = %+v", stored)
	}
	storedMessages, err := store.Messages(ctx, execution.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(storedMessages) != 1 || !bytes.Equal(storedMessages[0].JSON, encoded) {
		t.Fatalf("applied message bytes differ from accepted body: %+v", storedMessages)
	}
	storedBody, err := store.Content(ctx, accepted.ContentDigest)
	if err != nil || !bytes.Equal(storedBody, encoded) {
		t.Fatalf("accepted content bytes changed: %s, %v", storedBody, err)
	}

	cut, err := store.CaptureConversationCut(ctx, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	var replayed *contracts.ExecutionFact
	if err := store.WalkConversationCut(ctx, cut, func(fact contracts.ExecutionFact) error {
		if fact.Conversation.EntityKind == "steering" {
			copy := fact
			replayed = &copy
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if replayed == nil || replayed.Text != "include the deadline" || replayed.Conversation.LocalKey != "input/steering-input" {
		t.Fatalf("replayed steering fact = %+v", replayed)
	}
}

func TestResolveInputSelectionCarriesEffectiveRunOptions(t *testing.T) {
	store, workspace := delegationStore(t)
	service := NewService(store, nil, RunOptions{Workspace: workspace, MaxSteps: 7, FromTurn: 3})
	selection, err := service.ResolveInputSelection(context.Background(), RunRequest{Workspace: workspace, Role: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Role != "default" || selection.MaxSteps != 7 || selection.FromTurn != 3 || selection.Selection.ModelID == "" {
		t.Fatalf("resolved input selection = %+v", selection)
	}
}

func TestRuntimeInputServiceWrappersReturnStoredReceipt(t *testing.T) {
	store, workspace := delegationStore(t)
	service := NewService(store, nil, RunOptions{Workspace: workspace})
	session, err := store.CreateSession(context.Background(), history.SessionInput{ID: "wrapper-session", WorkspaceRoot: workspace,
		AgentChoice: json.RawMessage(`{"name":"default"}`), ActiveModel: contracts.JSON(contracts.OpenRouterModel("test/model"))})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.SaveRuntimeInput(context.Background(), contracts.InputReceipt{CoreID: "wrapper-core", SessionID: session.ID,
		RunID: "wrapper-run", ExecutionID: "wrapper-execution", InputID: "wrapper-input", Kind: "follow-up", Sequence: 1, Status: "queued"},
		"wrapper-signature", []byte("next task"))
	if err != nil {
		t.Fatal(err)
	}
	read, err := service.RuntimeInput(context.Background(), "wrapper-core", "wrapper-input", true)
	if err != nil || read.Text != "next task" || read.ContentDigest != receipt.ContentDigest {
		t.Fatalf("wrapped runtime input = %+v, %v", read, err)
	}
}
