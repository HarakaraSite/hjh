package httpapi

import (
	"net/http"

	"hjh/internal/api"
	"hjh/internal/app"
)

func (s *Server) rememberSelection(w http.ResponseWriter, r *http.Request) {
	var request api.RememberSelectionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.CoreID == "" || request.CoreID != s.runs.Core().CoreID {
		writeError(w, http.StatusConflict, "core_changed", "request targets a different Core")
		return
	}
	workspace, err := s.resolveWorkspace(r.Context(), request.SessionID, request.Workspace)
	if err != nil {
		writeFailure(w, err)
		return
	}
	preview, err := s.service.RememberSelection(r.Context(), app.RunRequest{
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
