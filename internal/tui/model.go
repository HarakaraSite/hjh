package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
)

type displayEntry struct {
	role string
	text string
}

type uiJob struct {
	id     uint64
	cancel context.CancelFunc
	done   chan struct{}
}

type activeRun struct {
	jobID          uint64
	id             string
	session        string
	model          string
	workspace      string
	handle         RunHandle
	actualRole     string
	actualModel    string
	lastStep       int
	lastSeq        uint64
	gotProjection  bool
	texts          map[textKey]api.TextState
	textOrder      []textKey
	operations     map[operationKey]api.OperationState
	operationOrder []operationKey
	executions     map[string]api.ExecutionState
	executionOrder []string
	canceling      bool
}

type textKey struct {
	executionID string
	step        int
}

type operationKey struct {
	executionID string
	id          string
}

type sessionAction struct {
	newSession bool
	resumeID   string
}

type model struct {
	ctx        context.Context
	cancelRoot context.CancelFunc
	backend    Backend
	send       func(tea.Msg)

	textarea textarea.Model
	viewport viewport.Model
	width    int
	height   int

	entries []displayEntry
	status  string

	sessionID     string
	modelID       string
	providerID    string
	effort        string
	authProfile   string
	maxSteps      *int
	fromTurn      *int64
	core          api.Core
	modelSet      bool
	roleID        string
	roleSet       bool
	lastRunModel  string
	lastRunRole   string
	lastSelection contracts.ModelSelection
	workspace     string
	pendingLogin  *api.Login

	sessionGeneration uint64
	sessionLoading    bool
	sessions          []api.Session
	pickerIndex       int
	pickerOpen        bool
	activeRun         *activeRun
	pendingSession    *sessionAction
	exitAfterRun      bool
	quitting          bool

	jobMu   sync.Mutex
	nextJob uint64
	jobs    map[uint64]*uiJob
	allJobs []*uiJob
}

type runProgressMsg struct {
	jobID uint64
	value apiclient.RunEvent
}

type runDoneMsg struct {
	jobID uint64
	value api.RunState
	err   error
}

type sessionLoadedMsg struct {
	jobID      uint64
	generation uint64
	session    api.Session
	messages   []api.Message
	err        error
}

type sessionsLoadedMsg struct {
	jobID      uint64
	generation uint64
	sessions   []api.Session
	err        error
}

type operationResultMsg struct {
	jobID      uint64
	generation uint64
	title      string
	text       string
	login      *api.Login
	err        error
}

func newModel(ctx context.Context, cancel context.CancelFunc, backend Backend, options Options) *model {
	ta := textarea.New()
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.MaxWidth = 0
	ta.MaxContentHeight = 0
	ta.Prompt = "> "
	ta.Placeholder = "依頼を入力してください"
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"), key.WithHelp("alt+enter", "改行"))
	ta.SetHeight(4)
	ta.Focus()

	vp := viewport.New()
	vp.SoftWrap = true
	vp.FillHeight = true
	vp.MouseWheelEnabled = true

	m := &model{
		ctx: ctx, cancelRoot: cancel, backend: backend,
		textarea: ta, viewport: vp,
		width:       80,
		height:      24,
		status:      "ready",
		sessionID:   options.SessionID,
		modelID:     options.ModelID,
		providerID:  options.ProviderID,
		effort:      options.Effort,
		authProfile: options.AuthProfile,
		maxSteps:    options.MaxSteps,
		fromTurn:    options.FromTurn,
		core:        options.Core,
		modelSet:    options.ModelID != "",
		roleID:      options.Role,
		roleSet:     options.Role != "",
		workspace:   options.Workspace,
		jobs:        make(map[uint64]*uiJob),
	}
	m.layout()
	return m
}

func (m *model) Init() tea.Cmd {
	if m.sessionID != "" && m.sessionGeneration == 0 {
		m.sessionGeneration++
		m.loadSession(m.sessionID, m.sessionGeneration)
	}
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case runProgressMsg:
		if m.activeRun != nil && msg.jobID == m.activeRun.jobID && msg.value.State.RunID == m.activeRun.id {
			m.acceptRunEvent(msg.value)
		}
		return m, nil
	case runDoneMsg:
		return m, m.finishRun(msg)
	case runCancelMsg:
		if m.activeRun != nil && msg.runID == m.activeRun.id {
			if msg.err != nil {
				m.activeRun.canceling = false
				m.status = "cancel request failed · " + msg.err.Error()
			} else {
				m.status = "cancel accepted · waiting for Core terminal result"
			}
		}
		return m, nil
	case sessionLoadedMsg:
		if msg.generation == m.sessionGeneration {
			m.sessionLoading = false
			if msg.err != nil {
				m.status = "session load failed: " + msg.err.Error()
			} else {
				m.sessionID = msg.session.ID
				m.workspace = msg.session.WorkspaceRoot
				m.lastRunModel = ""
				m.lastRunRole = msg.session.Agent
				_ = json.Unmarshal(msg.session.ActiveModel, &m.lastSelection)
				m.entries = conversationEntries(msg.messages)
				m.status = fmt.Sprintf("resumed · %d turns", msg.session.TurnCount)
			}
		}
		return m, nil
	case sessionsLoadedMsg:
		if msg.generation == m.sessionGeneration {
			if msg.err != nil {
				m.status = "sessions failed: " + msg.err.Error()
			} else if len(msg.sessions) == 0 {
				m.appendEntry("sessions", "No saved Sessions.")
				m.status = "no saved Sessions"
			} else {
				m.sessions = msg.sessions
				m.pickerIndex = 0
				m.pickerOpen = true
				m.status = "choose a Session · ↑/↓ or j/k · Enter resumes · Esc closes"
				m.refreshViewport(true)
			}
		}
		return m, nil
	case operationResultMsg:
		if msg.generation == m.sessionGeneration {
			if msg.err != nil {
				m.status = msg.title + " failed: " + msg.err.Error()
			} else {
				if msg.login != nil {
					m.pendingLogin = msg.login
				}
				m.appendEntry(msg.title, msg.text)
				m.status = msg.title + " loaded"
			}
		}
		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		if m.activeRun != nil {
			m.requestExit()
			return nil
		}
		m.quitting = true
		return tea.Quit
	case "esc":
		if m.activeRun != nil {
			m.requestRunCancel(m.activeRun)
			m.status = "cancelling active run · waiting for Core terminal result"
		} else {
			m.status = "ready"
		}
		if m.pickerOpen {
			m.pickerOpen = false
			m.refreshViewport(true)
		}
		return nil
	case "enter":
		if m.pickerOpen {
			m.selectSession()
			return nil
		}
		m.submitDraft()
		if m.quitting {
			return tea.Quit
		}
		return nil
	case "up", "k":
		if m.pickerOpen {
			m.movePicker(-1)
			return nil
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return cmd
	case "down", "j":
		if m.pickerOpen {
			m.movePicker(1)
			return nil
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return cmd
	case "pgup", "pgdown", "alt+up", "alt+down":
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return cmd
	default:
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return cmd
	}
}

func (m *model) submitDraft() {
	draft := m.textarea.Value()
	if strings.TrimSpace(draft) == "" {
		return
	}
	if isSlashCommand(strings.TrimSpace(draft)) {
		m.textarea.SetValue("")
		m.handleCommand(strings.TrimSpace(draft))
		return
	}
	if m.activeRun != nil {
		m.status = "run active · draft kept; wait for it to finish before submitting"
		return
	}
	if m.sessionLoading {
		m.status = "session is loading · draft kept"
		return
	}
	m.startRun(draft)
}

func isSlashCommand(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "/sessions", "/new", "/resume", "/history", "/executions", "/inspect", "/model", "/models", "/provider", "/providers", "/effort", "/auth", "/login", "/roles", "/role", "/help", "/quit":
		return true
	default:
		return false
	}
}

func (m *model) startRun(task string) {
	job := m.newJob(func() {})
	modelID := ""
	if m.modelSet {
		modelID = m.modelID
	}
	roleID := ""
	if m.roleSet {
		roleID = m.roleID
	}
	providerID, effort, authProfile := m.providerID, m.effort, m.authProfile
	run := &activeRun{
		jobID: job.id, id: uuid.NewString(), session: m.sessionID, model: modelID,
		workspace: m.workspace, texts: make(map[textKey]api.TextState),
		operations: make(map[operationKey]api.OperationState),
		executions: make(map[string]api.ExecutionState),
	}
	m.activeRun = run
	m.lastRunModel = ""
	m.entries = append(m.entries, displayEntry{role: "you", text: task})
	m.textarea.SetValue("")
	m.status = "starting run · Esc cancels and waits for Core"
	request := api.StartRunRequest{
		CoreID: m.core.CoreID, RunID: run.id, Task: task,
		SessionID: run.session, ModelID: run.model, ProviderID: providerID,
		Effort: effort, AuthProfile: authProfile, Workspace: run.workspace,
		Role: roleID, MaxSteps: m.maxSteps, FromTurn: m.fromTurn,
	}
	run.handle = m.backend.StartRun(request, func(event apiclient.RunEvent) {
		m.sendMessage(runProgressMsg{jobID: job.id, value: event})
	})
	m.launch(job, func() tea.Msg {
		result, err := run.handle.Wait()
		return runDoneMsg{jobID: job.id, value: result, err: err}
	})
}

func (m *model) acceptRunEvent(event apiclient.RunEvent) {
	run := m.activeRun
	if run == nil {
		return
	}
	state := event.State
	if state.CoreID != "" && state.CoreID != m.core.CoreID || state.RunID != run.id {
		return
	}
	if run.gotProjection && state.Seq < run.lastSeq {
		return
	}
	if event.Kind == "run.update" && run.gotProjection && state.Seq == run.lastSeq {
		return
	}
	if event.Kind == "run.snapshot" {
		run.texts = make(map[textKey]api.TextState)
		run.textOrder = nil
		run.operations = make(map[operationKey]api.OperationState)
		run.operationOrder = nil
		run.executions = make(map[string]api.ExecutionState)
		run.executionOrder = nil
		run.lastStep = 0
		run.actualRole = state.ActualRole
		run.actualModel = ""
	}
	for _, text := range state.Texts {
		key := textKey{executionID: text.ExecutionID, step: text.ModelStep}
		if _, exists := run.texts[key]; !exists {
			run.textOrder = append(run.textOrder, key)
		}
		run.texts[key] = text
		if text.ParentExecutionID == "" && text.ModelStep > run.lastStep {
			run.lastStep = text.ModelStep
		}
	}
	for _, operation := range state.Operations {
		key := operationKey{executionID: operation.ExecutionID, id: operation.ID}
		if _, exists := run.operations[key]; !exists {
			run.operationOrder = append(run.operationOrder, key)
		}
		run.operations[key] = operation
	}
	for _, execution := range state.Executions {
		if _, exists := run.executions[execution.ExecutionID]; !exists {
			run.executionOrder = append(run.executionOrder, execution.ExecutionID)
		}
		run.executions[execution.ExecutionID] = execution
	}
	run.gotProjection = true
	run.lastSeq = state.Seq
	if state.SessionID != "" {
		m.sessionID = state.SessionID
		run.session = state.SessionID
	}
	if state.ActualRole != "" {
		run.actualRole = state.ActualRole
	}
	if state.ActualSelection != nil {
		m.lastSelection = *state.ActualSelection
		run.actualModel = modelSelectionLabel(*state.ActualSelection)
	}
	for _, execution := range state.Executions {
		if execution.ParentExecutionID == "" {
			if run.actualRole == "" {
				run.actualRole = execution.Role
			}
			if execution.Selection != nil {
				m.lastSelection = *execution.Selection
				run.actualModel = modelSelectionLabel(*execution.Selection)
			}
		}
	}
	if run.actualRole != "" {
		m.lastRunRole = run.actualRole
	}
	if run.actualModel != "" {
		m.lastRunModel = run.actualModel
	}
	if event.Err != nil {
		m.status = "connection interrupted · reconnecting to run " + run.id
	} else if len(state.Texts) > 0 {
		step := run.lastStep
		m.status = "assistant is responding · step " + fmt.Sprint(step)
	} else if state.Phase != "" {
		m.status = "run " + state.Phase
	}
	m.refreshViewport(true)
}

func (m *model) finishRun(msg runDoneMsg) tea.Cmd {
	m.cancelJob(msg.jobID)
	run := m.activeRun
	if run == nil || msg.jobID != run.jobID {
		return nil
	}
	if msg.value.SessionID != "" {
		m.sessionID = msg.value.SessionID
	}
	projection := run.displayEntries()
	finalText := ""
	if msg.value.Result != nil && msg.value.Result.AgentResult != nil {
		finalText = msg.value.Result.AgentResult.FinalText
	}
	finalPlaced := false
	for _, entry := range projection {
		if finalText != "" && strings.HasPrefix(entry.role, "assistant") && entry.text == finalText {
			entry.role = "assistant"
			finalPlaced = true
		}
		m.entries = append(m.entries, entry)
	}
	if finalText != "" && !finalPlaced {
		m.entries = append(m.entries, displayEntry{role: "assistant", text: finalText})
	}
	if msg.err != nil {
		m.entries = append(m.entries, displayEntry{role: "run ended", text: msg.err.Error()})
		m.status = "run failed or stopped · draft remains available"
	} else if msg.value.Result != nil && msg.value.Result.OK {
		m.status = "run completed"
	} else if msg.value.Result != nil && msg.value.Result.Error != nil {
		m.entries = append(m.entries, displayEntry{role: "run ended", text: msg.value.Result.Error.Message})
		m.status = "run failed · draft remains available"
	} else if msg.value.Result != nil && msg.value.Result.AgentResult != nil && msg.value.Result.AgentResult.Error != "" {
		m.entries = append(m.entries, displayEntry{role: "run ended", text: msg.value.Result.AgentResult.Error})
		m.status = "run failed · draft remains available"
	} else if msg.value.Result != nil && msg.value.Result.AgentResult != nil {
		m.status = "run ended · " + msg.value.Result.AgentResult.Outcome
	} else {
		m.status = "run ended"
	}
	run.handle = nil
	m.activeRun = nil
	m.refreshViewport(true)
	if m.exitAfterRun {
		m.quitting = true
		return tea.Quit
	}
	if m.pendingSession != nil {
		action := *m.pendingSession
		m.pendingSession = nil
		m.applySessionAction(action)
	}
	return nil
}

func (m *model) requestExit() {
	if m.activeRun == nil {
		m.quitting = true
		return
	}
	m.exitAfterRun = true
	m.requestRunCancel(m.activeRun)
	m.status = "cancelling run and waiting for Core terminal result before exit"
}

type runCancelMsg struct {
	runID string
	err   error
}

func (m *model) requestRunCancel(run *activeRun) {
	if run == nil || run.handle == nil || run.canceling {
		return
	}
	handle, runID := run.handle, run.id
	run.canceling = true
	job := m.newJob(func() {})
	m.launch(job, func() tea.Msg {
		return runCancelMsg{runID: runID, err: handle.Cancel()}
	})
}

func (m *model) newJob(cancel context.CancelFunc) *uiJob {
	m.jobMu.Lock()
	defer m.jobMu.Unlock()
	m.nextJob++
	job := &uiJob{id: m.nextJob, cancel: cancel, done: make(chan struct{})}
	if m.jobs == nil {
		m.jobs = make(map[uint64]*uiJob)
	}
	m.jobs[job.id] = job
	m.allJobs = append(m.allJobs, job)
	return job
}

func (m *model) launch(job *uiJob, operation func() tea.Msg) {
	go func() {
		message := operation()
		m.sendMessage(message)
		close(job.done)
	}()
}

func (m *model) sendMessage(msg tea.Msg) {
	if m.send != nil {
		m.send(msg)
	}
}

func (m *model) cancelJob(id uint64) {
	m.jobMu.Lock()
	job := m.jobs[id]
	m.jobMu.Unlock()
	if job != nil {
		job.cancel()
	}
}

func (m *model) shutdown() {
	if run := m.activeRun; run != nil && run.handle != nil {
		_ = run.handle.Cancel()
		_, _ = run.handle.Wait()
	}
	if m.cancelRoot != nil {
		m.cancelRoot()
	}
	if closer, ok := m.backend.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	m.jobMu.Lock()
	jobs := append([]*uiJob(nil), m.allJobs...)
	m.jobMu.Unlock()
	for _, job := range jobs {
		job.cancel()
	}
	for _, job := range jobs {
		<-job.done
	}
}

func (m *model) layout() {
	if m.width < 1 {
		m.width = 80
	}
	if m.height < 1 {
		m.height = 24
	}
	inputHeight := 4
	transcriptHeight := m.height - 6 - 1 - 1 - inputHeight - 1
	if transcriptHeight < 1 {
		transcriptHeight = 1
	}
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(transcriptHeight)
	m.textarea.SetWidth(m.width)
	m.textarea.SetHeight(inputHeight)
	m.refreshViewport(true)
}

func (m *model) refreshViewport(gotoBottom bool) {
	if m.pickerOpen {
		m.viewport.SetContent(formatSessionPicker(m.sessions, m.pickerIndex))
		if len(m.sessions) > 0 {
			selectedLine := m.pickerIndex * 3
			if selectedLine < m.viewport.YOffset() {
				m.viewport.SetYOffset(selectedLine)
			} else if selectedLine >= m.viewport.YOffset()+m.viewport.Height() {
				m.viewport.SetYOffset(selectedLine - m.viewport.Height() + 1)
			}
		}
		return
	}
	var b strings.Builder
	for i, entry := range m.entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(entry.role)
		b.WriteString(":\n")
		b.WriteString(entry.text)
	}
	if m.activeRun != nil {
		for _, entry := range m.activeRun.displayEntries() {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(entry.role)
			b.WriteString(":\n")
			b.WriteString(entry.text)
		}
	}
	m.viewport.SetContent(b.String())
	if gotoBottom {
		m.viewport.GotoBottom()
	}
}

func (m *model) appendEntry(role, text string) {
	m.entries = append(m.entries, displayEntry{role: role, text: text})
	m.refreshViewport(true)
}

func (m *model) movePicker(delta int) {
	if !m.pickerOpen || len(m.sessions) == 0 {
		return
	}
	m.pickerIndex += delta
	if m.pickerIndex < 0 {
		m.pickerIndex = 0
	}
	if m.pickerIndex >= len(m.sessions) {
		m.pickerIndex = len(m.sessions) - 1
	}
	m.refreshViewport(false)
}

func (m *model) selectSession() {
	if len(m.sessions) == 0 || m.pickerIndex < 0 || m.pickerIndex >= len(m.sessions) {
		m.pickerOpen = false
		m.status = "no Session selected"
		m.refreshViewport(true)
		return
	}
	id := m.sessions[m.pickerIndex].ID
	m.pickerOpen = false
	m.refreshViewport(true)
	m.changeSession(sessionAction{resumeID: id})
}

func (m *model) View() tea.View {
	if m.width > 0 {
		m.refreshViewport(false)
	}
	session := m.sessionID
	if session == "" {
		session = "new (created on first run)"
	}
	workspace := m.workspace
	if workspace == "" {
		workspace = "(current directory)"
	}
	modelLabel := "from config or Session"
	if m.modelSet {
		modelLabel = m.modelID
	} else if m.lastRunModel != "" {
		modelLabel = "last run used " + m.lastRunModel
	}
	if m.activeRun != nil && m.activeRun.actualModel != "" {
		modelLabel = m.activeRun.actualModel + " (running)"
		if m.modelSet {
			modelLabel += " · next: " + m.modelID
		}
	}
	header := strings.Join([]string{
		"hjh  ·  workspace: " + workspace,
		"session: " + session,
		"model: " + modelLabel,
		"next connection: " + m.nextConnectionLabel(),
		"role: current " + m.currentRoleLabel() + " · next " + m.nextRoleLabel(),
		"status: " + m.status,
	}, "\n")
	content := strings.Join([]string{
		header,
		m.viewport.View(),
		"────────────────────────────────────────────────────────────────",
		"依頼を入力  ·  Enter: 送信  ·  Alt+Enter / Ctrl+J: 改行  ·  Esc: 停止  ·  Ctrl+C: 終了",
		m.textarea.View(),
	}, "\n")
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if cursor := m.textarea.Cursor(); cursor != nil {
		cursor.Position.Y += 6 + m.viewport.Height() + 2
		view.Cursor = cursor
	}
	return view
}

func selectedModel(raw json.RawMessage) string {
	var selection contracts.ModelSelection
	if json.Unmarshal(raw, &selection) == nil {
		return selection.ModelID
	}
	var id string
	_ = json.Unmarshal(raw, &id)
	return id
}

func modelSelectionLabel(selection contracts.ModelSelection) string {
	parts := make([]string, 0, 3)
	if selection.ModelID != "" {
		parts = append(parts, selection.ModelID)
	}
	if selection.Provider != "" {
		parts = append(parts, selection.Provider)
	}
	if selection.Effort != "" {
		parts = append(parts, "effort: "+selection.Effort)
	}
	return strings.Join(parts, " · ")
}

func (run *activeRun) displayEntries() []displayEntry {
	var entries []displayEntry
	for _, id := range run.executionOrder {
		execution := run.executions[id]
		if execution.ParentExecutionID == "" {
			continue
		}
		role := "child · " + execution.ExecutionID + " · parent " + execution.ParentExecutionID
		if execution.Role != "" {
			role = "child · role " + execution.Role + " · " + role
		}
		phase := execution.Phase
		if phase == "" {
			phase = "in progress"
		}
		if execution.Outcome != "" {
			phase += " · " + execution.Outcome
		}
		entries = append(entries, displayEntry{role: role, text: phase})
	}
	for _, key := range run.textOrder {
		text := run.texts[key]
		if text.Text == "" {
			continue
		}
		role := "assistant · step " + fmt.Sprint(text.ModelStep)
		if text.ParentExecutionID != "" {
			childRole := run.executions[text.ExecutionID].Role
			role = "child assistant · " + text.ExecutionID + " · step " + fmt.Sprint(text.ModelStep)
			if childRole != "" {
				role = "child assistant · role " + childRole + " · " + text.ExecutionID + " · step " + fmt.Sprint(text.ModelStep)
			}
		}
		entries = append(entries, displayEntry{role: role, text: text.Text})
	}
	for _, key := range run.operationOrder {
		operation := run.operations[key]
		if operation.Kind == "assistant_text" || operation.Kind == "model_result" || operation.Kind == "execution" && operation.Name == "run" {
			continue
		}
		label := operation.Kind
		if operation.Name != "" {
			label += " / " + operation.Name
		}
		if operation.Phase != "" {
			label += " · " + operation.Phase
		}
		if operation.Parent != "" {
			label += " · parent " + operation.Parent
		}
		role := "progress · " + label
		if operation.ParentExecutionID != "" {
			childRole := run.executions[operation.ExecutionID].Role
			role = "child progress · " + operation.ExecutionID + " · parent " + operation.ParentExecutionID + " · " + label
			if childRole != "" {
				role = "child progress · role " + childRole + " · " + operation.ExecutionID + " · parent " + operation.ParentExecutionID + " · " + label
			}
		}
		var summary strings.Builder
		if operation.ModelStep != 0 {
			fmt.Fprintf(&summary, "model step: %d", operation.ModelStep)
		}
		if operation.Error != "" {
			if summary.Len() > 0 {
				summary.WriteByte('\n')
			}
			summary.WriteString("error:\n")
			summary.WriteString(operation.Error)
		}
		if summary.Len() == 0 {
			summary.WriteString("in progress")
		}
		entries = append(entries, displayEntry{role: role, text: summary.String()})
	}
	return entries
}

func (m *model) loadSession(id string, generation uint64) {
	m.sessionLoading = true
	m.status = "loading Session " + id
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.launch(job, func() tea.Msg {
		session, err := m.backend.Session(ctx, id)
		if err != nil {
			return sessionLoadedMsg{jobID: job.id, generation: generation, err: err}
		}
		messages, err := m.backend.Conversation(ctx, id, 0, -1)
		return sessionLoadedMsg{jobID: job.id, generation: generation, session: session, messages: messages, err: err}
	})
}

var _ tea.Model = (*model)(nil)
