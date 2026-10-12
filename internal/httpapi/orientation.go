package httpapi

import (
	"net/http"

	"hjh/internal/app"
)

func (s *Server) orientation(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	workspace, err := s.resolveWorkspace(r.Context(), query.Get("sessionId"), query.Get("workspace"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	result, err := s.service.Orientation(r.Context(), app.RunRequest{Workspace: workspace,
		SessionID: query.Get("sessionId"), Role: query.Get("role")})
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
