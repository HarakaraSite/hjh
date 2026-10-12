package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/google/uuid"
	"hjh/internal/api"
	"hjh/internal/sessionid"
)

type recallUIOperation struct {
	coreID, sessionID string
	generation        uint64
	request           api.RecallOperationRequest
	processing        bool
}

type recallResultMsg struct {
	jobID     uint64
	operation *recallUIOperation
	result    api.RecallOperationResult
	err       error
}

func (m *model) contextCommand(fields []string) {
	if len(fields) != 1 {
		m.status = "usage: /context"
		return
	}
	request := m.orientationRequest()
	coreID, sessionID, generation := m.core.CoreID, m.sessionID, m.sessionGeneration
	runID := currentRunID(m.activeRun)
	selection := m.selectionRequest()
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.contextRequestID++
	requestID := m.contextRequestID
	m.contextJobID = job.id
	title := "context"
	if sessionID != "" {
		title += " · " + m.shortSessionID(sessionID)
	}
	m.status = "loading " + title
	m.launch(job, func() tea.Msg {
		orientation, orientationErr := m.backend.Orientation(ctx, request)
		var state api.SessionContext
		var stateErr error
		var preview api.SelectionPreview
		var selectionErr error
		if sessionID != "" {
			state, stateErr = m.backend.SessionContext(ctx, coreID, sessionID)
			preview, selectionErr = m.backend.Selection(ctx, selection)
		}
		return contextLoadedMsg{
			jobID: job.id, requestID: requestID, generation: generation,
			coreID: coreID, request: request, sessionID: sessionID, runID: runID,
			orientation: orientation, orientErr: orientationErr,
			state: state, stateErr: stateErr, selection: preview, selectErr: selectionErr,
		}
	})
}

func formatSessionContext(state api.SessionContext, knownIDs ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Session %s\nAdopted · %d turns · %d messages · revision %d", sessionid.Short(state.SessionID, knownIDs...),
		state.Canonical.TurnCount, state.Canonical.MessageCount, state.Canonical.StateRevision)
	if state.ActiveRunID != "" {
		fmt.Fprintf(&b, "\nActive run · %s", state.ActiveRunID)
	}
	if root := state.LatestRootExecution; root != nil {
		fmt.Fprintf(&b, "\nLatest root · %s · %s / %s", root.ExecutionID, root.Lifecycle, root.Outcome)
	}
	if request := state.LatestRequest; request != nil {
		fmt.Fprintf(&b, "\nSaved input preparation · execution %s · step %d · request %d\n%s / %s · effort %s\n%d items · %d messages · %d tools · %d instructions · %d wire bytes",
			request.ExecutionID, request.ModelStep, request.RequestOrdinal, request.Selection.Provider,
			request.Selection.ModelID, request.Selection.Effort, request.ItemCount, request.MessageCount,
			request.ToolContractCount, request.InstructionCount, request.WireBytes)
	} else {
		b.WriteString("\nSaved input preparation · none")
	}
	if budget := state.LatestBudget; budget != nil {
		b.WriteString(formatCoreBudget(*budget))
	}

	if pending := state.PendingRecall; pending != nil {
		fmt.Fprintf(&b, "\nPending recall · %s · %s · generation %d", pending.SourceExecutionID, pending.SourceOutcome, pending.Generation)
		if pending.ClaimedByRunID != "" {
			fmt.Fprintf(&b, " · claimed by %s", pending.ClaimedByRunID)
		}
	} else {
		b.WriteString("\nPending recall · none")
	}
	if use := state.LatestRecallAdmission; use != nil {
		fmt.Fprintf(&b, "\nRecall admission · %s → %s", use.SourceExecutionID, use.TargetExecutionID)
		if use.ReferenceMessageOrdinal != nil {
			fmt.Fprintf(&b, " · reference saved (message %d)", *use.ReferenceMessageOrdinal)
		}
		if use.RequestRecordOrdinal != nil {
			fmt.Fprintf(&b, " · input preparation saved (record %d)", *use.RequestRecordOrdinal)
		}
	}
	return b.String()
}

func (m *model) recallCommand(fields []string) {
	if len(fields) > 2 {
		m.status = "usage: /recall [ID|latest|clear]"
		return
	}
	if m.sessionID == "" {
		m.status = "start or resume a Session before preparing recall"
		return
	}
	request := api.RecallOperationRequest{CoreID: m.core.CoreID, Action: "prepare"}
	if len(fields) == 2 {
		if fields[1] == "clear" {
			request.Action = "clear"
		} else {
			request.Selector = fields[1]
		}
	}
	if m.recallOperations == nil {
		m.recallOperations = make(map[string]*recallUIOperation)
	}
	key := m.core.CoreID + "/" + m.sessionID
	op := m.recallOperations[key]
	if op != nil {
		if op.processing {
			m.status = "recall operation is being confirmed"
			return
		}
		if op.request.Action != request.Action || op.request.Selector != request.Selector {
			m.status = "previous recall outcome unconfirmed; retry the same /recall command first"
			return
		}
	} else {
		request.OperationID = uuid.NewString()
		op = &recallUIOperation{coreID: m.core.CoreID, sessionID: m.sessionID, request: request}
		m.recallOperations[key] = op
	}
	op.generation = m.sessionGeneration
	op.processing = true
	ctx, cancel := context.WithCancel(m.ctx)
	job := m.newJob(cancel)
	m.status = "confirming recall · " + op.request.OperationID
	m.launch(job, func() tea.Msg {
		result, err := m.backend.Recall(ctx, op.sessionID, op.request)
		if err != nil {
			var typed *api.Error
			if !errors.As(err, &typed) {
				// A transport failure can follow a successful Core operation. Query
				// the same identity; never replace it with a new operation ID.
				if saved, readErr := m.backend.RecallOperation(ctx, op.coreID, op.sessionID, op.request.OperationID); readErr == nil {
					result, err = saved, nil
				}
			}
		}
		return recallResultMsg{jobID: job.id, operation: op, result: result, err: err}
	})
}

func (m *model) applyRecallResult(msg recallResultMsg) {
	op := msg.operation
	if op == nil {
		return
	}
	op.processing = false
	var typed *api.Error
	known := msg.err == nil || errors.As(msg.err, &typed)
	if known {
		delete(m.recallOperations, op.coreID+"/"+op.sessionID)
	}
	if op.coreID != m.core.CoreID || op.sessionID != m.sessionID || op.generation != m.sessionGeneration {
		return
	}
	if msg.err != nil {
		m.status = "recall failed: " + msg.err.Error()
		if !known {
			m.status = "recall outcome unconfirmed · retry same command · " + op.request.OperationID
		}
		return
	}
	receipt := msg.result.Receipt
	text := "Recall operation · " + receipt.Result
	if receipt.SourceExecutionID != "" {
		text += " · source " + receipt.SourceExecutionID
	}
	text += "\n/context shows the current pending reference and its consumption."
	m.appendEntry("recall", text)
	m.status = "recall · " + receipt.Result
}
