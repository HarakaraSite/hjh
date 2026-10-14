package apiclient

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"hjh/internal/api"
)

func (c *Client) DeleteSession(ctx context.Context, id string) error {
	return c.jsonRequest(ctx, http.MethodDelete, apiPrefix+"/sessions/"+url.PathEscape(id), nil, nil)
}

func (c *Client) RenameSession(ctx context.Context, id, title string) (api.Session, error) {
	var session api.Session
	body := struct {
		Title string `json:"title"`
	}{title}
	err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/sessions/"+url.PathEscape(id)+"/rename", body, &session)
	return session, err
}

func (c *Client) ActiveSessionRun(ctx context.Context, coreID, sessionID string) (*api.RunState, error) {
	query := url.Values{"coreId": {coreID}}
	var state *api.RunState
	err := c.jsonRequest(ctx, http.MethodGet, apiPrefix+"/sessions/"+url.PathEscape(sessionID)+"/active-run?"+query.Encode(), nil, &state)
	return state, err
}

// ShutdownCore returns after the acknowledged Core has stopped serving. The
// shutdown acknowledgement itself precedes cancellation joins and final saves.
func (c *Client) ShutdownCore(ctx context.Context, coreID string) error {
	body := struct {
		CoreID string `json:"coreId"`
	}{coreID}
	var status struct {
		OK bool `json:"ok"`
	}
	if err := c.jsonRequest(ctx, http.MethodPost, apiPrefix+"/core/shutdown", body, &status); err != nil {
		return err
	}
	for {
		core, err := c.Core(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		if err != nil {
			if isAPIError(err) {
				return err
			}
			return nil
		}
		if core.CoreID != coreID {
			return nil
		}
		if !waitClient(ctx, 100*time.Millisecond) {
			return ctx.Err()
		}
	}
}
