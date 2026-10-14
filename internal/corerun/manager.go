// Package corerun owns HTTP-started runs independently of request lifetimes.
package corerun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/conversation"
	"hjh/internal/executiondata"
)

type runTerminal struct {
	ok             bool
	apiError       *api.Error
	executionSaved bool
	executionID    string
	resultOK       bool
	resultOutcome  string
	stopReason     string
	resultError    string
	diagnostic     *contracts.FailureDiagnostic
	fallback       *api.AgentResult
}

type runInput struct {
	request              api.StartRunRequest
	owner                *executiondata.Owner
	chain                *inputChain
	taskContentDigest    string
	followUpReceipt      *contracts.InputReceipt
	candidateExecutionID string
	recall               *contracts.RecallReference
}

type managedRun struct {
	fingerprint        string
	sessionReservation string
	state              api.RunState
	terminal           *runTerminal
	cancel             context.CancelFunc
	owner              *executiondata.Owner
	chain              *inputChain
	subscribers        map[*runStateSubscriber]chan struct{}
	viewConsumers      int // protected by Manager.mu

	resultMu     sync.Mutex
	resultFlight *terminalReadFlight

	viewMu       sync.Mutex
	view         *conversationView
	viewCreating chan struct{}
}

// conversationView keeps the heavy materialized view beside its lightweight
// execution-data subscription so both can be released together.
type conversationView struct {
	view *conversation.View
	data *executiondata.Subscription
}

type sessionViewFlight struct {
	ready       chan struct{}
	refs        int
	view        *conversation.View
	data        *executiondata.Subscription
	err         error
	invalidated bool // protected by Manager.mu
}

type Manager struct {
	mu               sync.Mutex
	service          *app.Service
	core             api.Core
	ctx              context.Context
	cancel           context.CancelFunc
	runs             map[string]*managedRun
	sessions         map[string]string
	deletingSessions map[string]bool
	sessionWrites    map[string]int
	inputSequences   map[string]uint64
	sessionVersions  map[string]uint64
	sessionViews     map[string]*sessionViewFlight
	recallPending    map[string]pendingRecall
	recallRevision   map[string]uint64
	recallOperations map[string]*recallOperation
	recallBySession  map[string]*recallOperation
	closing          bool
	wg               sync.WaitGroup
}

func New(service *app.Service, workspace string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{service: service, core: api.Core{CoreID: uuid.NewString(), APIVersion: "v2", Workspace: workspace},
		ctx: ctx, cancel: cancel, runs: map[string]*managedRun{}, sessions: map[string]string{}, deletingSessions: map[string]bool{},
		sessionWrites: map[string]int{}, inputSequences: map[string]uint64{},
		sessionVersions: map[string]uint64{}, sessionViews: map[string]*sessionViewFlight{}, recallPending: map[string]pendingRecall{},
		recallRevision: map[string]uint64{}, recallOperations: map[string]*recallOperation{}, recallBySession: map[string]*recallOperation{}}
}

func (m *Manager) Core() api.Core           { return m.core }
func (m *Manager) Context() context.Context { return m.ctx }

func (m *Manager) coreError(id string) error {
	if id != m.core.CoreID {
		return &api.Error{Code: "core_changed", Message: "Core has restarted; check the current Core before submitting again"}
	}
	return nil
}

// Start registers identity and Session ownership before any preparation starts.
func (m *Manager) Start(request api.StartRunRequest) (api.RunState, bool, error) {
	return m.StartContext(context.Background(), request)
}

func (m *Manager) StartContext(ctx context.Context, request api.StartRunRequest) (api.RunState, bool, error) {
	if request.Workspace == "" && request.SessionID == "" {
		request.Workspace = m.core.Workspace
	}
	fingerprint, err := requestFingerprint(request)
	if err != nil {
		return api.RunState{}, false, err
	}
	if request.MaxSteps != nil {
		value := *request.MaxSteps
		request.MaxSteps = &value
	}
	if request.FromTurn != nil {
		value := *request.FromTurn
		request.FromTurn = &value
	}

	m.mu.Lock()
	if err := m.coreError(request.CoreID); err != nil {
		m.mu.Unlock()
		return api.RunState{}, false, err
	}
	if existing := m.runs[request.RunID]; existing != nil {
		if m.deletingSessions[existing.sessionReservation] {
			m.mu.Unlock()
			return api.RunState{}, false, sessionBusy("", "Session deletion is in progress; retry after it completes")
		}
		if existing.fingerprint != fingerprint {
			m.mu.Unlock()
			return api.RunState{}, false, &api.Error{Code: "run_id_conflict", Message: "runId already identifies a different request"}
		}
		state := m.stateLocked(existing)
		m.mu.Unlock()
		if state.Phase == "terminal" {
			state, err = m.resolveTerminal(ctx, existing, state)
		}
		return state, false, err
	}
	if m.closing {
		m.mu.Unlock()
		return api.RunState{}, false, &api.Error{Code: "core_closing", Message: "Core is shutting down"}
	}
	reserved := request.SessionID
	if reserved == "" {
		reserved = uuid.NewString()
	}
	if m.deletingSessions[reserved] {
		m.mu.Unlock()
		return api.RunState{}, false, sessionBusy("", "Session deletion is in progress; retry after it completes")
	}
	if active := m.sessions[reserved]; active != "" {
		m.mu.Unlock()
		return api.RunState{}, false, &api.Error{Code: "session_busy", Message: "Session already has an active run", ActiveRunID: active}
	}
	if operation := m.recallBySession[reserved]; operation != nil {
		m.mu.Unlock()
		return api.RunState{}, false, &api.Error{Code: "session_busy", Message: "Session is applying a recall operation"}
	}

	ctx, cancel := context.WithCancel(m.ctx)
	owner := executiondata.NewOwner()
	chain := newInputChain(m, request.RunID, reserved, request.Workspace)
	chain.sequence = m.inputSequences[reserved]
	candidateExecutionID := uuid.NewString()
	var recall *contracts.RecallReference
	if pending, ok := m.recallPending[reserved]; ok && pending.claimedByRunID == "" {
		pending.claimedByRunID = request.RunID
		m.recallPending[reserved] = pending
		m.recallRevision[reserved]++
		reference := pending.reference
		recall = &reference
	}
	r := &managedRun{fingerprint: fingerprint, sessionReservation: reserved, cancel: cancel, owner: owner, chain: chain,
		state:       api.RunState{CoreID: m.core.CoreID, RunID: request.RunID, Phase: "preparing", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SessionID: request.SessionID},
		subscribers: map[*runStateSubscriber]chan struct{}{}}
	m.runs[request.RunID] = r
	m.sessions[reserved] = request.RunID
	m.sessionVersions[reserved]++
	m.wg.Add(1)
	input := runInput{request: request, owner: owner, chain: chain, candidateExecutionID: candidateExecutionID, recall: recall}
	state := m.stateLocked(r)
	m.mu.Unlock()
	go m.execute(ctx, r, input)
	return state, true, nil
}

func requestFingerprint(request api.StartRunRequest) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode run request identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (m *Manager) execute(ctx context.Context, r *managedRun, input runInput) {
	defer func() {
		r.cancel()
		m.inputRunWorkerFinished(input.chain)
		m.wg.Done()
	}()
	request := input.request
	workspace := request.Workspace
	if workspace == "" {
		workspace = m.core.Workspace
	}
	newID := ""
	if request.SessionID == "" {
		newID = r.sessionReservation
	}
	result, runErr := m.service.Run(ctx, app.RunRequest{Task: request.Task, SessionID: request.SessionID, Workspace: workspace, Role: request.Role,
		ProviderID: request.ProviderID, ModelID: request.ModelID, Effort: request.Effort, AuthProfile: request.AuthProfile,
		MaxSteps: request.MaxSteps, FromTurn: request.FromTurn, NewSessionID: newID, ExecutionDataOwner: input.owner,
		TaskContentDigest: input.taskContentDigest, Steering: input.chain.steeringSource(request.RunID),
		AdmissionExecutionID: input.candidateExecutionID, Recall: input.recall,
		OnAdmission: func(admission contracts.ExecutionAdmission) {
			m.recallAdmitted(r, input, admission)
		},
		OnSession: func(id string) {
			m.mu.Lock()
			r.state.SessionID = id
			m.changedLocked(r)
			m.mu.Unlock()
		},
		Progress: func(observation contracts.Observation) error {
			m.progress(r, observation)
			if input.followUpReceipt != nil && observation.Kind == "execution" && observation.Name == "run" &&
				(observation.Phase == "started" || observation.Phase == "start") && observation.ParentExecutionID == "" {
				if err := m.followUpStarted(input.chain, *input.followUpReceipt, observation.ExecutionID); err != nil {
					return err
				}
				input.followUpReceipt.Status = "started"
				input.followUpReceipt.StartedExecutionID = observation.ExecutionID
			}
			return nil
		}})
	m.releaseRecallClaim(r, input)

	if inputErr := m.finishInputRun(r, input, result, runErr); inputErr != nil {
		runErr = errors.Join(runErr, inputErr)
	}

	terminal := &runTerminal{ok: runErr == nil && result.Result.OK, executionID: result.ExecutionID,
		resultOK: result.Result.OK, resultOutcome: result.Result.Outcome, stopReason: result.Result.StopReason, resultError: result.Result.Error, diagnostic: contracts.FailureFor(runErr)}
	if runErr != nil {
		code := "run_failed"
		if errors.Is(runErr, context.Canceled) || ctx.Err() != nil {
			code = "run_cancelled"
		}
		terminal.apiError = &api.Error{Code: code, Message: runErr.Error(), Diagnostic: contracts.CloneFailure(terminal.diagnostic)}
	}
	if result.ExecutionID != "" {
		saved, loadErr := m.service.TerminalResult(context.Background(), result.ExecutionID)
		terminal.executionSaved = loadErr == nil && len(saved.RuntimeOutcome) != 0
		if !terminal.executionSaved {
			terminal.fallback = &api.AgentResult{OK: result.Result.OK, Task: result.Result.Task, Outcome: result.Result.Outcome,
				StopReason: result.Result.StopReason, FinalText: result.Result.FinalText, Error: result.Result.Error, Diagnostic: contracts.CloneFailure(terminal.diagnostic)}
		}
	}

	m.mu.Lock()
	if result.SessionID != "" {
		r.state.SessionID = result.SessionID
	}
	if result.ExecutionID != "" {
		r.state.ExecutionID = result.ExecutionID
	}
	r.terminal = terminal
	r.state.Phase = "terminal"
	if m.sessions[r.sessionReservation] == r.state.RunID {
		delete(m.sessions, r.sessionReservation)
		m.sessionVersions[r.sessionReservation]++
	}
	m.changedLocked(r)
	shouldRelease := r.viewConsumers == 0
	m.mu.Unlock()
	if shouldRelease {
		m.releaseViewIfUnused(r)
	}
}

func (m *Manager) progress(r *managedRun, observation contracts.Observation) {
	if observation.Kind != "execution" || observation.Name != "run" || (observation.Phase != "started" && observation.Phase != "start") || observation.ParentExecutionID != "" {
		return
	}
	var input struct {
		SessionID string                   `json:"sessionId"`
		Model     contracts.ModelSelection `json:"model"`
		Role      string                   `json:"role"`
		Context   *contracts.Orientation   `json:"context"`
	}
	_ = json.Unmarshal(observation.Input, &input)
	m.mu.Lock()
	if r.state.Phase == "preparing" {
		r.state.Phase = "running"
	}
	r.state.ExecutionID = observation.ExecutionID
	if input.SessionID != "" {
		r.state.SessionID = input.SessionID
	}
	if input.Role != "" {
		r.state.ActualRole = input.Role
	}
	if input.Context != nil {
		orientation := input.Context.Clone()
		r.state.ActualContext = &orientation
	}
	if input.Model != (contracts.ModelSelection{}) {
		selection := input.Model
		r.state.ActualSelection = &selection
	}
	if observation.ExecutionID != "" {
		r.state.InputAcceptance = r.chain.activate(r.state.RunID)
	}
	m.changedLocked(r)
	m.mu.Unlock()
}

func (m *Manager) changedLocked(r *managedRun) {
	r.state.Seq++
	for _, notify := range r.subscribers {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) findLocked(coreID, runID string) (*managedRun, error) {
	if err := m.coreError(coreID); err != nil {
		return nil, err
	}
	r := m.runs[runID]
	if r == nil {
		return nil, &api.Error{Code: "run_not_found", Message: "runId was not registered in this Core"}
	}
	if m.deletingSessions[r.sessionReservation] {
		return nil, sessionBusy("", "Session deletion is in progress; retry after it completes")
	}
	return r, nil
}

func (m *Manager) Get(coreID, runID string) (api.RunState, error) {
	return m.GetContext(context.Background(), coreID, runID)
}

func (m *Manager) GetContext(ctx context.Context, coreID, runID string) (api.RunState, error) {
	m.mu.Lock()
	r, err := m.findLocked(coreID, runID)
	if err != nil {
		m.mu.Unlock()
		return api.RunState{}, err
	}
	state := m.stateLocked(r)
	m.mu.Unlock()
	if state.Phase == "terminal" {
		return m.resolveTerminal(ctx, r, state)
	}
	return state, nil
}

func (m *Manager) Cancel(coreID, runID string) (api.RunState, error) {
	return m.CancelContext(context.Background(), coreID, runID)
}

func (m *Manager) CancelContext(ctx context.Context, coreID, runID string) (api.RunState, error) {
	m.mu.Lock()
	r, err := m.findLocked(coreID, runID)
	if err != nil {
		m.mu.Unlock()
		return api.RunState{}, err
	}
	if r.state.Phase != "terminal" {
		r.cancel()
		r.chain.mu.Lock()
		r.chain.steeringOpen = false
		r.chain.followUpOpen = false
		r.chain.signalLocked()
		r.chain.mu.Unlock()
		if r.state.Phase != "cancelling" || r.state.InputAcceptance != (api.InputAcceptance{}) {
			r.state.Phase = "cancelling"
			r.state.InputAcceptance = api.InputAcceptance{}
			m.changedLocked(r)
		}
	}
	state := m.stateLocked(r)
	m.mu.Unlock()
	if state.Phase == "terminal" {
		return m.resolveTerminal(ctx, r, state)
	}
	return state, nil
}

func (m *Manager) resolveTerminal(ctx context.Context, r *managedRun, state api.RunState) (api.RunState, error) {
	m.mu.Lock()
	terminal := r.terminal
	if terminal != nil {
		copy := *terminal
		if terminal.apiError != nil {
			apiError := *terminal.apiError
			apiError.Diagnostic = contracts.CloneFailure(apiError.Diagnostic)
			copy.apiError = &apiError
		}
		if terminal.fallback != nil {
			fallback := *terminal.fallback
			fallback.Diagnostic = contracts.CloneFailure(fallback.Diagnostic)
			copy.fallback = &fallback
		}
		terminal = &copy
	}
	m.mu.Unlock()
	if terminal == nil {
		return state, nil
	}
	result := &api.RunResult{OK: terminal.ok, SnapshotSave: m.service.SnapshotStatus(terminal.executionID),
		FailedChildSnapshotExecutionIDs: m.service.FailedChildSnapshotExecutionIDs(terminal.executionID)}
	if terminal.executionSaved {
		savedResult, err := r.readTerminal(ctx, func() (*api.AgentResult, error) {
			saved, err := m.service.TerminalResult(m.ctx, terminal.executionID)
			if err != nil {
				return nil, err
			}
			var agentResult api.AgentResult
			if err := json.Unmarshal(saved.RuntimeOutcome, &agentResult); err != nil {
				return nil, fmt.Errorf("decode saved terminal result: %w", err)
			}
			agentResult.Task = saved.Task
			return &agentResult, nil
		})
		if err != nil {
			return api.RunState{}, err
		}
		agentResult := *savedResult
		// Core finalization can fail after EndExecution committed. Preserve the
		// final run values while retrieving the body from its durable locator.
		agentResult.OK = terminal.resultOK
		agentResult.Diagnostic = contracts.CloneFailure(terminal.diagnostic)
		if terminal.resultOutcome != "" {
			agentResult.Outcome = terminal.resultOutcome
		}
		if terminal.stopReason != "" {
			agentResult.StopReason = terminal.stopReason
		}
		if terminal.resultError != "" {
			agentResult.Error = terminal.resultError
		}
		result.AgentResult = &agentResult
	} else if terminal.fallback != nil {
		fallback := *terminal.fallback
		fallback.Diagnostic = contracts.CloneFailure(fallback.Diagnostic)
		result.AgentResult = &fallback
	}
	if terminal.apiError != nil {
		apiError := *terminal.apiError
		apiError.Diagnostic = contracts.CloneFailure(apiError.Diagnostic)
		result.Error = &apiError
	}
	state.Result = result
	return state, nil
}

func (m *Manager) stateLocked(r *managedRun) api.RunState {
	s := r.state
	if s.ActualSelection != nil {
		v := *s.ActualSelection
		s.ActualSelection = &v
	}
	if s.Result != nil {
		v := *s.Result
		s.Result = &v
		if v.AgentResult != nil {
			a := *v.AgentResult
			a.Diagnostic = contracts.CloneFailure(a.Diagnostic)
			s.Result.AgentResult = &a
		}
		if v.Error != nil {
			e := *v.Error
			e.Diagnostic = contracts.CloneFailure(e.Diagnostic)
			s.Result.Error = &e
		}
	}
	return s
}

// Close excludes new admission, then cancels and joins every managed run.
func (m *Manager) Close() error {
	m.beginShutdown()
	m.wg.Wait()
	m.mu.Lock()
	runs := make([]*managedRun, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	m.mu.Unlock()
	for _, r := range runs {
		m.releaseView(r)
	}
	return nil
}
