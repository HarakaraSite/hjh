package tui

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
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
	cancelHook  func() error
	state       api.RunState
	err         error
	done        chan struct{}
	once        sync.Once
}

func (r *fakeRun) RunID() string { return r.id }
func (r *fakeRun) Cancel() error {
	r.mu.Lock()
	r.cancelCalls++
	err, hook := r.cancelErr, r.cancelHook
	r.mu.Unlock()
	if hook != nil {
		return hook()
	}
	return err
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
	core api.Core

	runCalls chan runCall
	roles    api.RoleCatalog
	sessions []api.Session
	session  api.Session
	messages []api.Message
	inspect  api.Inspection

	queryCalls chan [4]any
}

func (f *fakeBackend) Core(context.Context) (api.Core, error) { return f.core, nil }
func (f *fakeBackend) StartRun(request api.StartRunRequest, observer func(apiclient.RunEvent)) RunHandle {
	handle := &fakeRun{id: request.RunID, done: make(chan struct{})}
	f.runCalls <- runCall{request: request, observer: observer, handle: handle}
	return handle
}
func (f *fakeBackend) Roles(context.Context, apiclient.Scope) (api.RoleCatalog, error) {
	return f.roles, nil
}
func (f *fakeBackend) Sessions(context.Context) ([]api.Session, error) { return f.sessions, nil }
func (f *fakeBackend) Session(_ context.Context, id string) (api.Session, error) {
	if f.session.ID == id {
		return f.session, nil
	}
	return api.Session{}, context.Canceled
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
func (f *fakeBackend) Providers(context.Context, apiclient.Scope) ([]api.ProviderDefinition, error) {
	return nil, nil
}
func (f *fakeBackend) Models(context.Context, apiclient.Scope, string) ([]api.ModelInfo, error) {
	return nil, nil
}
func (f *fakeBackend) BeginLogin(context.Context, apiclient.Scope, string) (api.Login, error) {
	return api.Login{}, nil
}
func (f *fakeBackend) CompleteLogin(context.Context, api.Login, string) (api.Status, error) {
	return api.Status{}, nil
}
func (f *fakeBackend) LoginStatus(context.Context, apiclient.Scope, string) (api.Status, error) {
	return api.Status{}, nil
}

func testModel(backend *fakeBackend, options Options) (*model, <-chan tea.Msg, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	messages := make(chan tea.Msg, 32)
	m := newModel(ctx, cancel, backend, options)
	m.send = func(msg tea.Msg) { messages <- msg }
	m.layout()
	return m, messages, cancel
}

func press(m *model, code rune) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return cmd
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

func update(m *model, message tea.Msg) tea.Cmd {
	_, cmd := m.Update(message)
	return cmd
}

func deliverRunState(t *testing.T, m *model, messages <-chan tea.Msg, call runCall, kind string, state api.RunState) {
	t.Helper()
	call.observer(apiclient.RunEvent{Kind: kind, State: state})
	update(m, receive(t, messages))
}

func TestRunProjectionCoalescesByExecutionAndOperationID(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1", Workspace: "/core/work"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/core/work", ModelID: "test/model", Role: "reviewer"})
	defer func() { cancel(); m.shutdown() }()

	m.textarea.SetValue("日本語の依頼\n2行目: ファイルを確認してください")
	press(m, tea.KeyEnter)
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	if call.request.Task != "日本語の依頼\n2行目: ファイルを確認してください" || call.request.RunID == "" {
		t.Fatalf("start request = %#v", call.request)
	}
	if call.request.CoreID != "core-1" || call.request.Workspace != "/core/work" || call.request.ModelID != "test/model" || call.request.Role != "reviewer" {
		t.Fatalf("selection was not sent through the API: %#v", call.request)
	}

	selection := contracts.OpenRouterModel("test/model")
	first := api.RunState{
		CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "running", ExecutionID: "root-1",
		ActualSelection: &selection, ActualRole: "reviewer",
		Texts: []api.TextState{
			{ExecutionID: "root-1", ModelStep: 1, Text: "調査"},
			{ExecutionID: "child-1", ParentExecutionID: "root-1", ModelStep: 1, Text: "子Agentの確認"},
		},
		Operations: []api.OperationState{{ID: "tool-1", ExecutionID: "root-1", Kind: "model_tool", Name: "read_file", Phase: "started", ModelStep: 1}},
		Executions: []api.ExecutionState{{ExecutionID: "root-1", Role: "reviewer", Selection: &selection}, {ExecutionID: "child-1", ParentExecutionID: "root-1", Role: "explorer", Phase: "running"}},
	}
	deliverRunState(t, m, messages, call, "run.snapshot", first)

	second := first
	second.Seq = 2
	second.Texts = []api.TextState{
		{ExecutionID: "root-1", ModelStep: 1, Text: "調査しました"},
		{ExecutionID: "child-1", ParentExecutionID: "root-1", ModelStep: 1, Text: "子Agentが確認しました"},
	}
	second.Operations = []api.OperationState{{ID: "tool-1", ExecutionID: "root-1", Kind: "model_tool", Name: "read_file", Phase: "completed", ModelStep: 1}}
	second.Executions[1].Phase = "completed"
	deliverRunState(t, m, messages, call, "run.update", second)
	content := m.View().Content
	if strings.Count(content, "read_file") != 1 || !strings.Contains(content, "調査しました") || !strings.Contains(content, "子Agentが確認しました") {
		t.Fatalf("projection failed to upsert cumulative text and operation: %q", content)
	}
	if strings.Contains(content, "調査\n") || strings.Contains(content, "子Agentの確認") {
		t.Fatalf("old cumulative text remains after update: %q", content)
	}
	if !strings.Contains(content, "role explorer") || m.activeRun.actualRole != "reviewer" {
		t.Fatalf("child execution changed or lost role state: view=%q root=%q", content, m.activeRun.actualRole)
	}

	resnapshot := second
	resnapshot.Seq = 3
	resnapshot.Operations = []api.OperationState{{ID: "tool-1", ExecutionID: "root-1", Kind: "model_tool", Name: "read_file", Phase: "completed", ModelStep: 1}}
	deliverRunState(t, m, messages, call, "run.snapshot", resnapshot)
	stale := first
	deliverRunState(t, m, messages, call, "run.update", stale)
	if strings.Count(m.View().Content, "read_file") != 1 || strings.Contains(m.View().Content, "調査\n") {
		t.Fatalf("snapshot or stale event corrupted ID-keyed display: %q", m.View().Content)
	}

	terminal := resnapshot
	terminal.Seq = 4
	terminal.Phase = "terminal"
	terminal.Result = &api.RunResult{OK: true, AgentResult: &api.AgentResult{OK: true, Outcome: "final", FinalText: "最終回答です"}}
	call.observer(apiclient.RunEvent{Kind: "run.snapshot", State: terminal})
	call.handle.finish(terminal, nil)
	for i := 0; i < 2; i++ {
		update(m, receive(t, messages))
	}
	if strings.Count(m.View().Content, "最終回答です") != 1 || m.activeRun != nil {
		t.Fatalf("terminal result duplicated or run remained active: %q", m.View().Content)
	}
}

func TestEscAndSessionSwitchExplicitlyCancelThenWaitForTerminal(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1),
		session:  api.Session{ID: "saved", WorkspaceRoot: "/saved", TurnCount: 3},
		messages: []api.Message{{JSON: json.RawMessage(`{"role":"user","content":[{"type":"text","text":"保存済み"}]}`)}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/initial"})
	defer func() { cancel(); m.shutdown() }()

	m.textarea.SetValue("first task")
	press(m, tea.KeyEnter)
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if msg := receive(t, messages); func() bool { _, ok := msg.(runCancelMsg); return ok }() {
		update(m, msg)
	} else {
		t.Fatalf("expected cancel completion, got %T", msg)
	}
	call.handle.mu.Lock()
	cancelCalls := call.handle.cancelCalls
	call.handle.mu.Unlock()
	if cancelCalls != 1 || m.activeRun == nil {
		t.Fatalf("Esc did not explicitly cancel while retaining active run: calls=%d run=%#v", cancelCalls, m.activeRun)
	}

	m.handleCommand("/resume saved")
	if m.sessionID != "" || m.pendingSession == nil || m.pendingSession.resumeID != "saved" {
		t.Fatalf("Session switched before terminal: id=%q pending=%#v", m.sessionID, m.pendingSession)
	}
	terminal := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "terminal", SessionID: "created", Result: &api.RunResult{OK: false, Error: &api.Error{Code: "cancelled", Message: "cancelled"}}}
	call.handle.finish(terminal, nil)
	update(m, receive(t, messages))
	update(m, receive(t, messages))
	if m.sessionID != "saved" || m.workspace != "/saved" || !strings.Contains(m.View().Content, "保存済み") {
		t.Fatalf("Session did not load after terminal settlement: session=%q workspace=%q view=%q", m.sessionID, m.workspace, m.View().Content)
	}
}

func TestQuitRequestsCancelAndExitsOnlyAfterTerminal(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("work")
	press(m, tea.KeyEnter)
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	m.handleCommand("/quit")
	if m.quitting {
		t.Fatal("TUI quit before the Core returned a terminal result")
	}
	msg := receive(t, messages)
	if _, ok := msg.(runCancelMsg); !ok {
		t.Fatalf("expected cancel request completion, got %T", msg)
	}
	update(m, msg)
	terminal := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "terminal", Result: &api.RunResult{OK: false, AgentResult: &api.AgentResult{Outcome: "cancelled"}}}
	call.handle.finish(terminal, nil)
	if cmd := update(m, receive(t, messages)); cmd == nil {
		t.Fatal("TUI did not issue quit after terminal result")
	}
}

func TestLoginResultDoesNotChangeNextRunConnectionSelection(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, _, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/run-workspace", ProviderID: "openrouter", AuthProfile: "selected-profile"})
	defer func() { cancel(); m.shutdown() }()
	login := &api.Login{Workspace: "/login-workspace", ProviderID: "openai", AuthProfile: "resolved-profile"}
	update(m, operationResultMsg{generation: m.sessionGeneration, title: "ChatGPT login", login: login})
	if m.pendingLogin != login || m.workspace != "/run-workspace" || m.providerID != "openrouter" || m.authProfile != "selected-profile" {
		t.Fatalf("login changed next-run selection: pending=%#v workspace=%q provider=%q profile=%q", m.pendingLogin, m.workspace, m.providerID, m.authProfile)
	}
	m.textarea.SetValue("continue with selected connection")
	press(m, tea.KeyEnter)
	call := <-backend.runCalls
	defer call.handle.finish(api.RunState{Phase: "terminal", Result: &api.RunResult{}}, nil)
	if call.request.Workspace != "/run-workspace" || call.request.ProviderID != "openrouter" || call.request.AuthProfile != "selected-profile" {
		t.Fatalf("next run used login scope: %#v", call.request)
	}
}

func TestCancelCompletionCanArriveAfterRunFinishes(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core})
	defer func() { cancel(); m.shutdown() }()
	m.textarea.SetValue("finish while cancellation is pending")
	press(m, tea.KeyEnter)
	call := <-backend.runCalls
	cancelEntered := make(chan struct{})
	releaseCancel := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCancel) }) }
	defer release()
	call.handle.mu.Lock()
	call.handle.cancelHook = func() error {
		close(cancelEntered)
		<-releaseCancel
		return nil
	}
	call.handle.mu.Unlock()
	m.requestRunCancel(m.activeRun)
	select {
	case <-cancelEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("explicit cancel did not start")
	}
	terminal := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "terminal", Result: &api.RunResult{OK: true}}
	call.handle.finish(terminal, nil)
	finished := receive(t, messages)
	if _, ok := finished.(runDoneMsg); !ok {
		t.Fatalf("expected run completion while cancel is pending, got %T", finished)
	}
	update(m, finished)
	if m.activeRun != nil {
		t.Fatal("terminal run remained active")
	}
	release()
	cancelled := receive(t, messages)
	cancelMessage, ok := cancelled.(runCancelMsg)
	if !ok || cancelMessage.runID != call.request.RunID || cancelMessage.err != nil {
		t.Fatalf("cancel completion after run finished = %#v", cancelled)
	}
}

func TestResumeAndHistoryUsePublicSessionDTO(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), queryCalls: make(chan [4]any, 2),
		session:  api.Session{ID: "saved", WorkspaceRoot: "/saved", ActiveModel: contracts.JSON(contracts.OpenRouterModel("saved/model")), TurnCount: 3},
		messages: []api.Message{{JSON: contracts.JSON(contracts.User("保存済みの依頼"))}},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/initial"})
	defer func() { cancel(); m.shutdown() }()
	m.changeSession(sessionAction{resumeID: "saved"})
	update(m, receive(t, messages))
	<-backend.queryCalls // Conversation loaded with the resumed Session.
	if m.sessionID != "saved" || m.workspace != "/saved" || !strings.Contains(m.View().Content, "保存済みの依頼") {
		t.Fatalf("public Session could not be resumed: session=%q workspace=%q view=%q", m.sessionID, m.workspace, m.View().Content)
	}
	m.handleCommand("/history 2 3")
	call := <-backend.queryCalls
	if call[0] != "conversation" || call[1] != "saved" || call[2] != int64(2) || call[3] != int64(3) {
		t.Fatalf("history API request = %#v", call)
	}
	update(m, receive(t, messages))
	if !strings.Contains(m.View().Content, "history · saved") || !strings.Contains(m.View().Content, "保存済みの依頼") {
		t.Fatalf("history response not displayed: %q", m.View().Content)
	}
}
