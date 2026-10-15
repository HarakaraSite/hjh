package contracts

import (
	"context"
	"encoding/json"
)

// RequestContext owns a request-only candidate, not the loop transcript or SQL.
// The Worker calls it only within a projectRequest hook and joins pending calls
// before returning. Values crossing the VM boundary are detached JSON.
type RequestContext interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

type ProjectRequestHookInput struct {
	Runtime   HookRuntime `json:"runtime"`
	ModelStep int         `json:"modelStep"`
}
