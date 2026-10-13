// Package app supplies common run and Session operations for the CLI and future UI.
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"

	"hjh/internal/agent"
	"hjh/internal/buildinfo"
	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/executiondata"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

type RunOptions struct {
	Task                 string
	Role                 string
	SessionID            string
	Workspace            string
	ConfigPath           string
	ModelFavoritesDir    string
	ModelID              string
	ProviderID           string
	AuthProfile          string
	CredentialFile       string
	CredentialStateDir   string
	Credentials          *credentials.Manager
	HTTPClient           *http.Client
	Effort               string
	MaxSteps             int
	FromTurn             int64
	NewSessionID         string
	AdmissionExecutionID string
	Recall               *contracts.RecallReference
	OnSession            func(string)
	OnAdmission          func(contracts.ExecutionAdmission)
	Progress             contracts.Observer
	ExecutionDataOwner   *executiondata.Owner
	Steering             contracts.SteeringSource
	TaskContentDigest    string
}
type RunResult struct {
	SessionID   string       `json:"sessionId"`
	ExecutionID string       `json:"executionId"`
	Result      agent.Result `json:"result"`
}

func Run(ctx context.Context, store *history.Store, provider contracts.Provider, options RunOptions) (output RunResult, err error) {
	injected := provider != nil
	workspace := options.Workspace
	var session history.Session
	var active contracts.ModelSelection
	if options.SessionID != "" {
		session, err = store.Session(ctx, options.SessionID)
		if err != nil {
			return output, err
		}
		workspace = session.WorkspaceRoot
		if err = json.Unmarshal(session.ActiveModel, &active); err != nil {
			return output, err
		}
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return output, err
	}
	role := options.Role
	if role == "" && options.SessionID != "" {
		var choice struct {
			Name string `json:"name"`
		}
		if err = json.Unmarshal(session.AgentChoice, &choice); err != nil {
			return output, err
		}
		role = choice.Name
	}
	configPath, err := prepareConfig(workspace, options.ConfigPath)
	if err != nil {
		return output, err
	}
	snapshot, err := extensions.Capture(configPath, role)
	if err != nil {
		return output, err
	}
	workspaceContext, orientation, err := prepareWorkspaceContext(ctx, workspace, &snapshot)
	if err != nil {
		return output, err
	}
	registry, err := capturedRegistry(snapshot, configPath)
	if err != nil {
		return output, err
	}
	model, err := resolveModelSelection(registry, snapshot.Agent.Name, options.SessionID != "", active, workspace, options)
	if err != nil {
		return output, err
	}
	addExecutionContextInstruction(&snapshot, model, workspace)
	if options.SessionID == "" {
		session, err = store.CreateSession(ctx, history.SessionInput{ID: options.NewSessionID, WorkspaceRoot: workspace, AgentChoice: contracts.JSON(map[string]any{"name": snapshot.Agent.Name}), ActiveModel: contracts.JSON(model)})
		if err != nil {
			return output, err
		}
	}
	output.SessionID = session.ID
	if options.OnSession != nil {
		options.OnSession(session.ID)
	}
	factory := newConnectionFactory(options)
	defer factory.close()
	if options.CredentialFile != "" {
		factory.overrideProfile = model.AuthProfile
		factory.overrideFile = options.CredentialFile
	}
	if provider == nil {
		provider, err = factory.provider(ctx, registry, model, session.ID, configPath)
		if err != nil {
			return output, err
		}
	}
	snapshotJSON, err := executionSnapshot(snapshot)
	if err != nil {
		return output, err
	}
	if err = store.SaveConfiguration(ctx, snapshot.ConfigurationID, snapshotJSON); err != nil {
		return output, err
	}
	manifest, err := buildinfo.Current()
	if err != nil {
		return output, err
	}
	execution, err := store.BeginExecution(ctx, history.BeginExecutionInput{ExecutionID: options.AdmissionExecutionID,
		Task: options.Task, SessionID: session.ID, AgentName: snapshot.Agent.Name,
		Model: contracts.JSON(model), Build: contracts.JSON(manifest), ConfigurationID: snapshot.ConfigurationID,
		MaxSteps: int64(options.MaxSteps), TaskContentDigest: options.TaskContentDigest, Recall: options.Recall})
	if err != nil {
		return output, err
	}
	output.ExecutionID = execution.ID
	if options.OnAdmission != nil {
		admission := contracts.ExecutionAdmission{ExecutionID: execution.ID, RecallRecordID: execution.RecallRecordID}
		if options.Recall != nil {
			admission.RecallGeneration = options.Recall.Generation
		}
		options.OnAdmission(admission)
	}
	if options.ExecutionDataOwner == nil {
		options.ExecutionDataOwner = executiondata.NewOwner()
	}
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	control := &executionControl{cancel: cancel}
	progress := &progressSink{observer: options.Progress}
	output.Result, err = execute(workCtx, store, provider, executionInput{execution: execution, snapshot: snapshot,
		workspace: workspace, configPath: configPath, model: model, maxSteps: options.MaxSteps, recall: options.Recall,
		workspaceContext: workspaceContext, orientation: orientation,
		rootSessionID: session.ID, rootExecutionID: execution.ID, session: &session, previousModel: active, fromTurn: options.FromTurn,
		connections: factory, injectedProvider: injected, executionDataOwner: options.ExecutionDataOwner,
		steering: options.Steering}, control, progress)
	return output, err
}
