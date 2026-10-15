package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"hjh/internal/api"
)

type runtimeInputOperation struct {
	request       api.SubmitInputRequest
	coreID        string
	sessionID     string
	runID         string
	draft         string
	draftRevision uint64
}

type inputChainCancellation struct {
	coreID string
	runID  string
}

func (m *model) noteDraftEdit() {
	m.draftRevision++
	m.updateCommandMenu(false)
}

func (m *model) clearDraft() {
	if m.textarea.Value() != "" {
		m.textarea.SetValue("")
		m.noteDraftEdit()
	}
}

func (m *model) submitRuntimeInput(kind string) {
	run := m.activeRun
	if run == nil {
		m.status = "no active run accepts additional input"
		return
	}
	accepted := run.inputAcceptance.Steering
	if kind == "follow-up" {
		accepted = run.inputAcceptance.FollowUp
	}
	if !accepted {
		m.status = kind + " is not currently accepted · draft kept"
		return
	}
	draft := m.textarea.Value()
	if strings.TrimSpace(draft) == "" {
		m.status = "enter text before sending " + kind
		return
	}
	coreID := run.coreID
	if coreID == "" {
		coreID = m.core.CoreID
	}
	request := api.SubmitInputRequest{
		CoreID: coreID, SessionID: run.session, InputID: uuid.NewString(), Kind: kind, Text: draft,
	}
	if kind == "follow-up" {
		request.Next = &api.NextRunOptions{
			Role: roleID(m.roleID, m.roleSet), ProviderID: m.providerID, ModelID: modelID(m.modelID, m.modelSet),
			Effort: m.effort, AuthProfile: m.authProfile, MaxSteps: copyInt(m.maxSteps), FromTurn: copyInt64(m.fromTurn),
		}
	}
	m.runtimeInputQueue = append(m.runtimeInputQueue, runtimeInputOperation{
		request: request, coreID: coreID, sessionID: run.session, runID: run.id,
		draft: draft, draftRevision: m.draftRevision,
	})
	m.status = kind + " sending · draft remains until Core confirms receipt"
	m.startNextRuntimeInput()
}

func modelID(value string, set bool) string {
	if !set {
		return ""
	}
	return value
}

func roleID(value string, set bool) string {
	if !set {
		return ""
	}
	return value
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (m *model) startNextRuntimeInput() {
	if m.runtimeInputSending || len(m.runtimeInputQueue) == 0 {
		return
	}
	input := m.runtimeInputQueue[0]
	m.runtimeInputSending = true
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.launch(job, func() tea.Msg {
		receipt, err := m.submitAndReconcileInput(ctx, input)
		return runtimeInputSubmittedMsg{jobID: job.id, input: input, receipt: receipt, err: err}
	})
}

func (m *model) submitAndReconcileInput(ctx context.Context, input runtimeInputOperation) (api.InputReceipt, error) {
	receipt, err := m.backend.SubmitInput(ctx, input.runID, input.request)
	if err == nil || ctx.Err() != nil {
		return receipt, err
	}
	if isAPIError(err) {
		return api.InputReceipt{}, err
	}

	// The POST may have committed before its response was lost. Resolve that
	// exact operation by its original ID before considering a same-ID retry.
	receipt, lookupErr := m.backend.Input(ctx, input.coreID, input.runID, input.request.InputID, false)
	if lookupErr == nil {
		return receipt, nil
	}
	if ctx.Err() != nil {
		return api.InputReceipt{}, errors.Join(err, lookupErr)
	}

	receipt, retryErr := m.backend.SubmitInput(ctx, input.runID, input.request)
	if retryErr == nil {
		return receipt, nil
	}
	if !isAPIError(retryErr) && ctx.Err() == nil {
		if found, finalLookupErr := m.backend.Input(ctx, input.coreID, input.runID, input.request.InputID, false); finalLookupErr == nil {
			return found, nil
		} else {
			lookupErr = errors.Join(lookupErr, finalLookupErr)
		}
	}
	return api.InputReceipt{}, errors.Join(err, lookupErr, retryErr)
}

func isAPIError(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr)
}

func (m *model) acceptRuntimeInput(msg runtimeInputSubmittedMsg) tea.Cmd {
	if msg.jobID == 0 || len(m.runtimeInputQueue) == 0 || m.runtimeInputQueue[0].request.InputID != msg.input.request.InputID {
		return nil
	}
	m.runtimeInputQueue = m.runtimeInputQueue[1:]
	m.runtimeInputSending = false
	if msg.err != nil {
		if msg.input.sessionID == m.sessionID {
			m.status = msg.input.request.Kind + " unconfirmed · draft kept · " + msg.err.Error()
		}
		m.startNextRuntimeInput()
		return nil
	}
	if !receiptMatchesInput(msg.receipt, msg.input) {
		if msg.input.sessionID == m.sessionID {
			m.status = msg.input.request.Kind + " unconfirmed · receipt did not match the submitted input · draft kept"
		}
		m.startNextRuntimeInput()
		return nil
	}
	if msg.input.sessionID == m.sessionID && msg.receipt.CoreID == m.core.CoreID && msg.receipt.SessionID == m.sessionID {
		m.noteRuntimeInputRevision(msg.receipt.CoreID, msg.receipt.SessionID, msg.receipt.Revision)
		m.mergeRuntimeReceipt(msg.receipt)
	}
	showNotice := msg.input.sessionID == m.sessionID && (m.activeRun == nil || m.runtimeReceiptIsCurrent(msg.receipt))
	if receiptWasAccepted(msg.receipt) {
		if m.draftRevision == msg.input.draftRevision && m.core.CoreID == msg.input.coreID &&
			m.sessionID == msg.input.sessionID && m.textarea.Value() == msg.input.draft {
			m.clearDraft()
		}
		if showNotice {
			m.status = msg.input.request.Kind + " accepted · " + msg.receipt.Status
		}
	} else if showNotice {
		m.status = msg.input.request.Kind + " not accepted · draft kept · " + msg.receipt.Status
	}
	if msg.input.sessionID == m.sessionID {
		m.invalidateTranscript()
	}
	m.startNextRuntimeInput()
	return nil
}

func receiptMatchesInput(receipt api.InputReceipt, input runtimeInputOperation) bool {
	return receipt.CoreID == input.coreID && receipt.SessionID == input.sessionID && receipt.RunID == input.runID &&
		receipt.InputID == input.request.InputID && receipt.Kind == input.request.Kind
}

func receiptWasAccepted(receipt api.InputReceipt) bool {
	switch receipt.Kind {
	case "steering":
		return receipt.Status == "accepted" || receipt.Status == "applied" || receipt.Status == "unapplied"
	case "follow-up":
		return receipt.Status == "queued" || receipt.Status == "starting" || receipt.Status == "started" || receipt.Status == "not_started"
	default:
		return false
	}
}

func (m *model) loadRuntimeInputs(sessionID string, allCores bool) {
	if sessionID == "" || m.runtimeReadPending {
		return
	}
	coreID, after := m.core.CoreID, m.runtimeWatermarks[m.core.CoreID]
	if allCores || m.runtimeReceiptIndex == nil {
		coreID, after = "", 0
	} else if _, ok := m.runtimeWatermarks[m.core.CoreID]; !ok {
		coreID, after = "", 0
	}
	m.runtimeReadPending = true
	m.runtimeReadSession, m.runtimeReadCore, m.runtimeReadAfter = sessionID, coreID, after
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.runtimeReadJobID = job.id
	generation := m.sessionGeneration
	m.launch(job, func() tea.Msg {
		page, err := m.backend.Inputs(ctx, sessionID, coreID, after, false)
		return runtimeInputsLoadedMsg{jobID: job.id, generation: generation, sessionID: sessionID, queryCoreID: coreID, page: page, err: err}
	})
}

func (m *model) noteRuntimeInputRevision(coreID, sessionID string, revision uint64) {
	if coreID == "" {
		coreID = m.core.CoreID
	}
	if coreID != m.core.CoreID || sessionID == "" || sessionID != m.sessionID {
		return
	}
	if m.runtimeDesiredCore != coreID || m.runtimeDesiredSession != sessionID {
		m.runtimeDesiredCore = coreID
		m.runtimeDesiredSession = sessionID
		m.runtimeDesiredRevision = 0
	}
	if revision > m.runtimeDesiredRevision {
		m.runtimeDesiredRevision = revision
	}
	m.retryRuntimeInputCatchUp()
}

func (m *model) retryRuntimeInputCatchUp() {
	if m.runtimeDesiredCore != "" && m.runtimeDesiredCore == m.core.CoreID &&
		m.runtimeDesiredSession != "" && m.runtimeDesiredSession == m.sessionID &&
		m.runtimeDesiredRevision > m.runtimeWatermarks[m.runtimeDesiredCore] {
		m.loadRuntimeInputs(m.runtimeDesiredSession, false)
	}
}

func (m *model) acceptRuntimeInputs(msg runtimeInputsLoadedMsg) tea.Cmd {
	if !m.runtimeReadPending || msg.jobID != m.runtimeReadJobID {
		return nil
	}
	m.runtimeReadPending = false
	if msg.generation != m.sessionGeneration || msg.sessionID != m.sessionID {
		return nil
	}
	if msg.err != nil {
		m.runtimeReceiptError = "receipt refresh failed"
		m.status = "runtime input receipts unavailable · " + msg.err.Error()
		return nil
	}
	m.runtimeReceiptError = ""
	for _, receipt := range msg.page.Receipts {
		m.mergeRuntimeReceipt(receipt)
	}
	if msg.page.CoreID != "" {
		if current := m.runtimeWatermarks[msg.page.CoreID]; msg.page.Revision > current || msg.queryCoreID == "" {
			m.runtimeWatermarks[msg.page.CoreID] = msg.page.Revision
		}
	}
	m.retryRuntimeInputCatchUp()
	return nil
}

func (m *model) mergeRuntimeReceipt(receipt api.InputReceipt) {
	key := receipt.CoreID + "\x00" + receipt.InputID
	if m.runtimeReceiptIndex == nil {
		m.runtimeReceiptIndex = make(map[string]int)
	}
	if index, ok := m.runtimeReceiptIndex[key]; ok && index >= 0 && index < len(m.runtimeReceipts) {
		if receipt.Revision < m.runtimeReceipts[index].Revision {
			return
		}
		m.runtimeReceipts[index] = receipt
		return
	}
	m.runtimeReceiptIndex[key] = len(m.runtimeReceipts)
	m.runtimeReceipts = append(m.runtimeReceipts, receipt)
}

func (m *model) clearRuntimeInputs() {
	m.runtimeReceipts = nil
	m.runtimeReceiptIndex = make(map[string]int)
	m.runtimeWatermarks = make(map[string]uint64)
	m.runtimeDesiredCore = ""
	m.runtimeDesiredSession = ""
	m.runtimeDesiredRevision = 0
	m.runtimeReceiptError = ""
}

// Receipt history belongs to the Session; only the active run belongs in the footer.
func (m *model) runtimeInputStatus() string {
	if m.runtimeReceiptError != "" {
		return m.runtimeReceiptError
	}
	var latest *api.InputReceipt
	for i := range m.runtimeReceipts {
		receipt := &m.runtimeReceipts[i]
		if m.runtimeReceiptIsCurrent(*receipt) && (latest == nil || receipt.Revision >= latest.Revision) {
			latest = receipt
		}
	}
	if latest == nil {
		return ""
	}
	return runtimeInputReceiptStatus(*latest)
}

func (m *model) runtimeReceiptIsCurrent(receipt api.InputReceipt) bool {
	run := m.activeRun
	return run != nil && receipt.CoreID == run.coreID && receipt.SessionID == run.session && receipt.RunID == run.id
}

func runtimeInputReceiptStatus(receipt api.InputReceipt) string {
	label := receipt.Kind
	if receipt.Kind == "follow-up" {
		label = "follow-up"
	} else if receipt.Kind == "steering" {
		label = "steering"
	}
	status := label + " · " + receipt.Status
	if receipt.Reason != "" {
		status += " · " + receipt.Reason
	}
	return status
}

func formatRuntimeInputs(receipts []api.InputReceipt) string {
	if len(receipts) == 0 {
		return "No runtime inputs in this Session."
	}
	var b strings.Builder
	for i, receipt := range receipts {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s · %s · input %s · #%d · run %s · core %s · revision %d",
			receipt.Kind, receipt.Status, receipt.InputID, receipt.Sequence, receipt.RunID, receipt.CoreID, receipt.Revision)
		if !receipt.ManagedByThisCore {
			b.WriteString(" · historical read-only receipt")
		}
		if receipt.StartedRunID != "" {
			fmt.Fprintf(&b, " · started run %s", receipt.StartedRunID)
		}
		if receipt.Reason != "" {
			fmt.Fprintf(&b, " · %s", receipt.Reason)
		}
	}
	return b.String()
}

func (m *model) requestInputChainCancel(run *activeRun) {
	if run != nil {
		run.chainCancelRequested = true
		m.chainCancel = &inputChainCancellation{coreID: run.coreID, runID: run.id}
		m.chainCancelSettled = false
	} else if m.chainCancel == nil || m.chainCancelSettled {
		return
	}
	if m.pendingChainCancel {
		return
	}
	if m.chainCancel == nil || m.chainCancel.coreID == "" || m.chainCancel.runID == "" {
		m.status = "run input chain unavailable for cancellation"
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.pendingChainCancel = true
	m.chainCancelJobID = job.id
	target := *m.chainCancel
	m.launch(job, func() tea.Msg {
		state, err := m.cancelAndWaitInputChain(ctx, target)
		return runtimeInputChainSettledMsg{jobID: job.id, coreID: target.coreID, runID: target.runID, state: state, err: err}
	})
}

func (m *model) cancelAndWaitInputChain(ctx context.Context, target inputChainCancellation) (api.InputChainState, error) {
	if _, err := m.backend.CancelInputChain(ctx, target.coreID, target.runID); err != nil {
		state, getErr := m.backend.InputChain(ctx, target.coreID, target.runID)
		if getErr != nil {
			return api.InputChainState{}, errors.Join(err, getErr)
		}
		if state.Phase == "open" {
			if _, retryErr := m.backend.CancelInputChain(ctx, target.coreID, target.runID); retryErr != nil {
				state, getErr = m.backend.InputChain(ctx, target.coreID, target.runID)
				if getErr != nil || state.Phase == "open" {
					return api.InputChainState{}, errors.Join(err, retryErr, getErr)
				}
			}
		}
	}
	for {
		state, err := m.backend.InputChain(ctx, target.coreID, target.runID)
		if err != nil {
			return api.InputChainState{}, err
		}
		if state.Phase == "settled" {
			return state, nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return api.InputChainState{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *model) acceptInputChainSettlement(msg runtimeInputChainSettledMsg) tea.Cmd {
	if !m.pendingChainCancel || msg.jobID != m.chainCancelJobID || m.chainCancel == nil ||
		msg.coreID != m.chainCancel.coreID || msg.runID != m.chainCancel.runID {
		return nil
	}
	m.pendingChainCancel = false
	if msg.err != nil {
		m.status = "input chain cancellation not confirmed · " + msg.err.Error()
		return nil
	}
	m.chainCancelSettled = true
	m.status = "input chain settled · Core confirmed cancellation and saved statuses"
	m.applyPendingSessionIfSettled()
	return nil
}

func (m *model) applyPendingSessionIfSettled() {
	if m.pendingSession == nil || m.activeRun != nil || m.pendingChainCancel ||
		m.chainCancel != nil && !m.chainCancelSettled {
		return
	}
	action := *m.pendingSession
	m.pendingSession = nil
	m.chainCancel = nil
	m.chainCancelSettled = false
	m.applySessionAction(action)
}
