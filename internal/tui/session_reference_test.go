package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hjh/internal/api"
	"hjh/internal/sessionid"
)

type delayedSessionRequest struct {
	selector string
	reply    chan sessionResolvedMsg
}

type delayedSessionBackend struct {
	*fakeBackend
	requests chan delayedSessionRequest
}

func (b *delayedSessionBackend) ResolveSession(ctx context.Context, selector string) (api.Session, error) {
	request := delayedSessionRequest{selector: selector, reply: make(chan sessionResolvedMsg, 1)}
	select {
	case b.requests <- request:
	case <-ctx.Done():
		return api.Session{}, ctx.Err()
	}
	select {
	case result := <-request.reply:
		return api.Session{ID: result.id}, result.err
	case <-ctx.Done():
		return api.Session{}, ctx.Err()
	}
}

func TestPendingSessionReferenceKeepsDraftAndNewestSwitch(t *testing.T) {
	a := "aaaaaaaa-7552-485d-9922-a1d7ae426706"
	b := "bbbbbbbb-7552-485d-9922-a1d7ae426706"
	c := "cccccccc-7552-485d-9922-a1d7ae426706"
	base := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	backend := &delayedSessionBackend{fakeBackend: base, requests: make(chan delayedSessionRequest, 4)}
	m, messages, cancel := testModel(backend, Options{Core: base.core, SessionID: a})
	defer func() { cancel(); m.shutdown() }()
	resolved := func() {
		t.Helper()
		for {
			msg := receive(t, messages)
			update(m, msg)
			if _, ok := msg.(sessionResolvedMsg); ok {
				return
			}
		}
	}
	m.handleCommand("/resume " + b[:8])
	first := <-backend.requests
	m.textarea.SetValue("keep this draft")
	m.submitDraft()
	if !m.resumePending || m.activeRun != nil || m.textarea.Value() != "keep this draft" {
		t.Fatal("lookup sent the draft to the previous Session")
	}
	first.reply <- sessionResolvedMsg{err: errors.New("Session not found")}
	resolved()
	if m.resumePending {
		t.Fatal("failed lookup kept input blocked")
	}
	m.submitDraft()
	run := <-base.runCalls
	defer run.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	if run.request.SessionID != a || run.request.Task != "keep this draft" {
		t.Fatalf("draft or full identity was lost: %+v", run.request)
	}
	m.handleCommand("/resume " + b[:8])
	older := <-backend.requests
	m.handleCommand("/resume " + c[:8])
	newer := <-backend.requests
	newer.reply <- sessionResolvedMsg{id: c}
	resolved()
	older.reply <- sessionResolvedMsg{id: b}
	resolved()
	if m.pendingSession == nil || m.pendingSession.resumeID != c {
		t.Fatalf("older lookup replaced newest switch: %+v", m.pendingSession)
	}
	m.handleCommand("/resume " + b[:8])
	obsolete := <-backend.requests
	if m.pendingSession != nil {
		t.Fatal("new lookup left the previous switch pending")
	}
	// The parent can settle before lookup completion. Its cleanup must not
	// apply the previous target or invalidate the newest lookup generation.
	m.activeRun = nil
	m.pendingChainCancel = false
	m.chainCancel = nil
	m.applyPendingSessionIfSettled()
	if m.sessionID != a || !m.resumePending {
		t.Fatal("parent settlement applied an obsolete Session target")
	}
	obsolete.reply <- sessionResolvedMsg{id: b}
	resolved()
	load := <-backend.requests
	load.reply <- sessionResolvedMsg{id: b}
	for {
		msg := receive(t, messages)
		update(m, msg)
		if _, ok := msg.(sessionLoadedMsg); ok {
			break
		}
	}
	if m.sessionID != b || m.resumePending || m.sessionLoading {
		t.Fatal("latest lookup did not complete after parent settlement")
	}
	m.handleCommand("/resume " + c[:8])
	obsolete = <-backend.requests
	m.handleCommand("/new")
	obsolete.reply <- sessionResolvedMsg{id: c}
	resolved()
	if m.resumePending || m.pendingSession != nil || m.sessionID != "" {
		t.Fatalf("late lookup replaced /new: %+v", m.pendingSession)
	}
}

func TestCancelledPickerKeepsKnownSessionPrefixes(t *testing.T) {
	ids := []string{"b71a29a6-7552-485d-9922-a1d7ae426706", "b71a29a6-a552-485d-9922-a1d7ae426706"}
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core, SessionID: ids[0]})
	defer func() { cancel(); m.shutdown() }()
	m.sessions = []api.Session{{ID: ids[0]}, {ID: ids[1]}}
	before := m.shortSessionID(ids[0])
	m.openSessionPicker()
	m.closeSessionPicker()
	if after := m.shortSessionID(ids[0]); after != before || after == ids[0][:8] {
		t.Fatalf("cancelled picker forgot known collision: %s -> %s", before, after)
	}
}

func TestShortSessionResumePreservesAttachedRunAndFullIdentity(t *testing.T) {
	id := "b71a29a6-7552-485d-9922-a1d7ae426706"
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, session: api.Session{ID: id},
		activeSessionRun: &api.RunState{CoreID: "core-1", RunID: "existing-run", SessionID: id, Phase: "running"},
		attachCalls:      make(chan runCall, 1), view: api.ConversationSnapshot{Entities: map[string]api.ConversationEntity{}}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: sessionid.Short(id)})
	defer func() { cancel(); m.shutdown() }()
	m.Init()
	update(m, receive(t, messages))
	call := <-backend.attachCalls
	defer call.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	if m.sessionID != id || m.activeRun.session != id {
		t.Fatalf("short selector entered runtime ownership: %s %+v", m.sessionID, m.activeRun)
	}
	active := m.activeRun
	m.handleCommand("/resume " + sessionid.Short(id))
	for {
		msg := receive(t, messages)
		update(m, msg)
		if _, ok := msg.(sessionResolvedMsg); ok {
			break
		}
	}
	if m.activeRun != active || m.pendingSession != nil || !strings.Contains(m.status, "current Session · b71a29a6") {
		t.Fatalf("own short selector switched/cancelled current run: %s %+v", m.status, m.pendingSession)
	}
	call.handle.mu.Lock()
	cancelled := call.handle.cancelCalls
	call.handle.mu.Unlock()
	if cancelled != 0 {
		t.Fatal("own short selector cancelled attached Core work")
	}
	rows := inlineMenuRows("Sessions", nil, sessionPickerOptions([]api.Session{{ID: id}}, ""), 0, 100, 8)
	if text := strings.Join(rows, "\n"); strings.Contains(text, id) || !strings.Contains(text, sessionid.Short(id)) {
		t.Fatalf("picker does not show human reference: %s", text)
	}
	m.requestDetach()
	if strings.Contains(m.exitNotice, id) || !strings.Contains(m.exitNotice, "Session b71a29a6") {
		t.Fatalf("detach does not show human reference: %s", m.exitNotice)
	}
}
