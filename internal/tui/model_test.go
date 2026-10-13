package tui

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
	"hjh/internal/sessionid"
	"hjh/internal/tui/renderer"
)

type runCall struct {
	request  api.StartRunRequest
	observer func(apiclient.RunEvent)
	handle   *fakeRun
}

type fakeRun struct {
	id string

	mu          sync.Mutex
	cancelCalls int
	cancelErr   error
	state       api.RunState
	err         error
	done        chan struct{}
	once        sync.Once
}

func (r *fakeRun) RunID() string { return r.id }
func (r *fakeRun) Cancel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelCalls++
	return r.cancelErr
}
func (r *fakeRun) Wait() (api.RunState, error) {
	<-r.done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state, r.err
}
func (r *fakeRun) finish(state api.RunState, err error) {
	r.once.Do(func() {
		r.mu.Lock()
		r.state, r.err = state, err
		r.mu.Unlock()
		close(r.done)
	})
}

type fakeBackend struct {
	core             api.Core
	activeSessionRun *api.RunState
	attachCalls      chan runCall
	shutdownCalls    chan string
	shutdownDone     <-chan struct{}
	closeHandles     []*fakeRun
	closeFunc        func() error

	runCalls chan runCall
	roles    api.RoleCatalog
	sessions []api.Session
	session  api.Session
	view     api.ConversationSnapshot
	messages []api.Message
	inspect  api.Inspection

	selection                api.SelectionPreview
	selectionFunc            func(context.Context, api.SelectionRequest) (api.SelectionPreview, error)
	selectionCalls           chan api.SelectionRequest
	rememberSelectionFunc    func(context.Context, api.RememberSelectionRequest) (api.SelectionPreview, error)
	orientation              api.Orientation
	orientationFunc          func(context.Context, api.OrientationRequest) (api.Orientation, error)
	orientationCalls         chan api.OrientationRequest
	providers                []api.ProviderDefinition
	models                   []api.ModelInfo
	modelsCalls              chan modelsCall
	authProfiles             api.AuthProfiles
	authProfilesFunc         func(context.Context, api.SelectionRequest) (api.AuthProfiles, error)
	authProfilesCalls        chan api.SelectionRequest
	authAccounts             api.AuthAccounts
	authAccountsFunc         func(context.Context, apiclient.Scope, string, string) (api.AuthAccounts, error)
	authAccountsCalls        chan authAccountsCall
	registerAPIKeyFunc       func(context.Context, api.AuthRequest) (api.APIKeyState, error)
	registerAPIKeyCalls      chan api.AuthRequest
	selectAuthAccountFunc    func(context.Context, api.AuthRequest) (api.AuthAccounts, error)
	selectAuthAccountCalls   chan api.AuthRequest
	beginAuthAttemptFunc     func(context.Context, api.AuthRequest) (api.AuthAttempt, error)
	beginAuthAttemptCalls    chan api.AuthRequest
	authAttemptFunc          func(context.Context, string, string) (api.AuthAttempt, error)
	authAttemptCalls         chan authAttemptCall
	completeAuthAttemptFunc  func(context.Context, api.AuthRequest) (api.AuthAttempt, error)
	completeAuthAttemptCalls chan api.AuthRequest
	cancelAuthAttemptFunc    func(context.Context, api.AuthRequest) (api.AuthAttempt, error)
	cancelAuthAttemptCalls   chan api.AuthRequest
	deleteAuthAttemptFunc    func(context.Context, string, string) error
	deleteAuthAttemptCalls   chan authAttemptCall
	favoritesFunc            func(context.Context, apiclient.Scope, string) (api.ModelFavorites, error)
	favoritesCalls           chan modelsCall
	setFavoriteFunc          func(context.Context, api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error)
	setFavoriteCalls         chan api.SetModelFavoriteRequest

	queryCalls chan [4]any

	submitInputCalls chan runtimeInputCall
	submitInputFunc  func(context.Context, string, api.SubmitInputRequest) (api.InputReceipt, error)
	inputCalls       chan runtimeInputCall
	inputFunc        func(context.Context, string, string, string, bool) (api.InputReceipt, error)
	inputsCalls      chan runtimeInputsCall
	inputsFunc       func(context.Context, string, string, uint64, bool) (api.InputReceiptPage, error)
	cancelChainCalls chan inputChainCall
	cancelChainFunc  func(context.Context, string, string) (api.InputChainState, error)
	inputChainCalls  chan inputChainCall
	inputChainFunc   func(context.Context, string, string) (api.InputChainState, error)
}

type runtimeInputCall struct {
	coreID      string
	runID       string
	inputID     string
	request     api.SubmitInputRequest
	includeText bool
}

type runtimeInputsCall struct {
	sessionID     string
	coreID        string
	afterRevision uint64
	includeText   bool
}

type inputChainCall struct {
	coreID string
	runID  string
}

type modelsCall struct {
	scope       apiclient.Scope
	providerID  string
	authProfile string
}

type authAccountsCall struct {
	scope       apiclient.Scope
	providerID  string
	authProfile string
}

type authAttemptCall struct {
	coreID    string
	attemptID string
}

func (f *fakeBackend) Core(context.Context) (api.Core, error) { return f.core, nil }
func (f *fakeBackend) StartRun(request api.StartRunRequest, observer func(apiclient.RunEvent)) RunHandle {
	handle := &fakeRun{id: request.RunID, done: make(chan struct{})}
	f.runCalls <- runCall{request: request, observer: observer, handle: handle}
	return handle
}
func (f *fakeBackend) AttachRun(coreID, runID string, observer func(apiclient.RunEvent)) RunHandle {
	handle := &fakeRun{id: runID, done: make(chan struct{})}
	f.attachCalls <- runCall{request: api.StartRunRequest{CoreID: coreID, RunID: runID}, observer: observer, handle: handle}
	return handle
}
func (f *fakeBackend) ActiveSessionRun(context.Context, string, string) (*api.RunState, error) {
	return f.activeSessionRun, nil
}
func (f *fakeBackend) SubmitInput(ctx context.Context, runID string, request api.SubmitInputRequest) (api.InputReceipt, error) {
	if f.submitInputCalls != nil {
		f.submitInputCalls <- runtimeInputCall{coreID: request.CoreID, runID: runID, inputID: request.InputID, request: request}
	}
	if f.submitInputFunc != nil {
		return f.submitInputFunc(ctx, runID, request)
	}
	status := "accepted"
	if request.Kind == "follow-up" {
		status = "queued"
	}
	return api.InputReceipt{CoreID: request.CoreID, SessionID: request.SessionID, RunID: runID, InputID: request.InputID, Kind: request.Kind, Status: status}, nil
}
func (f *fakeBackend) Input(ctx context.Context, coreID, runID, inputID string, includeText bool) (api.InputReceipt, error) {
	if f.inputCalls != nil {
		f.inputCalls <- runtimeInputCall{coreID: coreID, runID: runID, inputID: inputID, includeText: includeText}
	}
	if f.inputFunc != nil {
		return f.inputFunc(ctx, coreID, runID, inputID, includeText)
	}
	return api.InputReceipt{}, &api.Error{Code: "input_not_found", Message: "input not found"}
}
func (f *fakeBackend) Inputs(ctx context.Context, sessionID, coreID string, afterRevision uint64, includeText bool) (api.InputReceiptPage, error) {
	if f.inputsCalls != nil {
		f.inputsCalls <- runtimeInputsCall{sessionID: sessionID, coreID: coreID, afterRevision: afterRevision, includeText: includeText}
	}
	if f.inputsFunc != nil {
		return f.inputsFunc(ctx, sessionID, coreID, afterRevision, includeText)
	}
	return api.InputReceiptPage{CoreID: coreID, SessionID: sessionID, Revision: afterRevision}, nil
}
func (f *fakeBackend) CancelInputChain(ctx context.Context, coreID, runID string) (api.InputChainState, error) {
	if f.cancelChainCalls != nil {
		f.cancelChainCalls <- inputChainCall{coreID: coreID, runID: runID}
	}
	if f.cancelChainFunc != nil {
		return f.cancelChainFunc(ctx, coreID, runID)
	}
	return api.InputChainState{CoreID: coreID, AnchorRunID: runID, Phase: "settled"}, nil
}
func (f *fakeBackend) InputChain(ctx context.Context, coreID, runID string) (api.InputChainState, error) {
	if f.inputChainCalls != nil {
		f.inputChainCalls <- inputChainCall{coreID: coreID, runID: runID}
	}
	if f.inputChainFunc != nil {
		return f.inputChainFunc(ctx, coreID, runID)
	}
	return api.InputChainState{CoreID: coreID, AnchorRunID: runID, Phase: "settled"}, nil
}
func (f *fakeBackend) RenameSession(_ context.Context, id, title string) (api.Session, error) {
	return api.Session{ID: id, Title: &title}, nil
}
func (f *fakeBackend) ShutdownCore(ctx context.Context, coreID string) error {
	if f.shutdownCalls != nil {
		f.shutdownCalls <- coreID
	}
	if f.shutdownDone != nil {
		select {
		case <-f.shutdownDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
func (f *fakeBackend) Close() error {
	for _, handle := range f.closeHandles {
		handle.finish(api.RunState{}, context.Canceled)
	}
	if f.closeFunc != nil {
		return f.closeFunc()
	}
	return nil
}
func (f *fakeBackend) Roles(context.Context, apiclient.Scope) (api.RoleCatalog, error) {
	return f.roles, nil
}
func (f *fakeBackend) Sessions(context.Context) ([]api.Session, error) { return f.sessions, nil }
func (f *fakeBackend) ResolveSession(ctx context.Context, selector string) (api.Session, error) {
	ids := make([]string, 0, len(f.sessions)+1)
	if f.session.ID != "" {
		ids = append(ids, f.session.ID)
	}
	for _, session := range f.sessions {
		if session.ID != f.session.ID {
			ids = append(ids, session.ID)
		}
	}
	id, err := sessionid.Resolve(selector, ids)
	if err != nil {
		return api.Session{}, err
	}
	return f.Session(ctx, id)
}
func (f *fakeBackend) Session(_ context.Context, id string) (api.Session, error) {
	if f.session.ID == id {
		return f.session, nil
	}
	return api.Session{}, context.Canceled
}
func (f *fakeBackend) SessionView(_ context.Context, id string) (api.ConversationSnapshot, error) {
	if f.queryCalls != nil {
		f.queryCalls <- [4]any{"session-view", id, nil, nil}
	}
	return f.view, nil
}
func (f *fakeBackend) Conversation(_ context.Context, id string, from, to int64) ([]api.Message, error) {
	if f.queryCalls != nil {
		f.queryCalls <- [4]any{"conversation", id, from, to}
	}
	return f.messages, nil
}
func (f *fakeBackend) Executions(_ context.Context, id string) ([]api.Execution, error) {
	if f.queryCalls != nil {
		f.queryCalls <- [4]any{"executions", id, nil, nil}
	}
	return nil, nil
}
func (f *fakeBackend) Inspect(context.Context, string, int64, int64) (api.Inspection, error) {
	return f.inspect, nil
}
func (f *fakeBackend) Record(_ context.Context, executionID string, ordinal int64) (api.Record, error) {
	for _, record := range f.inspect.Records {
		if record.ExecutionID == executionID && record.Ordinal == ordinal {
			return record, nil
		}
	}
	return api.Record{}, context.Canceled
}
func (f *fakeBackend) Orientation(ctx context.Context, request api.OrientationRequest) (api.Orientation, error) {
	if f.orientationCalls != nil {
		f.orientationCalls <- request
	}
	if f.orientationFunc != nil {
		return f.orientationFunc(ctx, request)
	}
	value := f.orientation
	if value.Mode == "" {
		value.Mode = "preview"
	}
	if value.Workspace == "" {
		value.Workspace = request.Workspace
	}
	if value.Role == "" {
		value.Role = request.Role
	}
	return value, nil
}
func (f *fakeBackend) Selection(ctx context.Context, request api.SelectionRequest) (api.SelectionPreview, error) {
	if f.selectionCalls != nil {
		f.selectionCalls <- request
	}
	if f.selectionFunc != nil {
		return f.selectionFunc(ctx, request)
	}
	preview := f.selection
	if preview.Role == "" {
		preview.Role = request.Role
	}
	if preview.Selection.Provider == "" {
		preview.Selection.Provider = request.ProviderID
	}
	if preview.Selection.ModelID == "" && request.ModelID != "" {
		preview.Selection.ModelID = request.ModelID
	}
	if preview.Selection.Effort == "" {
		preview.Selection.Effort = request.Effort
	}
	if preview.Selection.AuthProfile == "" {
		preview.Selection.AuthProfile = request.AuthProfile
	}
	return preview, nil
}
func (f *fakeBackend) RememberSelection(ctx context.Context, request api.RememberSelectionRequest) (api.SelectionPreview, error) {
	if f.rememberSelectionFunc != nil {
		return f.rememberSelectionFunc(ctx, request)
	}
	return api.SelectionPreview{}, nil
}
func (f *fakeBackend) Providers(context.Context, apiclient.Scope) ([]api.ProviderDefinition, error) {
	return f.providers, nil
}
func (f *fakeBackend) Models(_ context.Context, scope apiclient.Scope, providerID, authProfile string) ([]api.ModelInfo, error) {
	if f.modelsCalls != nil {
		f.modelsCalls <- modelsCall{scope: scope, providerID: providerID, authProfile: authProfile}
	}
	return f.models, nil
}
func (f *fakeBackend) ModelsWithAuth(ctx context.Context, scope apiclient.Scope, providerID, authProfile string) (api.ModelsResult, error) {
	models, err := f.Models(ctx, scope, providerID, authProfile)
	return api.ModelsResult{Models: models, AuthOwnerID: "auth-owner", AuthAccountID: "auth-account"}, err
}
func (f *fakeBackend) ModelFavorites(ctx context.Context, scope apiclient.Scope, providerID string) (api.ModelFavorites, error) {
	if f.favoritesCalls != nil {
		f.favoritesCalls <- modelsCall{scope: scope, providerID: providerID}
	}
	if f.favoritesFunc != nil {
		return f.favoritesFunc(ctx, scope, providerID)
	}
	return api.ModelFavorites{ProviderID: providerID}, nil
}
func (f *fakeBackend) SetModelFavorite(ctx context.Context, request api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error) {
	if f.setFavoriteCalls != nil {
		f.setFavoriteCalls <- request
	}
	if f.setFavoriteFunc != nil {
		return f.setFavoriteFunc(ctx, request)
	}
	result := api.ModelFavoriteResult{ModelFavorites: api.ModelFavorites{ProviderID: request.ProviderID}, ModelID: request.ModelID}
	if request.Favorite != nil {
		result.Favorite = *request.Favorite
	}
	return result, nil
}
func (f *fakeBackend) AuthProfiles(ctx context.Context, request api.SelectionRequest) (api.AuthProfiles, error) {
	if f.authProfilesCalls != nil {
		f.authProfilesCalls <- request
	}
	if f.authProfilesFunc != nil {
		return f.authProfilesFunc(ctx, request)
	}
	return f.authProfiles, nil
}
func (f *fakeBackend) AuthAccounts(ctx context.Context, scope apiclient.Scope, providerID, authProfile string) (api.AuthAccounts, error) {
	if f.authAccountsCalls != nil {
		f.authAccountsCalls <- authAccountsCall{scope: scope, providerID: providerID, authProfile: authProfile}
	}
	if f.authAccountsFunc != nil {
		return f.authAccountsFunc(ctx, scope, providerID, authProfile)
	}
	return f.authAccounts, nil
}
func (f *fakeBackend) RegisterAPIKey(ctx context.Context, request api.AuthRequest) (api.APIKeyState, error) {
	if f.registerAPIKeyCalls != nil {
		f.registerAPIKeyCalls <- request
	}
	if f.registerAPIKeyFunc != nil {
		return f.registerAPIKeyFunc(ctx, request)
	}
	return api.APIKeyState{Registered: true, Source: "registered", Revision: 1, MutationID: request.MutationID}, nil
}
func (f *fakeBackend) SelectAuthAccount(ctx context.Context, request api.AuthRequest) (api.AuthAccounts, error) {
	if f.selectAuthAccountCalls != nil {
		f.selectAuthAccountCalls <- request
	}
	if f.selectAuthAccountFunc != nil {
		return f.selectAuthAccountFunc(ctx, request)
	}
	return f.authAccounts, nil
}
func (f *fakeBackend) BeginAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	if f.beginAuthAttemptCalls != nil {
		f.beginAuthAttemptCalls <- request
	}
	if f.beginAuthAttemptFunc != nil {
		return f.beginAuthAttemptFunc(ctx, request)
	}
	return api.AuthAttempt{CoreID: request.CoreID, AttemptID: request.AttemptID, ProviderID: request.ProviderID, AuthProfile: request.AuthProfile, AccountID: request.AccountID, Phase: "ready", AuthorizationURL: "https://auth.example/authorize"}, nil
}
func (f *fakeBackend) AuthAttempt(ctx context.Context, coreID, attemptID string) (api.AuthAttempt, error) {
	if f.authAttemptCalls != nil {
		f.authAttemptCalls <- authAttemptCall{coreID: coreID, attemptID: attemptID}
	}
	if f.authAttemptFunc != nil {
		return f.authAttemptFunc(ctx, coreID, attemptID)
	}
	return api.AuthAttempt{CoreID: coreID, AttemptID: attemptID, Phase: "ready", AuthorizationURL: "https://auth.example/authorize"}, nil
}
func (f *fakeBackend) CompleteAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	if f.completeAuthAttemptCalls != nil {
		f.completeAuthAttemptCalls <- request
	}
	if f.completeAuthAttemptFunc != nil {
		return f.completeAuthAttemptFunc(ctx, request)
	}
	return api.AuthAttempt{CoreID: request.CoreID, AttemptID: request.AttemptID, Phase: "exchanging"}, nil
}
func (f *fakeBackend) CancelAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	if f.cancelAuthAttemptCalls != nil {
		f.cancelAuthAttemptCalls <- request
	}
	if f.cancelAuthAttemptFunc != nil {
		return f.cancelAuthAttemptFunc(ctx, request)
	}
	return api.AuthAttempt{CoreID: request.CoreID, AttemptID: request.AttemptID, Phase: "cancelled"}, nil
}
func (f *fakeBackend) DeleteAuthAttempt(ctx context.Context, coreID, attemptID string) error {
	if f.deleteAuthAttemptCalls != nil {
		f.deleteAuthAttemptCalls <- authAttemptCall{coreID: coreID, attemptID: attemptID}
	}
	if f.deleteAuthAttemptFunc != nil {
		return f.deleteAuthAttemptFunc(ctx, coreID, attemptID)
	}
	return nil
}

func testModel(backend Backend, options Options) (*model, <-chan tea.Msg, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	messages := make(chan tea.Msg, 32)
	m := newModel(ctx, cancel, backend, options)
	m.send = func(msg tea.Msg) { messages <- msg }
	return m, messages, cancel
}

func attachWriter(m *model) *bytes.Buffer {
	var output bytes.Buffer
	m.writer = renderer.New(&output)
	if err := m.writer.Start(); err != nil {
		panic(err)
	}
	return &output
}

func receive(t *testing.T, messages <-chan tea.Msg) tea.Msg {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for UI message")
		return nil
	}
}

// Async presentation reads can settle independently of the operation under test.
func receiveUntil(t *testing.T, m *model, messages <-chan tea.Msg, matches func(tea.Msg) bool) tea.Msg {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case message := <-messages:
			if matches(message) {
				return message
			}
			update(m, message)
		case <-timeout:
			t.Fatal("timed out waiting for requested UI message")
			return nil
		}
	}
}

func update(m *model, message tea.Msg) tea.Cmd {
	_, cmd := m.Update(message)
	return cmd
}

func conversationEntity(id, kind, text string, ordinal int64) api.ConversationEntity {
	return api.ConversationEntity{
		ID: id, Kind: kind, ExecutionID: "root-1",
		Position: api.ConversationPosition{RootExecutionOrdinal: 1, FirstSequence: uint64(ordinal), ItemIndex: 0},
		Text:     text, Status: "streaming",
	}
}

func deliverRunEvent(t *testing.T, m *model, messages <-chan tea.Msg, call runCall, event apiclient.RunEvent) tea.Cmd {
	t.Helper()
	call.observer(event)
	for {
		message := receive(t, messages)
		if progress, ok := message.(runProgressMsg); ok && progress.value.State.RunID == event.State.RunID {
			return update(m, message)
		}
		update(m, message)
	}
}

func TestConversationRevisionOrderMergeAndTerminalSnapshot(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1", Workspace: "/core/work"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/core/work", ModelID: "test/model", Role: "reviewer"})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()

	m.sessionEntities["assistant-stable"] = conversationEntity("assistant-stable", "assistant", "old answer", 2)
	m.textarea.SetValue("日本語の依頼\n2行目: ファイルを確認してください")
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	if call.request.Task != "日本語の依頼\n2行目: ファイルを確認してください" || call.request.RunID == "" {
		t.Fatalf("start request = %+v", call.request)
	}
	if call.request.CoreID != "core-1" || call.request.Workspace != "/core/work" || call.request.ModelID != "test/model" || call.request.Role != "reviewer" {
		t.Fatalf("selection was not sent through the API: %+v", call.request)
	}

	entities := map[string]api.ConversationEntity{
		"task":             conversationEntity("task", "task", "日本語の依頼", 1),
		"assistant-stable": conversationEntity("assistant-stable", "assistant", "**調査中**", 2),
		"thinking":         conversationEntity("thinking", "thinking", "公開thinking", 3),
		"tool": func() api.ConversationEntity {
			entity := conversationEntity("tool", "tool", "file details", 4)
			entity.Name = "read_file"
			entity.Preview = "path=README.md"
			return entity
		}(),
		"child": {
			ID: "child", Kind: "execution", ExecutionID: "child-1", ParentExecutionID: "root-1",
			Position: api.ConversationPosition{RootExecutionOrdinal: 1, FirstSequence: 5}, Text: "child started", Status: "streaming",
		},
		"child-assistant": {
			ID: "child-assistant", Kind: "assistant", ExecutionID: "child-1", ParentExecutionID: "root-1",
			Position: api.ConversationPosition{RootExecutionOrdinal: 1, FirstSequence: 6}, Text: "子Agentの確認", Status: "streaming",
		},
	}
	// Order is deliberately different: the TUI follows the Core's semantic positions.
	snapshot := api.ConversationSnapshot{ScopeID: call.request.RunID, ViewID: "run-view", Revision: 1, Order: []string{"tool", "task"}, Entities: entities}
	state := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "running", SessionID: "session-1", ActualRole: "reviewer"}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: &snapshot})
	transcript := renderer.ReceiptText(m.renderTranscript())
	if strings.Contains(transcript, "old answer") || !strings.Contains(transcript, "調査中") {
		t.Fatalf("run entity did not override matching Session entity: %q", transcript)
	}
	labels := []string{"you:", "assistant:", "thinking:", "tool / ", "child child-1 · started"}
	last := -1
	for _, label := range labels {
		position := strings.Index(transcript, label)
		if position < 0 || position <= last {
			t.Fatalf("Core-position conversation order lost at %q: %q", label, transcript)
		}
		last = position
	}
	if !strings.Contains(transcript, "調査中") || strings.Contains(transcript, "**調査中**") || !strings.Contains(m.renderTranscript(), "\x1b[") {
		t.Fatalf("conversation did not render Markdown with color: %q", transcript)
	}
	if !strings.Contains(transcript, "tool / read_file · path=README.md …") || strings.Contains(transcript, "file details") {
		t.Fatalf("tool conversation was not reduced to its preview: %q", transcript)
	}

	updated := entities["assistant-stable"]
	updated.Text = "**調査が完了しました**"
	state.Seq = 1 // Conversation revisions advance independently of run state sequence.
	conversationUpdate := api.ConversationUpdate{
		ScopeID: call.request.RunID, ViewID: "run-view", BaseRevision: 1, Revision: 2,
		Upserts: []api.ConversationEntity{updated},
	}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.update", State: state, Update: &conversationUpdate})
	transcript = renderer.ReceiptText(m.renderTranscript())
	if !strings.Contains(transcript, "調査が完了しました") || strings.Contains(transcript, "調査中") || m.activeRun.revision != 2 {
		t.Fatalf("same-sequence conversation update was not applied: revision=%d transcript=%q", m.activeRun.revision, transcript)
	}

	terminal := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 2, Phase: "terminal", SessionID: "session-1", Result: &api.RunResult{OK: true, AgentResult: &api.AgentResult{FinalText: "調査が完了しました"}}}
	call.handle.finish(terminal, nil)
	doneMessage := receiveUntil(t, m, messages, func(message tea.Msg) bool { _, ok := message.(runDoneMsg); return ok }).(runDoneMsg)
	update(m, doneMessage)
	if m.activeRun == nil {
		t.Fatal("Wait completed the run before run.terminal supplied its final conversation snapshot")
	}
	finalSnapshot := snapshot
	finalSnapshot.Revision = 3
	finalSnapshot.Entities = cloneEntities(entities)
	finalSnapshot.Entities["assistant-stable"] = updated
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: terminal, Conversation: &finalSnapshot})
	if m.activeRun == nil {
		t.Fatal("the final conversation snapshot settled the run before independent run.terminal")
	}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	if m.activeRun != nil || m.sessionID != "session-1" {
		t.Fatalf("terminal snapshot did not settle the run: active=%v session=%q", m.activeRun != nil, m.sessionID)
	}
	transcript = renderer.ReceiptText(m.renderTranscript())
	if strings.Count(transcript, "調査が完了しました") != 1 || m.View().AltScreen {
		t.Fatalf("final answer duplicated or TUI used alternate screen: %q", transcript)
	}
	if output.Len() == 0 {
		t.Fatal("normal-screen writer did not render the model")
	}
}

func TestJapaneseEditorModifierEnterPasteCompletionAndClear(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()

	for _, mod := range []tea.KeyMod{tea.ModAlt, tea.ModShift, tea.ModCtrl} {
		m.textarea.SetValue("日本語")
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: mod})
		if !strings.Contains(m.textarea.Value(), "\n") {
			t.Fatalf("modifier Enter %v did not insert a newline: %q", mod, m.textarea.Value())
		}
	}
	m.textarea.SetValue("日本語")
	update(m, tea.PasteMsg{Content: "貼り付け\nした文章"})
	if !strings.Contains(m.textarea.Value(), "貼り付け\nした文章") {
		t.Fatalf("bracketed paste did not reach the multiline editor: %q", m.textarea.Value())
	}
	m.clearDraft()
	update(m, tea.PasteMsg{Content: "/he"})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.textarea.Value() != "/help " {
		t.Fatalf("Tab did not complete a supported slash command: %q", m.textarea.Value())
	}
	m.clearDraft()
	update(m, tea.PasteMsg{Content: "/tools"})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.textarea.Value() != "/tools " {
		t.Fatalf("Tab did not complete /tools: %q", m.textarea.Value())
	}
	m.clearDraft()
	update(m, tea.PasteMsg{Content: "/tool"})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.textarea.Value() != "/tool " {
		t.Fatalf("Tab did not prefer exact /tool over /tools: %q", m.textarea.Value())
	}

	m.textarea.SetValue("日本語の依頼\n2行目")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	if call.request.Task != "日本語の依頼\n2行目" {
		t.Fatalf("plain Enter did not submit the multiline draft: %q", call.request.Task)
	}
	m.textarea.SetValue("discard me")
	m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	call.handle.mu.Lock()
	cancelCalls := call.handle.cancelCalls
	call.handle.mu.Unlock()
	if m.textarea.Value() != "" || m.activeRun == nil || cancelCalls != 0 || m.quitting {
		t.Fatalf("Ctrl+C must clear only the draft: draft=%q active=%v cancels=%d quitting=%v", m.textarea.Value(), m.activeRun != nil, cancelCalls, m.quitting)
	}
}

func TestF1ExplicitlyCancelsActiveRun(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), cancelChainCalls: make(chan inputChainCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("stop this run")
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF1})
	if target := <-backend.cancelChainCalls; target.coreID != "core-1" || target.runID != call.request.RunID {
		t.Fatalf("F1 cancelled a different input chain: %+v", target)
	}
	if _, ok := receive(t, messages).(runtimeInputChainSettledMsg); !ok {
		t.Fatal("F1 did not wait for the Core input chain to settle")
	}
	call.handle.mu.Lock()
	cancelCalls := call.handle.cancelCalls
	call.handle.mu.Unlock()
	if cancelCalls != 0 || m.activeRun == nil || m.quitting {
		t.Fatalf("F1 must use chain cancellation and leave the run attached until terminal: calls=%d active=%v quitting=%v", cancelCalls, m.activeRun != nil, m.quitting)
	}
}

func TestResizeKeepsStreamingMarkdownBlockMutableAcrossWidths(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	entity := conversationEntity("long", "assistant", strings.Repeat("Q", 3000), 1)
	m.sessionEntities["long"] = entity
	m.View()
	const bodyKey = "entity:long:body"
	if m.receiptByPart[bodyKey] != 0 || len(m.lastBodyRows) == 0 {
		t.Fatalf("unfinished Markdown block was partially acknowledged: receipt=%d body rows=%d", m.receiptByPart[bodyKey], len(m.lastBodyRows))
	}
	assertStreamingTail := func(width int) {
		t.Helper()
		bodyHeight := max(1, m.height-3-1-1)
		if len(m.lastBodyRows) > bodyHeight {
			t.Fatalf("unfinished body exceeded the live band at width %d: rows=%d height=%d", width, len(m.lastBodyRows), bodyHeight)
		}
		if got := m.markdownSourceCache[bodyKey].source; got != entity.Text {
			t.Fatalf("resize lost canonical pending source at width %d", width)
		}
		if m.receiptByPart[bodyKey] != 0 {
			t.Fatalf("resize advanced the unfinished block receipt at width %d: %d", width, m.receiptByPart[bodyKey])
		}
		visible := strings.Count(renderer.ReceiptText(strings.Join(m.lastBodyRows, "\n")), "Q")
		if visible == 0 {
			t.Fatalf("live tail at width %d omitted the unfinished source", width)
		}
	}
	if got := strings.Count(output.String(), "hjh v"); got != 1 {
		t.Fatalf("initial identity header was emitted %d times, want once", got)
	}
	if got := strings.Count(renderer.ReceiptText(output.String()), "Q"); got == 0 {
		t.Fatal("initial live frame did not render the unfinished source")
	}

	output.Reset()
	update(m, tea.WindowSizeMsg{Width: 44, Height: 24})
	m.View()
	assertStreamingTail(44)
	if got := strings.Count(output.String(), "hjh v"); got != 0 {
		t.Fatalf("resize re-emitted the scrollback header %d times", got)
	}

	entity.Text += "Q"
	m.sessionEntities["long"] = entity
	m.invalidateTranscript()
	output.Reset()
	m.View()
	assertStreamingTail(44)

	output.Reset()
	update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.View()
	assertStreamingTail(80)
}

func TestMarkdownStreamingKeepsSourceReceiptAndRendersNewBlocks(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()

	initial := strings.Repeat("Q", 2000) + "\n\n## Head"
	m.sessionEntities["markdown"] = conversationEntity("markdown", "assistant", initial, 1)
	m.View()
	const bodyKey = "entity:markdown:body"
	receipt := m.receiptByPart[bodyKey]
	if receipt <= 0 || receipt >= len(initial) {
		t.Fatalf("stable Markdown block was not acknowledged before the mutable tail: offset=%d source bytes=%d", receipt, len(initial))
	}
	boundary := false
	for _, block := range markdownSourceBlocks(initial) {
		if block.end == receipt {
			boundary = true
			break
		}
	}
	if !boundary {
		t.Fatalf("receipt %d is not an exact raw Markdown block boundary", receipt)
	}

	entity := m.sessionEntities["markdown"]
	entity.Text += "ing\n\n- 項目\n\n```go\nfmt.Println(\"ok\")\n```"
	m.sessionEntities["markdown"] = entity
	m.invalidateTranscript()
	output.Reset()
	m.View()
	display := renderer.ReceiptText(output.String())
	for _, want := range []string{"Heading", "項目", "fmt.Println(\"ok\")"} {
		if !strings.Contains(display, want) {
			t.Errorf("streamed Markdown suffix omitted %q: %q", want, display)
		}
	}
	if got := strings.Count(display, "fmt.Println(\"ok\")"); got != 1 {
		t.Fatalf("new code block was emitted %d times, want once: %q", got, display)
	}
	transcript := m.renderTranscript()
	for _, marker := range []string{"Heading", "項目", "fmt.Println"} {
		if !strings.Contains(renderer.ReceiptText(transcript), marker) {
			t.Errorf("rendered transcript omitted %q", marker)
		}
	}
	if m.sessionEntities["markdown"].Text != entity.Text {
		t.Fatal("rendering replaced the original Markdown")
	}
	if next := m.receiptByPart[bodyKey]; next < receipt || next > len(entity.Text) {
		t.Fatalf("receipt moved outside the entity's source byte range: old=%d new=%d source=%d", receipt, next, len(entity.Text))
	} else {
		boundary := next == 0
		for _, block := range markdownSourceBlocks(entity.Text) {
			boundary = boundary || block.end == next
		}
		if !boundary {
			t.Fatalf("updated receipt %d is not an exact raw Markdown block boundary", next)
		}
	}
}

func TestDraftOnlyViewReusesTranscriptDocument(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	m.sessionEntities["saved"] = conversationEntity("saved", "assistant", "保存された本文", 1)
	m.View()
	doc := m.transcript
	if doc == nil {
		t.Fatal("initial View did not build the conversation document")
	}

	m.textarea.SetValue("次の依頼")
	output.Reset()
	m.View()
	if m.transcript != doc || m.transcriptDirty {
		t.Fatal("draft-only View rebuilt or invalidated the conversation document")
	}
	if m.lastTranscriptBytes != doc.sourceLength || !strings.Contains(output.String(), "次の依頼") {
		t.Fatalf("draft redraw lost the cached conversation or editor content: source bytes=%d output=%q", m.lastTranscriptBytes, output.String())
	}
}

func TestExitFlushCommitsOnlyVisibleConversationBody(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	final := conversationEntity("final", "assistant", "短い最終回答", 1)
	final.Status = "completed"
	m.sessionEntities["final"] = final
	output.Reset()
	m.View()
	if !strings.Contains(renderer.ReceiptText(output.String()), "短い最終回答") {
		t.Fatal("completed conversation body was not committed to scrollback")
	}
	if len(m.lastBodyRows) != 0 {
		t.Fatalf("completed body remained in the mutable live band: %q", m.lastBodyRows)
	}
	output.Reset()
	if err := m.flushVisibleBody(); err != nil {
		t.Fatal(err)
	}
	sealed := output.String()
	if strings.Contains(renderer.ReceiptText(sealed), "短い最終回答") {
		t.Fatalf("exit flush duplicated the already committed answer: %q", sealed)
	}
	for _, chrome := range []string{"hjh v", "Enter send", "依頼を入力", "status:"} {
		if strings.Contains(sealed, chrome) {
			t.Errorf("exit flush committed mutable UI chrome %q: %q", chrome, sealed)
		}
	}
}

func TestResumeAndHistoryUseSessionViewAndPreserveLegacyHistoryCommand(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), queryCalls: make(chan [4]any, 3),
		session: api.Session{ID: "saved", WorkspaceRoot: "/saved", ActiveModel: contracts.JSON(contracts.OpenRouterModel("saved/model")), TurnCount: 3},
		view: api.ConversationSnapshot{ScopeID: "saved", ViewID: "saved-view", Entities: map[string]api.ConversationEntity{
			"saved-task": conversationEntity("saved-task", "task", "保存済みの依頼", 1),
		}},
		messages: []api.Message{{JSON: contracts.JSON(contracts.User("履歴コマンドの内容"))}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/initial"})
	defer func() { cancel(); m.shutdown() }()
	m.changeSession(sessionAction{resumeID: "saved"})
	loaded := receive(t, messages)
	update(m, loaded)
	call := <-backend.queryCalls
	if call[0] != "session-view" || call[1] != "saved" {
		t.Fatalf("resume did not load unified Session view: %+v", call)
	}
	if m.sessionID != "saved" || m.workspace != "/saved" || !strings.Contains(m.renderTranscript(), "保存済みの依頼") {
		t.Fatalf("public Session could not be resumed: session=%q workspace=%q transcript=%q", m.sessionID, m.workspace, m.renderTranscript())
	}
	m.handleCommand("/history 2 3")
	query := <-backend.queryCalls
	if query[0] != "conversation" || query[1] != "saved" || query[2] != int64(2) || query[3] != int64(3) {
		t.Fatalf("history API request = %+v", query)
	}
	for {
		message := receive(t, messages)
		update(m, message)
		if result, ok := message.(operationResultMsg); ok && result.title == "history · saved" {
			break
		}
	}
	if !strings.Contains(m.renderTranscript(), "history · saved") || !strings.Contains(m.renderTranscript(), "履歴コマンドの内容") {
		t.Fatalf("history response not displayed: %q", m.renderTranscript())
	}
}

func TestSessionSwitchWaitsForFinalRunConversation(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), queryCalls: make(chan [4]any, 2),
		session: api.Session{ID: "saved", WorkspaceRoot: "/saved", TurnCount: 3},
		view: api.ConversationSnapshot{ScopeID: "saved", ViewID: "saved-view", Entities: map[string]api.ConversationEntity{
			"saved-task": conversationEntity("saved-task", "task", "保存済み", 1),
		}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/initial"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("first task")
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	initialSnapshot := api.ConversationSnapshot{ScopeID: call.request.RunID, ViewID: "run-view", Revision: 1, Entities: map[string]api.ConversationEntity{}}
	initialState := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "running", SessionID: "created"}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: initialState, Conversation: &initialSnapshot})
	m.handleCommand("/resume saved")
	for {
		msg := receive(t, messages)
		update(m, msg)
		if _, ok := msg.(sessionResolvedMsg); ok {
			break
		}
	}
	if m.sessionID != "created" || m.pendingSession == nil || m.pendingSession.resumeID != "saved" {
		t.Fatalf("Session switched before terminal settlement: id=%q pending=%+v", m.sessionID, m.pendingSession)
	}
	for {
		msg := receive(t, messages)
		update(m, msg)
		if settlement, ok := msg.(runtimeInputChainSettledMsg); ok {
			if settlement.state.Phase != "settled" {
				t.Fatalf("Session switch did not wait on the input chain: %+v", settlement)
			}
			break
		}
	}

	terminal := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "terminal", SessionID: "created", Result: &api.RunResult{OK: false, Error: &api.Error{Code: "cancelled", Message: "cancelled"}}}
	call.handle.finish(terminal, nil)
	for {
		msg := receive(t, messages)
		update(m, msg)
		if _, ok := msg.(runDoneMsg); ok { // Wait result is held until the final snapshot event.
			break
		}
	}
	if m.activeRun == nil {
		t.Fatal("run settled before its terminal conversation snapshot")
	}
	terminalSnapshot := api.ConversationSnapshot{ScopeID: call.request.RunID, ViewID: "run-view", Revision: 2, Entities: map[string]api.ConversationEntity{
		"task": conversationEntity("task", "task", "first task", 1),
	}}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: terminal, Conversation: &terminalSnapshot})
	if m.activeRun == nil {
		t.Fatal("final snapshot settled the run before independent run.terminal")
	}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	for {
		msg := receive(t, messages)
		update(m, msg)
		if _, ok := msg.(sessionLoadedMsg); ok {
			break
		}
	}
	<-backend.queryCalls
	if m.sessionID != "saved" || m.workspace != "/saved" || !strings.Contains(m.renderTranscript(), "保存済み") {
		t.Fatalf("Session did not load after terminal settlement: session=%q workspace=%q transcript=%q", m.sessionID, m.workspace, m.renderTranscript())
	}
}

func TestRunDoneWaitsForTerminalEvenWhenInitialSnapshotIsTerminal(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("task")
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	initial := api.RunSnapshot{RunState: api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "terminal"}, Conversation: api.ConversationSnapshot{
		ScopeID: call.request.RunID, ViewID: "run-view", Revision: 1, Entities: map[string]api.ConversationEntity{},
	}}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: initial.RunState, Conversation: &initial.Conversation})
	terminal := initial.RunState
	terminal.Result = &api.RunResult{OK: true}
	call.handle.finish(terminal, nil)
	update(m, receive(t, messages))
	if m.activeRun == nil {
		t.Fatal("a terminal-phase snapshot completed the UI before run.terminal")
	}
	final := initial.Conversation
	final.Revision = 2
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: terminal, Conversation: &final})
	if m.activeRun == nil {
		t.Fatal("the final terminal snapshot settled the run before the independent terminal event")
	}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	if m.activeRun != nil {
		t.Fatal("independent run.terminal did not settle the run after the final snapshot")
	}
}

func TestLoginProfilePickerPreservesDraftAndDoesNotChangeNextRunSelection(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1),
		authProfilesCalls: make(chan api.SelectionRequest, 1),
		authProfiles: api.AuthProfiles{
			Target:   api.AuthTarget{ProviderID: "openrouter", AuthProfile: "selected-profile", Kind: "api-key"},
			Profiles: []api.AuthProfile{{AuthProfile: "selected-profile", Methods: []string{"api-key"}, Providers: []api.AuthProvider{{ID: "openrouter", DisplayName: "OpenRouter", Kind: "api-key"}}}},
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/run-workspace", ProviderID: "openrouter", AuthProfile: "selected-profile"})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("original request draft")
	m.handleCommand("/login")
	request := <-backend.authProfilesCalls
	if request.Workspace != "/run-workspace" || request.ProviderID != "openrouter" || request.AuthProfile != "selected-profile" {
		t.Fatalf("profile list did not use the next-run selection: %+v", request)
	}
	update(m, receive(t, messages))
	if m.authUI == nil || m.authUI.screen != authProfilesScreen || m.authUI.index != 0 || m.authUI.targetProvider != "" {
		t.Fatalf("login did not open the effective profile picker: %+v", m.authUI)
	}
	update(m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.textarea.Value() != "original request draft" || m.providerID != "openrouter" || m.authProfile != "selected-profile" {
		t.Fatalf("closing authentication changed draft/selection: draft=%q provider=%q profile=%q", m.textarea.Value(), m.providerID, m.authProfile)
	}
	m.textarea.SetValue("continue with selected connection")
	update(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	if call.request.Workspace != "/run-workspace" || call.request.ProviderID != "openrouter" || call.request.AuthProfile != "selected-profile" {
		t.Fatalf("next run used login scope: %+v", call.request)
	}
}

func TestLegacyLoginCallbackRouteShowsGuidanceWithoutRecordingURL(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, _, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	callback := "http://127.0.0.1:1455/auth/callback?code=private-code&state=private-state"
	m.textarea.SetValue("/login callback openai " + callback)
	output := attachWriter(m)
	m.View()
	output.Reset()
	m.submitDraft()
	m.View()
	if !strings.Contains(m.status, "masked authentication screen") || strings.Contains(m.status, callback) {
		t.Fatalf("legacy callback route did not give safe guidance: %q", m.status)
	}
	display := renderer.ReceiptText(output.String())
	if m.textarea.Value() != "" || strings.Contains(m.renderTranscript(), callback) || strings.Contains(display, callback) {
		t.Fatalf("legacy callback URL entered draft/transcript/display: draft=%q transcript=%q display=%q", m.textarea.Value(), m.renderTranscript(), display)
	}
	for _, entry := range m.entries {
		if strings.Contains(entry.text, callback) {
			t.Fatalf("legacy callback URL entered an entry: %+v", entry)
		}
	}
}
