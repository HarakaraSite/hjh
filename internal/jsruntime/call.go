package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/go-quickjs/go-quickjs"
	"github.com/go-quickjs/go-quickjs/stdlib"
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
	return o.runCall(request, root, false)
}

func (o *vmOwner) hook(request callRequest) (callResult, bool) {
	parent := contracts.CallParent(request.ctx)
	if parent == "" {
		parent = o.options.ObserverParent
	}
	root, err := o.start(parent, "hook", request.name, request.args)
	if err != nil {
		return callResult{err: err}, false
	}
	return o.runCall(request, root, true)
}

func (o *vmOwner) runCall(request callRequest, root string, lifecycleHook bool) (callResult, bool) {
	callCtx, cancel := context.WithCancel(o.ctx)
	agentCtx, agentCancel := context.WithCancel(callCtx)
	// Run cancellation interrupts JavaScript; closing the Loop also cancels
	// stdlib I/O, including a synchronous child_process call.
	stopRequest := cancelLoopOnRequest(request.ctx, cancel, o.loop)
	defer stopRequest()
	o.callCtx = callCtx
	o.requestContext = request.requestContext
	o.agentCtx = agentCtx
	o.agentCancel = agentCancel
	o.agentClosed = false
	o.agentJobs = map[string]*agentJob{}
	o.callRoot = root
	o.callStarts = map[string]contracts.Observation{root: o.active[root].started}
	o.delegationEnds = map[string]delegationObservationEnd{}

	var value json.RawMessage
	invokeName := "__invokeDirect"
	if lifecycleHook {
		invokeName = "__invokeLifecycleHook"
	}
	invoke, invokeErr := o.rt.Get(invokeName)
	if invokeErr == nil {
		promise, callErr := invoke.CallContext(callCtx, request.name, string(request.args), root)
		invokeErr = callErr
		if invokeErr == nil {
			value, invokeErr = o.awaitCallRoot(callCtx, promise)
		}
	}
	invokeErr = captureJavaScriptError(invokeErr)

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
	if drainErr := o.runLoop(callCtx, nil); drainErr != nil {
		return o.failCall(root, drainErr, callCtx.Err() != nil, cancel, agentCancel)
	}
	if joinErr := o.joinAgents(); joinErr != nil {
		return o.failCall(root, joinErr, false, cancel, agentCancel)
	}
	if endErr := o.closeDelegationObservations(); endErr != nil {
		return o.failCall(root, endErr, false, cancel, agentCancel)
	}

	// Settle against request cancellation before publishing success. If its
	// callback started, join it: a closed Loop must never remain reusable.
	stopRequest()
	if err := errors.Join(request.ctx.Err(), callCtx.Err()); err != nil {
		return o.failCall(root, err, true, cancel, agentCancel)
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

// cancelLoopOnRequest keeps cancellation attached through the final drain.
// The returned function detaches it, or joins an already-started callback.
func cancelLoopOnRequest(ctx context.Context, cancel context.CancelFunc, loop *stdlib.Loop) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		cancel()
		loop.Close()
	})
	return sync.OnceFunc(func() {
		if !stop() {
			<-done
		}
	})
}

func (o *vmOwner) failCall(root string, err error, stop bool, cancel, agentCancel context.CancelFunc) (callResult, bool) {
	// An uncaught outer error retires this VM. Stop stdlib I/O before joining
	// bridge jobs: those jobs may themselves be waiting on that I/O.
	o.loop.Close()
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
	err = errors.Join(err, o.close(err))
	return callResult{err: err}, stop
}

func (o *vmOwner) clearCallScope() {
	o.callCtx = nil
	o.requestContext = nil
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
	var rejected bool
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
		rejected = true
		o.loop.Close()
		o.closeAgentScope()
		// RunUntil can reach a due timer before rechecking closed/done after
		// microtasks. Retire the already-failed runtime on its owner now so
		// that callback cannot execute even in the current loop iteration.
		resultErr = errors.Join(resultErr, o.rt.Close())
	})
	if rejected && errors.Is(watchErr, quickjs.ErrClosed) {
		// Closure is the consequence of rejection, not its outward cause.
		watchErr = nil
	}
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
	settled := make(chan struct{})
	then, err := promise.Get("then")
	if err != nil {
		return captureJavaScriptError(err)
	}
	_, err = then.CallWithThisContext(ctx, promise,
		func(value quickjs.Value) {
			fulfilled(value)
			close(settled)
		},
		func(value quickjs.Value) {
			rejected(value)
			close(settled)
		},
	)
	if err != nil {
		return captureJavaScriptError(err)
	}
	return o.runLoop(ctx, settled)
}

// runLoop runs both stdlib tasks and Go bridge deliveries on the VM owner.
// A nil done drains all started operations, jobs, and timers.
func (o *vmOwner) runLoop(ctx context.Context, done <-chan struct{}) error {
	pumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	o.pumpCancel = cancel
	defer func() { o.pumpCancel = nil }()
	var err error
	if done == nil {
		err = o.loop.Run(pumpCtx)
	} else {
		err = o.loop.RunUntil(pumpCtx, done)
	}
	if o.completionErr != nil {
		completionErr := o.completionErr
		o.completionErr = nil
		return completionErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return captureJavaScriptError(err)
}
