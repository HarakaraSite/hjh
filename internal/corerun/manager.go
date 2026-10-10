// Package corerun owns HTTP-started runs independently of request lifetimes.
package corerun

import (
	"context"
	"errors"
	"reflect"
	"sync"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
)

type managedRun struct {
	request            api.StartRunRequest
	sessionReservation string
	state              api.RunState
	projection         *projection
	cancel             context.CancelFunc
	subscribers        map[*Subscription]chan struct{}
}

type Manager struct {
	mu       sync.Mutex
	service  *app.Service
	core     api.Core
	ctx      context.Context
	cancel   context.CancelFunc
	runs     map[string]*managedRun
	sessions map[string]string
	closing  bool
	wg       sync.WaitGroup
}

func New(service *app.Service, workspace string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{service: service, core: api.Core{CoreID: uuid.NewString(), APIVersion: "v1", Workspace: workspace},
		ctx: ctx, cancel: cancel, runs: map[string]*managedRun{}, sessions: map[string]string{}}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.coreError(request.CoreID); err != nil {
		return api.RunState{}, false, err
	}
	if existing := m.runs[request.RunID]; existing != nil {
		if !reflect.DeepEqual(existing.request, request) {
			return api.RunState{}, false, &api.Error{Code: "run_id_conflict", Message: "runId already identifies a different request"}
		}
		return m.stateLocked(existing), false, nil
	}
	if m.closing {
		return api.RunState{}, false, &api.Error{Code: "core_closing", Message: "Core is shutting down"}
	}
	reserved := request.SessionID
	if reserved == "" {
		reserved = uuid.NewString()
	}
	if active := m.sessions[reserved]; active != "" {
		return api.RunState{}, false, &api.Error{Code: "session_busy", Message: "Session already has an active run", ActiveRunID: active}
	}
	// Preserve pointer-valued options independently of the caller's DTO.
	if request.MaxSteps != nil {
		v := *request.MaxSteps
		request.MaxSteps = &v
	}
	if request.FromTurn != nil {
		v := *request.FromTurn
		request.FromTurn = &v
	}
	ctx, cancel := context.WithCancel(m.ctx)
	r := &managedRun{request: request, sessionReservation: reserved, cancel: cancel, projection: newProjection(),
		state: api.RunState{CoreID: m.core.CoreID, RunID: request.RunID, Phase: "preparing", SessionID: request.SessionID}, subscribers: map[*Subscription]chan struct{}{}}
	m.runs[request.RunID] = r
	m.sessions[reserved] = request.RunID
	m.wg.Add(1)
	go m.execute(ctx, r)
	return m.stateLocked(r), true, nil
}

func (m *Manager) execute(ctx context.Context, r *managedRun) {
	defer m.wg.Done()
	defer r.cancel()
	request := r.request
	workspace := request.Workspace
	if workspace == "" {
		workspace = m.core.Workspace
	}
	newID := ""
	if request.SessionID == "" {
		newID = r.sessionReservation
	}
	result, err := m.service.Run(ctx, app.RunRequest{Task: request.Task, SessionID: request.SessionID, Workspace: workspace, Role: request.Role,
		ProviderID: request.ProviderID, ModelID: request.ModelID, Effort: request.Effort, AuthProfile: request.AuthProfile,
		MaxSteps: request.MaxSteps, FromTurn: request.FromTurn, NewSessionID: newID,
		OnSession: func(id string) { m.mu.Lock(); defer m.mu.Unlock(); r.state.SessionID = id; m.changedLocked(r) },
		Progress: func(o contracts.Observation) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			r.projection.observe(&r.state, o)
			m.changedLocked(r)
			return nil
		}})
	m.mu.Lock()
	defer m.mu.Unlock()
	if result.SessionID != "" {
		r.state.SessionID = result.SessionID
	}
	if result.ExecutionID != "" {
		r.state.ExecutionID = result.ExecutionID
	}
	terminal := &api.RunResult{OK: err == nil && result.Result.OK}
	if result.ExecutionID != "" {
		v := result.Result
		terminal.AgentResult = &api.AgentResult{OK: v.OK, Task: v.Task, Outcome: v.Outcome, StopReason: v.StopReason, FinalText: v.FinalText, Error: v.Error}
	}
	if err != nil {
		code := "run_failed"
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			code = "run_cancelled"
		}
		terminal.Error = &api.Error{Code: code, Message: err.Error()}
	}
	r.state.Phase = "terminal"
	r.state.Result = terminal
	r.projection = nil
	delete(m.sessions, r.sessionReservation)
	m.changedLocked(r)
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
	return r, nil
}

// Get always exposes the retained terminal result, without requiring DB reads.
func (m *Manager) Get(coreID, runID string) (api.RunState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.findLocked(coreID, runID)
	if err != nil {
		return api.RunState{}, err
	}
	return m.stateLocked(r), nil
}

func (m *Manager) Cancel(coreID, runID string) (api.RunState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.findLocked(coreID, runID)
	if err != nil {
		return api.RunState{}, err
	}
	if r.state.Phase != "terminal" {
		r.cancel()
		if r.state.Phase != "cancelling" {
			r.state.Phase = "cancelling"
			m.changedLocked(r)
		}
	}
	return m.stateLocked(r), nil
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
			s.Result.AgentResult = &a
		}
		if v.Error != nil {
			e := *v.Error
			s.Result.Error = &e
		}
	}
	s.Texts = []api.TextState{}
	s.Operations = []api.OperationState{}
	s.Executions = []api.ExecutionState{}
	if r.projection != nil {
		r.projection.fill(&s)
	}
	return s
}

// Close excludes new admission, then cancels and joins every managed run.
// The caller closes authentication operations, Service and its Store afterward.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closing = true
	m.cancel()
	for _, r := range m.runs {
		if r.state.Phase != "terminal" {
			r.cancel()
			r.state.Phase = "cancelling"
			m.changedLocked(r)
		}
	}
	m.mu.Unlock()
	m.wg.Wait()
	return nil
}
