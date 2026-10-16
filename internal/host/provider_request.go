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

// ObservedResult separates a host return value from its execution summary.
// The enclosing public tool owns the complete body in ordinary history.
type ObservedResult struct {
	Value       any
	Observation any
}

// ProviderObservation records transport metadata without duplicating the body.
// Response parsing belongs to the requesting JavaScript module.
type ProviderObservation struct {
	Status        int `json:"status"`
	ResponseBytes int `json:"responseBytes"`
}
