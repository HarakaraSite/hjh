package app

import (
	"context"
	"encoding/json"
	"errors"

	"hjh/internal/contracts"
	"hjh/internal/credentials"
	"hjh/internal/extensions"
	"hjh/internal/history"
)

// Service supplies the same execution and query operations to product surfaces.
// The caller owns the store's lifetime; Service owns connection credentials and pending browser login lifetimes, not VM state.
type Service struct {
	store       *history.Store
	provider    contracts.Provider
	options     RunOptions
	connections *connectionFactory
}

func NewService(store *history.Store, provider contracts.Provider, options RunOptions) *Service {
	return &Service{store: store, provider: provider, options: options, connections: newConnectionFactory(options)}
}

// RunRequest contains selections for one execution, shared by all product surfaces.
type RunRequest struct {
	Task, SessionID, ModelID, Workspace, Role string
	ProviderID, Effort, AuthProfile           string
	NewSessionID                              string
	MaxSteps                                  *int
	FromTurn                                  *int64
	OnSession                                 func(string)
	Progress                                  contracts.Observer
}

func (s *Service) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	options := s.options
	options.Credentials = s.connections.credentials
	options.Task = request.Task
	if request.Workspace != "" {
		options.Workspace = request.Workspace
	}
	options.SessionID = request.SessionID
	options.NewSessionID = request.NewSessionID
	options.OnSession = request.OnSession
	if request.MaxSteps != nil {
		options.MaxSteps = *request.MaxSteps
	}
	if request.FromTurn != nil {
		options.FromTurn = *request.FromTurn
	}
	if request.ModelID != "" {
		options.ModelID = request.ModelID
	}
	if request.ProviderID != "" {
		options.ProviderID = request.ProviderID
	}
	if request.Effort != "" {
		options.Effort = request.Effort
	}
	if request.AuthProfile != "" {
		options.AuthProfile = request.AuthProfile
	}
	options.Role = request.Role
	options.Progress = request.Progress
	return Run(ctx, s.store, s.provider, options)
}

func (s *Service) Roles(ctx context.Context, workspace string) (extensions.RoleCatalog, error) {
	if err := ctx.Err(); err != nil {
		return extensions.RoleCatalog{}, err
	}
	if workspace == "" {
		workspace = s.options.Workspace
	}
	return Roles(workspace, s.options.ConfigPath)
}

// ResolveLoginTarget returns the selected ChatGPT provider and credential
// profile reference without exposing the profile's credential values.
func (s *Service) ResolveLoginTarget(ctx context.Context, workspace, providerID string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	registry, err := s.connectionRegistry(workspace)
	if err != nil {
		return "", "", err
	}
	if providerID == "" {
		providerID = registry.Config.Model.Provider
	}
	definition, err := registry.Definition(providerID)
	if err != nil {
		return "", "", err
	}
	if definition.Auth.Kind != credentials.KindChatGPTOAuth {
		return "", "", errors.New("selected provider does not use ChatGPT authentication")
	}
	return providerID, definition.Auth.Profile, nil
}

func (s *Service) Sessions(ctx context.Context) ([]history.Session, error) {
	return s.store.ListSessions(ctx)
}
func (s *Service) Session(ctx context.Context, id string) (history.Session, error) {
	return s.store.Session(ctx, id)
}
func (s *Service) Conversation(ctx context.Context, id string, from, to int64) ([]history.Message, error) {
	return s.store.Conversation(ctx, id, from, to)
}
func (s *Service) Executions(ctx context.Context, id string) ([]history.Execution, error) {
	return s.store.Executions(ctx, id)
}

type Inspection struct {
	Execution     history.Execution   `json:"execution"`
	Children      []history.Execution `json:"children"`
	Messages      []history.Message   `json:"messages"`
	Records       []history.Record    `json:"records"`
	Configuration json.RawMessage     `json:"configuration"`
	Context       json.RawMessage     `json:"context"`
}

func (s *Service) Inspect(ctx context.Context, id string, from, to int64) (output Inspection, err error) {
	output.Execution, err = s.store.Execution(ctx, id)
	if err != nil {
		return output, err
	}
	output.Children, err = s.store.Children(ctx, id)
	if err != nil {
		return output, err
	}
	output.Messages, err = s.store.Messages(ctx, id, from, to)
	if err != nil {
		return output, err
	}
	output.Records, err = s.store.Records(ctx, id, from, to)
	if err != nil {
		return output, err
	}
	configuration, err := s.store.Configuration(ctx, output.Execution.ConfigurationID)
	if err != nil {
		return output, err
	}
	output.Configuration = json.RawMessage(configuration.Snapshot)
	manifest, err := s.store.Context(ctx, id)
	if err != nil && !errors.Is(err, history.ErrNotFound) {
		return output, err
	}
	output.Context = json.RawMessage(manifest)
	return output, nil
}

// Close joins pending browser login listeners. The caller first cancels and
// joins runs and UI operations, then closes Service before its history store.
func (s *Service) Close() error { return s.connections.close() }
