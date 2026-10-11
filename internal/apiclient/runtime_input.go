package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"hjh/internal/api"
)

func (c *Client) SubmitInput(ctx context.Context, runID string, request api.SubmitInputRequest) (api.InputReceipt, error) {
	var result api.InputReceipt
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/runs/"+url.PathEscape(runID)+"/inputs", request, &result)
	return result, err
}

func (c *Client) Input(ctx context.Context, coreID, runID, inputID string, includeText bool) (api.InputReceipt, error) {
	query := url.Values{}
	query.Set("coreId", coreID)
	if includeText {
		query.Set("includeText", "true")
	}
	var result api.InputReceipt
	err := c.jsonRequest(ctx, http.MethodGet,
		apiPrefix+"/runs/"+url.PathEscape(runID)+"/inputs/"+url.PathEscape(inputID)+"?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) Inputs(ctx context.Context, sessionID, coreID string, afterRevision uint64, includeText bool) (api.InputReceiptPage, error) {
	query := url.Values{}
	if coreID != "" {
		query.Set("coreId", coreID)
		query.Set("afterRevision", strconv.FormatUint(afterRevision, 10))
	}
	if includeText {
		query.Set("includeText", "true")
	}
	var result api.InputReceiptPage
	err := c.jsonRequest(ctx, http.MethodGet,
		apiPrefix+"/sessions/"+url.PathEscape(sessionID)+"/inputs?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) CancelInputChain(ctx context.Context, coreID, runID string) (api.InputChainState, error) {
	var result api.InputChainState
	body := struct {
		CoreID string `json:"coreId"`
	}{CoreID: coreID}
	err := c.jsonRequest(ctx, http.MethodPost,
		apiPrefix+"/runs/"+url.PathEscape(runID)+"/input-chain/cancel", body, &result)
	return result, err
}

func (c *Client) InputChain(ctx context.Context, coreID, runID string) (api.InputChainState, error) {
	query := url.Values{}
	query.Set("coreId", coreID)
	var result api.InputChainState
	err := c.jsonRequest(ctx, http.MethodGet,
		apiPrefix+"/runs/"+url.PathEscape(runID)+"/input-chain?"+query.Encode(), nil, &result)
	return result, err
}
