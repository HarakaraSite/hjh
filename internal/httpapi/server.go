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

const apiPrefix = "/api/v2"

// Server maps public requests to the existing Service and Core run manager.
// The caller continues to own the history store passed to Service.
type Server struct {
	service *app.Service
	runs    *corerun.Manager

	shutdownOnce sync.Once
	shutdownCh   chan struct{}

	closeOnce sync.Once
	closeErr  error
}

func New(service *app.Service, runs *corerun.Manager) *Server {
	return &Server{service: service, runs: runs, shutdownCh: make(chan struct{})}
}

// ShutdownRequested is closed after a shutdown request has been acknowledged.
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdownCh }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, apiPrefix), "/")
	if !strings.HasPrefix(r.URL.Path, apiPrefix) {
		writeError(w, http.StatusNotFound, "not_found", "API route not found")
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) == 2 && parts[0] == "core" && parts[1] == "shutdown" {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		s.shutdown(w, r)
		return
	}
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
	if len(parts) == 4 && parts[0] == "executions" && parts[2] == "records" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		ordinal, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "record ordinal must be an integer")
			return
		}
		record, err := s.service.Record(r.Context(), parts[1], ordinal)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, recordDTO(record))
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
	if len(parts) == 1 && parts[0] == "model-favorites" {
		s.modelFavorites(w, r)
		return
	}
	if len(parts) == 1 && parts[0] == "selection" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		s.selection(w, r)
		return
	}
	if len(parts) >= 2 && parts[0] == "auth" {
		s.authRoute(w, r, parts[1:])
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
	state, created, err := s.runs.StartContext(r.Context(), request)
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

func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CoreID string `json:"coreId"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := s.runs.BeginShutdown(request.CoreID); err != nil {
		writeFailure(w, err)
		return
	}
	s.service.BeginModelFavoritesClose()
	s.service.BeginAuthClose()
	writeJSON(w, http.StatusAccepted, struct {
		OK bool `json:"ok"`
	}{OK: true})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	s.shutdownOnce.Do(func() { close(s.shutdownCh) })
}

func (s *Server) runRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) >= 2 && (parts[1] == "inputs" || parts[1] == "input-chain") {
		s.runtimeInputRoute(w, r, parts)
		return
	}
	if len(parts) == 1 {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		state, err := s.runs.GetContext(r.Context(), r.URL.Query().Get("coreId"), parts[0])
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
		state, err := s.runs.CancelContext(r.Context(), r.URL.Query().Get("coreId"), parts[0])
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
	subscription, err := s.runs.SubscribeContext(r.Context(), r.URL.Query().Get("coreId"), runID)
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

	initial, err := subscription.SnapshotContext(r.Context())
	if err != nil {
		writeFailure(w, err)
		return
	}
	if err = writeEvent(w, flusher, "run.snapshot", initial); err != nil {
		subscription.ReleaseInitialSnapshot()
		return
	}
	initialTerminal := initial.Phase == "terminal"
	initial = api.RunSnapshot{}
	subscription.ReleaseInitialSnapshot()
	if initialTerminal {
		final, finalErr := subscription.FinalSnapshotContext(r.Context())
		if finalErr != nil {
			return
		}
		if writeEvent(w, flusher, "run.snapshot", final) != nil {
			return
		}
		_ = writeEvent(w, flusher, "run.terminal", final.RunState)
		return
	}
	state := initial.RunState
	lastSeq := state.Seq
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.runs.Context().Done():
			return
		case <-subscription.Notify:
			update, _, updateErr := subscription.UpdateContext(r.Context())
			if updateErr != nil {
				return
			}
			state = update.RunState
			if state.Phase == "terminal" {
				final, finalErr := subscription.FinalSnapshotContext(r.Context())
				if finalErr != nil {
					return
				}
				if writeEvent(w, flusher, "run.snapshot", final) != nil {
					return
				}
				_ = writeEvent(w, flusher, "run.terminal", final.RunState)
				return
			}
			if state.Seq == lastSeq && update.Conversation == nil {
				continue
			}
			if writeEvent(w, flusher, "run.update", update) != nil {
				return
			}
			lastSeq = state.Seq
		case <-subscription.ConversationNotify:
			update, _, updateErr := subscription.UpdateContext(r.Context())
			if updateErr != nil {
				return
			}
			state = update.RunState
			if state.Phase == "terminal" {
				final, finalErr := subscription.FinalSnapshotContext(r.Context())
				if finalErr != nil {
					return
				}
				if writeEvent(w, flusher, "run.snapshot", final) != nil {
					return
				}
				_ = writeEvent(w, flusher, "run.terminal", final.RunState)
				return
			}
			if state.Seq == lastSeq && update.Conversation == nil {
				continue
			}
			if writeEvent(w, flusher, "run.update", update) != nil {
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
		result[i], err = s.sessionResponse(sessions[i])
		if err != nil {
			writeFailure(w, err)
			return
		}
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
		response, err := s.sessionResponse(session)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 4 && parts[1] == "recall" && parts[2] == "operations" {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		result, err := s.runs.RecallOperation(r.Context(), r.URL.Query().Get("coreId"), parts[0], parts[3])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "not_found", "Session route not found")
		return
	}
	switch parts[1] {
	case "context":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		result, err := s.runs.SessionContext(r.Context(), r.URL.Query().Get("coreId"), parts[0])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "recall":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var request api.RecallOperationRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result, err := s.runs.Recall(r.Context(), parts[0], request)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "inputs":
		s.sessionInputs(w, r, parts[0])
	case "rename":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var request struct {
			Title string `json:"title"`
		}
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		session, err := s.service.RenameSession(r.Context(), parts[0], request.Title)
		if err != nil {
			writeFailure(w, err)
			return
		}
		response, err := s.sessionResponse(session)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, response)
	case "active-run":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		state, err := s.runs.ActiveSessionRun(r.URL.Query().Get("coreId"), parts[0])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	case "view":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		view, err := s.runs.SessionView(r.Context(), parts[0])
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
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

func (s *Server) sessionResponse(value history.Session) (api.Session, error) {
	response := sessionDTO(value)
	active, err := s.runs.ActiveSessionRun(s.runs.Core().CoreID, value.ID)
	if err != nil {
		return api.Session{}, err
	}
	if active != nil {
		response.ActiveRunID = active.RunID
	}
	return response, nil
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
	catalog, err := s.service.ModelsWithAuth(r.Context(), workspace, r.URL.Query().Get("providerId"), r.URL.Query().Get("authProfile"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	w.Header().Set("Hjh-Auth-Owner-Id", catalog.AuthOwnerID)
	w.Header().Set("Hjh-Auth-Account-Id", catalog.AuthAccountID)
	result := make([]api.ModelInfo, len(catalog.Models))
	for i := range catalog.Models {
		result[i] = modelDTO(catalog.Models[i])
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) selection(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	request := api.SelectionRequest{
		SessionID: query.Get("sessionId"), Workspace: query.Get("workspace"), Role: query.Get("role"),
		ProviderID: query.Get("providerId"), ModelID: query.Get("modelId"), Effort: query.Get("effort"),
		AuthProfile: query.Get("authProfile"),
	}
	workspace, err := s.resolveWorkspace(r.Context(), request.SessionID, request.Workspace)
	if err != nil {
		writeFailure(w, err)
		return
	}
	preview, err := s.service.PreviewSelection(r.Context(), app.RunRequest{
		SessionID: request.SessionID, Workspace: workspace, Role: request.Role,
		ProviderID: request.ProviderID, ModelID: request.ModelID, Effort: request.Effort,
		AuthProfile: request.AuthProfile,
	})
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.SelectionPreview{Role: preview.Role, Selection: preview.Selection})
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

func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.service.BeginModelFavoritesClose()
		s.service.BeginAuthClose()
		_ = s.runs.Close()
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

func writeEvent(w http.ResponseWriter, flusher http.Flusher, name string, payload any) error {
	state := eventRunState(payload)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\nid: %s/%s/%d\ndata: %s\n\n", name, state.CoreID, state.RunID, state.Seq, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func eventRunState(payload any) api.RunState {
	switch value := payload.(type) {
	case api.RunState:
		return value
	case api.RunSnapshot:
		return value.RunState
	case api.RunUpdate:
		return value.RunState
	default:
		return api.RunState{}
	}
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
		case "invalid_input_kind", "invalid_request":
			status = http.StatusBadRequest
		case "core_changed", "run_id_conflict", "session_busy", "input_id_conflict", "run_not_accepting_input", "command_id_conflict", "recall_ambiguous":
			status = http.StatusConflict
		case "run_not_found", "input_not_found", "recall_not_found", "recall_operation_not_found":
			status = http.StatusNotFound
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
