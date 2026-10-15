package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"hjh/internal/contracts"
	"hjh/internal/executiondata"
	"hjh/internal/extensions"
	"hjh/internal/history"
	"hjh/internal/modelfavorites"
)

// Service supplies the same execution and query operations to product surfaces.
// The caller owns the store's lifetime; Service owns credentials, pending
// browser login lifetimes, in-process request/budget snapshots, and extension memory.
type Service struct {
	store         *history.Store
	provider      contracts.Provider
	options       RunOptions
	connections   *connectionFactory
	favorites     *modelfavorites.Store
	snapshotOwner *SnapshotOwner
	selectionMu   sync.Mutex
	closeOnce     sync.Once
	closeErr      error
}

func NewService(store *history.Store, provider contracts.Provider, options RunOptions) *Service {
	if options.SnapshotOwner == nil {
		options.SnapshotOwner = NewSnapshotOwner()
	}
	if options.ContextMemory == nil {
		options.ContextMemory = NewContextMemory()
	}
	return &Service{store: store, provider: provider, options: options, connections: newConnectionFactory(options),
		favorites: modelfavorites.New(options.ModelFavoritesDir), snapshotOwner: options.SnapshotOwner}
}

// RunRequest contains selections for one execution, shared by all product surfaces.
type RunRequest struct {
	Task, SessionID, ModelID, Workspace, Role string
	ProviderID, Effort, AuthProfile           string
	TaskContentDigest                         string
	NewSessionID                              string
	AdmissionExecutionID                      string
	Recall                                    *contracts.RecallReference
	MaxSteps                                  *int
	FromTurn                                  *int64
	Steering                                  contracts.SteeringSource
	OnSession                                 func(string)
	OnAdmission                               func(contracts.ExecutionAdmission)
	Progress                                  contracts.Observer
	ExecutionDataOwner                        *executiondata.Owner
}

func (s *Service) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	options := s.runOptions(request)
	return Run(ctx, s.store, s.provider, options)
}

// SnapshotStatus returns the in-process state, or the persisted state when no
// entry for that execution remains in this Service.
func (s *Service) SnapshotStatus(executionID string) SnapshotStatus {
	if status, exists := s.snapshotOwner.statusWithPresence(executionID); exists {
		return status
	}
	status, err := s.store.LatestSnapshotStatus(context.Background(), executionID)
	if err != nil {
		return SnapshotStatus{ExecutionID: executionID, State: contracts.SnapshotSaveUnavailable}
	}
	return status
}

// SaveSnapshot retries a frozen failed snapshot. Active-memory entries and
// persisted states without an in-process entry are returned without saving.
func (s *Service) SaveSnapshot(ctx context.Context, executionID string) (SnapshotStatus, error) {
	if _, exists := s.snapshotOwner.statusWithPresence(executionID); exists {
		return s.snapshotOwner.retry(ctx, s.store, executionID)
	}
	return s.store.LatestSnapshotStatus(ctx, executionID)
}

// SnapshotSessionID resolves the Session protected by a snapshot save. Live
// entries keep this identity in memory; older saved executions use history.
func (s *Service) SnapshotSessionID(ctx context.Context, executionID string) (string, error) {
	if sessionID, ok := s.snapshotOwner.sessionID(executionID); ok && sessionID != "" {
		return sessionID, nil
	}
	execution, err := s.store.Execution(ctx, executionID)
	if err != nil {
		return "", err
	}
	return execution.SessionCorrelation, nil
}

func (s *Service) FailedChildSnapshotExecutionIDs(rootExecutionID string) []string {
	return s.snapshotOwner.failedExecutionIDs(rootExecutionID)
}

func (s *Service) runOptions(request RunRequest) RunOptions {
	options := s.options
	options.Credentials = s.connections.credentials
	options.Task = request.Task
	options.TaskContentDigest = request.TaskContentDigest
	options.Steering = request.Steering
	if request.Workspace != "" {
		options.Workspace = request.Workspace
	}
	options.SessionID = request.SessionID
	options.NewSessionID = request.NewSessionID
	options.AdmissionExecutionID = request.AdmissionExecutionID
	options.Recall = request.Recall
	options.OnSession = request.OnSession
	options.OnAdmission = request.OnAdmission
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
	if request.Role != "" {
		options.Role = request.Role
	} else if request.SessionID != "" {
		// A service startup role is a default for new Sessions. A resumed
		// Session keeps its saved role unless the request explicitly changes it.
		options.Role = ""
	}
	options.Progress = request.Progress
	if request.ExecutionDataOwner != nil {
		options.ExecutionDataOwner = request.ExecutionDataOwner
	}
	return options
}

// SaveRuntimeInput persists an accepted input receipt and its shared body.
func (s *Service) SaveRuntimeInput(ctx context.Context, receipt contracts.InputReceipt, signature string, body []byte) (contracts.InputReceipt, error) {
	return s.store.SaveRuntimeInput(ctx, receipt, signature, body)
}

func (s *Service) RuntimeInput(ctx context.Context, coreID, inputID string, includeText bool) (contracts.InputReceipt, error) {
	return s.store.RuntimeInput(ctx, coreID, inputID, includeText)
}

func (s *Service) RuntimeInputs(ctx context.Context, sessionID, coreID, currentCoreID string, afterRevision uint64, includeText bool) (contracts.InputReceiptPage, error) {
	return s.store.RuntimeInputs(ctx, sessionID, coreID, currentCoreID, afterRevision, includeText)
}

func (s *Service) UpdateRuntimeInput(ctx context.Context, receipt contracts.InputReceipt) (contracts.InputReceipt, error) {
	return s.store.UpdateRuntimeInput(ctx, receipt)
}

func (s *Service) SaveInputControl(ctx context.Context, executionID string, control json.RawMessage) error {
	return s.store.SaveInputControl(ctx, executionID, control)
}

func (s *Service) SessionContext(ctx context.Context, sessionID string) (contracts.SessionContext, error) {
	result, err := s.store.SessionContext(ctx, sessionID)
	if err != nil {
		return result, err
	}
	if s.mergeSessionContextSnapshots(&result) {
		result, err = s.store.SessionContext(ctx, sessionID)
		if err != nil {
			return result, err
		}
		_ = s.mergeSessionContextSnapshots(&result)
	}
	return result, nil
}

func (s *Service) SelectRecallSource(ctx context.Context, sessionID, selector string) (contracts.ContextExecution, error) {
	return s.store.SelectRecallSource(ctx, sessionID, selector)
}

func (s *Service) SaveRecallOperation(ctx context.Context, receipt contracts.RecallReceipt, signature string) (contracts.RecallReceipt, error) {
	return s.store.SaveRecallOperation(ctx, receipt, signature)
}

func (s *Service) RecallOperation(ctx context.Context, coreID, operationID string) (contracts.RecallReceipt, string, error) {
	return s.store.RecallOperation(ctx, coreID, operationID)
}

func (s *Service) ResolveInputSelection(ctx context.Context, request RunRequest) (contracts.InputSelection, error) {
	preview, err := s.PreviewSelection(ctx, request)
	if err != nil {
		return contracts.InputSelection{}, err
	}
	options := s.runOptions(request)
	return contracts.InputSelection{Role: preview.Role, Selection: preview.Selection, MaxSteps: options.MaxSteps, FromTurn: options.FromTurn}, nil
}

func (s *Service) CaptureConversationCut(ctx context.Context, rootExecutionID string) (history.ConversationCut, error) {
	return s.store.CaptureConversationCut(ctx, rootExecutionID)
}

func (s *Service) CaptureSessionConversationCut(ctx context.Context, sessionID string) (history.SessionConversationCut, error) {
	return s.store.CaptureSessionConversationCut(ctx, sessionID)
}

func (s *Service) WalkConversationCut(ctx context.Context, cut history.ConversationCut, visit func(contracts.ExecutionFact) error) error {
	return s.store.WalkConversationCut(ctx, cut, visit)
}

func (s *Service) WalkSessionConversationCut(ctx context.Context, cut history.SessionConversationCut, visit func(contracts.ExecutionFact) error) error {
	return s.store.WalkSessionConversationCut(ctx, cut, visit)
}

func (s *Service) TerminalResult(ctx context.Context, executionID string) (history.TerminalResult, error) {
	return s.store.TerminalResult(ctx, executionID)
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

func (s *Service) Sessions(ctx context.Context) ([]history.Session, error) {
	return s.store.ListSessions(ctx)
}
func (s *Service) Session(ctx context.Context, id string) (history.Session, error) {
	return s.store.Session(ctx, id)
}
func (s *Service) DeleteSession(ctx context.Context, id string) error {
	err := s.store.DeleteSession(ctx, id)
	if err == nil || errors.Is(err, history.ErrNotFound) {
		s.snapshotOwner.deleteSession(id)
		s.options.ContextMemory.deleteSession(id)
	}
	return err
}
func (s *Service) RenameSession(ctx context.Context, id, title string) (history.Session, error) {
	return s.store.RenameSession(ctx, id, title)
}
func (s *Service) Conversation(ctx context.Context, id string, from, to int64) ([]history.Message, error) {
	return s.store.Conversation(ctx, id, from, to)
}
func (s *Service) Executions(ctx context.Context, id string) ([]history.Execution, error) {
	return s.store.Executions(ctx, id)
}

// Record reads one detail locator without loading execution messages, captured
// configuration, context, or any other tool result.
func (s *Service) Record(ctx context.Context, executionID string, ordinal int64) (history.Record, error) {
	capture, hasCapture := s.snapshotOwner.capture(executionID)
	records, err := s.store.Records(ctx, executionID, ordinal, ordinal)
	if err != nil {
		return history.Record{}, err
	}
	if len(records) == 0 {
		return history.Record{}, history.ErrNotFound
	}
	if err := s.mergeSnapshotRecord(&records[0], capture, hasCapture); err != nil {
		return history.Record{}, err
	}
	return records[0], nil
}

func (s *Service) Content(ctx context.Context, digest string) ([]byte, error) {
	return s.store.Content(ctx, digest)
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
	capture, hasCapture := s.snapshotOwner.capture(id)
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
	for index := range output.Records {
		if err = s.mergeSnapshotRecord(&output.Records[index], capture, hasCapture); err != nil {
			return output, err
		}
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

// BeginModelFavoritesClose stops new preference tickets without waiting for
// accepted reads or saves to complete.
func (s *Service) BeginModelFavoritesClose() { s.favorites.BeginClose() }

func (s *Service) BeginAuthClose() { s.connections.credentials.BeginClose() }

// Close retries frozen failed snapshots once, then joins preference operations
// and browser login listeners. The caller joins runs before closing Service and
// its history store.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.BeginModelFavoritesClose()
		s.BeginAuthClose()
		snapshotErrors := s.snapshotOwner.retryFailed(context.Background(), s.store)
		s.closeErr = errors.Join(errors.Join(snapshotErrors...), s.favorites.Close(), s.connections.close())
	})
	return s.closeErr
}
