package apiclient

import (
	"context"
	"net/http"
	"net/url"

	"hjh/internal/api"
)

func (c *Client) SessionContext(ctx context.Context, coreID, sessionID string) (api.SessionContext, error) {
	query := url.Values{}
	query.Set("coreId", coreID)
	var result api.SessionContext
	err := c.jsonRequest(ctx, http.MethodGet,
		apiPrefix+"/sessions/"+url.PathEscape(sessionID)+"/context?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) Recall(ctx context.Context, sessionID string, request api.RecallOperationRequest) (api.RecallOperationResult, error) {
	var result api.RecallOperationResult
	err := c.jsonRequest(ctx, http.MethodPost,
		apiPrefix+"/sessions/"+url.PathEscape(sessionID)+"/recall", request, &result)
	return result, err
}

func (c *Client) RecallOperation(ctx context.Context, coreID, sessionID, operationID string) (api.RecallOperationResult, error) {
	query := url.Values{}
	query.Set("coreId", coreID)
	var result api.RecallOperationResult
	err := c.jsonRequest(ctx, http.MethodGet,
		apiPrefix+"/sessions/"+url.PathEscape(sessionID)+"/recall/operations/"+url.PathEscape(operationID)+"?"+query.Encode(), nil, &result)
	return result, err
}
