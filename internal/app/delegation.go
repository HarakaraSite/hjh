package app

import (
	"context"
	"errors"

	"hjh/internal/contracts"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

type delegation struct {
	store    *history.Store
	provider contracts.Provider
	input    executionInput
	journal  *journal
	control  *executionControl
	progress *progressSink
	children *childRuns
}

type preparedChild struct {
	input    executionInput
	provider contracts.Provider
}

func (d *delegation) RunAgent(ctx context.Context, request contracts.AgentRunRequest) (contracts.AgentRunResult, error) {
	prepared, err := d.prepareChild(ctx, request)
	if err != nil {
		return contracts.AgentRunResult{}, err
	}
	result, err := d.runChild(ctx, request, prepared)
	if result.ExecutionID != "" {
		settled, readErr := d.store.Execution(context.WithoutCancel(ctx), result.ExecutionID)
		if readErr != nil {
			return result, d.control.storage(readErr)
		}
		d.journal.link(request.CallID, history.Relation{Relation: "child_terminal", TargetRecordID: settled.TerminalRecordID, Mandatory: true})
	}
	return result, err
}

func (d *delegation) prepareChild(ctx context.Context, request contracts.AgentRunRequest) (preparedChild, error) {
	if err := ctx.Err(); err != nil {
		return preparedChild{}, err
	}
	snapshot, err := extensions.CaptureWithOptions(d.input.configPath, extensions.CaptureOptions{Role: request.Role, Tools: request.Tools})
	if err != nil {
		return preparedChild{}, err
	}
	workspaceContext, orientation, err := prepareWorkspaceContext(ctx, d.input.workspace, &snapshot)
	if err != nil {
		return preparedChild{}, err
	}
	registry, err := capturedRegistry(snapshot, d.input.configPath)
	if err != nil {
		return preparedChild{}, err
	}
	model, err := registry.Resolve(d.input.model, registry.Config.Roles[snapshot.Agent.Name].Model)
	if err != nil {
		return preparedChild{}, err
	}
	if request.Model != nil {
		model, err = registry.Resolve(model, contracts.ModelSelection{
			Provider: request.Model.Provider, ModelID: request.Model.ModelID, Effort: request.Model.Effort,
		})
		if err != nil {
			return preparedChild{}, err
		}
	}
	provider := d.provider
	if !d.input.injectedProvider {
		provider, err = d.input.connections.provider(ctx, registry, model, d.input.rootSessionID, d.input.configPath)
		if err != nil {
			return preparedChild{}, err
		}
	}
	snapshotJSON, err := executionSnapshot(snapshot)
	if err != nil {
		return preparedChild{}, err
	}
	if err = ctx.Err(); err != nil {
		return preparedChild{}, err
	}
	persist := context.WithoutCancel(ctx)
	if err = d.control.storage(d.store.SaveConfiguration(persist, snapshot.ConfigurationID, snapshotJSON)); err != nil {
		return preparedChild{}, err
	}
	child, err := d.store.BeginChildExecution(persist, history.BeginChildExecutionInput{Task: request.Task,
		ParentExecutionID: d.input.execution.ID, SpawnCallID: request.CallID, AgentName: snapshot.Agent.Name,
		Model: contracts.JSON(model), Build: d.input.execution.Build, ConfigurationID: snapshot.ConfigurationID, MaxSteps: int64(d.input.maxSteps)})
	if err = d.control.storage(err); err != nil {
		return preparedChild{}, err
	}
	childInput := d.input
	childInput.execution, childInput.snapshot, childInput.session = child, snapshot, nil
	childInput.workspaceContext, childInput.orientation = workspaceContext, orientation
	childInput.recall = nil
	childInput.steering, childInput.childInstructions, childInput.childObserve = nil, nil, nil
	childInput.model = model
	childInput.spawnRecordID = d.journal.startReference(request.CallID)
	return preparedChild{input: childInput, provider: provider}, nil
}

func (d *delegation) runChild(ctx context.Context, request contracts.AgentRunRequest, prepared preparedChild) (contracts.AgentRunResult, error) {
	result, runErr := execute(ctx, d.store, prepared.provider, prepared.input, d.control, d.progress)
	child := prepared.input.execution
	persist := context.WithoutCancel(ctx)
	settled, readErr := d.store.Execution(persist, child.ID)
	if readErr != nil {
		return contracts.AgentRunResult{}, d.control.storage(readErr)
	}
	if fatal := d.control.failure(); fatal != nil {
		return contracts.AgentRunResult{}, &contracts.ExecutionError{Err: errors.Join(runErr, fatal)}
	}
	output := contracts.AgentRunResult{ExecutionID: child.ID, Outcome: settled.Outcome, FinalText: result.FinalText, Error: result.Error}
	if err := d.progress.observe(contracts.Observation{ExecutionID: child.ID, ParentExecutionID: child.ParentExecutionID,
		ID: child.ID, Kind: "execution", Name: "run", Phase: settled.Outcome, Result: contracts.JSON(output)}); err != nil {
		return output, err
	}
	if ctx.Err() != nil {
		return output, errors.Join(ctx.Err(), runErr)
	}
	return output, nil
}
