package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
)

func TestContinueSelectsSameWorkspaceLatestBeforeExistingRestore(t *testing.T) {
	sessions := []api.Session{
		{ID: "old", WorkspaceRoot: "/work/project", UpdatedAt: "2026-10-13T10:00:00Z"},
		{ID: "other", WorkspaceRoot: "/elsewhere", UpdatedAt: "2026-10-14T12:00:00Z"},
		{ID: "latest", WorkspaceRoot: "/work/project", UpdatedAt: "2026-10-14T10:00:00Z"},
	}
	backend := &fakeBackend{session: sessions[2]}
	options := Options{Continue: true, Workspace: "project", Core: api.Core{Workspace: "/work"}}
	if err := resolveStartupSession(t.Context(), backend, &options, sessions); err != nil || options.SessionID != "latest" || options.Workspace != "/work/project" {
		t.Fatalf("continue selected wrong Session: %+v %v", options, err)
	}
	options = Options{Continue: true, Core: api.Core{Workspace: "/work/project"}}
	if err := resolveStartupSession(t.Context(), backend, &options, sessions); err != nil || options.SessionID != "latest" {
		t.Fatalf("Core workspace default was lost: %+v %v", options, err)
	}
	options = Options{Continue: true, Workspace: "/empty"}
	if err := resolveStartupSession(t.Context(), backend, &options, sessions); err != nil || options.SessionID != "" {
		t.Fatalf("empty workspace cannot start new conversation: %+v %v", options, err)
	}
	m, _, cancel := testModel(backend, options)
	defer func() { cancel(); m.shutdown() }()
	if m.sessionID != "" || !strings.Contains(m.status, "starting a new conversation") {
		t.Fatalf("empty continue did not start new: %s", m.status)
	}
}

func TestResumeLatestRestoresConversationAndNextRunUsesItsIdentity(t *testing.T) {
	target := api.Session{ID: "12345678-target", WorkspaceRoot: "/work", UpdatedAt: "2026-10-14T10:00:00Z"}
	answer := conversationEntity("saved-answer", "assistant", "saved report to continue", 1)
	answer.Status = "completed"
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1", Workspace: "/work"}, session: target,
		sessions: []api.Session{{ID: "87654321-other", WorkspaceRoot: "/other", UpdatedAt: "2026-10-14T12:00:00Z"}, target},
		view:     api.ConversationSnapshot{Entities: map[string]api.ConversationEntity{answer.ID: answer}}, runCalls: make(chan runCall, 1),
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work", SessionID: "older"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("continue this draft")
	m.handleCommand("/resume latest")
	updateUntil(t, m, messages, func(msg tea.Msg) bool { _, ok := msg.(sessionLoadedMsg); return ok })
	if m.sessionID != target.ID || m.sessionLoading || m.resumePending || len(backend.runCalls) != 0 || !strings.Contains(ansi.Strip(m.renderTranscript()), answer.Text) || m.textarea.Value() != "continue this draft" {
		t.Fatalf("latest restore: id=%q loading=%t pending=%t runs=%d draft=%q transcript=%q status=%q", m.sessionID, m.sessionLoading, m.resumePending, len(backend.runCalls), m.textarea.Value(), m.renderTranscript(), m.status)
	}
	m.submitDraft()
	call := <-backend.runCalls
	call.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	if call.request.SessionID != target.ID || call.request.Task != "continue this draft" {
		t.Fatalf("continuation lost target/draft: %+v", call.request)
	}
}

func TestResumeLatestOwnActiveSessionAndMissingHistoryKeepCurrentRun(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "own active", true: "no history"}[missing], func(t *testing.T) {
			own := api.Session{ID: "12345678-own", WorkspaceRoot: "/work", UpdatedAt: "2026-10-14T10:00:00Z"}
			backend := &fakeBackend{core: api.Core{CoreID: "core-1", Workspace: "/work"}, session: own, sessions: []api.Session{own}}
			if missing {
				backend.sessions = nil
			}
			m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: own.ID, Workspace: "/work"})
			defer func() { cancel(); m.shutdown() }()
			run := &activeRun{id: "existing", session: own.ID}
			m.activeRun = run
			m.textarea.SetValue("keep this draft")
			m.handleCommand("/resume latest")
			updateUntil(t, m, messages, func(msg tea.Msg) bool { _, ok := msg.(sessionResolvedMsg); return ok })
			if m.activeRun != run || m.sessionID != own.ID || m.pendingSession != nil || m.resumePending || m.textarea.Value() != "keep this draft" {
				t.Fatal("latest stopped/switched the current run")
			}
			if missing && !strings.Contains(m.status, "no saved Session") {
				t.Fatal("missing history was not explained")
			}
		})
	}
}
