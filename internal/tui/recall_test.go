package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

func (f *fakeBackend) SessionContext(_ context.Context, coreID, sessionID string) (api.SessionContext, error) {
	return api.SessionContext{CoreID: coreID, SessionID: sessionID}, nil
}
func (f *fakeBackend) Recall(_ context.Context, sessionID string, request api.RecallOperationRequest) (api.RecallOperationResult, error) {
	return api.RecallOperationResult{Receipt: contracts.RecallReceipt{SessionID: sessionID, OperationID: request.OperationID, Result: "prepared"}, ManagedByThisCore: true}, nil
}
func (f *fakeBackend) RecallOperation(context.Context, string, string, string) (api.RecallOperationResult, error) {
	return api.RecallOperationResult{}, &api.Error{Code: "recall_not_found", Message: "not found"}
}

type recallBackend struct {
	*fakeBackend
	context func(context.Context, string, string) (api.SessionContext, error)
	recall  func(context.Context, string, api.RecallOperationRequest) (api.RecallOperationResult, error)
}

func (f *recallBackend) SessionContext(ctx context.Context, coreID, sessionID string) (api.SessionContext, error) {
	return f.context(ctx, coreID, sessionID)
}
func (f *recallBackend) Recall(ctx context.Context, sessionID string, request api.RecallOperationRequest) (api.RecallOperationResult, error) {
	return f.recall(ctx, sessionID, request)
}

func TestContextShowsSavedInputSeparatelyFromNextSelection(t *testing.T) {
	base := &fakeBackend{core: api.Core{CoreID: "core"}, selection: api.SelectionPreview{Role: "generic", Selection: contracts.ModelSelection{Provider: "next-provider", ModelID: "next-model"}}}
	backend := &recallBackend{fakeBackend: base, context: func(_ context.Context, coreID, sessionID string) (api.SessionContext, error) {
		if coreID != "core" || sessionID != "session" {
			t.Errorf("context target %s/%s", coreID, sessionID)
		}
		return api.SessionContext{SessionID: sessionID, LatestRequest: &contracts.ContextRequest{ExecutionID: "saved-execution", ModelStep: 3, Selection: contracts.ModelSelection{Provider: "saved-provider", ModelID: "saved-model"}}, PendingRecall: &contracts.PendingRecall{SourceExecutionID: "stopped", Generation: 2}}, nil
	}}
	m, messages, cancel := testModel(backend, Options{Core: base.core, SessionID: "session"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/context")
	update(m, receive(t, messages))
	text := m.entries[len(m.entries)-1].text
	for _, want := range []string{"Saved input preparation", "saved-execution", "saved-provider / saved-model", "Next selection at query", "next-provider / next-model", "Pending recall · stopped"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}

func TestRecallLateResponseDoesNotAffectNewSessionDraft(t *testing.T) {
	base := &fakeBackend{core: api.Core{CoreID: "core"}}
	entered, release := make(chan struct{}), make(chan struct{})
	backend := &recallBackend{fakeBackend: base, recall: func(_ context.Context, sessionID string, request api.RecallOperationRequest) (api.RecallOperationResult, error) {
		close(entered)
		<-release
		if sessionID != "old" {
			t.Errorf("recall target %s", sessionID)
		}
		return api.RecallOperationResult{Receipt: contracts.RecallReceipt{OperationID: request.OperationID, SourceExecutionID: "stopped", Result: "prepared"}}, nil
	}}
	m, messages, cancel := testModel(backend, Options{Core: base.core, SessionID: "old"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/recall latest")
	<-entered
	m.sessionID = "new"
	m.sessionGeneration++
	m.status = "new Session"
	m.textarea.SetValue("new draft")
	close(release)
	update(m, receive(t, messages))
	if m.status != "new Session" || m.textarea.Value() != "new draft" || len(m.entries) != 0 {
		t.Fatalf("late result altered new session: %s %s", m.status, m.textarea.Value())
	}
}

func TestRecallTransportRetryKeepsOperationIdentity(t *testing.T) {
	base := &fakeBackend{core: api.Core{CoreID: "core"}}
	var requests []api.RecallOperationRequest
	backend := &recallBackend{fakeBackend: base, recall: func(_ context.Context, _ string, request api.RecallOperationRequest) (api.RecallOperationResult, error) {
		requests = append(requests, request)
		if len(requests) == 1 {
			return api.RecallOperationResult{}, errors.New("connection closed after write")
		}
		return api.RecallOperationResult{Receipt: contracts.RecallReceipt{OperationID: request.OperationID, Result: "prepared", SourceExecutionID: "stopped"}}, nil
	}}
	m, messages, cancel := testModel(backend, Options{Core: base.core, SessionID: "session"})
	defer func() { cancel(); m.shutdown() }()
	m.handleCommand("/recall")
	update(m, receive(t, messages))
	if !strings.Contains(m.status, "unconfirmed") {
		t.Fatal(m.status)
	}
	m.handleCommand("/recall")
	update(m, receive(t, messages))
	if len(requests) != 2 || requests[0].OperationID == "" || requests[0].OperationID != requests[1].OperationID {
		t.Fatalf("retry identity: %+v", requests)
	}
	if !strings.Contains(m.status, "prepared") || len(m.recallOperations) != 0 {
		t.Fatal(m.status)
	}
}
