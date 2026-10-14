package corerun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/executiondata"
)

func (m *Manager) finishInputRun(run *managedRun, input runInput, result app.RunResult, runErr error) error {
	chain := input.chain
	reason := m.inputRunReason(chain, runErr, result.Result.OK)
	chain.sealRun(run.state.RunID, reason)
	steeringReason := reason
	if steeringReason == "" {
		steeringReason = "completed"
	}
	var finishErrors []error
	if err := chain.closeSteering(run.state.RunID, steeringReason); err != nil {
		finishErrors = append(finishErrors, fmt.Errorf("close steering inputs: %w", err))
	}
	if err := chain.waitWrites(context.Background()); err != nil {
		finishErrors = append(finishErrors, fmt.Errorf("wait for runtime input writes: %w", err))
	}
	chain.mu.Lock()
	priorFinalizationErr := chain.finalizationErr
	chain.mu.Unlock()
	if priorFinalizationErr != nil {
		finishErrors = append(finishErrors, priorFinalizationErr)
	}

	if input.followUpReceipt != nil && input.followUpReceipt.Status == "starting" {
		failureReason := "start_failed"
		m.mu.Lock()
		chain.mu.Lock()
		if chain.cancelReason != "" {
			failureReason = chain.cancelReason
		} else if m.closing {
			failureReason = "core_stopped"
		} else if reason == "cancelled" || reason == "core_stopped" {
			failureReason = reason
		}
		chain.mu.Unlock()
		m.mu.Unlock()
		if err := m.failFollowUp(chain, *input.followUpReceipt, failureReason); err != nil {
			finishErrors = append(finishErrors, fmt.Errorf("save follow-up start failure: %w", err))
		}
	}
	chain.mu.Lock()
	if chain.cancelled && reason == "" {
		reason = chain.cancelReason
		if reason == "" {
			reason = "cancelled"
		}
	}
	if reason == "" && len(finishErrors) != 0 {
		reason = "run_failed"
	}
	if reason != "" {
		chain.mu.Unlock()
		if err := m.stopQueuedFollowUps(chain, reason); err != nil {
			finishErrors = append(finishErrors, err)
		}
		if result.ExecutionID != "" {
			if err := m.saveInputControl(run, chain); err != nil {
				finishErrors = append(finishErrors, err)
			}
		}
		m.markInputFinalizationFailure(chain, finishErrors)
		return errors.Join(finishErrors...)
	}
	if len(chain.followUps) == 0 {
		chain.signalLocked()
		chain.mu.Unlock()
		if result.ExecutionID != "" {
			if err := m.saveInputControl(run, chain); err != nil {
				finishErrors = append(finishErrors, err)
			}
		}
		m.markInputFinalizationFailure(chain, finishErrors)
		return errors.Join(finishErrors...)
	}
	receipt := chain.followUps[0]
	chain.mu.Unlock()
	if result.ExecutionID != "" {
		if err := m.saveInputControl(run, chain); err != nil {
			finishErrors = append(finishErrors, err)
		}
	}
	if len(finishErrors) != 0 {
		if err := m.stopQueuedFollowUps(chain, "run_failed"); err != nil {
			finishErrors = append(finishErrors, err)
		}
		m.markInputFinalizationFailure(chain, finishErrors)
		return errors.Join(finishErrors...)
	}
	if err := m.startFollowUp(run, chain, receipt); err != nil {
		finishErrors = append(finishErrors, err)
		if result.ExecutionID != "" {
			if saveErr := m.saveInputControl(run, chain); saveErr != nil {
				finishErrors = append(finishErrors, saveErr)
			}
		}
	}
	m.markInputFinalizationFailure(chain, finishErrors)
	return errors.Join(finishErrors...)
}

func (m *Manager) inputRunReason(chain *inputChain, runErr error, resultOK bool) string {
	chain.mu.Lock()
	cancelled, cancelReason := chain.cancelled, chain.cancelReason
	chain.mu.Unlock()
	if cancelled {
		if cancelReason != "" {
			return cancelReason
		}
		return "cancelled"
	}
	m.mu.Lock()
	closing := m.closing
	m.mu.Unlock()
	if closing {
		return "core_stopped"
	}
	if errors.Is(runErr, context.Canceled) {
		return "cancelled"
	}
	if runErr != nil || !resultOK {
		return "run_failed"
	}
	return ""
}

func (m *Manager) stopQueuedFollowUps(chain *inputChain, reason string) error {
	chain.mu.Lock()
	queued := append([]contracts.InputReceipt(nil), chain.followUps...)
	chain.mu.Unlock()
	var stopErrors []error
	for _, receipt := range queued {
		receipt.Status = "not_started"
		receipt.Reason = reason
		_, err := chain.updateReceipt(receipt)
		if err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("save follow-up %q as not started: %w", receipt.InputID, err))
			continue
		}
		chain.mu.Lock()
		chain.removeFollowUpLocked(receipt.InputID)
		chain.maybeSettleLocked()
		chain.mu.Unlock()
	}
	chain.mu.Lock()
	chain.steeringOpen = false
	chain.followUpOpen = false
	hasSteering := false
	for _, queue := range chain.steering {
		hasSteering = hasSteering || len(queue) != 0
	}
	if len(chain.followUps) != 0 || hasSteering || chain.pendingWrites != 0 {
		chain.phase = "cancelling"
	} else if chain.phase != "settled" {
		chain.phase = "cancelling"
	}
	chain.signalLocked()
	chain.mu.Unlock()
	return errors.Join(stopErrors...)
}

func (m *Manager) saveInputControl(run *managedRun, chain *inputChain) error {
	chain.mu.Lock()
	pendingCount := len(chain.followUps) + chain.pendingWrites
	for _, inputs := range chain.steering {
		pendingCount += len(inputs)
	}
	control := struct {
		Closed           bool   `json:"closed"`
		SequenceFrontier uint64 `json:"sequenceFrontier"`
		PendingCount     int    `json:"pendingCount"`
	}{Closed: true, SequenceFrontier: chain.sequence, PendingCount: pendingCount}
	chain.mu.Unlock()

	encoded, err := json.Marshal(control)
	if err != nil {
		return fmt.Errorf("encode root input control: %w", err)
	}
	if err := m.service.SaveInputControl(context.Background(), run.state.ExecutionID, encoded); err != nil {
		return &contracts.ExecutionError{Err: fmt.Errorf("save root input control: %w", err)}
	}
	return nil
}

func (m *Manager) markInputFinalizationFailure(chain *inputChain, errs []error) {
	if len(errs) == 0 {
		return
	}
	chain.recordFinalizationFailure(errors.Join(errs...))
}

func (c *inputChain) recordFinalizationFailure(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.finalizationFailed = true
	c.finalizationErr = errors.Join(c.finalizationErr, err)
	c.phase = "cancelling"
	c.signalLocked()
	c.mu.Unlock()
}

func (m *Manager) inputRunWorkerFinished(chain *inputChain) {
	chain.mu.Lock()
	if chain.inFlightRuns > 0 {
		chain.inFlightRuns--
	}
	chain.maybeSettleLocked()
	chain.signalLocked()
	chain.mu.Unlock()
}

func (c *inputChain) maybeSettleLocked() {
	if c.finalizationFailed || c.inFlightRuns != 0 || c.pendingWrites != 0 || len(c.followUps) != 0 {
		return
	}
	for _, inputs := range c.steering {
		if len(inputs) != 0 {
			return
		}
	}
	c.activeRunID = ""
	c.phase = "settled"
}

func (c *inputChain) removeFollowUpLocked(inputID string) {
	kept := c.followUps[:0]
	for _, receipt := range c.followUps {
		if receipt.InputID != inputID {
			kept = append(kept, receipt)
		}
	}
	c.followUps = kept
	c.maybeSettleLocked()
}

func (m *Manager) startFollowUp(previous *managedRun, chain *inputChain, receipt contracts.InputReceipt) error {
	if receipt.Next == nil {
		return errors.Join(m.failFollowUp(chain, receipt, "start_failed"), m.stopQueuedFollowUps(chain, "run_failed"))
	}
	runID := uuid.NewString()
	receipt.Status = "starting"
	receipt.StartedRunID = runID
	updated, err := chain.updateReceipt(receipt)
	if err != nil {
		// Keep the durable queued receipt visible. The chain remains unsettled
		// because it cannot truthfully claim the reservation transition saved.
		chain.mu.Lock()
		chain.phase = "cancelling"
		chain.signalLocked()
		chain.mu.Unlock()
		return fmt.Errorf("save follow-up starting state: %w", err)
	}
	receipt = updated

	started, err := m.service.RuntimeInput(context.Background(), receipt.CoreID, receipt.InputID, true)
	if err != nil {
		return errors.Join(m.failFollowUp(chain, receipt, "start_failed"), m.stopQueuedFollowUps(chain, "run_failed"))
	}
	receipt.Text = ""
	selection := receipt.Next
	maxSteps, fromTurn := selection.MaxSteps, selection.FromTurn
	request := api.StartRunRequest{CoreID: m.core.CoreID, RunID: runID, Task: started.Text, SessionID: chain.sessionID,
		Workspace: chain.workspace, Role: selection.Role, ProviderID: selection.Selection.Provider,
		ModelID: selection.Selection.ModelID, Effort: selection.Selection.Effort, AuthProfile: selection.Selection.AuthProfile,
		MaxSteps: &maxSteps, FromTurn: &fromTurn}
	fingerprint, err := requestFingerprint(request)
	if err != nil {
		return errors.Join(m.failFollowUp(chain, receipt, "start_failed"), m.stopQueuedFollowUps(chain, "run_failed"))
	}

	m.mu.Lock()
	chain.mu.Lock()
	if m.closing || chain.cancelled || chain.activeRunID != previous.state.RunID || m.sessions[chain.sessionID] != previous.state.RunID {
		stopReason := chain.cancelReason
		if stopReason == "" {
			if m.closing {
				stopReason = "core_stopped"
			} else {
				stopReason = "cancelled"
			}
		}
		chain.mu.Unlock()
		m.mu.Unlock()
		return errors.Join(m.failFollowUp(chain, receipt, stopReason), m.stopQueuedFollowUps(chain, stopReason))
	}
	ctx, cancel := context.WithCancel(m.ctx)
	owner := executiondata.NewOwner()
	nextRun := &managedRun{fingerprint: fingerprint, sessionReservation: chain.sessionID, cancel: cancel, owner: owner, chain: chain,
		state:       api.RunState{CoreID: m.core.CoreID, RunID: runID, Phase: "preparing", StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SessionID: chain.sessionID},
		subscribers: map[*runStateSubscriber]chan struct{}{}}
	m.runs[runID] = nextRun
	m.sessions[chain.sessionID] = runID
	m.sessionVersions[chain.sessionID]++
	previous.state.NextRunID = runID
	m.changedLocked(previous)
	chain.removeFollowUpLocked(receipt.InputID)
	chain.activeRunID = runID
	chain.phase = "open"
	chain.inFlightRuns++
	chain.steeringOpen = false
	chain.followUpOpen = false
	chain.finishingRunID = ""
	chain.finishingReason = ""
	chain.signalLocked()
	m.wg.Add(1)
	chain.mu.Unlock()
	m.mu.Unlock()

	followUpReceipt := receipt
	followUpReceipt.Status = "starting"
	go m.execute(ctx, nextRun, runInput{request: request, owner: owner, chain: chain,
		taskContentDigest: receipt.ContentDigest, followUpReceipt: &followUpReceipt, candidateExecutionID: uuid.NewString()})
	return nil
}

func (m *Manager) failFollowUp(chain *inputChain, receipt contracts.InputReceipt, reason string) error {
	receipt.Status = "not_started"
	receipt.Reason = reason
	updated, err := chain.updateReceipt(receipt)
	if err == nil {
		chain.mu.Lock()
		chain.removeFollowUpLocked(receipt.InputID)
		chain.mu.Unlock()
		_ = updated
	} else {
		return fmt.Errorf("save follow-up not-started state: %w", err)
	}
	return nil
}

func (m *Manager) followUpStarted(chain *inputChain, receipt contracts.InputReceipt, executionID string) error {
	receipt.Status = "started"
	receipt.StartedExecutionID = executionID
	updated, err := chain.updateReceipt(receipt)
	if err != nil {
		return err
	}
	_ = updated
	return nil
}

var _ contracts.SteeringSource = (*runSteeringSource)(nil)
