// Package jsruntime owns a QuickJS runtime for the lifetime of one agent Worker.
package jsruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
}

type Worker struct {
	requests chan callRequest
	done     chan struct{}
	cancel   context.CancelFunc
	closeErr error
	defs     []contracts.ToolDefinition
}

type callRequest struct {
	ctx    context.Context
	name   string
	args   json.RawMessage
	result chan callResult
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
	stopParent := context.AfterFunc(ctx, cancel)
	w := &Worker{
		requests: make(chan callRequest),
		done:     make(chan struct{}),
		cancel: func() {
			stopParent()
			cancel()
		},
		defs: options.Snapshot.Definitions(),
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
	ready <- nil
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-w.requests:
			result, stop := owner.call(request)
			request.result <- result
			if stop {
				return
			}
		}
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
