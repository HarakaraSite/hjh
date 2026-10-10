// Package apiclient is the Go facade for the public hjh HTTP API.
// It contains communication helpers only; execution remains owned by the Core.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"hjh/internal/api"
)

const apiPrefix = "/api/v1"

// Client talks to one hjh Core. Close stops this client's HTTP/SSE requests;
// it never asks the Core to stop a run.
type Client struct {
	baseURL string
	http    *http.Client
	ctx     context.Context
	cancel  context.CancelFunc

	closeOnce sync.Once
}

func New(endpoint string) (*Client, error) {
	return NewWithHTTPClient(endpoint, http.DefaultClient)
}

// NewWithHTTPClient allows callers to provide transport settings while keeping
// all API behavior in the same facade.
func NewWithHTTPClient(endpoint string, client *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, fmt.Errorf("parse Core endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("Core endpoint must be an absolute HTTP URL")
	}
	if client == nil {
		client = http.DefaultClient
	}
	base := strings.TrimRight(parsed.String(), "/")
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{baseURL: base, http: client, ctx: ctx, cancel: cancel}, nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(c.cancel)
	return nil
}

func (c *Client) Core(ctx context.Context) (api.Core, error) {
	var result api.Core
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/core", nil, &result)
	return result, err
}

func (c *Client) Sessions(ctx context.Context) ([]api.Session, error) {
	var result []api.Session
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/sessions", nil, &result)
	return result, err
}

func (c *Client) Session(ctx context.Context, id string) (api.Session, error) {
	var result api.Session
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/sessions/"+url.PathEscape(id), nil, &result)
	return result, err
}

func (c *Client) Conversation(ctx context.Context, id string, fromTurn, toTurn int64) ([]api.Message, error) {
	query := url.Values{}
	query.Set("fromTurn", fmt.Sprint(fromTurn))
	query.Set("toTurn", fmt.Sprint(toTurn))
	var result []api.Message
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/sessions/"+url.PathEscape(id)+"/conversation?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) Executions(ctx context.Context, sessionID string) ([]api.Execution, error) {
	var result []api.Execution
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/sessions/"+url.PathEscape(sessionID)+"/executions", nil, &result)
	return result, err
}

func (c *Client) Inspect(ctx context.Context, executionID string, fromOrdinal, toOrdinal int64) (api.Inspection, error) {
	query := url.Values{}
	query.Set("fromOrdinal", fmt.Sprint(fromOrdinal))
	query.Set("toOrdinal", fmt.Sprint(toOrdinal))
	var result api.Inspection
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/executions/"+url.PathEscape(executionID)+"?"+query.Encode(), nil, &result)
	return result, err
}

// Scope is the resolved client context used by catalog and authentication
// operations. A Session ID lets the Core use that Session's saved workspace.
type Scope struct {
	SessionID string
	Workspace string
}

func (c *Client) Roles(ctx context.Context, scope Scope) (api.RoleCatalog, error) {
	query := scopeQuery(scope)
	var result api.RoleCatalog
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/roles?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) Providers(ctx context.Context, scope Scope) ([]api.ProviderDefinition, error) {
	query := scopeQuery(scope)
	var result []api.ProviderDefinition
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/providers?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) Models(ctx context.Context, scope Scope, providerID string) ([]api.ModelInfo, error) {
	query := scopeQuery(scope)
	query.Set("providerId", providerID)
	var result []api.ModelInfo
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/models?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) LoginStatus(ctx context.Context, scope Scope, providerID string) (api.Status, error) {
	query := scopeQuery(scope)
	query.Set("providerId", providerID)
	var result api.Status
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/auth/status?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) BeginLogin(ctx context.Context, scope Scope, providerID string) (api.Login, error) {
	var result api.Login
	body := struct {
		SessionID  string `json:"sessionId,omitempty"`
		Workspace  string `json:"workspace,omitempty"`
		ProviderID string `json:"providerId"`
	}{scope.SessionID, scope.Workspace, providerID}
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/auth/login", body, &result)
	return result, err
}

func (c *Client) CompleteLogin(ctx context.Context, login api.Login, callbackURL string) (api.Status, error) {
	var result api.Status
	body := struct {
		Workspace   string `json:"workspace"`
		ProviderID  string `json:"providerId"`
		CallbackURL string `json:"callbackUrl,omitempty"`
	}{login.Workspace, login.ProviderID, callbackURL}
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/auth/complete", body, &result)
	return result, err
}

func (c *Client) GetRun(ctx context.Context, coreID, runID string) (api.RunState, error) {
	query := url.Values{}
	query.Set("coreId", coreID)
	var result api.RunState
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/runs/"+url.PathEscape(runID)+"?"+query.Encode(), nil, &result)
	return result, err
}

func (c *Client) CancelRun(ctx context.Context, coreID, runID string) (api.RunState, error) {
	query := url.Values{}
	query.Set("coreId", coreID)
	var result api.RunState
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/runs/"+url.PathEscape(runID)+"/cancel?"+query.Encode(), struct{}{}, &result)
	return result, err
}

func scopeQuery(scope Scope) url.Values {
	query := url.Values{}
	if scope.SessionID != "" {
		query.Set("sessionId", scope.SessionID)
	}
	if scope.Workspace != "" {
		query.Set("workspace", scope.Workspace)
	}
	return query
}

func (c *Client) jsonRequest(parent context.Context, method, path string, body any, result any) error {
	ctx, cancel := c.requestContext(parent)
	defer cancel()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Core request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("create Core request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readAPIError(response)
	}
	if result == nil {
		_, err = io.Copy(io.Discard, response.Body)
		return err
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("decode Core response: %w", err)
	}
	return nil
}

func readAPIError(response *http.Response) error {
	var body api.ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err == nil && body.Error != nil {
		return body.Error
	}
	return fmt.Errorf("Core request failed with HTTP %s", response.Status)
}

func (c *Client) requestContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(c.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func isRunNotFound(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.Code == "run_not_found"
}
