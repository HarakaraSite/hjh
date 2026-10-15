package jsruntime

import (
	"encoding/json"
	"errors"

	"github.com/go-quickjs/go-quickjs"
	"hjh/internal/contracts"
)

func (o *vmOwner) startContextValue(operation, parent, input string) quickjs.Value {
	if o.callCtx == nil || o.requestContext == nil {
		return o.rejectedPromise(errors.New("context API is available during projectRequest"))
	}
	// Only lengths are observed: input may itself contain a replacement transcript.
	id, err := o.start(parent, "host", "context."+operation, contracts.JSON(map[string]int{"inputBytes": len(input)}))
	if err != nil {
		return o.rejectedPromise(err)
	}
	promise := o.rt.NewPromise()
	o.pending[id] = promise
	o.jobs.Add(1)
	ctx, owner := o.callCtx, o.requestContext
	go func() {
		defer o.jobs.Done()
		value, callErr := owner.Call(ctx, operation, json.RawMessage(input))
		metadata := map[string]any{"outputBytes": len(value)}
		if operation == "model.generate" && callErr == nil {
			var result struct {
				Usage        json.RawMessage `json:"usage"`
				FinishReason string          `json:"finishReason"`
			}
			if json.Unmarshal(value, &result) == nil {
				metadata["usage"] = result.Usage
				metadata["finishReason"] = result.FinishReason
			}
		}
		o.queueCompletion(jobCompletion{id: id, value: value, err: callErr,
			observation: contracts.JSON(metadata)})
	}()
	return promise.Value()
}
