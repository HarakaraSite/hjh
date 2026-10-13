package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
	"hjh/internal/tui/renderer"
)

type displayEntry struct {
	role string
	text string
}

type uiJob struct {
	id                 uint64
	cancel             context.CancelFunc
	done               chan struct{}
	closeBeforeBackend bool
}

type activeRun struct {
	startedAt             time.Time
	jobID                 uint64
	coreID                string
	id                    string
	session               string
	phase                 string
	attached              bool
	model                 string
	provider              string
	effort                string
	authProfile           string
	workspace             string
	handle                RunHandle
	actualRole            string
	nextRunID             string
	actualSelection       contracts.ModelSelection
	hasActualSelection    bool
	actualContext         *contracts.Orientation
	lastSeq               uint64
	admitted              bool
	scopeID               string
	viewID                string
	revision              uint64
	gotSnapshot           bool
	terminalSnapshotSeen  bool
	terminalEventSeen     bool
	initialSnapshotSeen   bool
	initialWasTerminal    bool
	terminalSnapshotCount int
	entities              map[string]api.ConversationEntity
	canceling             bool
	chainCancelRequested  bool
	inputRevision         uint64
	inputAcceptance       api.InputAcceptance
	pendingDone           *runDoneMsg
}

type sessionAction struct {
	newSession bool
	resumeID   string
}

type connectionPickerKind string

const (
	providerPicker connectionPickerKind = "provider"
	modelPicker    connectionPickerKind = "model"
	effortPicker   connectionPickerKind = "effort"
)

type connectionPickerState struct {
	kind             connectionPickerKind
	jobID            uint64
	catalogJobID     uint64
	favoritesJobID   uint64
	generation       uint64
	pickerID         uint64
	request          api.SelectionRequest
	preview          api.SelectionPreview
	providers        []api.ProviderDefinition
	models           []api.ModelInfo
	loading          bool
	err              error
	previewErr       error
	query            string
	index            int
	favorites        api.ModelFavorites
	favoritesKnown   bool
	favoritesLoading bool
	favoritesErr     error
	retainedModels   map[string]struct{}
	modelsLoading    bool
	authOwnerID      string
	authAccountID    string
}

type model struct {
	ctx        context.Context
	cancelRoot context.CancelFunc
	backend    Backend
	send       func(tea.Msg)

	textarea textarea.Model
	viewport viewport.Model
	writer   *renderer.Writer
	width    int
	height   int

	entries       []displayEntry
	status        string
	draftRevision uint64

	sessionEntities            map[string]api.ConversationEntity
	transcript                 *transcriptDocument
	transcriptDirty            bool
	receiptByPart              map[string]int
	markdownRenderer           *glamour.TermRenderer
	markdownRendererWidth      int
	markdownSourceCache        map[string]markdownSourceCacheEntry
	markdownBlockCache         map[markdownBlockKey]markdownBlockCache
	transcriptLineCache        map[transcriptLineKey]transcriptLineCache
	receiptOffset              int
	lastTranscriptBytes        int
	lastBodyRows               []string
	lastBodyStartRow           int
	pendingSealRows            []string
	headerPrinted              bool
	deferTranscriptUntilHeader bool
	renderErr                  error

	sessionID                string
	sessionTitle             string
	sessionCreatedAt         string
	modelID                  string
	providerID               string
	effort                   string
	authProfile              string
	maxSteps                 *int
	fromTurn                 *int64
	core                     api.Core
	headerOrientation        api.Orientation
	headerOrientationSet     bool
	headerOrientationPending bool
	headerOrientationFailed  bool
	headerOrientationJobID   uint64
	contextRequestID         uint64
	contextJobID             uint64
	modelSet                 bool
	roleID                   string
	roleSet                  bool
	lastRunRole              string
	lastSelection            contracts.ModelSelection
	footerInitialized        bool
	footerSelectionJobID     uint64
	footerSelection          api.SelectionPreview
	footerSelectionSet       bool
	footerSelectionError     error
	footerClockJobID         uint64
	footerClockFrame         uint64
	workspace                string
	authUI                   *authUIState
	authGeneration           uint64
	recallOperations         map[string]*recallUIOperation

	sessionGeneration      uint64
	selectionGeneration    uint64
	selectionSaveTail      <-chan struct{}
	sessionLoading         bool
	resumeRequest          uint64
	resumePending          bool
	sessions               []api.Session
	pickerIndex            int
	pickerOpen             bool
	pickerJobID            uint64
	connectionPicker       *connectionPickerState
	connectionPickerSeq    uint64
	modelFavoriteSnapshots map[string]api.ModelFavorites
	modelFavoriteOps       map[string]*modelFavoriteOperation
	modelFavoriteOpSeq     uint64
	commandMenu            *commandMenuState
	commandMenuDismissed   string
	activeRun              *activeRun
	pendingSession         *sessionAction
	chainCancel            *inputChainCancellation
	chainCancelJobID       uint64
	pendingChainCancel     bool
	chainCancelSettled     bool
	runtimeInputQueue      []runtimeInputOperation
	runtimeInputSending    bool
	runtimeReceipts        []api.InputReceipt
	runtimeReceiptIndex    map[string]int
	runtimeWatermarks      map[string]uint64
	runtimeReadPending     bool
	runtimeReadJobID       uint64
	runtimeReadSession     string
	runtimeReadCore        string
	runtimeReadAfter       uint64
	runtimeDesiredCore     string
	runtimeDesiredSession  string
	runtimeDesiredRevision uint64
	runtimeLatestStatus    string
	detachPending          bool
	detached               bool
	shutdownPending        bool
	quitting               bool
	exitNotice             string

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
	view       api.ConversationSnapshot
	run        *api.RunState
	err        error
}

type sessionsLoadedMsg struct {
	jobID      uint64
	generation uint64
	sessions   []api.Session
	err        error
}

type connectionPickerLoadedMsg struct {
	jobID      uint64
	generation uint64
	pickerID   uint64
	request    api.SelectionRequest
	preview    api.SelectionPreview
	providers  []api.ProviderDefinition
	models     []api.ModelInfo
	previewErr error
	err        error
}

type modelCatalogLoadedMsg struct {
	jobID               uint64
	pickerID            uint64
	selectionGeneration uint64
	coreID              string
	sessionID           string
	workspace           string
	providerID          string
	models              []api.ModelInfo
	authOwnerID         string
	authAccountID       string
	err                 error
}

type modelFavoritesLoadedMsg struct {
	jobID               uint64
	pickerID            uint64
	selectionGeneration uint64
	coreID              string
	sessionID           string
	workspace           string
	providerID          string
	operationID         uint64
	purpose             modelFavoritesReadPurpose
	favorites           api.ModelFavorites
	err                 error
}

type modelFavoriteSavedMsg struct {
	jobID       uint64
	operationID uint64
	coreID      string
	sessionID   string
	workspace   string
	providerID  string
	modelID     string
	favorite    bool
	result      api.ModelFavoriteResult
	err         error
}

type operationResultMsg struct {
	jobID      uint64
	generation uint64
	title      string
	text       string
	err        error
}

type orientationLoadedMsg struct {
	jobID      uint64
	generation uint64
	coreID     string
	request    api.OrientationRequest
	value      api.Orientation
	err        error
}

type contextLoadedMsg struct {
	jobID       uint64
	requestID   uint64
	generation  uint64
	coreID      string
	request     api.OrientationRequest
	sessionID   string
	runID       string
	orientation api.Orientation
	orientErr   error
	state       api.SessionContext
	stateErr    error
	selection   api.SelectionPreview
	selectErr   error
}

type runtimeInputSubmittedMsg struct {
	jobID   uint64
	input   runtimeInputOperation
	receipt api.InputReceipt
	err     error
}

type runtimeInputsLoadedMsg struct {
	jobID       uint64
	generation  uint64
	sessionID   string
	queryCoreID string
	page        api.InputReceiptPage
	err         error
}

type runtimeInputChainSettledMsg struct {
	jobID  uint64
	coreID string
	runID  string
	state  api.InputChainState
	err    error
}

func newModel(ctx context.Context, cancel context.CancelFunc, backend Backend, options Options) *model {
	ta := textarea.New()
	ta.CharLimit = 0
	ta.MaxHeight = 5
	ta.MaxWidth = 0
	ta.MaxContentHeight = 0
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.Prompt = "> "
	ta.Placeholder = ""
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "shift+enter", "ctrl+enter", "ctrl+j"), key.WithHelp("alt+enter", "改行"))
	ta.SetHeight(1)
	ta.SetVirtualCursor(false)
	ta.SetStyles(editorStyles())
	ta.Focus()

	vp := viewport.New()
	vp.SoftWrap = true
	vp.FillHeight = true
	vp.MouseWheelEnabled = true

	m := &model{
		ctx: ctx, cancelRoot: cancel, backend: backend,
		textarea: ta, viewport: vp,
		width:                  80,
		height:                 24,
		status:                 "ready",
		sessionEntities:        make(map[string]api.ConversationEntity),
		transcriptDirty:        true,
		receiptByPart:          make(map[string]int),
		sessionID:              options.SessionID,
		modelID:                options.ModelID,
		providerID:             options.ProviderID,
		effort:                 options.Effort,
		authProfile:            options.AuthProfile,
		maxSteps:               options.MaxSteps,
		fromTurn:               options.FromTurn,
		core:                   options.Core,
		modelSet:               options.ModelID != "",
		roleID:                 options.Role,
		roleSet:                options.Role != "",
		workspace:              options.Workspace,
		selectionGeneration:    1,
		jobs:                   make(map[uint64]*uiJob),
		runtimeReceiptIndex:    make(map[string]int),
		runtimeWatermarks:      make(map[string]uint64),
		modelFavoriteSnapshots: make(map[string]api.ModelFavorites),
		modelFavoriteOps:       make(map[string]*modelFavoriteOperation),
	}
	m.layout()
	return m
}

func (m *model) Init() tea.Cmd {
	m.footerInitialized = true
	if m.sessionID != "" && m.sessionGeneration == 0 {
		m.sessionGeneration++
		m.loadSession(m.sessionID, m.sessionGeneration)
	} else if m.sessionID == "" {
		m.requestFooterSelection()
		m.requestHeaderOrientation()
	}
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.updateMessage(msg)
	if m.activeRun != nil && m.footerClockJobID != m.activeRun.jobID {
		m.footerClockJobID = m.activeRun.jobID
		m.footerClockFrame = 0
		cmd = tea.Batch(cmd, m.footerTick())
	}
	return next, cmd
}

func (m *model) updateMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.CursorPositionMsg:
		if m.writer != nil {
			m.writer.ObserveCursor(msg.Y, msg.X)
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.sealCurrentFrame()
		m.width, m.height = msg.Width, msg.Height
		m.invalidateTranscript()
		m.layout()
		return m, nil
	case footerSelectionLoadedMsg:
		m.acceptFooterSelection(msg)
		return m, nil
	case selectionSaveFailedMsg:
		if msg.coreID == m.core.CoreID && msg.workspace == m.workspace {
			m.status = "model selection save failed: " + msg.err.Error()
		}
		return m, nil
	case footerTickMsg:
		if m.activeRun != nil && msg.jobID == m.activeRun.jobID {
			m.footerClockFrame++
			return m, m.footerTick()
		}
		if msg.jobID == m.footerClockJobID {
			m.footerClockJobID = 0
		}
		return m, nil
	case tea.MouseWheelMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	case runProgressMsg:
		if m.activeRun != nil && msg.jobID == m.activeRun.jobID && msg.value.State.RunID == m.activeRun.id {
			return m, m.acceptRunEvent(msg.value)
		}
		return m, nil
	case runDoneMsg:
		return m, m.acceptRunDone(msg)
	case orientationLoadedMsg:
		m.acceptHeaderOrientation(msg)
		return m, nil
	case contextLoadedMsg:
		m.acceptContextResult(msg)
		return m, nil
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
	case runtimeInputSubmittedMsg:
		return m, m.acceptRuntimeInput(msg)
	case runtimeInputsLoadedMsg:
		return m, m.acceptRuntimeInputs(msg)
	case runtimeInputChainSettledMsg:
		return m, m.acceptInputChainSettlement(msg)
	case sessionResolvedMsg:
		if msg.generation == m.sessionGeneration && msg.request == m.resumeRequest {
			m.resumePending = false
			if msg.err != nil {
				m.status = "session resolution failed: " + msg.err.Error()
			} else {
				m.changeSession(sessionAction{resumeID: msg.id})
			}
		}
		return m, nil
	case sessionLoadedMsg:
		if msg.generation == m.sessionGeneration {
			m.sessionLoading = false
			if msg.err != nil {
				m.status = "session load failed: " + msg.err.Error()
				m.rebaseAuthUIForCurrentRoute()
				if !m.headerPrinted {
					m.requestHeaderOrientation()
				}
			} else {
				if m.sessionID != msg.session.ID {
					m.headerPrinted = false
					m.resetHeaderOrientation()
				}
				m.sessionID = msg.session.ID
				m.setSessionMetadata(msg.session)
				m.lastRunRole = msg.session.Agent
				_ = json.Unmarshal(msg.session.ActiveModel, &m.lastSelection)
				m.applySessionSnapshot(msg.view)
				m.entries = nil
				m.invalidateTranscript()
				m.loadRuntimeInputs(msg.session.ID, true)
				m.nextSelectionChanged()
				m.status = fmt.Sprintf("resumed · %d turns", msg.session.TurnCount)
				if msg.run != nil {
					m.attachRun(*msg.run)
				}
				if !m.headerPrinted && !m.headerOrientationSet && !m.headerOrientationPending {
					m.requestHeaderOrientation()
				}
				m.rebaseAuthUIForCurrentRoute()
				m.refreshViewport(false)
			}
		}
		return m, nil
	case sessionsLoadedMsg:
		if msg.generation == m.sessionGeneration && msg.jobID == m.pickerJobID {
			if msg.err != nil {
				m.closeSessionPicker()
				m.status = "sessions failed: " + msg.err.Error()
			} else if len(msg.sessions) == 0 {
				m.closeSessionPicker()
				m.appendEntry("sessions", "No saved Sessions.")
				m.status = "no saved Sessions"
			} else {
				m.sessions = msg.sessions
				m.pickerIndex = 0
				for i, session := range m.sessions {
					if session.ID == m.sessionID {
						m.pickerIndex = i
						break
					}
				}
				m.viewport.SetYOffset(0)
				m.sealCurrentFrame()
				m.pickerOpen = true
				m.status = "choose a Session · ↑/↓ or j/k · Enter resumes · Esc closes"
				m.refreshViewport(true)
			}
		}
		return m, nil
	case tea.PasteMsg:
		if m.authUI != nil && m.authUI.open {
			m.appendAuthSecret(msg.Content)
			return m, nil
		}
		if m.connectionPicker != nil {
			m.appendModelQuery(msg.Content)
			return m, nil
		}
		before := m.textarea.Value()
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		if m.textarea.Value() != before {
			m.noteDraftEdit()
		}
		return m, cmd
	case connectionPickerLoadedMsg:
		picker := m.connectionPicker
		if picker != nil && m.pickerOpen && msg.jobID == picker.jobID &&
			msg.pickerID == picker.pickerID &&
			msg.generation == m.selectionGeneration && msg.request == m.selectionRequest() &&
			msg.generation == picker.generation && msg.request == picker.request {
			picker.loading = false
			picker.err = msg.err
			picker.previewErr = msg.previewErr
			if msg.err == nil {
				picker.preview = msg.preview
				picker.providers = msg.providers
				picker.models = msg.models
				if picker.kind == modelPicker {
					providerID := picker.preview.Selection.Provider
					picker.modelsLoading = providerID != ""
					picker.favoritesLoading = providerID != ""
					if providerID == "" {
						picker.favoritesLoading = false
					}
				}
				picker.index = picker.selectedIndex(m.connectionPickerOptions())
				m.status = "choose " + string(picker.kind) + " for next run"
				if picker.previewErr != nil {
					m.status = "current selection preview unavailable · choose a defined provider"
				}
				if picker.kind == modelPicker && picker.preview.Selection.Provider != "" {
					scope := apiclient.Scope{SessionID: picker.request.SessionID, Workspace: picker.request.Workspace}
					m.startModelCatalogRead(picker, scope)
					picker.favoritesJobID = m.startModelFavoritesRead(picker.pickerID, picker.generation, m.core.CoreID, scope,
						picker.preview.Selection.Provider, modelFavoritesReadOpen, 0)
				}
			} else {
				m.status = string(picker.kind) + " picker failed: " + msg.err.Error()
				picker.modelsLoading = false
				picker.favoritesLoading = false
			}
			m.refreshViewport(false)
		}
		return m, nil
	case authProfilesLoadedMsg:
		m.acceptAuthProfiles(msg)
		return m, nil
	case authAccountsLoadedMsg:
		return m, m.acceptAuthAccounts(msg)
	case authAPIKeySavedMsg:
		m.acceptAuthAPIKeySaved(msg)
		return m, nil
	case authAccountSelectedMsg:
		m.acceptAuthAccountSelected(msg)
		return m, nil
	case authAttemptStartedMsg:
		return m, m.acceptAuthAttemptStarted(msg)
	case authAttemptReadMsg:
		return m, m.acceptAuthAttemptRead(msg)
	case authAttemptCompletedMsg:
		return m, m.acceptAuthAttemptCompleted(msg)
	case authAttemptCancelledMsg:
		return m, m.acceptAuthAttemptCancelled(msg)
	case authAttemptDeletedMsg:
		m.acceptAuthAttemptDeleted(msg)
		return m, nil
	case authPollTickMsg:
		if state := m.authUI; state != nil && state.generation == msg.generation &&
			state.attemptCoreID == msg.coreID && state.attemptID == msg.attemptID &&
			state.attempt != nil && !state.attempt.Terminal() {
			m.readAuthAttempt(state)
		}
		return m, nil
	case modelCatalogLoadedMsg:
		m.acceptModelCatalog(msg)
		return m, nil
	case modelFavoritesLoadedMsg:
		m.acceptModelFavoritesRead(msg)
		return m, nil
	case modelFavoriteSavedMsg:
		m.acceptModelFavoriteWrite(msg)
		return m, nil
	case sessionRenamedMsg:
		if msg.generation == m.sessionGeneration && msg.id == m.sessionID {
			if msg.err != nil {
				m.status = "rename failed: " + msg.err.Error()
			} else {
				m.sessionTitle = sessionTitle(msg.session)
				m.status = "renamed · " + m.sessionTitle
			}
		}
		return m, nil
	case coreShutdownMsg:
		m.shutdownPending = false
		if msg.err != nil {
			m.status = "Core stop failed: " + msg.err.Error()
			return m, nil
		}
		m.status = "Core stopped · work saved"
		m.exitNotice = "Core stopped:\nwork saved"
		m.detached, m.quitting = true, true
		return m, tea.Quit
	case operationResultMsg:
		if msg.generation == m.sessionGeneration {
			if msg.err != nil {
				m.status = msg.title + " failed: " + msg.err.Error()
			} else {
				m.appendEntry(msg.title, msg.text)
				m.status = msg.title + " loaded"
			}
		}
		return m, nil
	case recallResultMsg:
		m.applyRecallResult(msg)
		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.authUI != nil && m.authUI.open {
		return m.handleAuthKey(msg)
	}
	switch msg.String() {
	case "ctrl+c":
		before := m.textarea.Value()
		m.textarea.SetValue("")
		if before != "" {
			m.noteDraftEdit()
		}
		m.status = "draft cleared"
		return nil
	case "f4":
		m.openSessionPicker()
		return nil
	case "ctrl+d":
		m.requestDetach()
		if m.quitting {
			return tea.Quit
		}
		return nil
	case "ctrl+q":
		m.requestCoreShutdown()
		return nil
	case "f1":
		if m.activeRun != nil {
			m.requestInputChainCancel(m.activeRun)
			m.status = "cancelling run input chain · waiting for Core to settle"
		} else {
			if m.chainCancel != nil && !m.chainCancelSettled {
				m.requestInputChainCancel(nil)
				m.status = "retrying run input chain cancellation"
			} else {
				m.status = "no active run to cancel"
			}
		}
		return nil
	case "f2":
		m.submitRuntimeInput("follow-up")
		return nil
	case "f3":
		m.submitRuntimeInput("steering")
		return nil
	}
	if m.connectionPicker != nil {
		return m.handleConnectionPickerKey(msg)
	}
	if m.handleCommandMenuKey(msg) {
		if m.quitting {
			return tea.Quit
		}
		return nil
	}
	switch msg.String() {
	case "esc":
		if m.pickerOpen {
			m.closeSessionPicker()
			m.status = "ready"
		}
		return nil
	case "tab":
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
	case "home", "end":
		if m.pickerOpen {
			if msg.String() == "home" {
				m.pickerIndex = 0
			} else {
				m.pickerIndex = len(m.sessions) - 1
			}
			m.refreshViewport(false)
			return nil
		}
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		return cmd
	case "pgup", "pgdown", "alt+up", "alt+down":
		if !m.pickerOpen {
			return nil
		}
		delta := max(1, m.viewport.Height()/4)
		if msg.String() == "pgup" || msg.String() == "alt+up" {
			delta = -delta
		}
		m.movePicker(delta)
		return nil
	default:
		before := m.textarea.Value()
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		if m.textarea.Value() != before {
			m.noteDraftEdit()
		}
		return cmd
	}
}

func (m *model) submitDraft() {
	draft := m.textarea.Value()
	if strings.TrimSpace(draft) == "" {
		return
	}
	if isSlashCommand(strings.TrimSpace(draft)) {
		m.clearDraft()
		m.handleCommand(strings.TrimSpace(draft))
		return
	}
	if m.activeRun != nil {
		m.status = "run active · draft kept; wait for it to finish before submitting"
		return
	}
	if m.shutdownPending || m.detachPending {
		m.status = "waiting to disconnect · draft kept"
		return
	}
	if m.sessionLoading || m.resumePending {
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
	for _, command := range slashCommands {
		if fields[0] == command.name {
			return true
		}
	}
	return false
}

func (m *model) startRun(task string) {
	// The transcript source puts command entries after conversation entities.
	// Seal the current frame before adding a new run so prior visible commands
	// cannot be reordered behind the run's streaming body on the next rebuild.
	m.sealCurrentFrame()
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
		startedAt: time.Now(),
		jobID:     job.id, coreID: m.core.CoreID, id: uuid.NewString(), session: m.sessionID, model: modelID,
		provider: providerID, effort: effort, authProfile: authProfile,
		workspace: m.workspace, entities: make(map[string]api.ConversationEntity),
	}
	m.activeRun = run
	m.clearDraft()
	m.status = "starting run · F1 cancels and waits for Core"
	request := api.StartRunRequest{
		CoreID: m.core.CoreID, RunID: run.id, Task: task,
		SessionID: run.session, ModelID: run.model, ProviderID: providerID,
		Effort: effort, AuthProfile: authProfile, Workspace: run.workspace,
		Role: roleID, MaxSteps: m.maxSteps, FromTurn: m.fromTurn,
	}
	run.handle = m.startRunAfterSelectionSave(request, func(event apiclient.RunEvent) {
		m.sendMessage(runProgressMsg{jobID: job.id, value: event})
	})
	m.launch(job, func() tea.Msg {
		result, err := run.handle.Wait()
		return runDoneMsg{jobID: job.id, value: result, err: err}
	})
}

func (m *model) acceptRunEvent(event apiclient.RunEvent) tea.Cmd {
	run := m.activeRun
	if run == nil {
		return nil
	}
	state := event.State
	if state.CoreID != "" && state.CoreID != run.coreID || state.RunID != run.id {
		return nil
	}
	if state.Seq >= run.lastSeq {
		if startedAt, err := time.Parse(time.RFC3339Nano, state.StartedAt); err == nil {
			run.startedAt = startedAt
		}
		run.lastSeq = state.Seq
		if state.Phase != "" {
			run.phase = state.Phase
		}
		run.inputRevision = state.InputRevision
		run.inputAcceptance = state.InputAcceptance
		run.nextRunID = state.NextRunID
		if state.ActualRole != "" {
			run.actualRole = state.ActualRole
		}
		if state.ActualSelection != nil {
			m.lastSelection = *state.ActualSelection
			run.actualSelection = *state.ActualSelection
			run.hasActualSelection = true
		}
		if state.ActualContext != nil {
			actual := state.ActualContext.Clone()
			run.actualContext = &actual
			m.adoptActualOrientation(actual)
		}
	}
	if state.SessionID != "" {
		firstAdmission := !run.admitted
		changedSession := m.sessionID != state.SessionID
		if changedSession && m.sessionID != "" {
			m.clearRuntimeInputs()
			m.runtimeReadPending = false
			m.runtimeReadJobID = 0
		}
		m.sessionID = state.SessionID
		run.session = state.SessionID
		run.admitted = true
		if firstAdmission || changedSession {
			m.nextSelectionChanged()
			m.loadRuntimeInputs(state.SessionID, true)
		}
		if changedSession && m.deferTranscriptUntilHeader && !m.headerPrinted {
			m.requestHeaderOrientation()
		}
	}
	m.releaseAttachedOrientationWait(run)
	m.noteRuntimeInputRevision(run.coreID, run.session, run.inputRevision)
	if run.actualRole != "" {
		m.lastRunRole = run.actualRole
	}
	conversationApplied := false
	switch event.Kind {
	case "run.snapshot":
		if event.Conversation != nil {
			snapshot := event.Conversation
			if !run.gotSnapshot || snapshot.ViewID != run.viewID {
				run.entities = cloneEntities(snapshot.Entities)
				run.scopeID, run.viewID, run.revision = snapshot.ScopeID, snapshot.ViewID, snapshot.Revision
				run.gotSnapshot = true
				conversationApplied = true
			} else if snapshot.ScopeID == run.scopeID && snapshot.Revision >= run.revision {
				run.entities = cloneEntities(snapshot.Entities)
				run.revision = snapshot.Revision
				conversationApplied = true
			}
		}
	case "run.update":
		if update := event.Update; update != nil && run.gotSnapshot &&
			update.ScopeID == run.scopeID && update.ViewID == run.viewID &&
			update.BaseRevision == run.revision && update.Revision > run.revision {
			for _, entity := range update.Upserts {
				run.entities[entity.ID] = entity
			}
			run.revision = update.Revision
			conversationApplied = true
		}
	}
	if conversationApplied {
		m.invalidateTranscript()
	}
	if event.Kind == "run.snapshot" && conversationApplied {
		if !run.initialSnapshotSeen {
			run.initialSnapshotSeen = true
			run.initialWasTerminal = state.Phase == "terminal"
		}
		if state.Phase == "terminal" {
			run.terminalSnapshotCount++
		}
		requiredTerminalSnapshots := 1
		if run.initialWasTerminal {
			requiredTerminalSnapshots = 2
		}
		run.terminalSnapshotSeen = run.terminalSnapshotCount >= requiredTerminalSnapshots
	}
	if event.Err != nil {
		m.status = "connection interrupted · reconnecting to run " + run.id
	} else if state.Phase != "" {
		m.status = "run " + state.Phase
	}
	if event.Kind == "run.terminal" && state.Phase == "terminal" {
		run.terminalEventSeen = true
	}
	if m.detachPending && run.admitted {
		m.finishDetach()
		return tea.Quit
	}
	if run.terminalSnapshotSeen && run.terminalEventSeen && run.pendingDone != nil {
		return m.finishRun(*run.pendingDone)
	}
	return nil
}

func (m *model) acceptRunDone(msg runDoneMsg) tea.Cmd {
	run := m.activeRun
	if run == nil || msg.jobID != run.jobID {
		return nil
	}
	coreID, sessionID := msg.value.CoreID, msg.value.SessionID
	if coreID == "" {
		coreID = run.coreID
	}
	if sessionID == "" {
		sessionID = run.session
	}
	if msg.value.ActualContext != nil {
		actual := msg.value.ActualContext.Clone()
		run.actualContext = &actual
		m.adoptActualOrientation(actual)
	}
	m.noteRuntimeInputRevision(coreID, sessionID, msg.value.InputRevision)
	if msg.err == nil && (!run.terminalSnapshotSeen || !run.terminalEventSeen) {
		copyOf := msg
		run.pendingDone = &copyOf
		m.status = "run ended · waiting for final conversation snapshot"
		return nil
	}
	return m.finishRun(msg)
}

func (m *model) finishRun(msg runDoneMsg) tea.Cmd {
	run := m.activeRun
	if run == nil || msg.jobID != run.jobID {
		return nil
	}
	m.cancelJob(msg.jobID)
	run.phase = "terminal"
	if msg.value.SessionID != "" {
		changedSession := m.sessionID != msg.value.SessionID
		m.sessionID = msg.value.SessionID
		if changedSession && m.deferTranscriptUntilHeader && !m.headerPrinted {
			m.requestHeaderOrientation()
		}
	}
	for id, entity := range run.entities {
		m.sessionEntities[id] = entity
	}
	m.invalidateTranscript()
	if msg.err != nil {
		m.appendEntry("run ended", msg.err.Error())
		m.status = "run failed or stopped · draft remains available"
	} else if msg.value.Result != nil && msg.value.Result.OK {
		m.status = "run completed"
	} else if msg.value.Result != nil && msg.value.Result.Error != nil {
		m.appendEntry("run ended", msg.value.Result.Error.Message)
		m.status = "run failed · draft remains available"
	} else if msg.value.Result != nil && msg.value.Result.AgentResult != nil && msg.value.Result.AgentResult.Error != "" {
		m.appendEntry("run ended", msg.value.Result.AgentResult.Error)
		m.status = "run failed · draft remains available"
	} else if msg.value.Result != nil && msg.value.Result.AgentResult != nil {
		m.status = "run ended · " + msg.value.Result.AgentResult.Outcome
	} else {
		m.status = "run ended"
	}
	coreID, sessionID, nextRunID := run.coreID, run.session, msg.value.NextRunID
	if nextRunID == "" {
		nextRunID = run.nextRunID
	}
	shouldAttachNext := nextRunID != "" && msg.err == nil && msg.value.Result != nil && msg.value.Result.OK &&
		!run.chainCancelRequested && !m.detachPending && !m.shutdownPending && m.pendingSession == nil
	run.handle = nil
	m.activeRun = nil
	m.nextSelectionChanged()
	if m.detachPending {
		m.finishDetach()
		return tea.Quit
	}
	if m.pendingSession != nil {
		m.applyPendingSessionIfSettled()
		return nil
	}
	if shouldAttachNext {
		m.attachRun(api.RunState{CoreID: coreID, RunID: nextRunID, SessionID: sessionID, Phase: "preparing"})
		m.status = "following up · attached to Core-started run " + nextRunID
	} else if run.attached && run.actualContext == nil {
		m.releaseAttachedOrientationWait(run)
	}
	return nil
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
		if message != nil {
			m.sendMessage(message)
		}
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
	if run := m.activeRun; !m.detached && run != nil && run.handle != nil {
		_ = run.handle.Cancel()
		_, _ = run.handle.Wait()
	}
	if m.authUI != nil {
		m.cancelAuthAttempt(m.authUI)
	}
	if m.cancelRoot != nil {
		m.cancelRoot()
	}
	m.jobMu.Lock()
	jobs := append([]*uiJob(nil), m.allJobs...)
	m.jobMu.Unlock()
	for _, job := range jobs {
		if !job.closeBeforeBackend {
			job.cancel()
		}
	}
	for _, job := range jobs {
		if job.closeBeforeBackend {
			<-job.done
		}
	}
	if closer, ok := m.backend.(interface{ Close() error }); ok {
		_ = closer.Close()
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
	m.textarea.SetWidth(m.width)
	m.viewport.SetWidth(m.width)
	viewportHeight := max(1, m.height-4-m.textarea.Height())
	m.viewport.SetHeight(viewportHeight)
	m.refreshViewport(true)
}

func (m *model) refreshViewport(gotoBottom bool) {
	if !m.pickerOpen {
		return
	}
	var rows []string
	var starts []int
	selected := m.pickerIndex
	if m.authUI != nil && m.authUI.open {
		rows, starts, selected, _, _ = m.authRows()
	} else if m.connectionPicker != nil {
		rows, starts = m.connectionPickerRows()
		selected = m.connectionPicker.index
	} else {
		rows, starts = sessionPickerRows(m.sessions, m.pickerIndex, m.width)
	}
	m.viewport.SoftWrap = false
	m.viewport.SetContent(strings.Join(rows, "\n"))
	if len(starts) > 0 && selected >= 0 && selected < len(starts) {
		start := starts[selected]
		end := len(rows)
		if selected+1 < len(starts) {
			end = starts[selected+1] - 1 // exclude the blank separator before the next option
		}
		viewHeight := max(1, m.viewport.Height())
		offset := m.viewport.YOffset()
		if end-start <= viewHeight {
			if start < offset {
				offset = start
			} else if end > offset+viewHeight {
				offset = end - viewHeight
			}
		} else if start < offset || start >= offset+viewHeight {
			offset = min(start, max(0, len(rows)-viewHeight))
		}
		m.viewport.SetYOffset(offset)
	}
}

func (m *model) appendEntry(role, text string) {
	m.entries = append(m.entries, displayEntry{role: role, text: text})
	m.invalidateTranscript()
	m.refreshViewport(true)
}

func (m *model) movePicker(delta int) {
	if !m.pickerOpen {
		return
	}
	if m.connectionPicker != nil {
		m.moveConnectionPicker(delta)
		return
	}
	if len(m.sessions) == 0 {
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
		m.closeSessionPicker()
		m.status = "no Session selected"
		return
	}
	id := m.sessions[m.pickerIndex].ID
	m.closeSessionPicker()
	m.changeSession(sessionAction{resumeID: id})
}

func (m *model) View() tea.View {
	footer := m.footerRows()
	printHeader := m.openingHeaderReady()
	frame, cursor, content, committedThrough, bodyStart, bodyRows := m.screenFrame(footer)
	frame.Overlay = m.pickerOpen
	if m.pickerOpen {
		printHeader = false
	} else {
		frame.SealedRows = append(frame.SealedRows, m.pendingSealRows...)
	}
	if printHeader {
		frame.SealedRows = append(frame.SealedRows, m.headerRows()...)
	}
	if m.writer != nil {
		if err := m.writer.Render(frame); err != nil {
			if !errors.Is(err, renderer.ErrAwaitingCursor) {
				m.renderErr = errors.Join(m.renderErr, err)
			}
		} else {
			if printHeader {
				m.headerPrinted = true
				m.deferTranscriptUntilHeader = false
			}
			doc := m.ensureTranscript()
			if !m.pickerOpen {
				if committedThrough > doc.startRow {
					m.receiptByPart = receiptsThroughRow(doc, m.receiptByPart, committedThrough)
					m.receiptOffset = max(m.receiptOffset, receiptOffset(doc.parts, m.receiptByPart))
				}
				doc.startRow = committedThrough
				m.lastBodyRows = append(m.lastBodyRows[:0], bodyRows...)
				m.lastBodyStartRow = bodyStart
			} else {
				m.lastBodyRows = nil
				m.lastBodyStartRow = doc.startRow
			}
			m.lastTranscriptBytes = doc.sourceLength
			if !m.pickerOpen {
				m.pendingSealRows = nil
			}
		}
	}
	view := tea.NewView(content)
	view.AltScreen = false
	view.Cursor = cursor
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

func (m *model) loadSession(id string, generation uint64) {
	m.sessionLoading = true
	m.status = "loading Session " + m.shortSessionID(id)
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.launch(job, func() tea.Msg {
		session, err := m.backend.ResolveSession(ctx, id)
		if err != nil {
			return sessionLoadedMsg{jobID: job.id, generation: generation, err: err}
		}
		run, err := m.backend.ActiveSessionRun(ctx, m.core.CoreID, session.ID)
		if err != nil {
			return sessionLoadedMsg{jobID: job.id, generation: generation, err: err}
		}
		view, err := m.backend.SessionView(ctx, session.ID)
		return sessionLoadedMsg{jobID: job.id, generation: generation, session: session, view: view, run: run, err: err}
	})
}

var _ tea.Model = (*model)(nil)
