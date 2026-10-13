package host

import (
	"context"
	"encoding/json"
)

// ProviderRequestArgs contains only public request data and a credential reference.
// The injected dispatcher adds authentication after host observations are recorded.
type ProviderRequestArgs struct {
	AuthProfile string            `json:"authProfile"`
	Endpoint    string            `json:"endpoint"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        json.RawMessage   `json:"body,omitempty"`
}

type ProviderRequest func(context.Context, ProviderRequestArgs) (HTTPResult, error)
