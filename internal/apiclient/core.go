package apiclient

import (
	"context"
	"net/http"

	"hjh/internal/api"
)

func (c *Client) CoreStatus(ctx context.Context) (api.CoreStatus, error) {
	var result api.CoreStatus
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/core/status", nil, &result)
	return result, err
}
