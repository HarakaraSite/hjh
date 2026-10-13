package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/contracts"
)

func testOrientation(mode, workspace, role, path, skill string) api.Orientation {
	return api.Orientation{
		Mode: mode, Workspace: workspace, Role: role,
		Base: contracts.BaseInstruction{
			BuiltinVersion: "12", ConfigPath: "/config/hjh.json", HasSystemInstruction: true,
		},
		Instructions: []contracts.InstructionSource{{Path: path, Digest: "digest"}},
		Skills:       []contracts.SkillSummary{{Name: skill, Description: "skill description", Path: "/skills/" + skill + "/SKILL.md"}},
	}
}

func TestExternalBaseInstructionSourceInHeaderAndContext(t *testing.T) {
	const path = "/home/user/.config/hjh/instructions/base.md"
	orientation := testOrientation("actual", "/work", "default", "/work/AGENTS.md", "report")
	orientation.Base = contracts.BaseInstruction{Path: path, Digest: "captured-digest", ConfigPath: "/config/hjh.json", HasSystemInstruction: true}
	m := model{headerOrientation: orientation, headerOrientationSet: true}
	base, _, _ := m.headerOrientationValues()
	details := formatOrientationDetails(orientation, "actual")
	for _, displayed := range []string{base, details} {
		if !strings.Contains(displayed, path) || strings.Contains(displayed, "built-in") {
			t.Fatalf("external base source misrepresented: %s", displayed)
		}
	}
}

func TestInitialInputCanRunBeforePreviewAndActualWinsHeaderBeforeBody(t *testing.T) {
	entered := make(chan api.OrientationRequest, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	backend := &fakeBackend{
		core:             api.Core{CoreID: "core-1", Workspace: "/work"},
		runCalls:         make(chan runCall, 1),
		orientationCalls: make(chan api.OrientationRequest, 1),
		orientationFunc: func(_ context.Context, request api.OrientationRequest) (api.Orientation, error) {
			entered <- request
			<-release
			return testOrientation("preview", request.Workspace, request.Role, "/preview/AGENTS.md", "preview-skill"), nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work", Role: "reviewer"})
	output := attachWriter(m)
	defer func() {
		releaseOnce.Do(func() { close(release) })
		if m.activeRun != nil && m.activeRun.handle != nil {
			m.activeRun.handle.(*fakeRun).finish(api.RunState{Phase: "terminal"}, context.Canceled)
		}
		cancel()
		m.shutdown()
		_ = m.writer.Close()
	}()

	m.Init()
	request := <-entered
	if request.SessionID != "" || request.Workspace != "/work" || request.Role != "reviewer" {
		t.Fatalf("startup orientation request = %+v", request)
	}
	previewJobID := m.headerOrientationJobID
	m.View()
	if m.headerPrinted {
		t.Fatal("header printed while its preview was pending")
	}
	m.textarea.SetValue("first request")
	m.submitDraft()
	call := <-backend.runCalls
	if call.request.Task != "first request" || m.textarea.Value() != "" {
		t.Fatalf("pending preview blocked first input: task=%q draft=%q", call.request.Task, m.textarea.Value())
	}

	actual := contracts.Orientation(testOrientation("actual", "/work", "reviewer", "/actual/AGENTS.md", "actual-skill"))
	state := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "running", SessionID: "session-1", ActualContext: &actual}
	conversation := &api.ConversationSnapshot{ViewID: "view-1", Revision: 1, Entities: map[string]api.ConversationEntity{
		"task":      conversationEntity("task", "task", "first request", 1),
		"assistant": conversationEntity("assistant", "assistant", "actual body", 2),
	}}
	task := conversation.Entities["task"]
	task.Status = "completed"
	conversation.Entities["task"] = task
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: state, Conversation: conversation})
	m.View()
	if !m.headerPrinted || m.headerOrientation.Mode != "actual" {
		t.Fatalf("run actual metadata did not settle header: printed=%v orientation=%+v", m.headerPrinted, m.headerOrientation)
	}
	rendered := ansi.Strip(output.String())
	actualAt, bodyAt := strings.Index(rendered, "/actual/AGENTS.md"), strings.Index(rendered, "actual body")
	if actualAt < 0 || bodyAt < 0 || actualAt >= bodyAt {
		t.Fatalf("header must be written before the live conversation body: transcript=%q output=%q", m.renderTranscript(), rendered)
	}
	if strings.Contains(rendered, "/preview/AGENTS.md") {
		t.Fatalf("preview was displayed before its response arrived: %q", rendered)
	}

	releaseOnce.Do(func() { close(release) })
	timeout := time.After(2 * time.Second)
	for {
		select {
		case message := <-messages:
			if loaded, ok := message.(orientationLoadedMsg); ok && loaded.jobID == previewJobID {
				update(m, message)
				goto latePreviewReceived
			}
			update(m, message)
		case <-timeout:
			t.Fatal("timed out waiting for delayed preview")
		}
	}

latePreviewReceived:
	output.Reset()
	m.View()
	if m.headerOrientation.Mode != "actual" || strings.Contains(output.String(), "/preview/AGENTS.md") {
		t.Fatalf("late preview replaced actual header: orientation=%+v output=%q", m.headerOrientation, output.String())
	}
	if strings.Count(rendered+output.String(), "hjh v") != 1 {
		t.Fatalf("opening header was emitted more than once: %q", rendered+output.String())
	}
}

func TestAttachedRunUsesActualOrientationForOpeningHeader(t *testing.T) {
	actual := contracts.Orientation(testOrientation("actual", "/saved", "writer", "/saved/AGENTS.md", "save-skill"))
	runState := api.RunState{CoreID: "core-1", RunID: "run-1", SessionID: "saved", Phase: "running", ActualContext: &actual}
	backend := &fakeBackend{
		core:             api.Core{CoreID: "core-1"},
		activeSessionRun: &runState,
		attachCalls:      make(chan runCall, 1),
		orientationCalls: make(chan api.OrientationRequest, 1),
		session:          api.Session{ID: "saved", WorkspaceRoot: "/saved"},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "saved"})
	output := attachWriter(m)
	defer func() {
		if m.activeRun != nil && m.activeRun.handle != nil {
			m.activeRun.handle.(*fakeRun).finish(api.RunState{Phase: "terminal"}, context.Canceled)
		}
		cancel()
		m.shutdown()
		_ = m.writer.Close()
	}()

	m.Init()
	update(m, receive(t, messages))
	<-backend.attachCalls
	m.View()
	if m.headerOrientation.Mode != "actual" || !strings.Contains(output.String(), "/saved/AGENTS.md") ||
		strings.Contains(output.String(), "unconfirmed") {
		t.Fatalf("attached run header did not use actual metadata: %s", output.String())
	}
	select {
	case request := <-backend.orientationCalls:
		t.Fatalf("active attach queried a preview despite having actual run metadata: %+v", request)
	default:
	}
}

func TestAttachedPreparingRunWaitsForActualOrientationBeforeOpeningHeader(t *testing.T) {
	runState := api.RunState{CoreID: "core-1", RunID: "run-1", SessionID: "saved", Phase: "preparing"}
	backend := &fakeBackend{
		core:             api.Core{CoreID: "core-1"},
		activeSessionRun: &runState,
		attachCalls:      make(chan runCall, 1),
		orientationCalls: make(chan api.OrientationRequest, 1),
		session:          api.Session{ID: "saved", WorkspaceRoot: "/saved"},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "saved"})
	output := attachWriter(m)
	defer func() {
		if m.activeRun != nil && m.activeRun.handle != nil {
			m.activeRun.handle.(*fakeRun).finish(api.RunState{Phase: "terminal"}, context.Canceled)
		}
		cancel()
		m.shutdown()
		_ = m.writer.Close()
	}()

	m.Init()
	update(m, receive(t, messages))
	run := <-backend.attachCalls
	m.View()
	if m.activeRun == nil || !m.activeRun.attached || !m.headerOrientationPending ||
		m.headerOrientationJobID != 0 || m.headerPrinted {
		t.Fatalf("preparing attach did not hold opening header for actual metadata: run=%+v pending=%v job=%d printed=%v",
			m.activeRun, m.headerOrientationPending, m.headerOrientationJobID, m.headerPrinted)
	}
	select {
	case request := <-backend.orientationCalls:
		t.Fatalf("preparing attach requested preview before run metadata: %+v", request)
	default:
	}

	actual := contracts.Orientation(testOrientation("actual", "/saved", "writer", "/actual/AGENTS.md", "actual-skill"))
	state := api.RunState{CoreID: "core-1", RunID: run.request.RunID, Seq: 1, Phase: "running", SessionID: "saved", ActualContext: &actual}
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.snapshot", State: state})
	m.View()
	if !m.headerPrinted || m.headerOrientationPending || m.headerOrientation.Mode != "actual" ||
		!strings.Contains(output.String(), "/actual/AGENTS.md") {
		t.Fatalf("actual metadata did not settle the attached opening header: pending=%v printed=%v orientation=%+v output=%q",
			m.headerOrientationPending, m.headerPrinted, m.headerOrientation, output.String())
	}
	select {
	case request := <-backend.orientationCalls:
		t.Fatalf("attached run issued a preview before actual metadata arrived: %+v", request)
	default:
	}
}

func TestAttachedRunWithoutActualContextFallsBackToPreviewAtTerminal(t *testing.T) {
	runState := api.RunState{CoreID: "core-1", RunID: "run-1", SessionID: "saved", Phase: "preparing"}
	backend := &fakeBackend{
		core:             api.Core{CoreID: "core-1"},
		activeSessionRun: &runState,
		attachCalls:      make(chan runCall, 1),
		orientationCalls: make(chan api.OrientationRequest, 1),
		orientation:      testOrientation("preview", "/saved", "writer", "/preview/AGENTS.md", "preview-skill"),
		session:          api.Session{ID: "saved", WorkspaceRoot: "/saved"},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "saved"})
	output := attachWriter(m)
	defer func() {
		if m.activeRun != nil && m.activeRun.handle != nil {
			m.activeRun.handle.(*fakeRun).finish(api.RunState{Phase: "terminal"}, context.Canceled)
		}
		cancel()
		m.shutdown()
		_ = m.writer.Close()
	}()

	m.Init()
	update(m, receive(t, messages))
	run := <-backend.attachCalls
	m.textarea.SetValue("keep this draft")
	m.View()
	select {
	case request := <-backend.orientationCalls:
		t.Fatalf("active attached run queried preview before terminal: %+v", request)
	default:
	}
	if m.headerPrinted || !m.headerOrientationPending {
		t.Fatalf("attached active run did not keep the opening header pending: printed=%v pending=%v", m.headerPrinted, m.headerOrientationPending)
	}

	run.handle.finish(api.RunState{
		CoreID: "core-1", RunID: "run-1", SessionID: "saved", Phase: "terminal",
		Result: &api.RunResult{OK: false, Error: &api.Error{Code: "provider_unavailable", Message: "provider preparation failed"}},
	}, errors.New("provider preparation failed"))
	for {
		message := receive(t, messages)
		update(m, message)
		if _, ok := message.(runDoneMsg); ok {
			break
		}
	}
	request := <-backend.orientationCalls
	if request.SessionID != "saved" || request.Workspace != "/saved" {
		t.Fatalf("terminal attached run preview request = %+v", request)
	}
	if m.headerPrinted || !m.headerOrientationPending || m.textarea.Value() != "keep this draft" {
		t.Fatalf("terminal preview did not keep header deferred and draft intact: printed=%v pending=%v draft=%q",
			m.headerPrinted, m.headerOrientationPending, m.textarea.Value())
	}
	for {
		message := receive(t, messages)
		_, ok := message.(orientationLoadedMsg)
		update(m, message)
		if ok {
			break
		}
	}
	m.View()
	if !m.headerPrinted || m.headerOrientation.Mode != "preview" ||
		!strings.Contains(output.String(), "/preview/AGENTS.md") || m.textarea.Value() != "keep this draft" {
		t.Fatalf("terminal preview did not settle the attached header: printed=%v orientation=%+v draft=%q output=%q",
			m.headerPrinted, m.headerOrientation, m.textarea.Value(), output.String())
	}
}

type orientationContextBackend struct {
	*fakeBackend
	sessionContextCalls int
}

func (b *orientationContextBackend) SessionContext(_ context.Context, coreID, sessionID string) (api.SessionContext, error) {
	b.sessionContextCalls++
	return api.SessionContext{CoreID: coreID, SessionID: sessionID}, nil
}

func TestNarrowHeaderKeepsTwoRowsAndContextWorksBeforeSessionCreation(t *testing.T) {
	base := &fakeBackend{
		core:             api.Core{CoreID: "core-1"},
		orientation:      testOrientation("preview", "/new", "reviewer", "/new/AGENTS.md", "available-skill"),
		orientationCalls: make(chan api.OrientationRequest, 1),
	}
	backend := &orientationContextBackend{fakeBackend: base}
	m, messages, cancel := testModel(backend, Options{Core: base.core, Workspace: "/new", Role: "reviewer"})
	defer func() { cancel(); m.shutdown() }()
	m.width, m.height = 50, 14
	if rows := m.headerRows(); len(rows) != 2 {
		t.Fatalf("compact header grew beyond two rows: %v", rows)
	}
	m.textarea.SetValue("draft remains available")
	m.handleCommand("/context")
	update(m, receive(t, messages))
	request := <-base.orientationCalls
	if request.SessionID != "" || request.Workspace != "/new" || request.Role != "reviewer" {
		t.Fatalf("pre-Session context request = %+v", request)
	}
	if backend.sessionContextCalls != 0 || m.sessionID != "" || m.textarea.Value() != "draft remains available" {
		t.Fatalf("context created or queried Session state, or consumed input: calls=%d session=%q draft=%q",
			backend.sessionContextCalls, m.sessionID, m.textarea.Value())
	}
	text := m.entries[len(m.entries)-1].text
	for _, marker := range []string{"Current orientation", "Base instructions", "config system instruction present", "/new/AGENTS.md", "available-skill", "Session context · unavailable until"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("context output missing %q: %s", marker, text)
		}
	}
}

func TestSessionSwitchIgnoresDelayedOrientationAndKeepsDraft(t *testing.T) {
	oldEntered := make(chan struct{})
	oldRelease := make(chan struct{})
	var releaseOnce sync.Once
	backend := &fakeBackend{
		core:             api.Core{CoreID: "core-1"},
		orientationCalls: make(chan api.OrientationRequest, 2),
		orientationFunc: func(_ context.Context, request api.OrientationRequest) (api.Orientation, error) {
			if request.Workspace == "/old" {
				close(oldEntered)
				<-oldRelease
				return testOrientation("preview", request.Workspace, request.Role, "/old/AGENTS.md", "old-skill"), nil
			}
			return testOrientation("preview", request.Workspace, request.Role, "/new/AGENTS.md", "new-skill"), nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/old", Role: "writer"})
	output := attachWriter(m)
	defer func() {
		releaseOnce.Do(func() { close(oldRelease) })
		cancel()
		m.shutdown()
		_ = m.writer.Close()
	}()

	m.Init()
	oldRequest := <-backend.orientationCalls
	<-oldEntered
	if oldRequest.Workspace != "/old" {
		t.Fatalf("initial orientation request = %+v", oldRequest)
	}
	m.workspace = "/new"
	m.textarea.SetValue("keep after /new")
	m.applySessionAction(sessionAction{newSession: true})
	newRequest := <-backend.orientationCalls
	if newRequest.Workspace != "/new" || newRequest.SessionID != "" || newRequest.Role != "writer" {
		t.Fatalf("new Session orientation request = %+v", newRequest)
	}
	update(m, receiveUntil(t, m, messages, func(message tea.Msg) bool {
		loaded, ok := message.(orientationLoadedMsg)
		return ok && loaded.request.Workspace == "/new"
	}))
	m.View()
	if !m.headerPrinted || m.headerOrientation.Workspace != "/new" || !strings.Contains(output.String(), "/new/AGENTS.md") {
		t.Fatalf("new Session did not settle with its own orientation: %+v\n%s", m.headerOrientation, output.String())
	}

	releaseOnce.Do(func() { close(oldRelease) })
	timeout := time.After(2 * time.Second)
	for {
		select {
		case message := <-messages:
			if loaded, ok := message.(orientationLoadedMsg); ok && loaded.request.Workspace == "/old" {
				update(m, message)
				goto oldPreviewReceived
			}
			update(m, message)
		case <-timeout:
			t.Fatal("timed out waiting for old orientation response")
		}
	}

oldPreviewReceived:
	output.Reset()
	m.View()
	if m.headerOrientation.Workspace != "/new" || strings.Contains(output.String(), "/old/AGENTS.md") ||
		m.textarea.Value() != "keep after /new" {
		t.Fatalf("old orientation response affected new Session: orientation=%+v draft=%q output=%q",
			m.headerOrientation, m.textarea.Value(), output.String())
	}
}

func TestSessionAdmissionRequeriesPendingPreviewAfterProviderPreparationFailure(t *testing.T) {
	oldEntered := make(chan struct{})
	oldRelease := make(chan struct{})
	boundEntered := make(chan struct{})
	boundRelease := make(chan struct{})
	var oldReleaseOnce, boundReleaseOnce sync.Once
	backend := &fakeBackend{
		core:             api.Core{CoreID: "core-1"},
		runCalls:         make(chan runCall, 1),
		orientationCalls: make(chan api.OrientationRequest, 2),
		orientationFunc: func(_ context.Context, request api.OrientationRequest) (api.Orientation, error) {
			if request.SessionID == "" {
				close(oldEntered)
				<-oldRelease
				return testOrientation("preview", request.Workspace, request.Role, "/unbound/AGENTS.md", "unbound-skill"), nil
			}
			close(boundEntered)
			<-boundRelease
			return testOrientation("preview", request.Workspace, request.Role, "/session/AGENTS.md", "session-skill"), nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work", Role: "writer"})
	output := attachWriter(m)
	defer func() {
		oldReleaseOnce.Do(func() { close(oldRelease) })
		boundReleaseOnce.Do(func() { close(boundRelease) })
		if m.activeRun != nil && m.activeRun.handle != nil {
			m.activeRun.handle.(*fakeRun).finish(api.RunState{Phase: "terminal"}, context.Canceled)
		}
		cancel()
		m.shutdown()
		_ = m.writer.Close()
	}()

	m.Init()
	oldRequest := <-backend.orientationCalls
	<-oldEntered
	if oldRequest.SessionID != "" {
		t.Fatalf("startup preview unexpectedly had a Session ID: %+v", oldRequest)
	}
	m.textarea.SetValue("first request")
	m.submitDraft()
	call := <-backend.runCalls
	state := api.RunState{CoreID: "core-1", RunID: call.request.RunID, Seq: 1, Phase: "preparing", SessionID: "session-1"}
	deliverRunEvent(t, m, messages, call, apiclient.RunEvent{Kind: "run.snapshot", State: state})
	boundRequest := <-backend.orientationCalls
	<-boundEntered
	boundJobID := m.headerOrientationJobID
	if boundRequest.SessionID != "session-1" {
		t.Fatalf("Session admission did not requery orientation with its bound ID: %+v", boundRequest)
	}

	call.handle.finish(api.RunState{
		CoreID: "core-1", RunID: call.request.RunID, Seq: 2, Phase: "terminal", SessionID: "session-1",
		Result: &api.RunResult{OK: false, Error: &api.Error{Code: "provider_unavailable", Message: "provider preparation failed"}},
	}, errors.New("provider preparation failed"))
	for {
		message := receive(t, messages)
		update(m, message)
		if _, ok := message.(runDoneMsg); ok {
			break
		}
	}
	if m.activeRun != nil || !m.headerOrientationPending || m.headerOrientationJobID != boundJobID {
		t.Fatalf("provider preparation failure lost the bound preview: active=%v pending=%v job=%d want=%d",
			m.activeRun != nil, m.headerOrientationPending, m.headerOrientationJobID, boundJobID)
	}
	m.View()
	if doc := m.ensureTranscript(); doc.startRow != 0 || len(m.pendingSealRows) != 0 {
		t.Fatalf("provider failure transcript was sealed before orientation settled: start=%d pending=%q", doc.startRow, m.pendingSealRows)
	}

	oldReleaseOnce.Do(func() { close(oldRelease) })
	for {
		message := receive(t, messages)
		loaded, ok := message.(orientationLoadedMsg)
		update(m, message)
		if ok && loaded.request.SessionID == "" {
			break
		}
	}
	if m.headerOrientationJobID != boundJobID || !m.headerOrientationPending || m.headerOrientationSet {
		t.Fatalf("stale unbound preview changed the pending header: job=%d pending=%v set=%v orientation=%+v",
			m.headerOrientationJobID, m.headerOrientationPending, m.headerOrientationSet, m.headerOrientation)
	}

	boundReleaseOnce.Do(func() { close(boundRelease) })
	for {
		message := receive(t, messages)
		loaded, ok := message.(orientationLoadedMsg)
		update(m, message)
		if ok && loaded.request.SessionID == "session-1" {
			break
		}
	}
	m.View()
	if m.headerOrientationPending || !m.headerPrinted || m.headerOrientation.Workspace != "/work" ||
		!strings.Contains(output.String(), "/session/AGENTS.md") || strings.Contains(output.String(), "/unbound/AGENTS.md") {
		t.Fatalf("bound preview did not settle the one-time header after preparation failure: pending=%v printed=%v orientation=%+v output=%q",
			m.headerOrientationPending, m.headerPrinted, m.headerOrientation, output.String())
	}
	if doc := m.ensureTranscript(); doc.startRow == 0 {
		t.Fatal("ready opening header did not commit the saved failure body in the same frame")
	}
	if !strings.Contains(output.String(), "provider preparation failed") {
		t.Fatalf("settled failure body was not printed after the header: %q", output.String())
	}
}

func TestDeferredOpeningHeaderPrecedesTranscriptWhenSessionSwitchSeals(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/old"})
	output := attachWriter(m)
	defer func() { cancel(); m.shutdown(); _ = m.writer.Close() }()
	m.deferTranscriptUntilHeader = true
	m.headerOrientationPending = true
	m.appendEntry("assistant", "old visible body")
	m.View()
	if m.headerPrinted || m.ensureTranscript().startRow != 0 || len(m.pendingSealRows) != 0 {
		t.Fatal("body was sealed or header printed while orientation was pending")
	}

	m.sealOpeningHeaderForSessionSwitch()
	m.sealCurrentFrame()
	sealed := strings.Join(m.pendingSealRows, "\n")
	headerAt, bodyAt := strings.Index(sealed, "unconfirmed"), strings.Index(sealed, "old visible body")
	if !m.headerPrinted || headerAt < 0 || bodyAt < 0 || headerAt >= bodyAt {
		t.Fatalf("session switch did not preserve old header before its transcript: headerPrinted=%v sealed=%q output=%q",
			m.headerPrinted, sealed, output.String())
	}
}

func TestFailedOrientationPreviewShowsUnconfirmedAndKeepsInput(t *testing.T) {
	backend := &fakeBackend{
		core:             api.Core{CoreID: "core-1"},
		orientationCalls: make(chan api.OrientationRequest, 1),
		orientationFunc: func(context.Context, api.OrientationRequest) (api.Orientation, error) {
			return api.Orientation{}, errors.New("orientation unavailable")
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, Workspace: "/work"})
	defer func() { cancel(); m.shutdown() }()
	m.Init()
	m.textarea.SetValue("keep this input")
	m.View()
	update(m, receiveUntil(t, m, messages, func(message tea.Msg) bool { _, ok := message.(orientationLoadedMsg); return ok }))
	m.View()
	if !m.headerOrientationFailed || m.textarea.Value() != "keep this input" {
		t.Fatalf("preview failure hid input or was not recorded: failed=%v draft=%q", m.headerOrientationFailed, m.textarea.Value())
	}
	if text := strings.Join(m.headerRows(), "\n"); !strings.Contains(text, "unconfirmed") {
		t.Fatalf("failed preview was not shown as unconfirmed: %s", text)
	}
}
