package corerun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"hjh/internal/api"
	"hjh/internal/app"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

type inputWrite struct {
	request   api.SubmitInputRequest
	signature string
	candidate contracts.InputReceipt
	body      []byte
	done      chan struct{}
	receipt   contracts.InputReceipt
	err       error
	created   bool
}

func inputSignature(request api.SubmitInputRequest, runID string) (string, error) {
	typed := struct {
		CoreID    string              `json:"coreId"`
		SessionID string              `json:"sessionId"`
		RunID     string              `json:"runId"`
		InputID   string              `json:"inputId"`
		Kind      string              `json:"kind"`
		Text      string              `json:"text"`
		Next      *api.NextRunOptions `json:"next,omitempty"`
	}{request.CoreID, request.SessionID, runID, request.InputID, request.Kind, request.Text, request.Next}
	encoded, err := json.Marshal(typed)
	if err != nil {
		return "", fmt.Errorf("encode runtime input identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func inputBody(request api.SubmitInputRequest) ([]byte, error) {
	if request.Kind == "follow-up" {
		return []byte(request.Text), nil
	}
	message := contracts.User(request.Text)
	steering := true
	message.Steering = &steering
	return contracts.EncodeJSON(message)
}

// SubmitInput registers an input before doing selection resolution or SQL.
// Once registered, the Core owns persistence even if the HTTP caller leaves.
func (m *Manager) SubmitInput(ctx context.Context, runID string, request api.SubmitInputRequest) (api.InputReceipt, bool, error) {
	request = cloneSubmitInput(request)
	if request.Kind != "steering" && request.Kind != "follow-up" {
		return api.InputReceipt{}, false, &api.Error{Code: "invalid_input_kind", Message: "kind must be steering or follow-up"}
	}
	signature, err := inputSignature(request, runID)
	if err != nil {
		return api.InputReceipt{}, false, err
	}
	body, err := inputBody(request)
	if err != nil {
		return api.InputReceipt{}, false, err
	}

	m.mu.Lock()
	r, err := m.findLocked(request.CoreID, runID)
	if err != nil {
		m.mu.Unlock()
		return api.InputReceipt{}, false, err
	}
	chain := r.chain
	chain.mu.Lock()
	if inFlight := chain.writes[request.InputID]; inFlight != nil {
		if inFlight.signature != signature {
			chain.mu.Unlock()
			m.mu.Unlock()
			return api.InputReceipt{}, false, inputIDConflict()
		}
		chain.mu.Unlock()
		m.mu.Unlock()
		return waitInputWrite(ctx, inFlight, false)
	}

	accepting := chain.activeRunID == runID && request.SessionID == chain.sessionID && r.state.ExecutionID != "" &&
		((request.Kind == "steering" && chain.steeringOpen) || (request.Kind == "follow-up" && chain.followUpOpen))
	if !accepting || m.closing {
		// A saved ID remains queryable and replayable after admission seals. The
		// durable store compares its signature without creating another body.
		if m.closing {
			chain.mu.Unlock()
			m.mu.Unlock()
			return api.InputReceipt{}, false, &api.Error{Code: "core_closing", Message: "Core is shutting down"}
		}
		m.wg.Add(1)
		m.sessionWrites[chain.sessionID]++
		chain.mu.Unlock()
		m.mu.Unlock()
		defer func() {
			m.finishSessionWrite(chain.sessionID)
			m.wg.Done()
		}()
		return m.replayOrClosed(ctx, request, runID, signature, body)
	}

	m.inputSequences[chain.sessionID]++
	chain.sequence = m.inputSequences[chain.sessionID]
	status := "accepted"
	if request.Kind == "follow-up" {
		status = "queued"
	}
	candidate := contracts.InputReceipt{SchemaVersion: 1, CoreID: m.core.CoreID, SessionID: chain.sessionID,
		RunID: runID, ExecutionID: r.state.ExecutionID, InputID: request.InputID, Kind: request.Kind,
		Sequence: chain.sequence, Status: status, AcceptedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	op := &inputWrite{request: request, signature: signature, candidate: candidate, body: body, done: make(chan struct{})}
	chain.writes[request.InputID] = op
	chain.pendingWrites++
	chain.signalLocked()
	m.wg.Add(1)
	chain.mu.Unlock()
	m.mu.Unlock()
	go m.persistInput(chain, op)
	return waitInputWrite(ctx, op, true)
}

func cloneSubmitInput(request api.SubmitInputRequest) api.SubmitInputRequest {
	if request.Next == nil {
		return request
	}
	next := *request.Next
	if next.MaxSteps != nil {
		value := *next.MaxSteps
		next.MaxSteps = &value
	}
	if next.FromTurn != nil {
		value := *next.FromTurn
		next.FromTurn = &value
	}
	request.Next = &next
	return request
}

func waitInputWrite(ctx context.Context, op *inputWrite, created bool) (api.InputReceipt, bool, error) {
	select {
	case <-ctx.Done():
		return api.InputReceipt{}, false, ctx.Err()
	case <-op.done:
	}
	if op.err != nil {
		return api.InputReceipt{}, false, op.err
	}
	receipt := op.receipt
	receipt.Text = ""
	return receipt, op.created && created, nil
}

func inputIDConflict() error {
	return &api.Error{Code: "input_id_conflict", Message: "inputId already identifies a different input"}
}

func (m *Manager) replayOrClosed(ctx context.Context, request api.SubmitInputRequest, runID, signature string, body []byte) (api.InputReceipt, bool, error) {
	// Check existence before asking the idempotent writer to compare signature;
	// a never-saved input must not become accepted after the owner is sealed.
	prior, err := m.service.RuntimeInput(ctx, request.CoreID, request.InputID, false)
	if errors.Is(err, history.ErrNotFound) {
		return api.InputReceipt{}, false, notAcceptingInput()
	}
	if err != nil {
		return api.InputReceipt{}, false, err
	}
	candidate := prior
	candidate.Text = ""
	receipt, err := m.service.SaveRuntimeInput(context.Background(), candidate, signature, body)
	if errors.Is(err, history.ErrInputIDConflict) {
		return api.InputReceipt{}, false, inputIDConflict()
	}
	if err != nil {
		return api.InputReceipt{}, false, err
	}
	if receipt.RunID != runID {
		return api.InputReceipt{}, false, inputIDConflict()
	}
	receipt.ManagedByThisCore = receipt.CoreID == m.core.CoreID
	receipt.Text = ""
	return receipt, false, nil
}

func notAcceptingInput() error {
	return &api.Error{Code: "run_not_accepting_input", Message: "run is not accepting this input"}
}

func (m *Manager) persistInput(chain *inputChain, op *inputWrite) {
	defer m.wg.Done()
	candidate := op.candidate
	prior, err := m.service.RuntimeInput(context.Background(), candidate.CoreID, candidate.InputID, false)
	if err == nil {
		// A durable input already owns its frozen selection and sequence. Replays
		// compare the original typed signature without consulting editable config.
		receipt, saveErr := m.service.SaveRuntimeInput(context.Background(), prior, op.signature, op.body)
		if errors.Is(saveErr, history.ErrInputIDConflict) {
			saveErr = inputIDConflict()
		}
		if saveErr == nil && receipt.RunID != candidate.RunID {
			saveErr = inputIDConflict()
		}
		if saveErr == nil {
			receipt.ManagedByThisCore = true
			receipt.Text = ""
		}
		m.completeInputWrite(chain, op, receipt, saveErr, false)
		return
	}
	if !errors.Is(err, history.ErrNotFound) {
		m.completeInputWrite(chain, op, contracts.InputReceipt{}, err, false)
		return
	}
	if op.request.Kind == "follow-up" {
		next := op.request.Next
		nextRun := api.NextRunOptions{}
		if next != nil {
			nextRun = *next
		}
		maxSteps, fromTurn := nextRun.MaxSteps, nextRun.FromTurn
		selection, err := m.service.ResolveInputSelection(context.Background(), app.RunRequest{SessionID: candidate.SessionID,
			Workspace: chain.workspace, Role: nextRun.Role, ProviderID: nextRun.ProviderID, ModelID: nextRun.ModelID,
			Effort: nextRun.Effort, AuthProfile: nextRun.AuthProfile, MaxSteps: maxSteps, FromTurn: fromTurn})
		if err != nil {
			m.completeInputWrite(chain, op, contracts.InputReceipt{}, err, false)
			return
		}
		candidate.Next = &selection
	}

	receipt, err := m.service.SaveRuntimeInput(context.Background(), candidate, op.signature, op.body)
	if errors.Is(err, history.ErrInputIDConflict) {
		err = inputIDConflict()
	}
	if err != nil {
		m.completeInputWrite(chain, op, contracts.InputReceipt{}, err, false)
		return
	}
	created := receipt.Sequence == candidate.Sequence
	if created {
		receipt, err = m.afterInputSaved(chain, op, receipt)
		if err != nil {
			chain.recordFinalizationFailure(err)
			m.completeInputWrite(chain, op, contracts.InputReceipt{}, err, true)
			return
		}
	}
	receipt.ManagedByThisCore = true
	m.completeInputWrite(chain, op, receipt, nil, created)
}

func (m *Manager) afterInputSaved(chain *inputChain, op *inputWrite, receipt contracts.InputReceipt) (contracts.InputReceipt, error) {
	chain.mu.Lock()
	stopped := chain.cancelled
	stopReason := chain.cancelReason
	finishReason := chain.finishingReason
	closed := chain.activeRunID != receipt.RunID || (chain.finishingRunID == receipt.RunID && finishReason != "")
	if !stopped && !closed {
		if receipt.Kind == "steering" {
			chain.steering[receipt.RunID] = append(chain.steering[receipt.RunID], contracts.SteeringInput{Receipt: receipt})
			sort.SliceStable(chain.steering[receipt.RunID], func(i, j int) bool {
				return chain.steering[receipt.RunID][i].Receipt.Sequence < chain.steering[receipt.RunID][j].Receipt.Sequence
			})
		} else {
			chain.followUps = append(chain.followUps, receipt)
			sort.SliceStable(chain.followUps, func(i, j int) bool { return chain.followUps[i].Sequence < chain.followUps[j].Sequence })
		}
		chain.revision = maxUint64(chain.revision, receipt.Revision)
		chain.signalLocked()
		chain.mu.Unlock()
		m.inputRevisionChanged(receipt, receipt.Revision)
		return receipt, nil
	}
	chain.mu.Unlock()

	if receipt.Kind == "steering" {
		receipt.Status = "unapplied"
	} else {
		receipt.Status = "not_started"
	}
	if stopped {
		receipt.Reason = stopReason
	} else if finishReason != "" {
		receipt.Reason = finishReason
	} else {
		receipt.Reason = "run_failed"
	}
	updated, err := m.service.UpdateRuntimeInput(context.Background(), receipt)
	if err != nil {
		return contracts.InputReceipt{}, err
	}
	chain.noteRevision(updated)
	return updated, nil
}

func (m *Manager) completeInputWrite(chain *inputChain, op *inputWrite, receipt contracts.InputReceipt, err error, created bool) {
	chain.mu.Lock()
	delete(chain.writes, op.request.InputID)
	chain.pendingWrites--
	op.body = nil
	op.request.Text = ""
	op.receipt = receipt
	op.err = err
	op.created = created
	chain.maybeSettleLocked()
	chain.signalLocked()
	close(op.done)
	chain.mu.Unlock()
	if err == nil && receipt.Revision != 0 {
		m.inputRevisionChanged(receipt, receipt.Revision)
	}
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func (m *Manager) inputRevisionChanged(receipt contracts.InputReceipt, revision uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs := map[*managedRun]bool{}
	if target := m.runs[receipt.RunID]; target != nil {
		runs[target] = true
	}
	if activeID := m.sessions[receipt.SessionID]; activeID != "" {
		if active := m.runs[activeID]; active != nil {
			runs[active] = true
		}
	}
	for run := range runs {
		if revision > run.state.InputRevision {
			run.state.InputRevision = revision
			m.changedLocked(run)
		}
	}
}

func (m *Manager) Input(ctx context.Context, coreID, runID, inputID string, includeText bool) (api.InputReceipt, error) {
	if coreID == "" {
		return api.InputReceipt{}, &api.Error{Code: "input_not_found", Message: "coreId is required to locate a runtime input"}
	}
	receipt, err := m.service.RuntimeInput(ctx, coreID, inputID, includeText)
	if errors.Is(err, history.ErrNotFound) || (err == nil && receipt.RunID != runID) {
		return api.InputReceipt{}, &api.Error{Code: "input_not_found", Message: "runtime input was not found for this run"}
	}
	if err != nil {
		return api.InputReceipt{}, err
	}
	receipt.ManagedByThisCore = coreID == m.core.CoreID
	return receipt, nil
}

func (m *Manager) Inputs(ctx context.Context, sessionID, coreID string, afterRevision uint64, includeText bool) (api.InputReceiptPage, error) {
	page, err := m.service.RuntimeInputs(ctx, sessionID, coreID, m.core.CoreID, afterRevision, includeText)
	if err != nil {
		return api.InputReceiptPage{}, err
	}
	m.mu.Lock()
	page.ActiveRunID = m.sessions[sessionID]
	m.mu.Unlock()
	for i := range page.Receipts {
		page.Receipts[i].ManagedByThisCore = page.Receipts[i].CoreID == m.core.CoreID
	}
	return page, nil
}

func (m *Manager) InputChain(coreID, runID string) (api.InputChainState, error) {
	m.mu.Lock()
	r, err := m.findLocked(coreID, runID)
	if err != nil {
		m.mu.Unlock()
		return api.InputChainState{}, err
	}
	chain := r.chain
	m.mu.Unlock()
	return chain.snapshot(), nil
}

func (c *inputChain) snapshot() api.InputChainState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return api.InputChainState{CoreID: c.manager.core.CoreID, SessionID: c.sessionID, AnchorRunID: c.anchorRunID,
		Phase: c.phase, ActiveRunID: c.activeRunID, PendingWrites: c.pendingWrites, Revision: c.revision}
}

func (m *Manager) CancelInputChain(coreID, runID string) (api.InputChainState, error) {
	m.mu.Lock()
	r, err := m.findLocked(coreID, runID)
	if err != nil {
		m.mu.Unlock()
		return api.InputChainState{}, err
	}
	chain := r.chain
	chain.mu.Lock()
	activeID := chain.activeRunID
	if chain.phase != "settled" {
		chain.cancelled = true
		chain.cancelReason = "cancelled"
		chain.phase = "cancelling"
		chain.steeringOpen = false
		chain.followUpOpen = false
		chain.signalLocked()
	}
	active := m.runs[activeID]
	if active != nil && active.state.Phase != "terminal" {
		active.cancel()
		if active.state.Phase != "cancelling" || active.state.InputAcceptance != (api.InputAcceptance{}) {
			active.state.Phase = "cancelling"
			active.state.InputAcceptance = api.InputAcceptance{}
			m.changedLocked(active)
		}
	}
	chain.mu.Unlock()
	state := chain.snapshot()
	m.mu.Unlock()
	return state, nil
}
