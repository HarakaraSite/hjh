// Package tui provides the interactive terminal surface for hjh.
package tui

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"hjh/internal/api"
	"hjh/internal/apiclient"
	"hjh/internal/tui/renderer"
)

// RunHandle exposes explicit run cancellation separately from the lifetime of
// the client's status/SSE communication.
type RunHandle = apiclient.RunHandle

// Backend is the public API facade used by the terminal UI. It carries only
// JSON contract values and never imports Core implementation packages.
type Backend interface {
	Core(context.Context) (api.Core, error)
	Orientation(context.Context, api.OrientationRequest) (api.Orientation, error)
	StartRun(api.StartRunRequest, func(apiclient.RunEvent)) RunHandle
	AttachRun(string, string, func(apiclient.RunEvent)) RunHandle
	ActiveSessionRun(context.Context, string, string) (*api.RunState, error)
	SubmitInput(context.Context, string, api.SubmitInputRequest) (api.InputReceipt, error)
	Input(context.Context, string, string, string, bool) (api.InputReceipt, error)
	Inputs(context.Context, string, string, uint64, bool) (api.InputReceiptPage, error)
	CancelInputChain(context.Context, string, string) (api.InputChainState, error)
	InputChain(context.Context, string, string) (api.InputChainState, error)
	SessionContext(context.Context, string, string) (api.SessionContext, error)
	Recall(context.Context, string, api.RecallOperationRequest) (api.RecallOperationResult, error)
	RecallOperation(context.Context, string, string, string) (api.RecallOperationResult, error)
	RenameSession(context.Context, string, string) (api.Session, error)
	ShutdownCore(context.Context, string) error
	Roles(context.Context, apiclient.Scope) (api.RoleCatalog, error)
	Sessions(context.Context) ([]api.Session, error)
	ResolveSession(context.Context, string) (api.Session, error)
	Session(context.Context, string) (api.Session, error)
	SessionView(context.Context, string) (api.ConversationSnapshot, error)
	Conversation(context.Context, string, int64, int64) ([]api.Message, error)
	Executions(context.Context, string) ([]api.Execution, error)
	Inspect(context.Context, string, int64, int64) (api.Inspection, error)
	Record(context.Context, string, int64) (api.Record, error)
	Selection(context.Context, api.SelectionRequest) (api.SelectionPreview, error)
	RememberSelection(context.Context, api.RememberSelectionRequest) (api.SelectionPreview, error)
	Providers(context.Context, apiclient.Scope) ([]api.ProviderDefinition, error)
	Models(context.Context, apiclient.Scope, string, string) ([]api.ModelInfo, error)
	ModelsWithAuth(context.Context, apiclient.Scope, string, string) (api.ModelsResult, error)
	ModelFavorites(context.Context, apiclient.Scope, string) (api.ModelFavorites, error)
	SetModelFavorite(context.Context, api.SetModelFavoriteRequest) (api.ModelFavoriteResult, error)
	AuthProfiles(context.Context, api.SelectionRequest) (api.AuthProfiles, error)
	AuthAccounts(context.Context, apiclient.Scope, string, string) (api.AuthAccounts, error)
	RegisterAPIKey(context.Context, api.AuthRequest) (api.APIKeyState, error)
	SelectAuthAccount(context.Context, api.AuthRequest) (api.AuthAccounts, error)
	BeginAuthAttempt(context.Context, api.AuthRequest) (api.AuthAttempt, error)
	AuthAttempt(context.Context, string, string) (api.AuthAttempt, error)
	CompleteAuthAttempt(context.Context, api.AuthRequest) (api.AuthAttempt, error)
	CancelAuthAttempt(context.Context, api.AuthRequest) (api.AuthAttempt, error)
	DeleteAuthAttempt(context.Context, string, string) error
}

type terminalOutputSink struct{ fd uintptr }

func (terminalOutputSink) Write(data []byte) (int, error) { return len(data), nil }
func (terminalOutputSink) Read([]byte) (int, error)       { return 0, io.EOF }
func (terminalOutputSink) Close() error                   { return nil }
func (sink terminalOutputSink) Fd() uintptr               { return sink.fd }

// Options selects the starting workspace, Session, model, and explicit role.
// Model choices are remembered by Core for later Sessions in the workspace.
// Role choices apply to later runs in this TUI process.
type Options struct {
	Core                            api.Core
	Workspace                       string
	SessionID                       string
	ModelID                         string
	Role                            string
	ProviderID, Effort, AuthProfile string
	MaxSteps                        *int
	FromTurn                        *int64
}

// Run starts the terminal UI and joins every backend operation before it
// returns, including when the parent context ends while a run is active.
func Run(ctx context.Context, backend Backend, options Options, input io.Reader, output io.Writer) (retErr error) {
	core, err := backend.Core(ctx)
	if err != nil {
		if closer, ok := backend.(io.Closer); ok {
			return errors.Join(err, closer.Close())
		}
		return err
	}
	if options.Workspace == "" {
		options.Workspace = core.Workspace
	}
	options.Core = core
	sessions, err := backend.Sessions(ctx)
	if err != nil {
		if closer, ok := backend.(io.Closer); ok {
			err = errors.Join(err, closer.Close())
		}
		return err
	}
	if options.SessionID != "" {
		session, resolveErr := backend.ResolveSession(ctx, options.SessionID)
		if resolveErr != nil {
			if closer, ok := backend.(io.Closer); ok {
				resolveErr = errors.Join(resolveErr, closer.Close())
			}
			return resolveErr
		}
		options.SessionID = session.ID
	}
	uiCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := newModel(uiCtx, cancel, backend, options)
	// Bubble Tea sends its first size asynchronously. Read the actual output
	// terminal now so an early restored view cannot spill an 80-column frame
	// into a narrower terminal's scrollback before that message arrives.
	if file, ok := output.(interface{ Fd() uintptr }); ok {
		if width, height, err := term.GetSize(file.Fd()); err == nil {
			m.width, m.height = width, height
			m.layout()
		}
	}
	m.sessions = sessions
	for _, session := range sessions {
		if session.ID == m.sessionID {
			m.setSessionMetadata(session)
			break
		}
	}
	m.writer = renderer.New(output)
	if err := m.writer.Start(); err != nil {
		m.shutdown()
		return errors.Join(err, m.writer.Close())
	}
	defer func() { retErr = errors.Join(retErr, m.writer.Close()) }()
	var teaOutput io.Writer = io.Discard
	if file, ok := output.(interface{ Fd() uintptr }); ok {
		teaOutput = terminalOutputSink{fd: file.Fd()}
	}
	p := tea.NewProgram(m,
		tea.WithContext(uiCtx),
		tea.WithInput(input),
		tea.WithOutput(teaOutput),
	)
	m.send = p.Send
	_, err = p.Run()
	err = errors.Join(err, m.renderErr)
	err = errors.Join(err, m.flushVisibleBody())
	m.shutdown()
	if closer, ok := backend.(io.Closer); ok {
		err = errors.Join(err, closer.Close())
	}
	return err
}

// flushVisibleBody leaves the last conversation rows in normal-screen
// scrollback before Writer.Close erases the mutable header and editor band.
func (m *model) flushVisibleBody() error {
	if m.writer == nil {
		return nil
	}
	if !m.headerPrinted {
		m.headerOrientationPending = false
		m.headerOrientationFailed = true
		m.pendingSealRows = append(m.pendingSealRows, m.headerRows()...)
		m.headerPrinted = true
	}
	m.deferTranscriptUntilHeader = false
	m.sealTranscriptDocument()
	if m.exitNotice != "" {
		m.pendingSealRows = append(m.pendingSealRows, renderer.WrapText(m.exitNotice, max(1, m.width))...)
		m.exitNotice = ""
	}
	if len(m.pendingSealRows) == 0 {
		return nil
	}
	if err := m.writer.Render(renderer.Frame{
		SealedRows: append([]string(nil), m.pendingSealRows...),
		LiveRows:   []string{""},
		Cursor:     renderer.Cursor{},
		Size:       renderer.Size{Columns: max(1, m.width), Rows: max(1, m.height)},
	}); err != nil {
		return err
	}
	m.pendingSealRows = nil
	return nil
}
