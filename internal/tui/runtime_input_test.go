package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

func TestRuntimeInputKeepsAnEditedThenRestoredDraftWhileReceiptIsPending(t *testing.T) {
	started := make(chan runtimeInputCall, 1)
	accept := make(chan struct{}, 1)
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), submitInputCalls: started,
		submitInputFunc: func(_ context.Context, runID string, request api.SubmitInputRequest) (api.InputReceipt, error) {
			<-accept
			return api.InputReceipt{CoreID: request.CoreID, SessionID: request.SessionID, RunID: runID,
				InputID: request.InputID, Kind: request.Kind, Status: "queued", Revision: 1}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	defer func() {
		select {
		case accept <- struct{}{}:
		default:
		}
		cancel()
		m.shutdown()
	}()
	m.startRun("current task")
	run := <-backend.runCalls
	defer run.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	state := api.RunState{CoreID: "core-1", RunID: run.request.RunID, Seq: 1, Phase: "running", SessionID: "session-1",
		InputAcceptance: api.InputAcceptance{FollowUp: true}}
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.snapshot", State: state,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Entities: map[string]api.ConversationEntity{}}})
	m.textarea.SetValue("follow-up text")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF2})
	input := <-started
	if input.request.Text != "follow-up text" || input.request.Kind != "follow-up" || input.request.Next == nil {
		t.Fatalf("F2 did not capture its text and selection: %+v", input.request)
	}
	// Edit and restore the same text while the accepted response is pending.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m.handleKey(tea.KeyPressMsg{Code: 't', Text: "t"})
	if m.textarea.Value() != "follow-up text" || m.draftRevision == 0 {
		t.Fatalf("draft edit did not advance its revision: value=%q revision=%d", m.textarea.Value(), m.draftRevision)
	}
	accept <- struct{}{}
	result := receiveRuntimeInputResult(t, m, messages, input.request.InputID)
	update(m, result)
	if m.textarea.Value() != "follow-up text" {
		t.Fatalf("accepted response cleared a draft edited and restored while pending: %q", m.textarea.Value())
	}
}

func TestAcceptedReceiptClearsCapturedDraftAfterOriginalRunEnds(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	defer func() { cancel(); m.shutdown() }()

	m.sessionID = "session-1"
	m.textarea.SetValue("submitted task")
	m.draftRevision = 4
	input := runtimeInputOperation{
		request: api.SubmitInputRequest{CoreID: "core-1", SessionID: "session-1", InputID: "input-1", Kind: "steering", Text: "submitted task"},
		coreID:  "core-1", sessionID: "session-1", runID: "run-1", draft: "submitted task", draftRevision: 4,
	}
	m.runtimeInputQueue = []runtimeInputOperation{input}
	m.runtimeInputSending = true
	receipt := api.InputReceipt{CoreID: "core-1", SessionID: "session-1", RunID: "run-1", InputID: "input-1", Kind: "steering", Status: "accepted"}

	// The old Run may be terminal by the time its durable receipt reaches the UI.
	update(m, runtimeInputSubmittedMsg{jobID: 1, input: input, receipt: receipt})
	if m.activeRun != nil || m.textarea.Value() != "" {
		t.Fatalf("accepted receipt depended on the old run remaining active: active=%v draft=%q", m.activeRun != nil, m.textarea.Value())
	}
}

func TestLateAcceptedReceiptDoesNotLeakIntoNewSession(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}}
	m, _, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-new"})
	defer func() { cancel(); m.shutdown() }()

	current := api.InputReceipt{CoreID: "core-1", SessionID: "session-new", RunID: "current-run", InputID: "current-input",
		Kind: "steering", Status: "applied", Revision: 2}
	m.sessionID = "session-new"
	m.textarea.SetValue("new session draft")
	m.runtimeReceipts = []api.InputReceipt{current}
	m.runtimeReceiptIndex = map[string]int{"core-1\x00current-input": 0}
	m.activeRun = &activeRun{coreID: current.CoreID, session: current.SessionID, id: current.RunID}
	input := runtimeInputOperation{
		request: api.SubmitInputRequest{CoreID: "core-1", SessionID: "session-old", InputID: "old-input", Kind: "follow-up", Text: "old task"},
		coreID:  "core-1", sessionID: "session-old", runID: "old-run", draft: "old task",
	}
	m.runtimeInputQueue = []runtimeInputOperation{input}
	m.runtimeInputSending = true
	oldReceipt := api.InputReceipt{CoreID: "core-1", SessionID: "session-old", RunID: "old-run", InputID: "old-input", Kind: "follow-up", Status: "queued"}

	// The Core committed for the original target, but the visible Session changed before the response arrived.
	update(m, runtimeInputSubmittedMsg{jobID: 1, input: input, receipt: oldReceipt})
	if len(m.runtimeReceipts) != 1 || m.runtimeReceipts[0].InputID != "current-input" ||
		m.runtimeInputStatus() != runtimeInputReceiptStatus(current) || m.textarea.Value() != "new session draft" {
		t.Fatalf("old Session receipt polluted current UI state: receipts=%+v status=%q draft=%q", m.runtimeReceipts, m.runtimeInputStatus(), m.textarea.Value())
	}
}

func TestLostSubmitResponseLooksUpTheOriginalInputID(t *testing.T) {
	submits := 0
	inputLookups := make(chan runtimeInputCall, 1)
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), inputCalls: inputLookups,
		submitInputFunc: func(_ context.Context, runID string, request api.SubmitInputRequest) (api.InputReceipt, error) {
			submits++
			if submits != 1 {
				t.Fatalf("uncertain submit was replayed instead of looked up: count=%d", submits)
			}
			return api.InputReceipt{}, errors.New("connection dropped after commit")
		},
		inputFunc: func(_ context.Context, coreID, runID, inputID string, includeText bool) (api.InputReceipt, error) {
			return api.InputReceipt{CoreID: coreID, SessionID: "session-1", RunID: runID, InputID: inputID, Kind: "steering", Status: "accepted"}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	defer func() { cancel(); m.shutdown() }()
	m.startRun("current task")
	run := <-backend.runCalls
	defer run.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	state := api.RunState{CoreID: "core-1", RunID: run.request.RunID, Seq: 1, Phase: "running", SessionID: "session-1",
		InputAcceptance: api.InputAcceptance{Steering: true}}
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.snapshot", State: state,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Entities: map[string]api.ConversationEntity{}}})
	m.textarea.SetValue("apply this")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF3})
	lookup := <-inputLookups
	result := receiveRuntimeInputResult(t, m, messages, lookup.inputID)
	update(m, result)
	if lookup.coreID != "core-1" || lookup.runID != run.request.RunID || lookup.inputID == "" || m.textarea.Value() != "" {
		t.Fatalf("lost response was not resolved against its original ID: lookup=%+v draft=%q", lookup, m.textarea.Value())
	}
}

func TestFollowUpCapturesSelectionAndAttachesToCoreStartedRun(t *testing.T) {
	firstAccepted := make(chan struct{})
	inputCalls := make(chan runtimeInputCall, 2)
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), attachCalls: make(chan runCall, 1), submitInputCalls: inputCalls,
		submitInputFunc: func(_ context.Context, runID string, request api.SubmitInputRequest) (api.InputReceipt, error) {
			if request.Kind == "follow-up" {
				<-firstAccepted
			}
			status := "accepted"
			if request.Kind == "follow-up" {
				status = "queued"
			}
			return api.InputReceipt{CoreID: request.CoreID, SessionID: request.SessionID, RunID: runID,
				InputID: request.InputID, Kind: request.Kind, Status: status}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1", ProviderID: "before", ModelID: "model-before", Role: "role-before"})
	defer func() { cancel(); m.shutdown() }()
	m.startRun("current task")
	first := <-backend.runCalls
	defer first.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	running := api.RunState{CoreID: "core-1", RunID: first.request.RunID, Seq: 1, Phase: "running", SessionID: "session-1",
		InputAcceptance: api.InputAcceptance{FollowUp: true, Steering: true}}
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.snapshot", State: running,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Entities: map[string]api.ConversationEntity{}}})
	m.textarea.SetValue("first queued task")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF2})
	firstInput := <-inputCalls
	m.providerID, m.modelID, m.roleID = "after", "model-after", "role-after"
	m.textarea.SetValue("steer the current task")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF3})
	select {
	case call := <-inputCalls:
		t.Fatalf("later input passed the outstanding request: %+v", call)
	default:
	}
	if firstInput.request.Next == nil || firstInput.request.Next.ProviderID != "before" ||
		firstInput.request.Next.ModelID != "model-before" || firstInput.request.Next.Role != "role-before" {
		t.Fatalf("F2 selection changed after admission was queued: %+v", firstInput.request.Next)
	}
	close(firstAccepted)
	firstResult := receiveRuntimeInputResult(t, m, messages, firstInput.request.InputID)
	update(m, firstResult)
	secondInput := <-inputCalls
	if secondInput.request.Kind != "steering" || secondInput.request.Text != "steer the current task" || secondInput.request.Next != nil {
		t.Fatalf("queued F3 request did not follow F2 with its own captured text: %+v", secondInput.request)
	}
	secondResult := receiveRuntimeInputResult(t, m, messages, secondInput.request.InputID)
	update(m, secondResult)

	applied := secondResult.receipt
	applied.Status, applied.Revision = "applied", 2
	m.mergeRuntimeReceipt(applied)
	if !strings.Contains(m.footerNotice(), "steering · applied") {
		t.Fatalf("current steering missing before follow-up: %q", m.footerNotice())
	}

	terminal := api.RunState{CoreID: "core-1", RunID: first.request.RunID, Seq: 2, Phase: "terminal", SessionID: "session-1",
		NextRunID: "core-started-follow-up", Result: &api.RunResult{OK: true}}
	first.handle.finish(terminal, nil)
	receiveRunDone(t, m, messages, first.request.RunID)
	terminalSnapshot := api.ConversationSnapshot{ViewID: "run-view", Revision: 2, Entities: map[string]api.ConversationEntity{}}
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.snapshot", State: terminal, Conversation: &terminalSnapshot})
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	var attached runCall
	select {
	case attached = <-backend.attachCalls:
	case <-time.After(2 * time.Second):
		t.Fatalf("Core-started follow-up was not attached: active=%+v status=%q", m.activeRun, m.status)
	}
	defer attached.handle.finish(api.RunState{CoreID: "core-1", RunID: attached.request.RunID, Phase: "terminal"}, context.Canceled)
	if strings.Contains(m.footerNotice(), "steering") {
		t.Fatalf("Core-started follow-up inherited old steering: %q", m.footerNotice())
	}
	if attached.request.RunID != terminal.NextRunID || attached.request.CoreID != "core-1" || len(backend.runCalls) != 0 {
		t.Fatalf("TUI replayed queued text instead of attaching to Core's run: attached=%+v starts=%d", attached.request, len(backend.runCalls))
	}
}

func receiveRuntimeInputResult(t *testing.T, m *model, messages <-chan tea.Msg, inputID string) runtimeInputSubmittedMsg {
	t.Helper()
	for {
		message := receive(t, messages)
		if result, ok := message.(runtimeInputSubmittedMsg); ok && result.input.request.InputID == inputID {
			return result
		}
		update(m, message)
	}
}

func receiveRunDone(t *testing.T, m *model, messages <-chan tea.Msg, runID string) runDoneMsg {
	t.Helper()
	for {
		message := receive(t, messages)
		if result, ok := message.(runDoneMsg); ok && result.value.RunID == runID {
			update(m, result)
			return result
		}
		update(m, message)
	}
}

func TestF1AfterTerminalCancelsTheAttachedFollowUpChain(t *testing.T) {
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), attachCalls: make(chan runCall, 1),
		cancelChainCalls: make(chan inputChainCall, 1),
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	defer func() { cancel(); m.shutdown() }()
	m.startRun("current task")
	first := <-backend.runCalls
	defer first.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	running := api.RunState{CoreID: "core-1", RunID: first.request.RunID, Seq: 1, Phase: "running", SessionID: "session-1"}
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.snapshot", State: running,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Entities: map[string]api.ConversationEntity{}}})
	terminal := api.RunState{CoreID: "core-1", RunID: first.request.RunID, Seq: 2, Phase: "terminal", SessionID: "session-1",
		NextRunID: "follow-up-2", Result: &api.RunResult{OK: true}}
	first.handle.finish(terminal, nil)
	receiveRunDone(t, m, messages, first.request.RunID)
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.snapshot", State: terminal,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Revision: 2, Entities: map[string]api.ConversationEntity{}}})
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	var child runCall
	select {
	case child = <-backend.attachCalls:
	case <-time.After(2 * time.Second):
		t.Fatalf("follow-up was not attached before F1: active=%+v status=%q", m.activeRun, m.status)
	}
	if child.request.RunID != "follow-up-2" {
		t.Fatalf("attached run = %+v", child.request)
	}
	// F1 lands after the parent terminal and attach, before the child publishes
	// its first event. Cancellation still targets the same Core-owned chain.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF1})
	select {
	case target := <-backend.cancelChainCalls:
		if target.coreID != "core-1" || target.runID != "follow-up-2" {
			t.Fatalf("F1 cancelled the wrong post-terminal chain: %+v", target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("F1 did not cancel the follow-up chain")
	}
	if len(backend.runCalls) != 0 {
		t.Fatal("TUI started queued task text again after Core already started its run")
	}
	child.handle.finish(api.RunState{CoreID: "core-1", RunID: "follow-up-2", Phase: "terminal"}, context.Canceled)
}

func TestResumeLoadsHistoricalReceiptsAndStatusUpdatesReplaceTheSameInput(t *testing.T) {
	old := api.InputReceipt{CoreID: "core-old", SessionID: "session-1", RunID: "old-run", InputID: "old-input-id",
		Kind: "follow-up", Sequence: 3, Revision: 4, Status: "started", ManagedByThisCore: false, Text: "raw-body-should-not-show"}
	current := api.InputReceipt{CoreID: "core-new", SessionID: "session-1", RunID: "new-run", InputID: "current-input-id",
		Kind: "steering", Sequence: 1, Revision: 1, Status: "accepted", ManagedByThisCore: true}
	calls := 0
	backend := &fakeBackend{core: api.Core{CoreID: "core-new"}, inputsFunc: func(_ context.Context, sessionID, coreID string, after uint64, includeText bool) (api.InputReceiptPage, error) {
		calls++
		if sessionID != "session-1" || includeText {
			t.Fatalf("receipt query expanded content or changed Session: %s %t", sessionID, includeText)
		}
		if calls == 1 {
			if coreID != "" || after != 0 {
				t.Fatalf("restart did not request all Core epochs: core=%q after=%d", coreID, after)
			}
			return api.InputReceiptPage{CoreID: "core-new", SessionID: sessionID, Revision: 1, Receipts: []api.InputReceipt{old, current}}, nil
		}
		if coreID != "core-new" || after != 1 {
			t.Fatalf("incremental query did not use the current watermark: core=%q after=%d", coreID, after)
		}
		updated := current
		updated.Status, updated.Revision, updated.ManagedByThisCore = "applied", 2, true
		return api.InputReceiptPage{CoreID: "core-new", SessionID: sessionID, Revision: 2, Receipts: []api.InputReceipt{updated}}, nil
	}}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	defer cancel()
	m.sessionID = "session-1"
	m.activeRun = &activeRun{coreID: "core-new", id: "new-run", session: "session-1", inputRevision: 2}
	m.loadRuntimeInputs("session-1", true)
	m.noteRuntimeInputRevision("core-new", "session-1", 2)
	first := receive(t, messages)
	update(m, first)
	second := receive(t, messages)
	update(m, second)
	if len(m.runtimeReceipts) != 2 || m.runtimeReceipts[0].CoreID != "core-old" || m.runtimeReceipts[0].Status != "started" ||
		m.runtimeReceipts[1].CoreID != "core-new" || m.runtimeReceipts[1].InputID != "current-input-id" ||
		m.runtimeReceipts[1].Status != "applied" || m.runtimeReceipts[1].Revision != 2 {
		t.Fatalf("status revision appended a duplicate or failed to replace the old receipt: %+v", m.runtimeReceipts)
	}
	listed := formatRuntimeInputs(m.runtimeReceipts)
	for _, part := range []string{"old-input-id", "#3", "historical read-only receipt", "current-input-id", "applied"} {
		if !strings.Contains(listed, part) {
			t.Fatalf("receipt list omitted %q: %s", part, listed)
		}
	}
	if strings.Contains(listed, old.Text) {
		t.Fatalf("receipt list displayed raw input text: %s", listed)
	}
}

func TestReceiptReadCatchesUpAfterRunTerminates(t *testing.T) {
	firstPageRelease := make(chan struct{}, 1)
	firstPageStarted := make(chan struct{}, 1)
	inputCalls := make(chan runtimeInputsCall, 2)
	readCount := 0
	accepted := api.InputReceipt{CoreID: "core-1", SessionID: "session-1", RunID: "run-1", InputID: "input-1",
		Kind: "steering", Status: "accepted", Revision: 1}
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), inputsCalls: inputCalls,
		inputsFunc: func(ctx context.Context, sessionID, coreID string, after uint64, includeText bool) (api.InputReceiptPage, error) {
			readCount++
			if readCount == 1 {
				firstPageStarted <- struct{}{}
				select {
				case <-firstPageRelease:
				case <-ctx.Done():
					return api.InputReceiptPage{}, ctx.Err()
				}
				return api.InputReceiptPage{CoreID: "core-1", SessionID: sessionID, Revision: 1, Receipts: []api.InputReceipt{accepted}}, nil
			}
			if coreID != "core-1" || after != 1 {
				t.Fatalf("receipt catch-up did not continue from the delayed watermark: core=%q after=%d", coreID, after)
			}
			applied := accepted
			applied.Status, applied.Revision = "applied", 2
			return api.InputReceiptPage{CoreID: coreID, SessionID: sessionID, Revision: 2, Receipts: []api.InputReceipt{applied}}, nil
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	var run runCall
	defer func() {
		select {
		case firstPageRelease <- struct{}{}:
		default:
		}
		if run.handle != nil {
			run.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
		}
		cancel()
		m.shutdown()
	}()

	m.startRun("current task")
	run = <-backend.runCalls
	running := api.RunState{CoreID: "core-1", RunID: run.request.RunID, Seq: 1, Phase: "running", SessionID: "session-1", InputRevision: 1}
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.snapshot", State: running,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Entities: map[string]api.ConversationEntity{}}})
	<-firstPageStarted
	if call := <-inputCalls; call.coreID != "" || call.afterRevision != 0 {
		t.Fatalf("first receipt read did not load all Core epochs: %+v", call)
	}

	terminal := api.RunState{CoreID: "core-1", RunID: run.request.RunID, Seq: 2, Phase: "terminal", SessionID: "session-1",
		InputRevision: 2, Result: &api.RunResult{OK: true}}
	run.handle.finish(terminal, nil)
	receiveRunDone(t, m, messages, run.request.RunID)
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.snapshot", State: terminal,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Revision: 2, Entities: map[string]api.ConversationEntity{}}})
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	if m.activeRun != nil || m.runtimeDesiredRevision != 2 {
		t.Fatalf("terminal state did not preserve the target receipt revision: active=%v desired=%d", m.activeRun != nil, m.runtimeDesiredRevision)
	}

	firstPageRelease <- struct{}{}
	first := receiveRuntimeInputsLoaded(t, m, messages)
	update(m, first)
	secondCall := <-inputCalls
	if secondCall.coreID != "core-1" || secondCall.afterRevision != 1 {
		t.Fatalf("terminal receipt revision was not fetched incrementally: %+v", secondCall)
	}
	second := receiveRuntimeInputsLoaded(t, m, messages)
	update(m, second)
	if len(m.runtimeReceipts) != 1 || m.runtimeReceipts[0].Status != "applied" || m.runtimeReceipts[0].Revision != 2 ||
		m.runtimeWatermarks["core-1"] != 2 || m.runtimeInputStatus() != "" {
		t.Fatalf("receipt state stopped at the delayed page after run completion: receipts=%+v watermarks=%+v status=%q",
			m.runtimeReceipts, m.runtimeWatermarks, m.runtimeInputStatus())
	}
}

func receiveRuntimeInputsLoaded(t *testing.T, m *model, messages <-chan tea.Msg) runtimeInputsLoadedMsg {
	t.Helper()
	for {
		message := receive(t, messages)
		if loaded, ok := message.(runtimeInputsLoadedMsg); ok {
			return loaded
		}
		update(m, message)
	}
}

func TestSessionSwitchWaitsForInputChainSettlementAfterRunTerminal(t *testing.T) {
	chainPoll := make(chan struct{}, 1)
	settled := make(chan struct{})
	backend := &fakeBackend{
		core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1),
		cancelChainCalls: make(chan inputChainCall, 1), session: api.Session{ID: "saved", WorkspaceRoot: "/saved"},
		cancelChainFunc: func(_ context.Context, coreID, runID string) (api.InputChainState, error) {
			return api.InputChainState{CoreID: coreID, AnchorRunID: runID, Phase: "cancelling"}, nil
		},
		inputChainFunc: func(ctx context.Context, coreID, runID string) (api.InputChainState, error) {
			select {
			case chainPoll <- struct{}{}:
			default:
			}
			select {
			case <-settled:
				return api.InputChainState{CoreID: coreID, AnchorRunID: runID, Phase: "settled"}, nil
			case <-ctx.Done():
				return api.InputChainState{}, ctx.Err()
			}
		},
	}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "created"})
	defer func() { cancel(); m.shutdown() }()
	m.startRun("current task")
	run := <-backend.runCalls
	defer run.handle.finish(api.RunState{Phase: "terminal"}, context.Canceled)
	// The Session is known in this focused scenario; receipt discovery is
	// covered separately and does not participate in chain settlement.
	m.activeRun.admitted = true
	state := api.RunState{CoreID: "core-1", RunID: run.request.RunID, Seq: 1, Phase: "running", SessionID: "created"}
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.snapshot", State: state,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Entities: map[string]api.ConversationEntity{}}})
	if m.runtimeDesiredCore != "core-1" || m.runtimeDesiredSession != "created" {
		t.Fatalf("run state did not establish the receipt revision scope: core=%q session=%q", m.runtimeDesiredCore, m.runtimeDesiredSession)
	}
	m.handleCommand("/resume saved")
	for {
		msg := receive(t, messages)
		update(m, msg)
		if _, ok := msg.(sessionResolvedMsg); ok {
			break
		}
	}
	if target := <-backend.cancelChainCalls; target.coreID != "core-1" || target.runID != run.request.RunID {
		t.Fatalf("Session switch cancelled the wrong input chain: %+v", target)
	}
	<-chainPoll

	terminal := api.RunState{CoreID: "core-1", RunID: run.request.RunID, Seq: 2, Phase: "terminal", SessionID: "created",
		Result: &api.RunResult{Error: &api.Error{Code: "cancelled", Message: "cancelled"}}}
	run.handle.finish(terminal, nil)
	for {
		message := receive(t, messages)
		if _, ok := message.(runDoneMsg); ok {
			update(m, message)
			break
		}
		update(m, message)
	}
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.snapshot", State: terminal,
		Conversation: &api.ConversationSnapshot{ViewID: "run-view", Revision: 2, Entities: map[string]api.ConversationEntity{}}})
	deliverRunEvent(t, m, messages, run, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	if m.activeRun != nil || m.sessionID != "created" || m.pendingSession == nil {
		t.Fatalf("Session switched before its input chain settled: active=%v session=%q pending=%+v", m.activeRun != nil, m.sessionID, m.pendingSession)
	}
	select {
	case message := <-messages:
		if _, ok := message.(sessionLoadedMsg); ok {
			t.Fatal("Session loaded before the Core reported a settled input chain")
		}
	default:
	}

	close(settled)
	chainResult := receive(t, messages)
	if _, ok := chainResult.(runtimeInputChainSettledMsg); !ok {
		t.Fatalf("input chain did not report settlement: %T", chainResult)
	}
	update(m, chainResult)
	for {
		message := receive(t, messages)
		if _, ok := message.(sessionLoadedMsg); ok {
			update(m, message)
			break
		}
		update(m, message)
	}
	if m.sessionID != "saved" || m.workspace != "/saved" || m.runtimeDesiredCore != "" || m.runtimeDesiredSession != "" ||
		m.runtimeDesiredRevision != 0 {
		t.Fatalf("Session switch did not resume after settlement with a reset receipt revision scope: session=%q workspace=%q desired=%q/%q@%d",
			m.sessionID, m.workspace, m.runtimeDesiredCore, m.runtimeDesiredSession, m.runtimeDesiredRevision)
	}
}

func TestSteeringFooterDoesNotCarryIntoNextTurn(t *testing.T) {
	backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, runCalls: make(chan runCall, 1), submitInputCalls: make(chan runtimeInputCall, 1)}
	m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "session-1"})
	defer func() { cancel(); m.shutdown() }()
	m.startRun("first turn")
	first := <-backend.runCalls
	defer first.handle.finish(api.RunState{}, context.Canceled)
	running := api.RunState{CoreID: "core-1", RunID: first.request.RunID, SessionID: "session-1", Seq: 1, Phase: "running",
		InputAcceptance: api.InputAcceptance{Steering: true}}
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.snapshot", State: running,
		Conversation: &api.ConversationSnapshot{ViewID: "first", Entities: map[string]api.ConversationEntity{}}})
	m.textarea.SetValue("keep the deadline")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF3})
	input := <-backend.submitInputCalls
	result := receiveRuntimeInputResult(t, m, messages, input.request.InputID)
	update(m, result)
	if !strings.Contains(m.footerNotice(), "steering · accepted") {
		t.Fatalf("current acceptance missing: %q", m.footerNotice())
	}
	applied := result.receipt
	applied.Status, applied.Revision = "applied", 2
	m.mergeRuntimeReceipt(applied)
	if !strings.Contains(m.footerNotice(), "steering · applied") {
		t.Fatalf("current application missing: %q", m.footerNotice())
	}

	terminal := running
	terminal.Seq, terminal.Phase, terminal.Result = 2, "terminal", &api.RunResult{OK: true}
	first.handle.finish(terminal, nil)
	receiveRunDone(t, m, messages, first.request.RunID)
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.snapshot", State: terminal,
		Conversation: &api.ConversationSnapshot{ViewID: "first", Revision: 2, Entities: map[string]api.ConversationEntity{}}})
	deliverRunEvent(t, m, messages, first, apiclient.RunEvent{Kind: "run.terminal", State: terminal})
	if m.activeRun != nil || strings.Contains(m.footerNotice(), "steering") {
		t.Fatalf("terminal run still shown as current input: %q", m.footerNotice())
	}
	m.startRun("next turn")
	second := <-backend.runCalls
	defer second.handle.finish(api.RunState{}, context.Canceled)
	if strings.Contains(m.footerNotice(), "steering") {
		t.Fatalf("next turn inherited previous steering: %q", m.footerNotice())
	}

	// A delayed POST acknowledgement and receipt refresh still belong to the first run.
	m.runtimeInputQueue = []runtimeInputOperation{result.input}
	m.runtimeInputSending = true
	result.receipt = applied
	update(m, result)
	m.loadRuntimeInputs("session-1", true)
	page := receiveRuntimeInputsLoaded(t, m, messages)
	page.page.Receipts = []api.InputReceipt{applied}
	update(m, page)
	if strings.Contains(m.footerNotice(), "steering") {
		t.Fatalf("delayed old receipt repopulated next turn: %q", m.footerNotice())
	}
	if len(m.runtimeReceipts) != 1 || m.runtimeReceipts[0] != applied || input.request.Text != "keep the deadline" ||
		!strings.Contains(formatRuntimeInputs(m.runtimeReceipts), "applied") {
		t.Fatalf("display filtering changed the input or receipt history: %+v", m.runtimeReceipts)
	}

	running.RunID, running.Seq = second.request.RunID, 1
	deliverRunEvent(t, m, messages, second, apiclient.RunEvent{Kind: "run.snapshot", State: running,
		Conversation: &api.ConversationSnapshot{ViewID: "second", Entities: map[string]api.ConversationEntity{}}})
	m.textarea.SetValue("new steering")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyF3})
	newInput := <-backend.submitInputCalls
	update(m, receiveRuntimeInputResult(t, m, messages, newInput.request.InputID))
	if !strings.Contains(m.footerNotice(), "steering · accepted") {
		t.Fatalf("next turn cannot show its own acceptance: %q", m.footerNotice())
	}
}

func TestSteeringFooterOnSessionRestore(t *testing.T) {
	for _, active := range []bool{false, true} {
		name := "idle"
		if active {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			current := api.InputReceipt{CoreID: "core-1", SessionID: "saved", RunID: "current-run", InputID: "current-input",
				Kind: "steering", Status: "accepted", Revision: 1}
			old := current
			old.RunID, old.InputID, old.Status, old.Revision = "old-run", "old-input", "applied", 9
			backend := &fakeBackend{core: api.Core{CoreID: "core-1"}, session: api.Session{ID: "saved"}, attachCalls: make(chan runCall, 1),
				inputsFunc: func(context.Context, string, string, uint64, bool) (api.InputReceiptPage, error) {
					return api.InputReceiptPage{CoreID: "core-1", SessionID: "saved", Revision: 9, Receipts: []api.InputReceipt{current, old}}, nil
				}}
			if active {
				backend.activeSessionRun = &api.RunState{CoreID: "core-1", SessionID: "saved", RunID: "current-run", Phase: "running"}
			}
			m, messages, cancel := testModel(backend, Options{Core: backend.core, SessionID: "saved"})
			defer func() { cancel(); m.shutdown() }()
			m.loadSession("saved", m.sessionGeneration)
			loaded := receiveUntil(t, m, messages, func(msg tea.Msg) bool { _, ok := msg.(sessionLoadedMsg); return ok })
			update(m, loaded)
			if active {
				call := <-backend.attachCalls
				defer call.handle.finish(api.RunState{}, context.Canceled)
			}
			update(m, receiveRuntimeInputsLoaded(t, m, messages))
			want := ""
			if active {
				want = "steering · accepted"
			}
			if m.footerNotice() != want || len(m.runtimeReceipts) != 2 {
				t.Fatalf("restore mixed current status and history: notice=%q receipts=%+v", m.footerNotice(), m.runtimeReceipts)
			}
		})
	}
}
