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
}

func (d *delegation) RunAgent(ctx context.Context, request contracts.AgentRunRequest) (output contracts.AgentRunResult, err error) {
	if err = ctx.Err(); err != nil {
		return output, err
	}
	snapshot, err := extensions.Capture(d.input.configPath, request.Role)
	if err != nil {
		return output, err
	}
	workspaceContext, orientation, err := prepareWorkspaceContext(ctx, d.input.workspace, &snapshot)
	if err != nil {
		return output, err
	}
	registry, err := capturedRegistry(snapshot, d.input.configPath)
	if err != nil {
		return output, err
	}
	model, err := registry.Resolve(d.input.model, registry.Config.Roles[snapshot.Agent.Name].Model)
	if err != nil {
		return output, err
	}
	provider := d.provider
	if !d.input.injectedProvider {
		provider, err = d.input.connections.provider(ctx, registry, model, d.input.rootSessionID, d.input.configPath)
		if err != nil {
			return output, err
		}
	}
	snapshotJSON, err := executionSnapshot(snapshot)
	if err != nil {
		return output, err
	}
	if err = ctx.Err(); err != nil {
		return output, err
	}
	persist := context.WithoutCancel(ctx)
	if err = d.control.storage(d.store.SaveConfiguration(persist, snapshot.ConfigurationID, snapshotJSON)); err != nil {
		return output, err
	}
	child, err := d.store.BeginChildExecution(persist, history.BeginChildExecutionInput{Task: request.Task,
		ParentExecutionID: d.input.execution.ID, SpawnCallID: request.CallID, AgentName: snapshot.Agent.Name,
		Model: contracts.JSON(model), Build: d.input.execution.Build, ConfigurationID: snapshot.ConfigurationID, MaxSteps: int64(d.input.maxSteps)})
	if err = d.control.storage(err); err != nil {
		return output, err
	}
	childInput := d.input
	childInput.execution, childInput.snapshot, childInput.session = child, snapshot, nil
	childInput.workspaceContext, childInput.orientation = workspaceContext, orientation
	childInput.recall = nil
	childInput.model = model
	childInput.spawnRecordID = d.journal.startReference(request.CallID)
	result, runErr := execute(ctx, d.store, provider, childInput, d.control, d.progress)
	settled, readErr := d.store.Execution(persist, child.ID)
	if readErr != nil {
		return output, d.control.storage(readErr)
	}
	if fatal := d.control.failure(); fatal != nil {
		return output, &contracts.ExecutionError{Err: errors.Join(runErr, fatal)}
	}
	d.journal.link(request.CallID, history.Relation{Relation: "child_terminal", TargetRecordID: settled.TerminalRecordID, Mandatory: true})
	output = contracts.AgentRunResult{ExecutionID: child.ID, Outcome: settled.Outcome, FinalText: result.FinalText, Error: result.Error}
	if err = d.progress.observe(contracts.Observation{ExecutionID: child.ID, ParentExecutionID: child.ParentExecutionID,
		ID: child.ID, Kind: "execution", Name: "run", Phase: settled.Outcome, Result: contracts.JSON(output)}); err != nil {
		return output, err
	}
	if ctx.Err() != nil {
		return output, errors.Join(ctx.Err(), runErr)
	}
	return output, nil
}
