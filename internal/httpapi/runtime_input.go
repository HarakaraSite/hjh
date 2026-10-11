package httpapi

import (
	"net/http"
	"strconv"

	"hjh/internal/api"
)

func (s *Server) runtimeInputRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	runID := parts[0]
	switch {
	case len(parts) == 2 && parts[1] == "inputs":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var request api.SubmitInputRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		receipt, created, err := s.runs.SubmitInput(r.Context(), runID, request)
		if err != nil {
			writeFailure(w, err)
			return
		}
		status := http.StatusOK
		if created {
			status = http.StatusAccepted
		}
		writeJSON(w, status, receipt)
	case len(parts) == 3 && parts[1] == "inputs":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		includeText, err := inputIncludeText(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		receipt, err := s.runs.Input(r.Context(), r.URL.Query().Get("coreId"), runID, parts[2], includeText)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, receipt)
	case len(parts) == 2 && parts[1] == "input-chain":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		state, err := s.runs.InputChain(r.URL.Query().Get("coreId"), runID)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	case len(parts) == 3 && parts[1] == "input-chain" && parts[2] == "cancel":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var request struct {
			CoreID string `json:"coreId"`
		}
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		state, err := s.runs.CancelInputChain(request.CoreID, runID)
		if err != nil {
			writeFailure(w, err)
			return
		}
		status := http.StatusAccepted
		if state.Phase == "settled" {
			status = http.StatusOK
		}
		writeJSON(w, status, state)
	default:
		writeError(w, http.StatusNotFound, "not_found", "runtime input route not found")
	}
}

func (s *Server) sessionInputs(w http.ResponseWriter, r *http.Request, sessionID string) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	query := r.URL.Query()
	var revision uint64
	if value := query.Get("afterRevision"); value != "" {
		var err error
		revision, err = strconv.ParseUint(value, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "afterRevision must be an unsigned integer")
			return
		}
	}
	includeText, err := inputIncludeText(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	page, err := s.runs.Inputs(r.Context(), sessionID, query.Get("coreId"), revision, includeText)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func inputIncludeText(r *http.Request) (bool, error) {
	value := r.URL.Query().Get("includeText")
	if value == "" {
		return false, nil
	}
	return strconv.ParseBool(value)
}
