package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-quickjs/go-quickjs"
	"hjh/internal/contracts"
)

func (o *vmOwner) call(request callRequest) (callResult, bool) {
	input := append(json.RawMessage(nil), request.args...)
	parent := contracts.CallParent(request.ctx)
	if parent == "" {
		parent = o.options.ObserverParent
	}
	root, err := o.startCall(contracts.ModelToolCall(request.ctx), parent, request.name, input)
	if err != nil {
		return callResult{err: err}, false
	}

	callCtx, cancel := context.WithCancel(o.ctx)
	agentCtx, agentCancel := context.WithCancel(callCtx)
	stopRequest := context.AfterFunc(request.ctx, cancel)
	o.callCtx = callCtx
	o.agentCtx = agentCtx
	o.agentCancel = agentCancel
	o.agentClosed = false
	o.agentJobs = map[string]*agentJob{}
	o.callRoot = root
	o.callStarts = map[string]contracts.Observation{root: o.active[root].started}
	o.delegationEnds = map[string]delegationObservationEnd{}

	var value json.RawMessage
	invoke, invokeErr := o.rt.Get("__invokeDirect")
	if invokeErr == nil {
		var promise quickjs.Value
		promise, invokeErr = invoke.CallContext(callCtx, request.name, string(request.args), root)
		if invokeErr == nil {
			value, invokeErr = o.awaitCallRoot(callCtx, promise)
		}
	}
	invokeErr = captureJavaScriptError(invokeErr)
	stopRequest()

	if callCtx.Err() != nil {
		callErr := errors.Join(callCtx.Err(), invokeErr)
		return o.failCall(root, callErr, true, cancel, agentCancel)
	}
	if invokeErr != nil {
		return o.failCall(root, invokeErr, false, cancel, agentCancel)
	}

	// Root settlement cancels the child scope in the promise callback. Join
	// that initial set before draining host work, as a pending hook can still
	// start a late AgentRunner while the host work finishes.
	if joinErr := o.joinAgents(); joinErr != nil {
		return o.failCall(root, joinErr, false, cancel, agentCancel)
	}
	if drainErr := o.drainHost(callCtx); drainErr != nil {
		return o.failCall(root, drainErr, callCtx.Err() != nil, cancel, agentCancel)
	}
	if joinErr := o.joinAgents(); joinErr != nil {
		return o.failCall(root, joinErr, false, cancel, agentCancel)
	}
	if endErr := o.closeDelegationObservations(); endErr != nil {
		return o.failCall(root, endErr, false, cancel, agentCancel)
	}

	cancel()
	if _, active := o.active[root]; active {
		if endErr := o.end(root, "completed", value, nil); endErr != nil {
			o.finishOpen("failed", endErr)
			o.clearCallScope()
			return callResult{err: endErr}, false
		}
	}
	o.clearCallScope()
	return callResult{value: value}, false
}

func (o *vmOwner) failCall(root string, err error, stop bool, cancel, agentCancel context.CancelFunc) (callResult, bool) {
	cancel()
	o.closeAgentScope()
	agentCancel()
	joinErr := o.joinAgents()
	o.jobs.Wait()
	err = errors.Join(err, joinErr)
	o.recordCompletionsOnShutdown()
	o.stopPending(err)
	err = errors.Join(err, o.closeDelegationObservations())
	phase := "failed"
	if stop {
		phase = "cancelled"
	}
	o.finishOpen(phase, err)
	if _, active := o.active[root]; active {
		err = errors.Join(err, o.end(root, phase, json.RawMessage("null"), err))
	}
	o.clearCallScope()
	return callResult{err: err}, stop
}

func (o *vmOwner) clearCallScope() {
	o.callCtx = nil
	o.agentCtx = nil
	o.agentCancel = nil
	o.agentClosed = false
	o.agentJobs = map[string]*agentJob{}
	o.callRoot = ""
	o.callStarts = map[string]contracts.Observation{}
	o.delegationEnds = map[string]delegationObservationEnd{}
}

func (o *vmOwner) awaitCallRoot(ctx context.Context, promise quickjs.Value) (json.RawMessage, error) {
	var value json.RawMessage
	var resultErr error
	watchErr := o.watchPromise(ctx, promise, func(v quickjs.Value) {
		if !v.IsString() {
			resultErr = fmt.Errorf("JavaScript invocation returned a non-string JSON encoding")
		} else {
			encoded := v.String()
			if !json.Valid([]byte(encoded)) {
				resultErr = fmt.Errorf("JavaScript returned invalid JSON")
			} else {
				value = json.RawMessage(encoded)
			}
		}
		o.closeAgentScope()
	}, func(v quickjs.Value) {
		resultErr = o.capturePromiseRejection(v)
		o.closeAgentScope()
	})
	return value, errors.Join(watchErr, resultErr)
}

func (o *vmOwner) awaitModule(ctx context.Context, promise quickjs.Value) error {
	var resultErr error
	if err := o.watchPromise(ctx, promise, func(quickjs.Value) {}, func(v quickjs.Value) {
		resultErr = o.capturePromiseRejection(v)
	}); err != nil {
		return err
	}
	return resultErr
}

func (o *vmOwner) watchPromise(ctx context.Context, promise quickjs.Value, fulfilled, rejected func(quickjs.Value)) error {
	settled := false
	then, err := promise.Get("then")
	if err != nil {
		return captureJavaScriptError(err)
	}
	_, err = then.CallWithThisContext(ctx, promise,
		func(value quickjs.Value) {
			fulfilled(value)
			settled = true
		},
		func(value quickjs.Value) {
			rejected(value)
			settled = true
		},
	)
	if err != nil {
		return captureJavaScriptError(err)
	}
	for !settled {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = o.rt.RunJobsContext(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return captureJavaScriptError(err)
		}
		if settled {
			break
		}
		select {
		case <-o.completeWake:
			if err = o.resolveCompletions(); err != nil {
				return captureJavaScriptError(err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (o *vmOwner) drainHost(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := o.rt.RunJobsContext(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return captureJavaScriptError(err)
		}
		hadCompletions := o.hasCompletions()
		if err := o.resolveCompletions(); err != nil {
			return captureJavaScriptError(err)
		}
		if hadCompletions {
			// Settling a Go Promise can queue a JavaScript continuation that
			// starts more host or agent work. Run those jobs before deciding
			// that the call has drained.
			continue
		}
		if len(o.pending) == 0 && !o.hasCompletions() && !o.rt.HasPendingJobs() {
			return nil
		}
		if len(o.pending) == 0 {
			continue
		}
		select {
		case <-o.completeWake:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
