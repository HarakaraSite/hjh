package httpapi

import (
	"net/http"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

func (s *Server) executionRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 3 || parts[1] != "snapshots" || parts[2] != "save" {
		writeError(w, http.StatusNotFound, "not_found", "Execution route not found")
		return
	}
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var request api.SnapshotSaveRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	status, err := s.runs.SaveSnapshot(r.Context(), request.CoreID, parts[0])
	if err != nil && status.State != contracts.SnapshotSaveFailed {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
