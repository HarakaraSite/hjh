package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/apiclient"
)

func TestNoticeCancellationBeforeFirstAdmission(t *testing.T) {
	release := make(chan struct{})
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), cancelChainCalls: make(chan inputChainCall, 1),
		cancelChainFunc: func(ctx context.Context, coreID, runID string) (api.InputChainState, error) {
			select {
			case <-release:
				return api.InputChainState{CoreID: coreID, AnchorRunID: runID, Phase: "settled"}, nil
			case <-ctx.Done():
				return api.InputChainState{}, ctx.Err()
			}
		}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	t.Cleanup(func() { cancel(); m.shutdown() })
	m.startRun("first task")
	call := <-backend.runCalls
	t.Cleanup(func() { call.handle.finish(api.RunState{}, context.Canceled) })
	m.requestInputChainCancel(m.activeRun)
	<-backend.cancelChainCalls
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot",
		State:        api.RunState{CoreID: "core-1", SessionID: "created", RunID: call.request.RunID, Seq: 1, Phase: "running"},
		Conversation: &api.ConversationSnapshot{ViewID: "first", Entities: map[string]api.ConversationEntity{}}})
	if m.chainCancel.sessionID != "created" || !m.showRunNotice("core-1", "created", call.request.RunID) {
		t.Fatalf("first admission did not synchronize cancellation target: cancellation=%+v display=%+v", m.chainCancel, m.runNoticeTarget)
	}
	close(release)
	for {
		msg := receive(t, messages)
		update(m, msg)
		if _, ok := msg.(runtimeInputChainSettledMsg); ok {
			break
		}
	}
	if !m.chainCancelSettled || !strings.HasPrefix(m.status, "input chain settled") {
		t.Fatalf("current cancellation lost after first admission: settled=%t status=%q", m.chainCancelSettled, m.status)
	}
}

func TestNoticeFinalConfirmedSessionIdentity(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	t.Cleanup(func() { cancel(); m.shutdown() })
	m.startRun("first task")
	call := <-backend.runCalls
	t.Cleanup(func() { call.handle.finish(api.RunState{}, context.Canceled) })
	// The interrupted stream did not deliver admission; Wait still returns the
	// Core-confirmed Session identity with its final state.
	call.handle.finish(api.RunState{CoreID: "core-1", SessionID: "created", RunID: call.request.RunID, Phase: "terminal"}, errors.New("stream interrupted"))
	receiveRunDone(t, m, messages, call.request.RunID)
	if m.activeRun != nil || m.sessionID != "created" || !m.showRunNotice("core-1", "created", call.request.RunID) {
		t.Fatalf("final identity was not retained while idle: active=%v session=%q target=%+v", m.activeRun != nil, m.sessionID, m.runNoticeTarget)
	}
	input := noticeInput(m, call.request.RunID, "current-input")
	m.runtimeInputQueue = []runtimeInputOperation{input}
	m.runtimeInputSending = true
	update(m, noticeInputResult(input, "error"))
	if !strings.HasPrefix(m.status, "follow-up unconfirmed") {
		t.Fatalf("confirmed final target's result was suppressed: %q", m.status)
	}
}
