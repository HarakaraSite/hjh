package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"hjh/internal/agent"
	"hjh/internal/contextbudget"
	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/extensions"
	"hjh/internal/history"
	"hjh/internal/host"
	"hjh/internal/jsruntime"
	"hjh/internal/recall"
	"hjh/internal/workspacecontext"
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
	workspaceContext                     workspacecontext.Snapshot
	orientation                          contracts.Orientation
	workspace, configPath, rootSessionID string
	rootExecutionID                      string
	model                                contracts.ModelSelection
	maxSteps                             int
	recall                               *contracts.RecallReference
	session                              *history.Session // root only; children start with independent messages
	previousModel                        contracts.ModelSelection
	fromTurn                             int64
	spawnRecordID                        string
	connections                          *connectionFactory
	injectedProvider                     bool
	executionDataOwner                   *executiondata.Owner
	steering                             contracts.SteeringSource
	childInstructions                    *childInstructionOwner
	childObserve                         contracts.Observer
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
	owner := input.executionDataOwner
	if owner == nil {
		owner = executiondata.NewOwner()
	}
	j := newJournal(persist, store, execution.ID, input.rootExecutionID, execution.Task, owner, func(o contracts.Observation) error {
		if input.childObserve != nil {
			if e := input.childObserve(o); e != nil {
				return e
			}
		}
		return progress.observe(o)
	})
	j.parentExecutionID, j.control = execution.ParentExecutionID, control
	j.spawnCallID = execution.SpawnCallID
	result = agent.Result{Task: execution.Task, Outcome: "contract_failure", StopReason: "contract_failure"}
	var additional *steeringAdapter
	if execution.ParentExecutionID == "" && input.steering != nil {
		additional = &steeringAdapter{store: store, journal: j, source: input.steering}
		defer func() {
			err = errors.Join(err, additional.Close(steeringCloseReason(ctx, result, err)))
		}()
	}
	j.rootExecutionOrdinal, err = store.ExecutionDisplayOrdinal(persist, input.rootExecutionID)
	if err != nil {
		if input.childInstructions != nil {
			input.childInstructions.attachJournal(nil, err)
			_ = input.childInstructions.Close(steeringCloseReason(ctx, result, err))
		}
		return result, control.storage(err)
	}
	if input.childInstructions != nil {
		input.childInstructions.attachJournal(j, nil)
	}
	if input.spawnRecordID != "" {
		j.link(execution.ID, history.Relation{Relation: "parent_call", TargetRecordID: input.spawnRecordID, Mandatory: true})
	}
	var worker *jsruntime.Worker
	var lifecycle *executionLifecycle
	var runner *delegation
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
		if additional != nil {
			err = errors.Join(err, additional.Close(steeringCloseReason(ctx, result, err)))
		}
		if input.childInstructions != nil {
			err = errors.Join(err, input.childInstructions.Close(steeringCloseReason(ctx, result, err)))
		}
		if runner != nil {
			runner.children.Close()
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
		settledResult := result
		settleErr := j.end(outcome, settledResult)
		err = errors.Join(err, settleErr)
		var adoptionErr error
		if outcome == history.OutcomeCompleted && settleErr == nil && execution.ParentExecutionID == "" {
			adoptionErr = control.storage(store.AdoptCanonical(persist, execution.ID, ""))
			err = errors.Join(err, adoptionErr)
		}
		if err != nil {
			result.OK = false
			result.Error = err.Error()
		}
		if lifecycle != nil {
			adoptionMessage := ""
			if adoptionErr != nil {
				adoptionMessage = adoptionErr.Error()
			}
			err = errors.Join(err, lifecycle.finish(persist, store, settledResult, string(outcome), adoptionMessage, settleErr == nil))
		}
		if worker != nil {
			err = errors.Join(err, worker.Close())
		}
		if fatal := control.failure(); fatal != nil {
			err = errors.Join(err, &contracts.ExecutionError{Err: fatal})
		}
	}()
	if err = j.observe(contracts.Observation{ID: execution.ID, Kind: "execution", Name: "run", Phase: "started",
		Input: contracts.JSON(map[string]any{"task": execution.Task, "sessionId": input.rootSessionID, "model": input.model,
			"role": input.snapshot.Agent.Name, "context": input.orientation})}); err != nil {
		return result, err
	}
	resources := append([]extensions.Resource(nil), input.snapshot.Resources...)
	for _, instruction := range input.workspaceContext.Instructions {
		resources = append(resources, extensions.Resource{LogicalIdentity: "workspace-instruction:" + instruction.Path,
			SourceLocator: instruction.Path, ContentDigest: instruction.Digest, Bytes: instruction.Bytes})
	}
	for _, resource := range resources {
		_, e := store.AppendOccurrence(persist, execution.ID, history.OccurrenceInput{Kind: "resource_revision", Payload: contracts.JSON(map[string]any{
			"configurationId": input.snapshot.ConfigurationID, "logicalIdentity": resource.LogicalIdentity, "sourceLocator": resource.SourceLocator,
			"contentDigest": resource.ContentDigest}), Content: resource.Bytes})
		if err = control.storage(e); err != nil {
			return result, err
		}
	}
	var messages []contracts.Message
	var recallSource *recallAttribution
	if input.recall != nil {
		evidence, e := store.RecallEvidence(ctx, input.recall.SourceExecutionID)
		if e != nil {
			return result, e
		}
		var selection contracts.ModelSelection
		if e = json.Unmarshal(evidence.Execution.Model, &selection); e != nil {
			return result, fmt.Errorf("app recall: decode source model selection: %w", e)
		}
		source := recall.Evidence{Execution: recall.Execution{ExecutionID: evidence.Execution.ID,
			SessionID: evidence.Execution.SessionCorrelation, ParentExecutionID: evidence.Execution.ParentExecutionID,
			Turn: evidence.Execution.Turn, AgentName: evidence.Execution.AgentName, Selection: selection,
			Lifecycle: evidence.Execution.Lifecycle, Outcome: evidence.Execution.Outcome, Task: evidence.Execution.Task}}
		for _, message := range evidence.Messages {
			source.Messages = append(source.Messages, recall.Message{ExecutionID: message.ExecutionID,
				Ordinal: message.Ordinal, JSON: message.JSON})
		}
		for _, record := range evidence.Records {
			source.Records = append(source.Records, recall.Record{ID: record.ID, ExecutionID: record.ExecutionID,
				Ordinal: record.Ordinal, Kind: record.Kind, Payload: record.Payload, Conversation: record.Conversation})
		}
		projection, e := recall.Project(source)
		if e != nil {
			return result, e
		}
		message, attribution, e := recallMessage(projection, *input.recall, execution.ID, execution.RecallRecordID)
		if e != nil {
			return result, e
		}
		if _, e = j.appendMessageOrdinal(message); e != nil {
			return result, e
		}
		recallSource = &attribution
		messages = append(messages, message)
	}
	runner = &delegation{store: store, provider: provider, input: input, journal: j, control: control, progress: progress}
	runner.children = newChildRuns(ctx, runner)
	registry, err := capturedRegistry(input.snapshot, input.configPath)
	if err != nil {
		return result, err
	}
	runtimeHost, err := host.New(input.workspace)
	if err != nil {
		return result, err
	}
	runtimeHost.RequestProvider = input.connections.toolRequestProvider(registry, input.configPath)
	worker, err = jsruntime.New(persist, jsruntime.Options{Snapshot: input.snapshot, Workspace: input.workspace, Observer: j.observe,
		ObserverParent: execution.ID, Host: runtimeHost, Agents: runner, AgentControl: runner.children})
	if err != nil {
		return result, err
	}
	lifecycle = newExecutionLifecycle(input, worker, j)
	if err = lifecycle.start(ctx); err != nil {
		return result, err
	}
	attribution := contextAttribution{store: store, ctx: persist, executionID: execution.ID, recall: recallSource,
		instructionSources: lifecycle.instructionSources()}
	budget, metadataError := input.connections.budget(ctx, registry, input.model, input.injectedProvider, input.configPath)
	var prefix []contextbudget.Entry
	for i, message := range messages {
		prefix = append(prefix, contextbudget.Entry{Message: message, Source: contextbudget.Source{
			Kind: "prefix", ExecutionID: execution.ID, Turn: execution.Turn, MessageOrdinal: int64(i)}})
	}
	projector := contextbudget.New(budget, budgetHistoryReader(store, input), prefix)
	notices := map[string]bool{}
	var additionalInput agent.AdditionalInput
	if additional != nil {
		additionalInput = additional
	} else if input.childInstructions != nil {
		additionalInput = input.childInstructions
	}
	result, err = agent.Run(ctx, agent.Input{Task: execution.Task, Model: input.model, Instruction: lifecycle.instruction,
		History: messages, Tools: worker.Definitions(), MaxSteps: input.maxSteps, AdditionalInput: additionalInput,
		ProjectRequest: func(projectCtx context.Context, step int, request contracts.ModelRequest) (contracts.ModelRequest, error) {
			current := make([]contextbudget.Entry, 0, len(request.Messages)-len(prefix))
			for i, message := range request.Messages[len(prefix):] {
				current = append(current, contextbudget.Entry{Message: message, Source: contextbudget.Source{
					Kind: "current", ExecutionID: execution.ID, Turn: execution.Turn, MessageOrdinal: int64(i + len(prefix))}})
			}
			request.Messages = nil
			projection, projectErr := projector.Project(projectCtx, request, current, provider.Prepare)
			var exceeded *contextbudget.ExceededError
			if projectErr != nil && !errors.As(projectErr, &exceeded) {
				return request, projectErr
			}
			payload := contracts.JSON(map[string]any{"modelStep": step, "modelSelection": input.model,
				"fact": projection.Fact, "metadataError": metadataError, "exceeded": exceeded != nil})
			if _, e := store.AppendOccurrence(persist, execution.ID, history.OccurrenceInput{
				Kind: "context_budget", Payload: payload, Content: projection.Request.WireBody}); e != nil {
				return request, control.storage(e)
			}
			attribution.sources, attribution.budget = projection.Sources, payload
			if e := contextBudgetNotice(j, step, projection.Fact, notices); e != nil {
				return request, e
			}
			return projection.Request, projectErr
		}}, provider, guardedExecutor{worker, control},
		func(m contracts.Message) error {
			return j.appendMessage(m)
		}, j.observe, func(step int, req contracts.ModelRequest) error {
			if e := control.failure(); e != nil {
				return &contracts.ExecutionError{Err: e}
			}
			return control.storage(attribution.request(step, req))
		})
	return result, err
}
