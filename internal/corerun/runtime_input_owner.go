package corerun

import (
	"context"
	"sort"
	"sync"

	"hjh/internal/api"
	"hjh/internal/contracts"
)

// inputChain follows one normal run and the follow-ups it accepts. A new
// normal run in the same Session receives a different chain.
type inputChain struct {
	mu sync.Mutex

	manager            *Manager
	anchorRunID        string
	sessionID          string
	workspace          string
	activeRunID        string
	phase              string
	sequence           uint64
	revision           uint64
	steeringOpen       bool
	followUpOpen       bool
	cancelled          bool
	cancelReason       string
	finishingRunID     string
	finishingReason    string
	pendingWrites      int
	inFlightRuns       int
	finalizationFailed bool
	finalizationErr    error

	writes    map[string]*inputWrite
	steering  map[string][]contracts.SteeringInput
	followUps []contracts.InputReceipt
	wake      chan struct{}
}

func newInputChain(manager *Manager, anchorRunID, sessionID, workspace string) *inputChain {
	return &inputChain{manager: manager, anchorRunID: anchorRunID, sessionID: sessionID, workspace: workspace,
		activeRunID: anchorRunID, phase: "open", inFlightRuns: 1,
		writes: map[string]*inputWrite{}, steering: map[string][]contracts.SteeringInput{}, wake: make(chan struct{})}
}

func (c *inputChain) signalLocked() {
	close(c.wake)
	c.wake = make(chan struct{})
}

func (c *inputChain) noteRevision(receipt contracts.InputReceipt) {
	c.mu.Lock()
	c.revision = maxUint64(c.revision, receipt.Revision)
	c.signalLocked()
	c.mu.Unlock()
	c.manager.inputRevisionChanged(receipt, receipt.Revision)
}

type runSteeringSource struct {
	chain *inputChain
	runID string
}

func (c *inputChain) steeringSource(runID string) contracts.SteeringSource {
	return &runSteeringSource{chain: c, runID: runID}
}

func (s *runSteeringSource) Pending(ctx context.Context, final bool) ([]contracts.SteeringInput, error) {
	c := s.chain
	for {
		c.mu.Lock()
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		if c.cancelled {
			c.mu.Unlock()
			return nil, context.Canceled
		}
		frontier := c.sequence
		var waiting []*inputWrite
		for _, write := range c.writes {
			if write.candidate.Sequence <= frontier {
				waiting = append(waiting, write)
			}
		}
		if len(waiting) != 0 {
			c.mu.Unlock()
			sort.Slice(waiting, func(i, j int) bool { return waiting[i].candidate.Sequence < waiting[j].candidate.Sequence })
			for _, write := range waiting {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-write.done:
				}
			}
			continue
		}
		queue := c.steering[s.runID]
		pending := make([]contracts.SteeringInput, 0, len(queue))
		for _, input := range queue {
			if input.Receipt.Status == "accepted" {
				pending = append(pending, input)
			}
		}
		if len(pending) == 0 && final && c.activeRunID == s.runID && c.steeringOpen {
			c.steeringOpen = false
			c.signalLocked()
			c.mu.Unlock()
			c.manager.inputAcceptanceChanged(s.runID)
			return nil, nil
		}
		for i := range queue {
			for _, input := range pending {
				if queue[i].Receipt.InputID == input.Receipt.InputID {
					queue[i].Receipt.Status = "applying"
				}
			}
		}
		c.steering[s.runID] = queue
		c.mu.Unlock()
		return pending, nil
	}
}

func (s *runSteeringSource) Applied(input contracts.SteeringInput, revision uint64) {
	c := s.chain
	c.mu.Lock()
	queue := c.steering[s.runID]
	kept := queue[:0]
	for _, queued := range queue {
		if queued.Receipt.InputID != input.Receipt.InputID {
			kept = append(kept, queued)
		}
	}
	if len(kept) == 0 {
		delete(c.steering, s.runID)
	} else {
		c.steering[s.runID] = kept
	}
	c.revision = maxUint64(c.revision, revision)
	c.signalLocked()
	c.mu.Unlock()
	receipt := input.Receipt
	receipt.Revision = revision
	c.manager.inputRevisionChanged(receipt, revision)
}

func (s *runSteeringSource) Close(reason string) error {
	return s.chain.closeSteering(s.runID, reason)
}

func (c *inputChain) closeSteering(runID, reason string) error {
	c.mu.Lock()
	if c.cancelReason != "" {
		reason = c.cancelReason
	}
	changed := c.activeRunID == runID && c.steeringOpen
	c.steeringOpen = false
	c.signalLocked()
	c.mu.Unlock()
	if changed {
		c.manager.inputAcceptanceChanged(runID)
	}

	for {
		c.mu.Lock()
		writes := make([]*inputWrite, 0)
		for _, write := range c.writes {
			if write.candidate.RunID == runID {
				writes = append(writes, write)
			}
		}
		if len(writes) == 0 {
			// Removing an entry compacts the owner's backing array. Iterate a
			// snapshot so every accepted input receives its durable stop reason.
			queue := append([]contracts.SteeringInput(nil), c.steering[runID]...)
			c.mu.Unlock()
			var firstErr error
			for _, input := range queue {
				if input.Receipt.Status != "accepted" && input.Receipt.Status != "applying" {
					continue
				}
				receipt := input.Receipt
				receipt.Status = "unapplied"
				receipt.Reason = reason
				_, err := c.updateReceipt(receipt)
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					continue
				}
				c.mu.Lock()
				c.removeSteeringLocked(runID, input.Receipt.InputID)
				c.signalLocked()
				c.mu.Unlock()
			}
			return firstErr
		}
		c.mu.Unlock()
		for _, write := range writes {
			<-write.done
		}
	}
}

func (c *inputChain) removeSteeringLocked(runID, inputID string) {
	queue := c.steering[runID]
	kept := queue[:0]
	for _, input := range queue {
		if input.Receipt.InputID != inputID {
			kept = append(kept, input)
		}
	}
	if len(kept) == 0 {
		delete(c.steering, runID)
	} else {
		c.steering[runID] = kept
	}
}

func (c *inputChain) activate(runID string) api.InputAcceptance {
	c.mu.Lock()
	if c.activeRunID == runID && c.phase == "open" && !c.cancelled {
		c.steeringOpen = true
		c.followUpOpen = true
	}
	acceptance := api.InputAcceptance{Steering: c.activeRunID == runID && c.steeringOpen, FollowUp: c.activeRunID == runID && c.followUpOpen}
	c.signalLocked()
	c.mu.Unlock()
	return acceptance
}

func (c *inputChain) sealRun(runID, reason string) {
	c.mu.Lock()
	changed := false
	if c.activeRunID == runID {
		if c.steeringOpen || c.followUpOpen {
			changed = true
		}
		c.steeringOpen = false
		c.followUpOpen = false
	}
	c.finishingRunID = runID
	c.finishingReason = reason
	c.signalLocked()
	c.mu.Unlock()
	if changed {
		c.manager.inputAcceptanceChanged(runID)
	}
}

func (m *Manager) inputAcceptanceChanged(runID string) {
	m.mu.Lock()
	r := m.runs[runID]
	if r == nil {
		m.mu.Unlock()
		return
	}
	acceptance := r.chain.snapshotAcceptance(runID)
	if acceptance != r.state.InputAcceptance {
		r.state.InputAcceptance = acceptance
		m.changedLocked(r)
	}
	m.mu.Unlock()
}

func (c *inputChain) snapshotAcceptance(runID string) api.InputAcceptance {
	c.mu.Lock()
	defer c.mu.Unlock()
	return api.InputAcceptance{Steering: runID == c.activeRunID && c.steeringOpen,
		FollowUp: runID == c.activeRunID && c.followUpOpen}
}

func (c *inputChain) waitWrites(ctx context.Context) error {
	for {
		c.mu.Lock()
		if c.pendingWrites == 0 {
			c.mu.Unlock()
			return nil
		}
		wake := c.wake
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
	}
}

func (c *inputChain) updateReceipt(receipt contracts.InputReceipt) (contracts.InputReceipt, error) {
	c.mu.Lock()
	c.pendingWrites++
	c.signalLocked()
	c.mu.Unlock()
	updated, err := c.manager.service.UpdateRuntimeInput(context.Background(), receipt)
	c.mu.Lock()
	c.pendingWrites--
	c.maybeSettleLocked()
	c.signalLocked()
	c.mu.Unlock()
	if err == nil {
		c.noteRevision(updated)
	}
	return updated, err
}
