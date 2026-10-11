package apiclient

import (
	"context"
	"net/http"

	"hjh/internal/api"
)

// ModelFavorites reads saved model IDs for one provider in the supplied scope.
func (c *Client) ModelFavorites(ctx context.Context, scope Scope, providerID string) (api.ModelFavorites, error) {
	query := scopeQuery(scope)
	query.Set("providerId", providerID)
	var result api.ModelFavorites
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/model-favorites?"+query.Encode(), nil, &result)
	return result, err
}

// SetModelFavorite applies the request's explicit desired value.
func (c *Client) SetModelFavorite(ctx context.Context, request api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error) {
	var result api.ModelFavoriteResult
	err := c.jsonRequest(ctx, http.MethodPut, apiPrefix+"/model-favorites", request, &result)
	return result, err
}
