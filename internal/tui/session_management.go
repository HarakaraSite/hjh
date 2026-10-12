package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/sessionid"
	"hjh/internal/tui/renderer"
)

type sessionRenamedMsg struct {
	generation uint64
	id         string
	session    api.Session
	err        error
}

type coreShutdownMsg struct{ err error }

func (m *model) knownSessionIDs() []string {
	ids := make([]string, len(m.sessions))
	for i, session := range m.sessions {
		ids[i] = session.ID
	}
	return ids
}

func (m *model) shortSessionID(id string) string {
	return sessionid.Short(id, m.knownSessionIDs()...)
}

type sessionResolvedMsg struct {
	generation uint64
	request    uint64
	id         string
	err        error
}

// Resolve before deciding whether to stop the current run. Resuming its own
// short ID must leave the attached run in place.
func (m *model) resolveResume(selector string) {
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	generation := m.sessionGeneration
	m.resumeRequest++
	m.resumePending = true
	m.pendingSession = nil
	request := m.resumeRequest
	m.status = "resolving Session " + m.shortSessionID(selector)
	m.launch(job, func() tea.Msg {
		session, err := m.backend.ResolveSession(ctx, selector)
		return sessionResolvedMsg{generation: generation, request: request, id: session.ID, err: err}
	})
}

func sessionTitle(session api.Session) string {
	if session.Title != nil {
		return *session.Title
	}
	return ""
}

func (m *model) openSessionPicker() {
	m.closeConnectionPicker()
	m.closeSessionPicker()
	m.sealCurrentFrame()
	m.pickerOpen = true
	m.pickerIndex = 0
	m.refreshViewport(false)
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	generation := m.sessionGeneration
	m.pickerJobID = job.id
	m.status = "loading Sessions"
	m.launch(job, func() tea.Msg {
		sessions, err := m.backend.Sessions(ctx)
		return sessionsLoadedMsg{jobID: job.id, generation: generation, sessions: sessions, err: err}
	})
}

func (m *model) closeSessionPicker() {
	if m.pickerJobID != 0 {
		m.cancelJob(m.pickerJobID)
	}
	m.pickerJobID = 0
	m.pickerOpen = false
}

func (m *model) renameSession(title string) {
	if title == "" {
		m.status = "usage: /rename TEXT"
		return
	}
	if m.sessionID == "" || m.sessionLoading {
		m.status = "start or resume a Session before renaming"
		return
	}
	id, generation := m.sessionID, m.sessionGeneration
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.status = "renaming Session"
	m.launch(job, func() tea.Msg {
		session, err := m.backend.RenameSession(ctx, id, title)
		return sessionRenamedMsg{generation: generation, id: id, session: session, err: err}
	})
}

func (m *model) attachRun(state api.RunState) {
	job := m.newJob(func() {})
	coreID := state.CoreID
	if coreID == "" {
		coreID = m.core.CoreID
	}
	run := &activeRun{
		jobID: job.id, coreID: coreID, id: state.RunID, session: state.SessionID, workspace: m.workspace,
		phase: state.Phase, attached: true,
		admitted: state.SessionID != "",
		entities: make(map[string]api.ConversationEntity), inputRevision: state.InputRevision,
		inputAcceptance: state.InputAcceptance,
	}
	run.startedAt, _ = time.Parse(time.RFC3339Nano, state.StartedAt)
	if state.ActualSelection != nil {
		run.actualSelection = *state.ActualSelection
		run.hasActualSelection = true
	}
	if state.ActualContext != nil {
		actual := state.ActualContext.Clone()
		run.actualContext = &actual
		m.adoptActualOrientation(actual)
	}
	if m.chainCancel != nil && m.chainCancelSettled {
		m.chainCancel = nil
		m.chainCancelSettled = false
	}
	m.activeRun = run
	m.noteRuntimeInputRevision(coreID, run.session, state.InputRevision)
	m.status = "attaching to run " + run.id
	run.handle = m.backend.AttachRun(coreID, run.id, func(event apiclient.RunEvent) {
		m.sendMessage(runProgressMsg{jobID: job.id, value: event})
	})
	m.launch(job, func() tea.Msg {
		state, err := run.handle.Wait()
		return runDoneMsg{jobID: job.id, value: state, err: err}
	})
}

func (m *model) requestDetach() {
	m.resumeRequest++
	m.resumePending = false
	if m.shutdownPending {
		m.status = "waiting for Core to stop and save"
		return
	}
	m.pendingSession = nil
	if m.activeRun != nil && !m.activeRun.admitted {
		m.detachPending = true
		m.status = "waiting for saved Session before detach"
		return
	}
	m.finishDetach()
}

func (m *model) finishDetach() {
	m.detachPending = false
	m.detached, m.quitting = true, true
	text := "Core continues running"
	if m.sessionID != "" {
		text += " · Session " + m.shortSessionID(m.sessionID)
	}
	if m.activeRun != nil {
		text += " · run " + m.activeRun.id
	}
	m.exitNotice = "detached:\n" + text
}

func (m *model) requestCoreShutdown() {
	m.resumeRequest++
	m.resumePending = false
	if m.shutdownPending {
		return
	}
	m.shutdownPending = true
	m.detachPending = false
	m.pendingSession = nil
	m.status = "stopping Core · cancelling runs and waiting for saves"
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	coreID := m.core.CoreID
	m.launch(job, func() tea.Msg {
		return coreShutdownMsg{err: m.backend.ShutdownCore(ctx, coreID)}
	})
}

// Rows and selection offsets use the same cell wrapping as the terminal, so
// long names, Japanese text, and narrow windows keep the selected row visible.
func sessionPickerRows(sessions []api.Session, selected, width int) ([]string, []int) {
	rows := renderer.WrapText("Saved Sessions · ↑/↓ or j/k · PgUp/PgDn · Home/End · Enter resumes · Esc closes", max(1, width))
	starts := make([]int, len(sessions))
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
	}
	for i, session := range sessions {
		rows = append(rows, "")
		starts[i] = len(rows)
		marker := "  "
		if i == selected {
			marker = "▶ "
		}
		title := sessionTitle(session)
		if title == "" {
			title = "(untitled)"
		}
		running := ""
		if session.ActiveRunID != "" {
			running = " · running"
		}
		text := fmt.Sprintf("%s%s · %s%s\n  model: %s · turns: %d · updated: %s\n  workspace: %s", marker, title, sessionid.Short(session.ID, ids...), running, selectedModel(session.ActiveModel), session.TurnCount, session.UpdatedAt, session.WorkspaceRoot)
		rows = append(rows, renderer.WrapText(strings.TrimSuffix(text, "\n"), max(1, width))...)
	}
	return rows, starts
}
