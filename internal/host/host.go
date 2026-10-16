// Package host provides context-aware operations used by extension modules.
// It has no dependency on the JavaScript runtime or history store.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
)

type Host struct {
	Workspace       string
	HTTPClient      *http.Client
	RequestProvider ProviderRequest
	// CommandEnv supplies credential-filtered environment metadata to Workers.
	CommandEnv map[string]string
}

func New(workspace string) (*Host, error) {
	if workspace == "" {
		workspace = "."
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	return &Host{Workspace: abs}, nil
}

type HTTPResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

func (h *Host) Call(ctx context.Context, name string, input json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch name {
	case "requestProvider":
		var args ProviderRequestArgs
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, err
		}
		if h.RequestProvider == nil {
			return nil, errors.New("credential-aware requests are not configured")
		}
		response, err := h.RequestProvider(ctx, args)
		if err != nil {
			return nil, err
		}
		return ObservedResult{
			Value: response,
			Observation: ProviderObservation{Status: response.Status, ResponseBytes: len(response.Body)},
		}, nil
	default:
		return nil, fmt.Errorf("unknown host operation: %s", name)
	}
}
