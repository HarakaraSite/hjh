package httpapi

import (
	"context"
	"net/http"

	"hjh/internal/api"
)

func (s *Server) modelFavorites(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		query := r.URL.Query()
		resolveWorkspace := func(ctx context.Context) (string, error) {
			return s.resolveWorkspace(ctx, query.Get("sessionId"), query.Get("workspace"))
		}
		result, err := s.service.ModelFavorites(r.Context(), resolveWorkspace, query.Get("providerId"))
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPut:
		var request api.SetModelFavoriteRequest
		if err := decodeJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if request.CoreID == "" || request.CoreID != s.runs.Core().CoreID {
			writeError(w, http.StatusConflict, "core_changed", "request targets a different Core")
			return
		}
		if request.Favorite == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "favorite is required")
			return
		}
		resolveWorkspace := func(ctx context.Context) (string, error) {
			return s.resolveWorkspace(ctx, request.SessionID, request.Workspace)
		}
		result, err := s.service.SetModelFavorite(r.Context(), resolveWorkspace, request.ProviderID, request.ModelID, *request.Favorite)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPut)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}
