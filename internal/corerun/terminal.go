package corerun

import (
	"context"

	"hjh/internal/api"
)

// A result read belongs to its overlapping consumers, never to the run index.
// The loader uses Core's context so cancelling one HTTP request does not abort
// the result read needed by another request. No body survives the last reader.
type terminalReadFlight struct {
	ready  chan struct{}
	refs   int
	result *api.AgentResult
	err    error
}

func (r *managedRun) readTerminal(ctx context.Context, load func() (*api.AgentResult, error)) (*api.AgentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.resultMu.Lock()
	flight := r.resultFlight
	leader := flight == nil
	if leader {
		flight = &terminalReadFlight{ready: make(chan struct{})}
		r.resultFlight = flight
	}
	flight.refs++
	r.resultMu.Unlock()
	defer func() {
		r.resultMu.Lock()
		flight.refs--
		if flight.refs == 0 && r.resultFlight == flight {
			r.resultFlight = nil
		}
		r.resultMu.Unlock()
	}()
	if leader {
		flight.result, flight.err = load()
		close(flight.ready)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-flight.ready:
	}
	if flight.err != nil {
		return nil, flight.err
	}
	if flight.result == nil {
		return nil, nil
	}
	result := *flight.result
	return &result, nil
}
