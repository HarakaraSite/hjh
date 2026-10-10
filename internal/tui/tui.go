// Package tui provides the interactive terminal surface for hjh.
package tui

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"
	"hjh/internal/api"
	"hjh/internal/apiclient"
)

// RunHandle exposes explicit run cancellation separately from the lifetime of
// the client's status/SSE communication.
type RunHandle = apiclient.RunHandle

// Backend is the public API facade used by the terminal UI. It carries only
// JSON contract values and never imports Core implementation packages.
type Backend interface {
	Core(context.Context) (api.Core, error)
	StartRun(api.StartRunRequest, func(apiclient.RunEvent)) RunHandle
	Roles(context.Context, apiclient.Scope) (api.RoleCatalog, error)
	Sessions(context.Context) ([]api.Session, error)
	Session(context.Context, string) (api.Session, error)
	Conversation(context.Context, string, int64, int64) ([]api.Message, error)
	Executions(context.Context, string) ([]api.Execution, error)
	Inspect(context.Context, string, int64, int64) (api.Inspection, error)
	Providers(context.Context, apiclient.Scope) ([]api.ProviderDefinition, error)
	Models(context.Context, apiclient.Scope, string) ([]api.ModelInfo, error)
	BeginLogin(context.Context, apiclient.Scope, string) (api.Login, error)
	CompleteLogin(context.Context, api.Login, string) (api.Status, error)
	LoginStatus(context.Context, apiclient.Scope, string) (api.Status, error)
}

// Options selects the starting workspace, Session, model, and explicit role.
// A model or role chosen with /model or /role applies to later runs in this TUI process.
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
func Run(ctx context.Context, backend Backend, options Options, input io.Reader, output io.Writer) error {
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
	uiCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := newModel(uiCtx, cancel, backend, options)
	p := tea.NewProgram(m,
		tea.WithContext(uiCtx),
		tea.WithInput(input),
		tea.WithOutput(output),
	)
	m.send = p.Send
	_, err = p.Run()
	m.shutdown()
	if closer, ok := backend.(io.Closer); ok {
		err = errors.Join(err, closer.Close())
	}
	return err
}
