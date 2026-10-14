package tui

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/sessionid"
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
	generation    uint64
	request       uint64
	id            string
	err           error
	latestMissing bool
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
	workspace, coreWorkspace := m.workspace, m.core.Workspace
	m.status = "resolving Session " + m.shortSessionID(selector)
	m.launch(job, func() tea.Msg {
		if selector == "latest" {
			sessions, err := m.backend.Sessions(ctx)
			if err != nil {
				return sessionResolvedMsg{generation: generation, request: request, err: err}
			}
			id, found := latestSessionID(sessions, workspace, coreWorkspace)
			if !found {
				return sessionResolvedMsg{generation: generation, request: request, latestMissing: true}
			}
			selector = id
		}
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
	m.sessionPickerNotice = ""
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
	m.sessionDeleteTarget = ""
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
	saveTail := m.selectionSaveTail
	m.launch(job, func() tea.Msg {
		if err := waitForSelectionSave(ctx, saveTail); err != nil {
			return coreShutdownMsg{err: err}
		}
		return coreShutdownMsg{err: m.backend.ShutdownCore(ctx, coreID)}
	})
}

type sessionDeletedMsg struct {
	jobID       uint64
	id, shortID string
	err         error
}

func (m *model) handleSessionDeleteKey(msg tea.KeyPressMsg) bool {
	if m.pickerJobID != 0 {
		return msg.String() != "esc"
	}
	if m.sessionDeleteJobID != 0 {
		if msg.String() == "esc" {
			m.closeSessionPicker()
		}
		return true
	}
	if m.sessionDeleteTarget != "" {
		switch msg.String() {
		case "esc":
			m.sessionDeleteTarget = ""
			m.status = "deletion cancelled"
		case "enter":
			id := m.sessionDeleteTarget
			shortID := m.shortSessionID(id)
			job := m.newJob(func() {})
			job.closeBeforeBackend = true
			ctx := context.WithoutCancel(m.ctx)
			saveTail := m.selectionSaveTail
			m.sessionDeleteJobID = job.id
			m.status = "deleting Session " + shortID
			// Closing the picker does not cancel an already confirmed deletion.
			m.launch(job, func() tea.Msg {
				_ = waitForSelectionSave(ctx, saveTail)
				return sessionDeletedMsg{jobID: job.id, id: id, shortID: shortID, err: m.backend.DeleteSession(ctx, id)}
			})
		}
		return true
	}
	if msg.String() == "delete" {
		if m.pickerIndex >= 0 && m.pickerIndex < len(m.sessions) {
			m.sessionPickerNotice = ""
			m.sessionDeleteTarget = m.sessions[m.pickerIndex].ID
			m.status = "confirm deletion of Session " + m.shortSessionID(m.sessionDeleteTarget)
		}
		return true
	}
	return false
}

func (m *model) finishSessionDeletion(msg sessionDeletedMsg) {
	if msg.jobID != m.sessionDeleteJobID {
		return
	}
	m.sessionDeleteJobID = 0
	m.sessionDeleteTarget = ""
	if msg.err != nil {
		m.status = "Session deletion failed: " + msg.err.Error()
		m.sessionPickerNotice = "Delete failed: " + msg.err.Error()
		var apiErr *api.Error
		if errors.As(msg.err, &apiErr) && apiErr.Code == "session_busy" && apiErr.ActiveRunID != "" {
			m.sessionPickerNotice = "Resume this Session and stop its run with F1 before deleting."
			if m.activeRun != nil && m.activeRun.id == apiErr.ActiveRunID {
				m.sessionPickerNotice = "Stop the run with F1 before deleting this Session."
			}
		}
		return
	}
	sessions := m.sessions[:0]
	for _, session := range m.sessions {
		if session.ID != msg.id {
			sessions = append(sessions, session)
		}
	}
	m.sessions = sessions
	m.pickerIndex = max(0, min(m.pickerIndex, len(sessions)-1))
	if m.sessionID == msg.id {
		// Core has completed all writes before deletion; drop any late run event.
		if m.activeRun != nil && m.activeRun.session == msg.id {
			m.activeRun = nil
		}
		m.sessionID = "" // Avoid saving preferences against the removed Session.
		m.applySessionAction(sessionAction{newSession: true})
	} else if len(sessions) == 0 && m.connectionPicker == nil && !m.screenOverlayOpen() {
		m.closeSessionPicker()
	}
	m.status = "deleted Session " + msg.shortID
	m.sessionPickerNotice = m.status
}
