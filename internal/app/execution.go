package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"hjh/internal/agent"
	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/history"
	"hjh/internal/jsruntime"
)

// executionControl belongs to one root run and is shared by its descendants.
// Storage errors remain in Go even when generated JavaScript catches a rejection.
type executionControl struct {
	mu     sync.Mutex
	err    error
	cancel context.CancelFunc
}

func (c *executionControl) failure() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
func (c *executionControl) storage(err error) error {
	if err == nil {
		return nil
	}
	if c == nil {
		return err
	}
	c.mu.Lock()
	c.err = errors.Join(c.err, err)
	c.mu.Unlock()
	c.cancel()
	return &contracts.ExecutionError{Err: err}
}

// Display serialization is separate from each execution's journal state.
type progressSink struct {
	mu       sync.Mutex
	observer contracts.Observer
}

func (p *progressSink) observe(o contracts.Observation) error {
	if p.observer == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.observer(o)
}

type executionInput struct {
	execution                            history.Execution
	snapshot                             extensions.Snapshot
	workspace, configPath, rootSessionID string
	model                                contracts.ModelSelection
	maxSteps                             int
	session                              *history.Session // root only; children start with independent messages
	previousModel                        contracts.ModelSelection
	fromTurn                             int64
	spawnRecordID                        string
	connections                          *connectionFactory
	injectedProvider                     bool
}

type guardedExecutor struct {
	worker  *jsruntime.Worker
	control *executionControl
}

func (e guardedExecutor) Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if err := e.control.failure(); err != nil {
		return nil, &contracts.ExecutionError{Err: err}
	}
	value, err := e.worker.Call(ctx, name, args)
	if fatal := e.control.failure(); fatal != nil {
		return nil, &contracts.ExecutionError{Err: errors.Join(err, fatal)}
	}
	return value, err
}

// execute owns one admitted execution. Root and child use the same Agent loop,
// VM, source archive and settlement; only root loads/adopts Session messages.
func execute(ctx context.Context, store *history.Store, provider contracts.Provider, input executionInput,
	control *executionControl, progress *progressSink) (result agent.Result, err error) {
	persist := context.WithoutCancel(ctx)
	execution := input.execution
	j := newJournal(persist, store, execution.ID, progress.observe)
	j.parentExecutionID, j.control = execution.ParentExecutionID, control
	if input.spawnRecordID != "" {
		j.link(execution.ID, history.Relation{Relation: "parent_call", TargetRecordID: input.spawnRecordID, Mandatory: true})
	}
	result = agent.Result{Task: execution.Task, Outcome: "contract_failure", StopReason: "contract_failure"}
	var worker *jsruntime.Worker
	stopWatcher, watched := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-ctx.Done():
			_ = j.observe(contracts.Observation{Kind: "control", Name: "cancel", Phase: "requested", Error: ctx.Err().Error()})
		case <-stopWatcher:
		}
	}()
	defer func() {
		if worker != nil {
			err = errors.Join(err, worker.Close())
		}
		close(stopWatcher)
		<-watched
		fatal := control.failure()
		if fatal != nil {
			err = errors.Join(err, &contracts.ExecutionError{Err: fatal})
		}
		outcome := history.OutcomeFailed
		if result.OK && err == nil {
			outcome = history.OutcomeCompleted
		} else if ctx.Err() != nil {
			outcome = history.OutcomeCancelled
		}
		if fatal != nil && execution.ParentExecutionID == "" {
			outcome = history.OutcomeFailed
			result.Outcome, result.StopReason = "history_failure", "history_failure"
		} else if outcome == history.OutcomeCancelled {
			result.Outcome, result.StopReason = "cancelled", "cancelled"
		}
		if err != nil {
			result.OK = false
			result.Error = err.Error()
		}
		settleErr := control.storage(store.EndExecution(persist, execution.ID, outcome, contracts.JSON(result), ""))
		err = errors.Join(err, settleErr)
		if outcome == history.OutcomeCompleted && settleErr == nil && execution.ParentExecutionID == "" {
			err = errors.Join(err, control.storage(store.AdoptCanonical(persist, execution.ID, "")))
		}
		if err != nil {
			result.OK = false
			result.Error = err.Error()
		}
	}()
	if err = j.observe(contracts.Observation{ID: execution.ID, Kind: "execution", Name: "run", Phase: "started",
		Input: contracts.JSON(map[string]any{"task": execution.Task, "sessionId": input.rootSessionID, "model": input.model, "role": input.snapshot.Agent.Name})}); err != nil {
		return result, err
	}
	for _, resource := range input.snapshot.Resources {
		_, e := store.AppendOccurrence(persist, execution.ID, history.OccurrenceInput{Kind: "resource_revision", Payload: contracts.JSON(map[string]any{
			"configurationId": input.snapshot.ConfigurationID, "logicalIdentity": resource.LogicalIdentity, "sourceLocator": resource.SourceLocator,
			"contentDigest": resource.ContentDigest}), Content: resource.Bytes})
		if err = control.storage(e); err != nil {
			return result, err
		}
	}
	var messages []contracts.Message
	if input.session != nil {
		stored, e := store.Conversation(ctx, input.rootSessionID, input.fromTurn, -1)
		if e != nil {
			return result, e
		}
		for _, saved := range stored {
			var m contracts.Message
			if e = json.Unmarshal(saved.JSON, &m); e != nil {
				return result, e
			}
			if saved.TurnNumber < input.session.PrivateStateFromTurn || input.previousModel.Provider != input.model.Provider || input.previousModel.ModelID != input.model.ModelID {
				m.ProviderState = nil
			}
			messages = append(messages, m)
		}
	}
	runner := &delegation{store: store, provider: provider, input: input, journal: j, control: control, progress: progress}
	worker, err = jsruntime.New(ctx, jsruntime.Options{Snapshot: input.snapshot, Workspace: input.workspace, Observer: j.observe,
		ObserverParent: execution.ID, Agents: runner})
	if err != nil {
		return result, err
	}
	attribution := contextAttribution{store: store, ctx: persist, executionID: execution.ID}
	result, err = agent.Run(ctx, agent.Input{Task: execution.Task, Model: input.model, Instruction: input.snapshot.SystemInstruction,
		History: messages, Tools: worker.Definitions(), MaxSteps: input.maxSteps}, provider, guardedExecutor{worker, control},
		func(m contracts.Message) error {
			b, e := contracts.EncodeJSON(m)
			if e != nil {
				return e
			}
			if _, e = store.AppendMessage(persist, execution.ID, b); e != nil {
				return control.storage(e)
			}
			kind := m.Role + "_message"
			if m.Role == "tool" {
				kind = "tool_result"
			}
			_, e = store.AppendOccurrence(persist, execution.ID, history.OccurrenceInput{Kind: kind, Payload: b, Content: b})
			return control.storage(e)
		}, j.observe, func(step int, req contracts.ModelRequest) error {
			if e := control.failure(); e != nil {
				return &contracts.ExecutionError{Err: e}
			}
			return control.storage(attribution.request(step, req))
		})
	return result, err
}
