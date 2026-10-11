package apiclient

import (
	"context"
	"net/http"
	"net/url"

	"hjh/internal/api"
)

func (c *Client) AuthProfiles(ctx context.Context, request api.SelectionRequest) (api.AuthProfiles, error) {
	query := scopeQuery(Scope{SessionID: request.SessionID, Workspace: request.Workspace})
	for key, value := range map[string]string{
		"role": request.Role, "providerId": request.ProviderID, "modelId": request.ModelID,
		"effort": request.Effort, "authProfile": request.AuthProfile,
	} {
		if value != "" {
			query.Set(key, value)
		}
	}
	var result api.AuthProfiles
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/auth/profiles?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) AuthAccounts(ctx context.Context, scope Scope, providerID, authProfile string) (api.AuthAccounts, error) {
	query := scopeQuery(scope)
	query.Set("providerId", providerID)
	query.Set("authProfile", authProfile)
	var result api.AuthAccounts
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/auth/accounts?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) RegisterAPIKey(ctx context.Context, request api.AuthRequest) (api.APIKeyState, error) {
	var result api.APIKeyState
	err := c.jsonRequest(ctx, http.MethodPut, apiPrefix+"/auth/api-key", request, &result)
	return result, err
}

func (c *Client) SelectAuthAccount(ctx context.Context, request api.AuthRequest) (api.AuthAccounts, error) {
	var result api.AuthAccounts
	err := c.jsonRequest(ctx, http.MethodPut, apiPrefix+"/auth/account-selection", request, &result)
	return result, err
}

func (c *Client) BeginAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	var result api.AuthAttempt
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/auth/attempts", request, &result)
	return result, err
}

func (c *Client) AuthAttempt(ctx context.Context, coreID, attemptID string) (api.AuthAttempt, error) {
	query := url.Values{}
	query.Set("coreId", coreID)
	var result api.AuthAttempt
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/auth/attempts/"+url.PathEscape(attemptID)+"?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) CompleteAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	var result api.AuthAttempt
	body := struct {
		CoreID      string `json:"coreId"`
		CallbackURL string `json:"callbackUrl,omitempty"`
	}{request.CoreID, request.CallbackURL}
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/auth/attempts/"+url.PathEscape(request.AttemptID)+"/complete", body, &result)
	return result, err
}

func (c *Client) CancelAuthAttempt(ctx context.Context, request api.AuthRequest) (api.AuthAttempt, error) {
	var result api.AuthAttempt
	body := struct {
		CoreID string `json:"coreId"`
	}{request.CoreID}
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/auth/attempts/"+url.PathEscape(request.AttemptID)+"/cancel", body, &result)
	return result, err
}

func (c *Client) DeleteAuthAttempt(ctx context.Context, coreID, attemptID string) error {
	query := url.Values{}
	query.Set("coreId", coreID)
	return c.jsonRequest(ctx, http.MethodDelete, apiPrefix+"/auth/attempts/"+url.PathEscape(attemptID)+"?"+query.Encode(), nil, nil)
}
