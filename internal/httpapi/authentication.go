package httpapi

import (
	"encoding/json"
	"net/http"

	"hjh/internal/api"
)

func (s *Server) authRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "auth route not found")
		return
	}
	switch {
	case len(parts) == 1 && parts[0] == "profiles":
		s.authProfiles(w, r)
	case len(parts) == 1 && parts[0] == "accounts":
		s.authAccounts(w, r)
	case len(parts) == 1 && parts[0] == "status":
		s.authStatus(w, r)
	case len(parts) == 1 && parts[0] == "api-key":
		s.registerAPIKey(w, r)
	case len(parts) == 1 && parts[0] == "account-selection":
		s.selectAuthAccount(w, r)
	case len(parts) == 1 && parts[0] == "attempts":
		s.beginAuthAttempt(w, r)
	case len(parts) == 2 && parts[0] == "attempts":
		s.authAttempt(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "attempts" && parts[2] == "complete":
		s.completeAuthAttempt(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "attempts" && parts[2] == "cancel":
		s.cancelAuthAttempt(w, r, parts[1])
	default:
		writeError(w, http.StatusNotFound, "not_found", "auth route not found")
	}
}

func (s *Server) authProfiles(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
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
	request.Workspace = workspace
	profiles, err := s.service.AuthProfiles(r.Context(), request)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (s *Server) authAccounts(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	query := r.URL.Query()
	workspace, err := s.resolveWorkspace(r.Context(), query.Get("sessionId"), query.Get("workspace"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	accounts, err := s.service.AuthAccounts(r.Context(), workspace, query.Get("providerId"), query.Get("authProfile"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	query := r.URL.Query()
	workspace, err := s.resolveWorkspace(r.Context(), query.Get("sessionId"), query.Get("workspace"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	status, err := s.service.AuthStatus(r.Context(), workspace, query.Get("providerId"), query.Get("authProfile"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, statusDTO(status))
}

func (s *Server) registerAPIKey(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPut) {
		return
	}
	var request api.AuthRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	if !s.validAuthWrite(w, request, true) {
		return
	}
	if request.MutationID == "" || request.Value == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "mutationId and value are required")
		return
	}
	state, err := s.service.RegisterAPIKey(s.runs.Context(), request)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) selectAuthAccount(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPut) {
		return
	}
	var request api.AuthRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	if !s.validAuthWrite(w, request, true) {
		return
	}
	if request.AccountID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "accountId is required")
		return
	}
	accounts, err := s.service.SelectAuthAccount(s.runs.Context(), request)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, accounts)
}

func (s *Server) beginAuthAttempt(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var request api.AuthRequest
	if !decodeAuthJSON(w, r, &request) {
		return
	}
	if !s.validAuthWrite(w, request, true) {
		return
	}
	if request.AttemptID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "attemptId is required")
		return
	}
	attempt, err := s.service.BeginAuthAttempt(s.runs.Context(), request)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, attempt)
}

func (s *Server) authAttempt(w http.ResponseWriter, r *http.Request, attemptID string) {
	switch r.Method {
	case http.MethodGet:
		coreID := r.URL.Query().Get("coreId")
		if !s.requireAuthCore(w, coreID) {
			return
		}
		attempt, err := s.service.AuthAttempt(r.Context(), coreID, attemptID)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, attempt)
	case http.MethodDelete:
		coreID := r.URL.Query().Get("coreId")
		if !s.requireAuthCore(w, coreID) {
			return
		}
		if err := s.service.DeleteAuthAttempt(r.Context(), coreID, attemptID); err != nil {
			writeFailure(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		requireMethod(w, r, http.MethodGet)
	}
}

func (s *Server) completeAuthAttempt(w http.ResponseWriter, r *http.Request, attemptID string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		CoreID      string `json:"coreId"`
		CallbackURL string `json:"callbackUrl,omitempty"`
	}
	if !decodeAuthJSON(w, r, &body) {
		return
	}
	if !s.requireAuthCore(w, body.CoreID) {
		return
	}
	attempt, err := s.service.CompleteAuthAttempt(s.runs.Context(), api.AuthRequest{
		CoreID: body.CoreID, AttemptID: attemptID, CallbackURL: body.CallbackURL,
	})
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, attempt)
}

func (s *Server) cancelAuthAttempt(w http.ResponseWriter, r *http.Request, attemptID string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		CoreID string `json:"coreId"`
	}
	if !decodeAuthJSON(w, r, &body) {
		return
	}
	if !s.requireAuthCore(w, body.CoreID) {
		return
	}
	attempt, err := s.service.CancelAuthAttempt(s.runs.Context(), api.AuthRequest{CoreID: body.CoreID, AttemptID: attemptID})
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, attempt)
}

func (s *Server) requireAuthCore(w http.ResponseWriter, coreID string) bool {
	if coreID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "coreId is required")
		return false
	}
	if coreID != s.runs.Core().CoreID {
		writeFailure(w, &api.Error{Code: "core_changed", Message: "Core changed"})
		return false
	}
	if err := s.runs.Context().Err(); err != nil {
		writeFailure(w, &api.Error{Code: "core_closed", Message: "Core is closing"})
		return false
	}
	return true
}

func (s *Server) validAuthWrite(w http.ResponseWriter, request api.AuthRequest, providerRequired bool) bool {
	if !s.requireAuthCore(w, request.CoreID) {
		return false
	}
	if request.ProviderID == "" && providerRequired {
		writeError(w, http.StatusBadRequest, "invalid_request", "providerId is required")
		return false
	}
	return true
}

func decodeAuthJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid auth request JSON")
		return false
	}
	return true
}
