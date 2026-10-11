package corerun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"hjh/internal/api"
	"hjh/internal/contracts"
	"hjh/internal/history"
)

type pendingRecall struct {
	reference      contracts.RecallReference
	sourceOutcome  string
	preparedAt     string
	claimedByRunID string
}

type recallOperation struct {
	sessionID  string
	request    api.RecallOperationRequest
	signature  string
	generation uint64
	result     api.RecallOperationResult
	err        error
	done       chan struct{}
}

func recallSignature(sessionID string, request api.RecallOperationRequest) (string, error) {
	var selector *string
	if request.Selector != "" {
		value := request.Selector
		selector = &value
	}
	typed := struct {
		SessionID string  `json:"sessionId"`
		Action    string  `json:"action"`
		Selector  *string `json:"selector,omitempty"`
	}{sessionID, request.Action, selector}
	encoded, err := json.Marshal(typed)
	if err != nil {
		return "", fmt.Errorf("encode recall operation identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// SessionContext combines one history metadata read with a short snapshot of
// this Core's active reservation and in-memory pending recall reference.
func (m *Manager) SessionContext(ctx context.Context, coreID, sessionID string) (api.SessionContext, error) {
	if err := m.coreError(coreID); err != nil {
		return api.SessionContext{}, err
	}
	view, err := m.service.SessionContext(ctx, sessionID)
	if err != nil {
		return api.SessionContext{}, err
	}
	view.CoreID = m.core.CoreID
	view.SessionID = sessionID

	m.mu.Lock()
	view.RecallRevision = m.recallRevision[sessionID]
	if runID := m.sessions[sessionID]; runID != "" {
		if run := m.runs[runID]; run != nil && run.state.Phase != "terminal" && run.state.SessionID == sessionID {
			view.ActiveRunID = runID
		}
	}
	if pending, ok := m.recallPending[sessionID]; ok {
		view.PendingRecall = &contracts.PendingRecall{
			Generation: pending.reference.Generation, OperationID: pending.reference.OperationID,
			SourceExecutionID: pending.reference.SourceExecutionID, SourceOutcome: pending.sourceOutcome,
			PreparedAt: pending.preparedAt, ClaimedByRunID: pending.claimedByRunID,
		}
	}
	m.mu.Unlock()
	return view, nil
}

// Recall registers a prepare/clear operation with the Session reservation
// owner. Once registered, persistence continues even if the HTTP caller leaves.
func (m *Manager) Recall(ctx context.Context, sessionID string, request api.RecallOperationRequest) (api.RecallOperationResult, error) {
	if sessionID == "" || request.OperationID == "" {
		return api.RecallOperationResult{}, &api.Error{Code: "invalid_request", Message: "sessionId and operationId are required"}
	}
	if request.Action != "prepare" && request.Action != "clear" {
		return api.RecallOperationResult{}, &api.Error{Code: "invalid_request", Message: "action must be prepare or clear"}
	}
	if request.Action == "clear" && request.Selector != "" {
		return api.RecallOperationResult{}, &api.Error{Code: "invalid_request", Message: "clear does not accept a selector"}
	}
	if err := m.coreError(request.CoreID); err != nil {
		return api.RecallOperationResult{}, err
	}
	signature, err := recallSignature(sessionID, request)
	if err != nil {
		return api.RecallOperationResult{}, err
	}

	// A local processing entry takes precedence so a replay waits through both
	// the durable write and publication of the resulting pending state.
	if result, handled, waitErr := m.waitLocalRecall(ctx, request, sessionID, signature); handled {
		return result, waitErr
	}

	// Durable replay is checked before current Session busy state. It returns the
	// original receipt and never reconstructs pending state.
	prior, priorSignature, err := m.service.RecallOperation(ctx, request.CoreID, request.OperationID)
	if err == nil {
		if priorSignature != signature {
			return api.RecallOperationResult{}, recallCommandConflict()
		}
		if prior.SessionID != sessionID {
			return api.RecallOperationResult{}, recallCommandConflict()
		}
		if result, handled, waitErr := m.waitLocalRecall(ctx, request, sessionID, signature); handled {
			return result, waitErr
		}
		return api.RecallOperationResult{Receipt: prior, ManagedByThisCore: prior.CoreID == m.core.CoreID}, nil
	}
	if errors.Is(err, history.ErrCommandIDConflict) {
		return api.RecallOperationResult{}, recallCommandConflict()
	}
	if !errors.Is(err, history.ErrNotFound) {
		return api.RecallOperationResult{}, err
	}

	m.mu.Lock()
	if err := m.coreError(request.CoreID); err != nil {
		m.mu.Unlock()
		return api.RecallOperationResult{}, err
	}
	if op := m.recallOperations[request.OperationID]; op != nil {
		if op.signature != signature {
			m.mu.Unlock()
			return api.RecallOperationResult{}, recallCommandConflict()
		}
		m.mu.Unlock()
		return waitRecallOperation(ctx, op)
	}
	if m.closing {
		m.mu.Unlock()
		return api.RecallOperationResult{}, &api.Error{Code: "core_closing", Message: "Core is shutting down"}
	}
	if runID := m.sessions[sessionID]; runID != "" {
		m.mu.Unlock()
		return api.RecallOperationResult{}, sessionBusy(runID, "Session execution and its reservation chain have not settled")
	}
	if op := m.recallBySession[sessionID]; op != nil {
		m.mu.Unlock()
		return api.RecallOperationResult{}, sessionBusy("", "Session is applying another recall operation")
	}
	generation := m.recallRevision[sessionID]
	result := ""
	if request.Action == "prepare" {
		generation++
	} else if _, ok := m.recallPending[sessionID]; ok {
		generation++
		result = "cleared"
	} else {
		result = "already_clear"
	}
	op := &recallOperation{sessionID: sessionID, request: request, signature: signature, generation: generation,
		result: api.RecallOperationResult{Receipt: contracts.RecallReceipt{Result: result}}, done: make(chan struct{})}
	m.recallOperations[request.OperationID] = op
	m.recallBySession[sessionID] = op
	m.wg.Add(1)
	m.mu.Unlock()

	go m.persistRecallOperation(op)
	return waitRecallOperation(ctx, op)
}

func (m *Manager) waitLocalRecall(ctx context.Context, request api.RecallOperationRequest, sessionID, signature string) (api.RecallOperationResult, bool, error) {
	m.mu.Lock()
	op := m.recallOperations[request.OperationID]
	if op == nil {
		m.mu.Unlock()
		return api.RecallOperationResult{}, false, nil
	}
	if op.signature != signature || op.sessionID != sessionID {
		m.mu.Unlock()
		return api.RecallOperationResult{}, true, recallCommandConflict()
	}
	m.mu.Unlock()
	result, err := waitRecallOperation(ctx, op)
	return result, true, err
}

func waitRecallOperation(ctx context.Context, op *recallOperation) (api.RecallOperationResult, error) {
	select {
	case <-ctx.Done():
		return api.RecallOperationResult{}, ctx.Err()
	case <-op.done:
		return op.result, op.err
	}
}

func (m *Manager) persistRecallOperation(op *recallOperation) {
	defer m.wg.Done()
	request := op.request
	// Recall may have completed after this request's initial durable lookup but
	// before it acquired the Manager reservation. Recheck after registration so
	// a delayed same-ID POST returns the saved result without republishing a
	// pending reference that may already have been claimed and consumed.
	prior, priorSignature, err := m.service.RecallOperation(context.Background(), request.CoreID, request.OperationID)
	if err == nil {
		if priorSignature != op.signature || prior.SessionID != op.sessionID {
			m.completeRecallOperation(op, api.RecallOperationResult{}, recallCommandConflict())
			return
		}
		m.completeRecallOperation(op, api.RecallOperationResult{Receipt: prior, ManagedByThisCore: prior.CoreID == m.core.CoreID}, nil)
		return
	}
	if errors.Is(err, history.ErrCommandIDConflict) {
		m.completeRecallOperation(op, api.RecallOperationResult{}, recallCommandConflict())
		return
	}
	if !errors.Is(err, history.ErrNotFound) {
		m.completeRecallOperation(op, api.RecallOperationResult{}, err)
		return
	}

	receipt := contracts.RecallReceipt{SchemaVersion: 1, CoreID: request.CoreID, SessionID: op.sessionID,
		OperationID: request.OperationID, Action: request.Action, Selector: request.Selector, Generation: op.generation,
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano), Result: op.result.Receipt.Result}
	var source contracts.ContextExecution
	if request.Action == "prepare" {
		source, err = m.service.SelectRecallSource(context.Background(), op.sessionID, request.Selector)
		if err != nil {
			m.completeRecallOperation(op, api.RecallOperationResult{}, mapRecallSourceError(err))
			return
		}
		receipt.SourceExecutionID = source.ExecutionID
		receipt.Result = "prepared"
	}

	saved, err := m.service.SaveRecallOperation(context.Background(), receipt, op.signature)
	if errors.Is(err, history.ErrCommandIDConflict) {
		err = recallCommandConflict()
	}
	if err != nil {
		m.completeRecallOperation(op, api.RecallOperationResult{}, err)
		return
	}
	if request.Action == "prepare" {
		if saved.SourceExecutionID != source.ExecutionID {
			// A durable same-ID receipt is authoritative. Resolve its saved source
			// metadata without holding the Manager lock before publishing it.
			source, err = m.service.SelectRecallSource(context.Background(), op.sessionID, saved.SourceExecutionID)
			if err != nil {
				m.completeRecallOperation(op, api.RecallOperationResult{}, err)
				return
			}
		}
	}

	m.mu.Lock()
	if m.recallOperations[request.OperationID] == op {
		if request.Action == "prepare" {
			m.recallPending[op.sessionID] = pendingRecall{reference: contracts.RecallReference{CoreID: saved.CoreID,
				OperationID: saved.OperationID, SourceExecutionID: saved.SourceExecutionID, Generation: saved.Generation},
				sourceOutcome: source.Outcome, preparedAt: saved.CompletedAt}
			m.recallRevision[op.sessionID] = saved.Generation
		} else if saved.Result == "cleared" {
			delete(m.recallPending, op.sessionID)
			m.recallRevision[op.sessionID] = saved.Generation
		}
		result := api.RecallOperationResult{Receipt: saved, ManagedByThisCore: saved.CoreID == m.core.CoreID}
		op.result = result
		delete(m.recallOperations, request.OperationID)
		delete(m.recallBySession, op.sessionID)
		close(op.done)
	}
	m.mu.Unlock()
}

func (m *Manager) completeRecallOperation(op *recallOperation, result api.RecallOperationResult, err error) {
	m.mu.Lock()
	if m.recallOperations[op.request.OperationID] == op {
		op.result = result
		op.err = err
		delete(m.recallOperations, op.request.OperationID)
		delete(m.recallBySession, op.sessionID)
		close(op.done)
	}
	m.mu.Unlock()
}

// RecallOperation joins an operation currently publishing on this Core, or
// reads its saved receipt. Reading a past Core's receipt never restores state.
func (m *Manager) RecallOperation(ctx context.Context, coreID, sessionID, operationID string) (api.RecallOperationResult, error) {
	if coreID == "" || sessionID == "" || operationID == "" {
		return api.RecallOperationResult{}, &api.Error{Code: "invalid_request", Message: "coreId, sessionId, and operationId are required"}
	}
	if coreID == m.core.CoreID {
		m.mu.Lock()
		op := m.recallOperations[operationID]
		if op != nil && op.sessionID != sessionID {
			m.mu.Unlock()
			return api.RecallOperationResult{}, recallOperationNotFound()
		}
		m.mu.Unlock()
		if op != nil {
			return waitRecallOperation(ctx, op)
		}
	}
	receipt, _, err := m.service.RecallOperation(ctx, coreID, operationID)
	return m.recallOperationResultAfterRead(ctx, coreID, sessionID, operationID, receipt, err)
}

// recallOperationResultAfterRead closes the race between the initial local
// registration check and the durable read. A POST may register and save its
// receipt while this GET is reading; in that case the durable receipt exists
// before pending state has been published. Join the local operation before
// treating the receipt as complete.
func (m *Manager) recallOperationResultAfterRead(ctx context.Context, coreID, sessionID, operationID string,
	receipt contracts.RecallReceipt, readErr error) (api.RecallOperationResult, error) {
	if coreID == m.core.CoreID {
		m.mu.Lock()
		op := m.recallOperations[operationID]
		if op != nil && op.sessionID != sessionID {
			m.mu.Unlock()
			return api.RecallOperationResult{}, recallOperationNotFound()
		}
		m.mu.Unlock()
		if op != nil {
			return waitRecallOperation(ctx, op)
		}
	}
	if errors.Is(readErr, history.ErrNotFound) || errors.Is(readErr, history.ErrCommandIDConflict) {
		return api.RecallOperationResult{}, recallOperationNotFound()
	}
	if readErr != nil {
		return api.RecallOperationResult{}, readErr
	}
	if receipt.SessionID != sessionID {
		return api.RecallOperationResult{}, recallOperationNotFound()
	}
	return api.RecallOperationResult{Receipt: receipt, ManagedByThisCore: receipt.CoreID == m.core.CoreID}, nil
}

func (m *Manager) recallAdmitted(run *managedRun, input runInput, admission contracts.ExecutionAdmission) {
	if admission.ExecutionID == "" || admission.ExecutionID != input.candidateExecutionID {
		return
	}
	m.mu.Lock()
	run.state.ExecutionID = admission.ExecutionID
	if input.recall != nil {
		pending, ok := m.recallPending[run.sessionReservation]
		if ok && pending.reference.Generation == input.recall.Generation && pending.claimedByRunID == run.state.RunID &&
			admission.RecallGeneration == input.recall.Generation {
			delete(m.recallPending, run.sessionReservation)
			m.recallRevision[run.sessionReservation]++
		}
	}
	m.changedLocked(run)
	m.mu.Unlock()
}

func (m *Manager) releaseRecallClaim(run *managedRun, input runInput) {
	if input.recall == nil {
		return
	}
	m.mu.Lock()
	if pending, ok := m.recallPending[run.sessionReservation]; ok && pending.reference.Generation == input.recall.Generation && pending.claimedByRunID == run.state.RunID {
		pending.claimedByRunID = ""
		m.recallPending[run.sessionReservation] = pending
		m.recallRevision[run.sessionReservation]++
	}
	m.mu.Unlock()
}

func sessionBusy(activeRunID, reason string) *api.Error {
	return &api.Error{Code: "session_busy", Message: reason, ActiveRunID: activeRunID}
}

func recallCommandConflict() *api.Error {
	return &api.Error{Code: "command_id_conflict", Message: "operationId already identifies a different command"}
}

func recallOperationNotFound() *api.Error {
	return &api.Error{Code: "recall_operation_not_found", Message: "recall operation was not found for this Session and Core"}
}

func mapRecallSourceError(err error) error {
	switch {
	case errors.Is(err, history.ErrRecallAmbiguous):
		return &api.Error{Code: "recall_ambiguous", Message: err.Error()}
	case errors.Is(err, history.ErrRecallNotFound), errors.Is(err, history.ErrNotFound):
		return &api.Error{Code: "recall_not_found", Message: err.Error()}
	default:
		return err
	}
}
