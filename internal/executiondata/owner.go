// Package executiondata coordinates the durable order of one root execution
// and all of its descendants. It owns no database connection or conversation
// projection.
package executiondata

import (
	"context"
	"errors"
	"sync"

	"hjh/internal/contracts"
)

// Owner serializes persistence and cut capture for one root execution tree.
// onCommit runs under the gate after a successful commit and must only perform
// a short, non-blocking handoff to its consumer.
type Owner struct {
	mu              sync.Mutex
	rootExecutionID string
	through         uint64
	nextID          uint64
	subscriptions   map[uint64]func(contracts.ExecutionCommit)
}

func NewOwner() *Owner {
	return &Owner{subscriptions: make(map[uint64]func(contracts.ExecutionCommit))}
}

// Commit assigns a candidate range, persists it under the shared gate, and
// publishes the saved facts only after persistence succeeds. Failed candidates
// are reused by the next commit, so the durable sequence remains a prefix.
func (o *Owner) Commit(ctx context.Context, draft contracts.ExecutionFact, commit contracts.ExecutionDataCommit) error {
	if commit == nil {
		return errors.New("execution data: missing commit function")
	}
	if o == nil {
		return errors.New("execution data: missing owner")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	first := o.through + 1
	saved, err := commit(first)
	if err != nil {
		return err
	}
	if saved.SequenceCount == 0 {
		return errors.New("execution data: successful commit has no sequence")
	}
	if o.rootExecutionID == "" {
		o.rootExecutionID = draft.RootExecutionID
	}
	o.through += saved.SequenceCount
	if saved.RootExecutionID == "" {
		saved.RootExecutionID = o.rootExecutionID
	}
	saved.ThroughSequence = o.through
	for _, sink := range o.subscriptions {
		sink(saved)
	}
	return nil
}

// ThroughSequence returns the durable prefix while holding the same gate used
// by Commit and CaptureCut.
func (o *Owner) ThroughSequence() uint64 {
	if o == nil {
		return 0
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.through
}

// Subscription receives commits after the storage transaction has succeeded.
// Close releases its sink from the owner.
type Subscription struct {
	owner *Owner
	id    uint64
	once  sync.Once
}

func (s *Subscription) Close() {
	if s == nil || s.owner == nil {
		return
	}
	s.once.Do(func() {
		s.owner.mu.Lock()
		delete(s.owner.subscriptions, s.id)
		s.owner.mu.Unlock()
	})
}

// CaptureAndSubscribe fixes a storage cut and installs its commit sink while
// holding the same gate. The capture callback must perform only a short cut
// query and release every SQL row before returning. Replay and normalization
// belong after this function returns. The sink must be a short, non-blocking
// handoff and must not call back into this owner.
func CaptureAndSubscribe[T any](owner *Owner, capture func(rootExecutionID string, throughSequence uint64) (T, error), sink func(contracts.ExecutionCommit)) (T, *Subscription, error) {
	var zero T
	if owner == nil {
		return zero, nil, errors.New("execution data: missing owner")
	}
	if capture == nil {
		return zero, nil, errors.New("execution data: missing cut capture")
	}
	if sink == nil {
		return zero, nil, errors.New("execution data: missing commit sink")
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	cut, err := capture(owner.rootExecutionID, owner.through)
	if err != nil {
		return zero, nil, err
	}
	owner.nextID++
	id := owner.nextID
	owner.subscriptions[id] = sink
	return cut, &Subscription{owner: owner, id: id}, nil
}
