package apiclient

import (
	"context"
	"net/http"

	"hjh/internal/api"
)

// Orientation queries the Core's context sources without starting a run.
func (c *Client) Orientation(ctx context.Context, request api.OrientationRequest) (api.Orientation, error) {
	query := scopeQuery(Scope{SessionID: request.SessionID, Workspace: request.Workspace})
	if request.Role != "" {
		query.Set("role", request.Role)
	}
	var result api.Orientation
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/orientation?"+query.Encode(), nil, &result)
	return result, err
}
