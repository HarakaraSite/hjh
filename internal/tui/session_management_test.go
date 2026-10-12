package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

func TestDetachWaitsForDurableSessionAndNeverCancels(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer cancel()
	m.startRun("continue after detach")
	call := <-backend.runCalls
	backend.closeHandles = []*fakeRun{call.handle}
	m.requestDetach()
	if m.quitting || !m.detachPending {
		t.Fatal("detached before saved Session was known")
	}
	state := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Phase: "preparing", SessionID: "durable-session"}
	cmd := deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &api.ConversationSnapshot{ViewID: "v", Entities: map[string]api.ConversationEntity{}}})
	if cmd == nil || !m.quitting || !m.detached || !strings.Contains(m.exitNotice, "durable-") {
		t.Fatal("detach did not leave its reconnection address")
	}
	m.shutdown()
	call.handle.mu.Lock()
	calls := call.handle.cancelCalls
	call.handle.mu.Unlock()
	if calls != 0 {
		t.Fatalf("detach cancelled Core run %d times", calls)
	}
}

func TestResumeAttachesExactRunAndCurrentSessionSelectionKeepsIt(t *testing.T) {
	state := api.RunState{CoreID: "core-1", RunID: "existing-run", SessionID: "saved", Phase: "running"}
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, activeSessionRun: &state, attachCalls: make(chan runCall, 1), runCalls: make(chan runCall, 1), session: api.Session{ID: "saved"}, view: api.ConversationSnapshot{Entities: map[string]api.ConversationEntity{"answer": conversationEntity("answer", "assistant", "saved prefix", 2)}}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "saved"})
	defer func() { cancel(); m.shutdown() }()
	m.Init()
	update(m, receive(t, messages))
	call := <-backend.attachCalls
	defer call.handle.finish(api.RunState{}, context.Canceled)
	if call.request.RunID != state.RunID || m.activeRun.id != state.RunID || len(backend.runCalls) != 0 {
		t.Fatal("resume started a different run")
	}
	m.changeSession(sessionAction{resumeID: "saved"})
	if m.pendingSession != nil || m.activeRun.canceling {
		t.Fatal("current Session selection cancelled its run")
	}
	updated := conversationEntity("answer", "assistant", "saved prefix continued", 2)
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &api.ConversationSnapshot{ViewID: "v", Entities: map[string]api.ConversationEntity{"answer": updated}}})
	if strings.Count(ansi.Strip(m.renderTranscript()), "saved prefix continued") != 1 || len(m.mergedEntities()) != 1 {
		t.Fatalf("history and live snapshot duplicated: %s", m.renderTranscript())
	}
}

func TestSessionPickerCurrentSelectionVisibilityAndRename(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	for i := 0; i < 30; i++ {
		title := fmt.Sprintf("日本語の長い作業名 %d", i)
		backend.sessions = append(backend.sessions, api.Session{ID: fmt.Sprintf("session-%02d", i), Title: &title, WorkspaceRoot: "/long/workspace", ActiveRunID: "running"})
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-18"})
	defer func() { cancel(); m.shutdown() }()
	update(m, tea.WindowSizeMsg{Width: 38, Height: 20})
	update(m, tea.KeyPressMsg{Code: tea.KeyF4})
	update(m, receive(t, messages))
	if !m.pickerOpen || m.pickerIndex != 18 || !strings.Contains(m.viewport.View(), "▶") {
		t.Fatalf("current selection invisible: index=%d %q", m.pickerIndex, m.viewport.View())
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyHome})
	if m.pickerIndex != 0 || !strings.Contains(m.viewport.View(), "▶") {
		t.Fatal("Home lost selection")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if m.pickerIndex != 29 || !strings.Contains(m.viewport.View(), "▶") {
		t.Fatal("End lost selection")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m.handleCommand("/rename 日本語の作業 名前")
	update(m, receive(t, messages))
	if m.sessionTitle != "日本語の作業 名前" {
		t.Fatalf("rename=%q", m.sessionTitle)
	}
	if isSlashCommand("/view") {
		t.Fatal("/view must remain excluded")
	}
}

func TestQuitWaitsForCoreSaveWithoutUsingRunCancel(t *testing.T) {
	saved := make(chan struct{})
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), shutdownCalls: make(chan string, 1), shutdownDone: saved}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer cancel()
	m.startRun("work")
	call := <-backend.runCalls
	backend.closeHandles = []*fakeRun{call.handle}
	m.handleCommand("/quit")
	select {
	case id := <-backend.shutdownCalls:
		if id != "core-1" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("no Core stop request")
	}
	if m.quitting || !m.shutdownPending {
		t.Fatal("quit before Core save completed")
	}
	close(saved)
	cmd := update(m, receive(t, messages))
	if cmd == nil || !m.quitting || !m.detached {
		t.Fatal("TUI did not exit after Core stopped")
	}
	m.shutdown()
	call.handle.mu.Lock()
	calls := call.handle.cancelCalls
	call.handle.mu.Unlock()
	if calls != 0 {
		t.Fatalf("Core stop redundantly called run cancel: %d", calls)
	}
}

func TestDetachNoticeIsFlushedAfterStreamingBody(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer cancel()
	output := attachWriter(m)
	m.startRun("streaming detach")
	call := <-backend.runCalls
	backend.closeHandles = []*fakeRun{call.handle}
	state := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Phase: "running", SessionID: "saved-streaming"}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &api.ConversationSnapshot{ViewID: "v", Entities: map[string]api.ConversationEntity{"answer": conversationEntity("answer", "assistant", "unfinished paragraph", 2)}}})
	m.View()
	m.requestDetach()
	if err := m.flushVisibleBody(); err != nil {
		t.Fatal(err)
	}
	m.shutdown()
	if err := m.writer.Close(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "detached:") != 1 || !strings.Contains(output.String(), "saved-st") || !strings.Contains(strings.ReplaceAll(output.String(), "\r\n", ""), call.request.RunID) {
		t.Fatalf("detach notice lost at writer close: %q", output.String())
	}
}

func TestEscapeWhileLoadingSessionsDoesNotReopenPicker(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, sessions: []api.Session{{ID: "saved"}}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	update(m, tea.KeyPressMsg{Code: tea.KeyF4})
	if !m.pickerOpen {
		t.Fatal("F4 must show a closable loading picker")
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	update(m, receive(t, messages))
	if m.pickerOpen || m.pickerJobID != 0 {
		t.Fatal("late Session response reopened a closed picker")
	}
}
