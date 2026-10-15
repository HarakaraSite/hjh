// Package jsruntime owns a QuickJS runtime for the lifetime of one agent Worker.
package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/host"
)

type Options struct {
	Snapshot       extensions.Snapshot
	Workspace      string
	Observer       contracts.Observer
	ObserverParent string
	Host           *host.Host
	Agents         contracts.AgentRunner
	AgentControl   contracts.AgentController
}

type Worker struct {
	requests    chan callRequest
	done        chan struct{}
	cancel      context.CancelFunc
	closeErr    error
	defs        []contracts.ToolDefinition
	handlers    []string
	unavailable atomic.Bool
}

type callRequest struct {
	ctx            context.Context
	name           string
	args           json.RawMessage
	hook           bool
	requestContext contracts.RequestContext
	result         chan callResult
}

type callResult struct {
	value json.RawMessage
	err   error
}

// New creates a Worker and evaluates its captured extension modules before it
// returns. The runtime is created, used, and closed only by its owner goroutine.
func New(ctx context.Context, options Options) (*Worker, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifeCtx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		requests: make(chan callRequest),
		done:     make(chan struct{}),
		defs:     options.Snapshot.Definitions(),
	}
	stopParent := context.AfterFunc(ctx, func() {
		w.unavailable.Store(true)
		cancel()
	})
	w.cancel = func() {
		w.unavailable.Store(true)
		stopParent()
		cancel()
	}
	ready := make(chan error, 1)
	go w.serve(lifeCtx, options, ready)
	select {
	case err := <-ready:
		if err != nil {
			w.cancel()
			<-w.done
			return nil, err
		}
		return w, nil
	case <-ctx.Done():
		w.cancel()
		<-w.done
		return nil, ctx.Err()
	}
}

func (w *Worker) Definitions() []contracts.ToolDefinition {
	definitions := make([]contracts.ToolDefinition, len(w.defs))
	for i, definition := range w.defs {
		definitions[i] = definition
		definitions[i].InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
	}
	return definitions
}

// HookHandlers returns the supported hook exports that were callable in the
// captured module after it was loaded. The returned list is detached from the
// Worker state.
func (w *Worker) HookHandlers() []string {
	handlers := make([]string, len(w.handlers))
	copy(handlers, w.handlers)
	return handlers
}

// Available reports whether the Worker still owns an open runtime.
func (w *Worker) Available() bool {
	if w == nil {
		return false
	}
	if w.unavailable.Load() {
		return false
	}
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

func (w *Worker) Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !json.Valid(args) && len(args) != 0 {
		return nil, fmt.Errorf("tool %s arguments are not valid JSON", name)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if _, ok := w.definition(name); !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	request := callRequest{
		ctx:    ctx,
		name:   name,
		args:   append(json.RawMessage(nil), args...),
		result: make(chan callResult, 1),
	}
	select {
	case w.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.done:
		return nil, w.closeErrOrClosed()
	}
	select {
	case result := <-request.result:
		return result.value, result.err
	case <-ctx.Done():
		w.cancel()
		select {
		case result := <-request.result:
			if result.err != nil {
				return nil, result.err
			}
			return result.value, nil
		case <-w.done:
			return nil, ctx.Err()
		}
	case <-w.done:
		select {
		case result := <-request.result:
			return result.value, result.err
		default:
			return nil, w.closeErrOrClosed()
		}
	}
}

// Hook invokes one lifecycle hook on the Worker owner goroutine. Tool hooks
// remain part of Call and are not callable through this method.
func (w *Worker) Hook(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	return w.invokeHook(ctx, name, input, nil)
}

// ProjectRequest invokes the external request hook without archiving its
// returned value. Context changes are staged through the supplied owner.
func (w *Worker) ProjectRequest(ctx context.Context, input contracts.ProjectRequestHookInput, owner contracts.RequestContext) error {
	_, err := w.invokeHook(ctx, "projectRequest", contracts.JSON(input), owner)
	return err
}

func (w *Worker) invokeHook(ctx context.Context, name string, input json.RawMessage, owner contracts.RequestContext) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if !json.Valid(input) {
		return nil, fmt.Errorf("hook %s input is not valid JSON", name)
	}
	if !isLifecycleHook(name) {
		return nil, fmt.Errorf("unsupported lifecycle hook: %s", name)
	}
	if !w.Available() {
		select {
		case <-w.done:
			return nil, w.closeErrOrClosed()
		default:
			return nil, errors.New("Worker is unavailable")
		}
	}
	if !containsHandler(w.handlers, name) {
		return json.RawMessage("null"), nil
	}
	request := callRequest{
		ctx:            ctx,
		name:           name,
		args:           append(json.RawMessage(nil), input...),
		hook:           true,
		requestContext: owner,
		result:         make(chan callResult, 1),
	}
	select {
	case w.requests <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.done:
		return nil, w.closeErrOrClosed()
	}
	select {
	case result := <-request.result:
		return result.value, result.err
	case <-ctx.Done():
		w.cancel()
		select {
		case result := <-request.result:
			if result.err != nil {
				return nil, result.err
			}
			return result.value, nil
		case <-w.done:
			return nil, ctx.Err()
		}
	case <-w.done:
		select {
		case result := <-request.result:
			return result.value, result.err
		default:
			return nil, w.closeErrOrClosed()
		}
	}
}

func (w *Worker) Close() error {
	w.cancel()
	<-w.done
	return w.closeErr
}

func (w *Worker) serve(ctx context.Context, options Options, ready chan<- error) {
	defer close(w.done)
	owner := newVMOwner(ctx, options, w.defs)
	defer func() {
		w.closeErr = errors.Join(w.closeErr, owner.close(ctx.Err()))
	}()
	if err := owner.open(); err != nil {
		w.closeErr = err
		ready <- err
		return
	}
	w.handlers = append([]string(nil), owner.hookHandlers...)
	ready <- nil
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-w.requests:
			var result callResult
			var stop bool
			if request.hook {
				result, stop = owner.hook(request)
			} else {
				result, stop = owner.call(request)
			}
			if stop {
				w.unavailable.Store(true)
			}
			request.result <- result
			if stop {
				return
			}
		}
	}
}

func containsHandler(handlers []string, name string) bool {
	for _, handler := range handlers {
		if handler == name {
			return true
		}
	}
	return false
}

func isLifecycleHook(name string) bool {
	switch name {
	case "runtimeStart", "beforeTurn", "projectRequest", "afterTurn", "runtimeStop":
		return true
	default:
		return false
	}
}

func (w *Worker) definition(name string) (contracts.ToolDefinition, bool) {
	for _, def := range w.defs {
		if def.Name == name {
			return def, true
		}
	}
	return contracts.ToolDefinition{}, false
}

func (w *Worker) closeErrOrClosed() error {
	if w.closeErr != nil {
		return w.closeErr
	}
	return errors.New("Worker is closed")
}
