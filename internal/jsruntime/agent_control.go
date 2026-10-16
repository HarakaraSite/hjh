package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-quickjs/go-quickjs"
	"hjh/internal/contracts"
)

func (o *vmOwner) startAgentControlValue(operation, parent, input string) quickjs.Value {
	promise, err := o.startAgentControl(operation, parent, json.RawMessage(input))
	if err != nil {
		return o.rejectedPromise(err)
	}
	return promise
}

func (o *vmOwner) startAgentControl(operation, parent string, input json.RawMessage) (quickjs.Value, error) {
	if o.callCtx == nil {
		return quickjs.Value{}, errors.New("agent control operation called outside a Worker call")
	}
	if len(input) == 0 || !json.Valid(input) {
		return quickjs.Value{}, errors.New("agent control operation arguments are not valid JSON")
	}
	if o.options.AgentControl == nil {
		return quickjs.Value{}, errors.New("agent controller is not configured")
	}
	id, err := o.start(parent, "agent", "agents."+operation, input)
	if err != nil {
		return quickjs.Value{}, err
	}
	promise := o.rt.NewPromise()
	o.pending[id] = promise
	o.jobs.Add(1)
	o.loop.Begin()
	ctx := o.callCtx
	go func() {
		defer o.jobs.Done()
		value, callErr := o.callAgentControl(ctx, operation, id, input)
		encoded, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			callErr = errors.Join(callErr, encodeErr)
		}
		o.queueCompletion(jobCompletion{id: id, value: encoded, err: callErr})
	}()
	return promise.Value(), nil
}

func (o *vmOwner) callAgentControl(ctx context.Context, operation, callID string, input json.RawMessage) (any, error) {
	controller := o.options.AgentControl
	switch operation {
	case "spawn":
		var arguments struct {
			Task  *string                       `json:"task"`
			Role  string                        `json:"role"`
			Model *contracts.AgentModelOverride `json:"model"`
			Tools *[]string                     `json:"tools"`
		}
		if err := json.Unmarshal(input, &arguments); err != nil {
			return nil, fmt.Errorf("spawn_agent arguments are not valid: %w", err)
		}
		if arguments.Task == nil {
			return nil, errors.New("spawn_agent requires a string task field")
		}
		request := contracts.AgentRunRequest{Task: *arguments.Task, Role: arguments.Role,
			Model: arguments.Model, Tools: arguments.Tools, CallID: callID}
		return controller.SpawnAgent(ctx, request)
	case "status", "collect", "cancel":
		var query contracts.AgentQuery
		if err := json.Unmarshal(input, &query); err != nil {
			return nil, fmt.Errorf("%s arguments are not valid: %w", operation, err)
		}
		query.CallID = callID
		switch operation {
		case "status":
			return controller.StatusAgent(ctx, query)
		case "collect":
			return controller.CollectAgent(ctx, query)
		default:
			return controller.CancelAgent(ctx, query)
		}
	case "steer":
		var request contracts.AgentSteerRequest
		if err := json.Unmarshal(input, &request); err != nil {
			return nil, fmt.Errorf("steer_agent arguments are not valid: %w", err)
		}
		request.CallID = callID
		return controller.SteerAgent(ctx, request)
	default:
		return nil, fmt.Errorf("unknown agent control operation %q", operation)
	}
}
