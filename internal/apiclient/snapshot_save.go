package apiclient

import (
	"context"
	"net/http"
	"net/url"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

// SaveSnapshot retries a frozen failed execution snapshot through its owning Core.
func (c *Client) SaveSnapshot(ctx context.Context, executionID, coreID string) (contracts.SnapshotStatus, error) {
	var result contracts.SnapshotStatus
	err := c.jsonRequest(ctx, http.MethodPost,
		apiPrefix+"/executions/"+url.PathEscape(executionID)+"/snapshots/save",
		api.SnapshotSaveRequest{CoreID: coreID}, &result)
	return result, err
}
