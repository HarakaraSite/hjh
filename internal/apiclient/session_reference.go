package apiclient

import (
	"context"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/sessionid"
)

// ResolveSession expands a human selector before the client uses the ID in
// subscriptions, selection, runs or other API operations.
func (c *Client) ResolveSession(ctx context.Context, selector string) (api.Session, error) {
	if id, err := uuid.Parse(selector); err == nil && len(selector) == 36 {
		return c.Session(ctx, id.String())
	}
	sessions, err := c.Sessions(ctx)
	if err != nil {
		return api.Session{}, err
	}
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = session.ID
	}
	id, err := sessionid.Resolve(selector, ids)
	if err != nil {
		return api.Session{}, err
	}
	return c.Session(ctx, id)
}
