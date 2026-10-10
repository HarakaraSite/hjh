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
	"hjh/internal/extensions"
	"hjh/internal/history"
)

type RunOptions struct {
	Task           string
	Role           string
	SessionID      string
	Workspace      string
	ConfigPath     string
	ModelID        string
	ProviderID     string
	AuthProfile    string
	CredentialFile string
	Credentials    *credentials.Manager
	HTTPClient     *http.Client
	Effort         string
	MaxSteps       int
	FromTurn       int64
	NewSessionID   string
	OnSession      func(string)
	Progress       contracts.Observer
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
	registry, err := capturedRegistry(snapshot, configPath)
	if err != nil {
		return output, err
	}
	model := active
	if options.SessionID == "" {
		model, err = registry.Resolve(contracts.ModelSelection{}, registry.Config.Model)
		if err == nil {
			model, err = registry.Resolve(model, registry.Config.Roles[snapshot.Agent.Name].Model)
		}
		if err != nil {
			return output, err
		}
	}
	model, err = registry.Resolve(model, contracts.ModelSelection{Provider: options.ProviderID, ModelID: options.ModelID,
		AuthProfile: options.AuthProfile, Effort: options.Effort})
	if err != nil {
		return output, err
	}
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
		provider, err = factory.provider(ctx, registry, model, session.ID)
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
	execution, err := store.BeginExecution(ctx, history.BeginExecutionInput{Task: options.Task, SessionID: session.ID, AgentName: snapshot.Agent.Name,
		Model: contracts.JSON(model), Build: contracts.JSON(manifest), ConfigurationID: snapshot.ConfigurationID, MaxSteps: int64(options.MaxSteps)})
	if err != nil {
		return output, err
	}
	output.ExecutionID = execution.ID
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	control := &executionControl{cancel: cancel}
	progress := &progressSink{observer: options.Progress}
	output.Result, err = execute(workCtx, store, provider, executionInput{execution: execution, snapshot: snapshot,
		workspace: workspace, configPath: configPath, model: model, maxSteps: options.MaxSteps,
		rootSessionID: session.ID, session: &session, previousModel: active, fromTurn: options.FromTurn,
		connections: factory, injectedProvider: injected}, control, progress)
	return output, err
}
