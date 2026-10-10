// Package httpapi exposes the Core operations as HTTP JSON and SSE.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/history"
)

const apiPrefix = "/api/v1"

// Server maps public requests to the existing Service and Core run manager.
// The caller continues to own the history store passed to Service.
type Server struct {
	service *app.Service
	runs    *corerun.Manager

	authMu     sync.Mutex
	authClosed bool
	authWG     sync.WaitGroup
	closeOnce  sync.Once
	closeErr   error
}

func New(service *app.Service, runs *corerun.Manager) *Server {
	return &Server{service: service, runs: runs}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, apiPrefix), "/")
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		writeError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] == "core" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, s.runs.Core())
		return
	}
	if len(parts) == 1 && parts[0] == "runs" {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.startRun(w, r)
		return
	}
	if len(parts) >= 2 && parts[0] == "runs" {
		s.runRoute(w, r, parts[1:])
		return
	}
	if len(parts) == 1 && parts[0] == "sessions" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.listSessions(w, r)
		return
	}
	if len(parts) >= 2 && parts[0] == "sessions" {
		s.sessionRoute(w, r, parts[1:])
		return
	}
	if len(parts) == 2 && parts[0] == "executions" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.inspect(w, r, parts[1])
		return
	}
	if len(parts) == 1 && parts[0] == "roles" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.roles(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "providers" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.providers(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "models" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.models(w, r)
		return
	}
	if len(parts) == 2 && parts[0] == "auth" {
		s.authRoute(w, r, parts[1])
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "API route not found")
}

func (s *Server) startRun(w http.ResponseWriter, r *http.Request) {
	var request api.StartRunRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.CoreID == "" || request.RunID == "" || request.Task == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "coreId, runId, and task are required")
		return
	}
	workspace, err := s.resolveWorkspace(r.Context(), request.SessionID, request.Workspace)
	if err != nil {
		writeFailure(w, err)
		return
	}
	request.Workspace = workspace
	state, created, err := s.runs.Start(request)
	if err != nil {
		writeFailure(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(w, status, state)
}

func (s *Server) runRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		state, err := s.runs.Get(r.URL.Query().Get("coreId"), parts[0])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		state, err := s.runs.Cancel(r.URL.Query().Get("coreId"), parts[0])
		if err != nil {
			writeFailure(w, err)
			return
		}
		status := http.StatusAccepted
		if state.Phase == "terminal" {
			status = http.StatusOK
		}
		writeJSON(w, status, state)
		return
	}
	if len(parts) == 2 && parts[1] == "events" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.events(w, r, parts[0])
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "run route not found")
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, runID string) {
	subscription, err := s.runs.Subscribe(r.URL.Query().Get("coreId"), runID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer subscription.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unavailable", "HTTP streaming is unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	state, err := subscription.Snapshot()
	if err != nil {
		writeFailure(w, err)
		return
	}
	if err = writeEvent(w, flusher, "run.snapshot", state); err != nil {
		return
	}
	if state.Phase == "terminal" {
		_ = writeEvent(w, flusher, "run.terminal", state)
		return
	}
	lastSeq := state.Seq
	for {
		select {
		case <-r.Context().Done():
			return
		case _, open := <-subscription.Notify:
			state, err = subscription.Snapshot()
			if err != nil {
				return
			}
			if state.Phase == "terminal" {
				if writeEvent(w, flusher, "run.snapshot", state) != nil {
					return
				}
				_ = writeEvent(w, flusher, "run.terminal", state)
				return
			}
			if !open {
				return
			}
			if state.Seq == lastSeq {
				continue
			}
			if writeEvent(w, flusher, "run.update", state) != nil {
				return
			}
			lastSeq = state.Seq
		}
	}
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.service.Sessions(r.Context())
	if err != nil {
		writeFailure(w, err)
		return
	}
	result := make([]api.Session, len(sessions))
	for i := range sessions {
		result[i] = sessionDTO(sessions[i])
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) sessionRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 1 {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		session, err := s.service.Session(r.Context(), parts[0])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sessionDTO(session))
		return
	}
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "not_found", "Session route not found")
		return
	}
	switch parts[1] {
	case "conversation":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		from, err := queryInt64(r, "fromTurn", 0)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		to, err := queryInt64(r, "toTurn", -1)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		messages, err := s.service.Conversation(r.Context(), parts[0], from, to)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, messagesDTO(messages))
	case "executions":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		executions, err := s.service.Executions(r.Context(), parts[0])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, executionsDTO(executions))
	default:
		writeError(w, http.StatusNotFound, "not_found", "Session route not found")
	}
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request, executionID string) {
	from, err := queryInt64(r, "fromOrdinal", 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	to, err := queryInt64(r, "toOrdinal", -1)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	inspection, err := s.service.Inspect(r.Context(), executionID, from, to)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inspectionDTO(inspection))
}

func (s *Server) roles(w http.ResponseWriter, r *http.Request) {
	workspace, err := s.resolveWorkspace(r.Context(), r.URL.Query().Get("sessionId"), r.URL.Query().Get("workspace"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	catalog, err := s.service.Roles(r.Context(), workspace)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rolesDTO(catalog))
}

func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	workspace, err := s.resolveWorkspace(r.Context(), r.URL.Query().Get("sessionId"), r.URL.Query().Get("workspace"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	definitions, err := s.service.Providers(r.Context(), workspace)
	if err != nil {
		writeFailure(w, err)
		return
	}
	result := make([]api.ProviderDefinition, len(definitions))
	for i := range definitions {
		result[i] = providerDTO(definitions[i])
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	workspace, err := s.resolveWorkspace(r.Context(), r.URL.Query().Get("sessionId"), r.URL.Query().Get("workspace"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	models, err := s.service.Models(r.Context(), workspace, r.URL.Query().Get("providerId"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	result := make([]api.ModelInfo, len(models))
	for i := range models {
		result[i] = modelDTO(models[i])
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) authRoute(w http.ResponseWriter, r *http.Request, operation string) {
	switch operation {
	case "status":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		workspace, err := s.resolveWorkspace(r.Context(), r.URL.Query().Get("sessionId"), r.URL.Query().Get("workspace"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		status, err := s.service.LoginStatus(r.Context(), workspace, r.URL.Query().Get("providerId"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, statusDTO(status))
	case "login":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var request authRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		workspace, err := s.resolveWorkspace(r.Context(), request.SessionID, request.Workspace)
		if err != nil {
			writeFailure(w, err)
			return
		}
		ctx, done, err := s.beginAuthOperation()
		if err != nil {
			writeFailure(w, err)
			return
		}
		defer done()
		providerID, authProfile, err := s.service.ResolveLoginTarget(ctx, workspace, request.ProviderID)
		if err != nil {
			writeFailure(w, err)
			return
		}
		login, err := s.service.BeginLogin(ctx, workspace, providerID)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, api.Login{AuthorizationURL: login.AuthorizationURL, RedirectURI: login.RedirectURI,
			Workspace: workspace, ProviderID: providerID, AuthProfile: authProfile})
	case "complete":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var request authRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		workspace := request.Workspace
		if workspace == "" {
			workspace = s.runs.Core().Workspace
		}
		ctx, done, err := s.beginAuthOperation()
		if err != nil {
			writeFailure(w, err)
			return
		}
		defer done()
		status, err := s.service.CompleteLogin(ctx, workspace, request.ProviderID, request.CallbackURL)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, statusDTO(status))
	default:
		writeError(w, http.StatusNotFound, "not_found", "auth route not found")
	}
}

type authRequest struct {
	SessionID   string `json:"sessionId,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	ProviderID  string `json:"providerId,omitempty"`
	CallbackURL string `json:"callbackUrl,omitempty"`
}

func (s *Server) resolveWorkspace(ctx context.Context, sessionID, requested string) (string, error) {
	if sessionID != "" {
		session, err := s.service.Session(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if session.WorkspaceRoot != "" {
			return session.WorkspaceRoot, nil
		}
	}
	if requested != "" {
		return requested, nil
	}
	return s.runs.Core().Workspace, nil
}

func (s *Server) beginAuthOperation() (context.Context, func(), error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.authClosed {
		return nil, nil, &api.Error{Code: "core_closed", Message: "Core is closing"}
	}
	ctx := s.runs.Context()
	if err := ctx.Err(); err != nil {
		return nil, nil, &api.Error{Code: "core_closed", Message: "Core is closing"}
	}
	s.authWG.Add(1)
	return ctx, s.authWG.Done, nil
}

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.authMu.Lock()
		s.authClosed = true
		s.authMu.Unlock()
		_ = s.runs.Close()
		s.authWG.Wait()
		s.closeErr = s.service.Close()
	})
	return s.closeErr
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	return false
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func queryInt64(r *http.Request, name string, fallback int64) (int64, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return parsed, nil
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, name string, state api.RunState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\nid: %s/%s/%d\ndata: %s\n\n", name, state.CoreID, state.RunID, state.Seq, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, api.ErrorResponse{Error: &api.Error{Code: code, Message: message}})
}

func writeFailure(w http.ResponseWriter, err error) {
	var coded *api.Error
	if errors.As(err, &coded) {
		status := http.StatusInternalServerError
		switch coded.Code {
		case "run_not_found":
			status = http.StatusNotFound
		case "core_changed", "run_id_conflict", "session_busy":
			status = http.StatusConflict
		case "core_closed", "core_closing":
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, api.ErrorResponse{Error: coded})
		return
	}
	if errors.Is(err, history.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, "operation_failed", err.Error())
}
