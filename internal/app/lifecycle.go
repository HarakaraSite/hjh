package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"hjh/internal/contracts"
	"hjh/internal/history"
	"hjh/internal/jsruntime"
)

// executionLifecycle owns dynamic instruction contributions and hook boundaries
// for one root or child Worker. The VM and storage retain their own ownership.
type executionLifecycle struct {
	input       executionInput
	worker      *jsruntime.Worker
	journal     *journal
	runtime     contracts.HookRuntime
	instruction string
	components  []json.RawMessage
	handlers    map[string]bool
	turnStarted bool
}

func newExecutionLifecycle(input executionInput, worker *jsruntime.Worker, journal *journal) *executionLifecycle {
	now := time.Now()
	zone := os.Getenv("TZ")
	if zone == "" {
		zone, _ = now.Zone()
	}
	role := "root"
	if input.execution.ParentExecutionID != "" {
		role = "child"
	}
	l := &executionLifecycle{input: input, worker: worker, journal: journal,
		instruction: input.snapshot.SystemInstruction,
		components:  append([]json.RawMessage{}, input.snapshot.InstructionComponents...),
		handlers:    map[string]bool{}, runtime: contracts.HookRuntime{
			WorkspaceRoot: input.workspace, AgentName: input.snapshot.Agent.Name, Role: role,
			SessionID: input.rootSessionID, ExecutionID: input.execution.ID,
			ParentExecutionID: input.execution.ParentExecutionID, SpawnCallID: input.execution.SpawnCallID,
			TurnNumber: input.execution.Turn, Model: input.model, StartedAt: now.Format(time.RFC3339Nano), TimeZone: zone}}
	for _, name := range worker.HookHandlers() {
		l.handlers[name] = true
	}
	return l
}

func (l *executionLifecycle) context() contracts.HookContext {
	h := contracts.HookHistory{SessionID: l.input.rootSessionID, FromTurn: l.input.fromTurn,
		NextTurn: l.input.execution.Turn, ExecutionLocators: []string{l.input.execution.ID}}
	if l.input.session != nil {
		h.NextTurn, h.MessageCount = l.input.session.NextTurn, l.input.session.MessageCount
	}
	return contracts.HookContext{SystemInstruction: l.instruction, InstructionComponents: l.components,
		Tools: l.worker.Definitions(), History: h}
}

func (l *executionLifecycle) start(ctx context.Context) error {
	if l.input.snapshot.HookModule() != "" {
		if err := l.journal.observe(contracts.Observation{ID: uuid.NewString(), Kind: "hook", Name: "handlers", Phase: "configured",
			Result: contracts.JSON(map[string]any{"module": l.input.snapshot.HookModule(), "handlers": append([]string{}, l.worker.HookHandlers()...)})}); err != nil {
			return err
		}
	}
	if err := l.addContext(ctx, "runtimeStart", nil); err != nil {
		return err
	}
	l.turnStarted = true
	return l.addContext(ctx, "beforeTurn", &l.input.execution.Task)
}

func (l *executionLifecycle) addContext(ctx context.Context, name string, task *string) error {
	if !l.handlers[name] {
		return nil
	}
	value, err := l.worker.Hook(ctx, name, contracts.JSON(contracts.LifecycleHookInput{
		Runtime: l.runtime, Context: l.context(), Task: task}))
	if err != nil {
		return errors.Join(fmt.Errorf("%s hook: %w", name, err), l.notice(name, "failed", err.Error()))
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || len(value) == 0 {
		return nil
	}
	var addition contracts.HookContextAddition
	if err := json.Unmarshal(value, &addition); err != nil || addition.Context == nil {
		err := fmt.Errorf("%s hook must return {context: string[]} or no value", name)
		return errors.Join(err, l.notice(name, "failed", err.Error()))
	}
	text := strings.Join(addition.Context, "\n")
	if text == "" {
		return nil
	}
	l.instruction += "\n\n" + text
	l.components = append(l.components, contracts.JSON(map[string]string{
		"identity": "instruction:hook:" + name, "text": text, "hook": name,
		"sourceLocator": l.input.snapshot.HookModule(), "recordId": l.journal.hookResultReference(name)}))
	return nil
}

func (l *executionLifecycle) notice(name, phase, message string) error {
	return l.journal.observe(contracts.Observation{Kind: "context", Name: "hook/" + name, Phase: phase,
		Preview: "Hook " + name + ": " + message, Error: message})
}

// finish preserves the settled model result. JavaScript cleanup failures are
// durable notices; a failure to persist those facts remains a storage failure.
func (l *executionLifecycle) finish(ctx context.Context, store *history.Store, result any, reason, adoptionError string, settled bool) error {
	var settlement *contracts.HookSettlement
	if settled {
		execution, err := store.Execution(ctx, l.input.execution.ID)
		if err != nil {
			return l.journal.control.storage(err)
		}
		settlement = &contracts.HookSettlement{Outcome: execution.Outcome, Adoption: execution.Adoption,
			TerminalRecordID: execution.TerminalRecordID, AdoptionError: adoptionError}
		l.journal.enterPostSettlement()
		// The context locator describes the now-persisted canonical range.
		if l.input.session != nil {
			session, err := store.Session(ctx, l.input.rootSessionID)
			if err != nil {
				return l.journal.control.storage(err)
			}
			l.input.session = &session
		}
	}
	var finishErr error
	for _, name := range []string{"afterTurn", "runtimeStop"} {
		if !l.handlers[name] || (name == "afterTurn" && !l.turnStarted) {
			continue
		}
		if name == "afterTurn" && !settled {
			finishErr = errors.Join(finishErr, l.skipped(name, "settlement_unavailable"))
			continue
		}
		if !l.worker.Available() {
			finishErr = errors.Join(finishErr, l.skipped(name, "worker_unavailable"))
			continue
		}
		input := contracts.LifecycleHookInput{Runtime: l.runtime, Context: l.context(), Settlement: settlement}
		if name == "afterTurn" {
			input.Outcome = contracts.JSON(result)
		} else {
			input.Reason = reason
		}
		_, err := l.worker.Hook(ctx, name, contracts.JSON(input))
		if err != nil {
			finishErr = errors.Join(finishErr, l.notice(name, "failed", err.Error()))
		}
	}
	return finishErr
}

func (l *executionLifecycle) skipped(name, reason string) error {
	err := l.journal.observe(contracts.Observation{ID: uuid.NewString(), Kind: "hook", Name: name,
		Phase: "skipped", Error: reason})
	return errors.Join(err, l.notice(name, "skipped", reason))
}
