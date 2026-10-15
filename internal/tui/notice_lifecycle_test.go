package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

// Drive the same start/admission/terminal paths as the UI rather than giving
// activeRun a display-only fallback in production.
func startNoticeRun(t *testing.T, m *model, messages <-chan tea.Msg, backend *fakeBackend, sessionID string) runCall {
	t.Helper()
	m.startRun("task")
	call := <-backend.runCalls
	t.Cleanup(func() { call.handle.finish(api.RunState{}, context.Canceled) })
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot",
		State: api.RunState{CoreID: backend.core.CoreID, SessionID: sessionID, RunID: call.request.RunID, Seq: 1, Phase: "running",
			InputAcceptance: api.InputAcceptance{FollowUp: true, Steering: true}},
		Conversation: &api.ConversationSnapshot{ViewID: call.request.RunID, Entities: map[string]api.ConversationEntity{}}})
	return call
}

func finishNoticeRun(t *testing.T, m *model, messages <-chan tea.Msg, call runCall) {
	t.Helper()
	state := api.RunState{CoreID: m.activeRun.coreID, SessionID: m.activeRun.session, RunID: call.request.RunID,
		Seq: 2, Phase: "terminal", Result: &api.RunResult{OK: true}}
	call.handle.finish(state, nil)
	receiveRunDone(t, m, messages, call.request.RunID)
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: state,
		Conversation: &api.ConversationSnapshot{ViewID: call.request.RunID, Revision: 2, Entities: map[string]api.ConversationEntity{}}})
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.terminal", State: state})
	if m.activeRun != nil {
		t.Fatal("run did not finish")
	}
}

func noticeInput(m *model, runID, inputID string) runtimeInputOperation {
	return runtimeInputOperation{
		coreID: m.core.CoreID, sessionID: m.sessionID, runID: runID,
		request: api.SubmitInputRequest{CoreID: m.core.CoreID, SessionID: m.sessionID, InputID: inputID, Kind: "follow-up", Text: "queued task"},
		draft:   "queued task", draftRevision: m.draftRevision,
	}
}

func noticeInputResult(input runtimeInputOperation, result string) runtimeInputSubmittedMsg {
	msg := runtimeInputSubmittedMsg{jobID: 123, input: input,
		receipt: api.InputReceipt{CoreID: input.coreID, SessionID: input.sessionID, RunID: input.runID,
			InputID: input.request.InputID, Kind: input.request.Kind, Status: "queued", Revision: 1}}
	switch result {
	case "error":
		msg.err = errors.New("submit unavailable")
	case "mismatch":
		msg.receipt.RunID = "different-run"
	case "not accepted":
		msg.receipt.Status = "rejected"
	}
	return msg
}

// Plan A: the Session owns refresh failure; recovery does not replace an
// unrelated operation notice, and current receipt status remains independent.
func TestNoticeReceiptRefreshRecovery(t *testing.T) {
	fail := true
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1),
		inputsFunc: func(_ context.Context, sessionID, coreID string, after uint64, _ bool) (api.InputReceiptPage, error) {
			if fail {
				return api.InputReceiptPage{}, errors.New("receipt detail")
			}
			return api.InputReceiptPage{CoreID: coreID, SessionID: sessionID, Revision: after}, nil
		}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	t.Cleanup(func() { cancel(); m.shutdown() })
	m.status = "rename complete"
	m.loadRuntimeInputs(m.sessionID, true)
	update(m, receiveRuntimeInputsLoaded(t, m, messages))
	if m.status != "rename complete" || !strings.Contains(m.footerNotice(), "Session receipt refresh failed · receipt detail") {
		t.Fatalf("refresh error has multiple owners or lost detail: status=%q footer=%q", m.status, m.footerNotice())
	}

	call := startNoticeRun(t, m, messages, backend, "session-1")
	update(m, receiveRuntimeInputsLoaded(t, m, messages))
	current := api.InputReceipt{CoreID: "core-1", SessionID: "session-1", RunID: call.request.RunID,
		InputID: "steer", Kind: "steering", Status: "applied", Revision: 1}
	m.mergeRuntimeReceipt(current)
	if m.runtimeInputStatus() != "steering · applied" || !strings.Contains(m.footerNotice(), "Session receipt refresh failed · receipt detail") ||
		!strings.Contains(m.footerNotice(), "steering · applied") {
		t.Fatalf("new run hid the unresolved Session error or current receipt: %q", m.footerNotice())
	}
	fail = false
	m.status = "follow-up accepted · queued"
	m.loadRuntimeInputs(m.sessionID, false)
	update(m, receiveRuntimeInputsLoaded(t, m, messages))
	if m.runtimeReceiptError != "" || m.status != "follow-up accepted · queued" || strings.Contains(m.footerNotice(), "refresh failed") {
		t.Fatalf("recovery retained failure or erased another notice: status=%q footer=%q", m.status, m.footerNotice())
	}

	fail = true
	m.loadRuntimeInputs(m.sessionID, false)
	update(m, receiveRuntimeInputsLoaded(t, m, messages))
	finishNoticeRun(t, m, messages, call)
	m.applySessionAction(sessionAction{newSession: true})
	if m.runtimeReceiptError != "" || m.runNoticeTarget != (runNoticeTarget{}) || strings.Contains(m.footerNotice(), "refresh failed") {
		t.Fatalf("Session switch retained old display state: target=%+v footer=%q", m.runNoticeTarget, m.footerNotice())
	}
	// A completed read from the previous Session must not restore its error.
	update(m, runtimeInputsLoadedMsg{jobID: 123, generation: m.sessionGeneration - 1, sessionID: "session-1", err: errors.New("old error")})
	if m.runtimeReceiptError != "" {
		t.Fatal("old Session response restored the error")
	}
}

// Plan B: every response branch uses the captured operation's target, including
// after a newer run has itself ended. Queue/data/draft processing is independent.
func TestNoticeOldInputResultsDoNotReplaceNextRun(t *testing.T) {
	for _, idle := range []bool{false, true} {
		for _, result := range []string{"error", "mismatch", "accepted", "not accepted"} {
			name := "running/" + result
			if idle {
				name = "idle/" + result
			}
			t.Run(name, func(t *testing.T) {
				backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), submitInputCalls: make(chan runtimeInputCall, 1)}
				m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
				t.Cleanup(func() { cancel(); m.shutdown() })
				first := startNoticeRun(t, m, messages, backend, "session-1")
				finishNoticeRun(t, m, messages, first)
				old := noticeInput(m, first.request.RunID, "old-input")
				second := startNoticeRun(t, m, messages, backend, "session-1")
				if idle {
					finishNoticeRun(t, m, messages, second)
				}
				m.status = "current operation notice"
				m.textarea.SetValue(old.draft)
				next := noticeInput(m, second.request.RunID, "next-input")
				m.runtimeInputQueue = []runtimeInputOperation{old, next}
				m.runtimeInputSending = true
				msg := noticeInputResult(old, result)
				if result == "mismatch" {
					// Even a receipt naming the displayed run does not own this operation.
					msg.receipt.RunID = second.request.RunID
				}
				update(m, msg)
				if m.status != "current operation notice" {
					t.Fatalf("old %s overwrote the displayed run: %q", result, m.status)
				}
				wantDraft := old.draft
				if result == "accepted" {
					wantDraft = ""
				}
				if m.textarea.Value() != wantDraft {
					t.Fatalf("notice filtering changed draft reconciliation: %q", m.textarea.Value())
				}
				if result == "accepted" || result == "not accepted" {
					if len(m.runtimeReceipts) != 1 || m.runtimeReceipts[0] != msg.receipt || m.runtimeDesiredRevision != 1 {
						t.Fatalf("old response lost receipt merge/revision: %+v desired=%d", m.runtimeReceipts, m.runtimeDesiredRevision)
					}
				}
				if len(m.runtimeInputQueue) != 1 || !m.runtimeInputSending {
					t.Fatalf("old response stopped queue: %+v", m.runtimeInputQueue)
				}
				queued := <-backend.submitInputCalls
				if queued.inputID != next.request.InputID || queued.request.Text != next.draft {
					t.Fatalf("next operation was changed: %+v", queued)
				}
				update(m, receiveRuntimeInputResult(t, m, messages, queued.inputID))
			})
		}
	}
}

// Plan B plus first Session admission: start begins with no Session, then the
// same run's responses must remain visible both while running and while idle.
func TestNoticeCurrentInputResultsAfterFirstAdmission(t *testing.T) {
	for _, idle := range []bool{false, true} {
		name := "running"
		if idle {
			name = "idle"
		}
		t.Run(name, func(t *testing.T) {
			backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
			m, messages, cancel := testModel(backend, Options{Core: backend.core})
			t.Cleanup(func() { cancel(); m.shutdown() })
			call := startNoticeRun(t, m, messages, backend, "new-session")
			if call.request.SessionID != "" || !m.showRunNotice("core-1", "new-session", call.request.RunID) {
				t.Fatalf("first admission did not synchronize display identity: request=%+v target=%+v", call.request, m.runNoticeTarget)
			}
			if idle {
				finishNoticeRun(t, m, messages, call)
			}
			for _, result := range []string{"error", "mismatch", "accepted", "not accepted"} {
				m.textarea.SetValue("queued task")
				input := noticeInput(m, call.request.RunID, "input-"+result)
				m.runtimeInputQueue = []runtimeInputOperation{input}
				m.runtimeInputSending = true
				update(m, noticeInputResult(input, result))
				want := "follow-up unconfirmed"
				if result == "accepted" {
					want = "follow-up accepted"
				} else if result == "not accepted" {
					want = "follow-up not accepted"
				}
				if !strings.HasPrefix(m.status, want) {
					t.Fatalf("current %s result suppressed after admission: %q", result, m.status)
				}
			}
		})
	}
}

// Plan C: cancellation notices share the same lifetime, but settlement still
// releases a pending Session switch even when that notice belongs to an old run.
func TestNoticeCancellationResultsAndSettlement(t *testing.T) {
	cases := []struct {
		name                             string
		old, idle, failed, switchSession bool
	}{
		{name: "current failure while running", failed: true},
		{name: "current settlement while idle", idle: true},
		{name: "old failure during next run", old: true, failed: true},
		{name: "old settlement during next run", old: true},
		{name: "old failure after next run ends", old: true, idle: true, failed: true},
		{name: "old settlement releases Session switch after next run ends", old: true, idle: true, switchSession: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
			m, messages, cancel := testModel(backend, Options{Core: backend.core})
			t.Cleanup(func() { cancel(); m.shutdown() })
			first := startNoticeRun(t, m, messages, backend, "new-session")
			target := inputChainCancellation{coreID: "core-1", sessionID: "new-session", runID: first.request.RunID}
			current := first
			if tc.old {
				finishNoticeRun(t, m, messages, first)
				current = startNoticeRun(t, m, messages, backend, "new-session")
			}
			if tc.idle {
				finishNoticeRun(t, m, messages, current)
			}
			m.chainCancel, m.pendingChainCancel, m.chainCancelJobID = &target, true, 123
			m.status = "current operation notice"
			if tc.switchSession {
				m.pendingSession = &sessionAction{newSession: true}
			}
			msg := runtimeInputChainSettledMsg{jobID: 123, coreID: target.coreID, runID: target.runID}
			if tc.failed {
				msg.err = errors.New("cancel detail")
			}
			update(m, msg)
			if tc.switchSession {
				if m.pendingSession != nil || m.sessionID != "" || m.chainCancel != nil || m.runNoticeTarget != (runNoticeTarget{}) || m.pendingChainCancel {
					t.Fatalf("suppressed notice blocked Session switch: session=%q pending=%+v target=%+v", m.sessionID, m.pendingSession, m.runNoticeTarget)
				}
				return
			}
			if m.pendingChainCancel || m.chainCancelSettled == tc.failed {
				t.Fatalf("notice filtering changed settlement state: pending=%t settled=%t", m.pendingChainCancel, m.chainCancelSettled)
			}
			if tc.old {
				if m.status != "current operation notice" {
					t.Fatalf("old cancellation overwrote notice: %q", m.status)
				}
			} else {
				want := "input chain settled"
				if tc.failed {
					want = "input chain cancellation not confirmed · cancel detail"
				}
				if !strings.HasPrefix(m.status, want) {
					t.Fatalf("current cancellation suppressed: %q", m.status)
				}
			}
		})
	}
}
