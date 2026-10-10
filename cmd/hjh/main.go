package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"hjh/internal/app"
	"hjh/internal/corerun"
	"hjh/internal/credentials"
	"hjh/internal/history"
	"hjh/internal/httpapi"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
func run(args []string, stdin io.ReadCloser, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: hjh serve|run|roles|providers|models|auth|sessions|history|inspect [options]")
		return 2
	}
	if args[0] == "tui" {
		fmt.Fprintln(stderr, "the TUI is a separate hjh-tui executable")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(stderr)
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	workspace := f.String("workspace", cwd, "workspace directory")
	dbPath := f.String("db", "", "history database (default: WORKSPACE/.hjh/history.sqlite3)")
	sessionID := f.String("session", "", "Session ID to resume or view")
	executionID := f.String("execution", "", "execution ID to inspect")
	modelID := f.String("model", "", "model ID on the selected connection")
	providerID := f.String("provider", "", "provider ID from external config")
	authProfile := f.String("auth-profile", "", "credential profile reference")
	role := f.String("role", "", "role for the next run")
	effort := f.String("effort", "", "reasoning effort (default: auto)")
	configPath := f.String("config", "", "extension config JSON (default: WORKSPACE/.hjh/extensions/config.json)")
	credentialFile := f.String("credential-file", "", "override API key file for the selected credential profile")
	maxSteps := f.Int("max-steps", 0, "model step limit; 0 is unlimited")
	stream := f.Bool("stream", false, "display assistant text on stdout as it arrives")
	fromTurn := f.Int64("from-turn", 0, "first complete conversation turn (inclusive)")
	toTurn := f.Int64("to-turn", -1, "last complete conversation turn (inclusive)")
	fromOrdinal := f.Int64("from-ordinal", 0, "first execution record/message ordinal (inclusive)")
	toOrdinal := f.Int64("to-ordinal", -1, "last execution record/message ordinal (inclusive)")
	listen := f.String("listen", "127.0.0.1:8765", "HTTP API listen address for serve")
	if err = f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	*workspace, err = filepath.Abs(*workspace)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	writeJSON := func(v any) error {
		e := json.NewEncoder(stdout)
		e.SetIndent("", "  ")
		e.SetEscapeHTML(false)
		return e.Encode(v)
	}
	// Role catalog inspection prepares the same editable config as a run, but
	// does not need a history database, provider, or API credential.
	if args[0] == "roles" {
		catalog, catalogErr := app.Roles(*workspace, *configPath)
		if catalogErr == nil {
			catalogErr = writeJSON(catalog)
		}
		if catalogErr != nil {
			fmt.Fprintln(stderr, catalogErr)
			return 1
		}
		return 0
	}
	options := app.RunOptions{Workspace: *workspace, ConfigPath: *configPath, CredentialFile: *credentialFile,
		MaxSteps: *maxSteps, FromTurn: *fromTurn}
	if args[0] == "serve" {
		return serve(ctx, *workspace, *dbPath, *configPath, *credentialFile, *maxSteps, *fromTurn, *listen, stderr)
	}
	if args[0] == "providers" || args[0] == "models" || args[0] == "auth" {
		service := app.NewService(nil, nil, options)
		defer service.Close()
		switch args[0] {
		case "providers":
			var value any
			value, err = service.Providers(ctx, *workspace)
			if err == nil {
				err = writeJSON(value)
			}
		case "models":
			var value any
			value, err = service.Models(ctx, *workspace, *providerID)
			if err == nil {
				err = writeJSON(value)
			}
		case "auth":
			operation := "status"
			if len(f.Args()) > 0 {
				operation = f.Args()[0]
			}
			switch operation {
			case "status":
				var value any
				value, err = service.LoginStatus(ctx, *workspace, *providerID)
				if err == nil {
					err = writeJSON(value)
				}
			case "login":
				value, loginErr := loginWithChatGPT(ctx, service, *workspace, *providerID, stdin, stderr)
				err = loginErr
				if err == nil {
					err = writeJSON(value)
				}
			default:
				err = fmt.Errorf("auth operation must be status or login")
			}
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			if ctx.Err() != nil {
				return 130
			}
			return 1
		}
		return 0
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(*workspace, ".hjh", "history.sqlite3")
	}
	*dbPath, err = filepath.Abs(*dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var store *history.Store
	if args[0] == "run" {
		if err = os.MkdirAll(filepath.Dir(*dbPath), 0755); err == nil {
			store, err = history.Open(*dbPath)
		}
	} else {
		store, err = history.OpenReadOnly(*dbPath)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer store.Close()
	service := app.NewService(store, nil, app.RunOptions{})
	defer func() { _ = service.Close() }()
	switch args[0] {
	case "run":
		service = app.NewService(store, nil, options)
		var task string
		if len(f.Args()) > 0 {
			task = strings.Join(f.Args(), " ")
		} else {
			b, e := readInput(ctx, stdin)
			if e != nil {
				err = e
				break
			}
			task = strings.TrimSpace(string(b))
		}
		if task == "" {
			err = fmt.Errorf("supply a task as arguments or stdin")
			break
		}
		output := runOutput{stdout: stdout, stderr: stderr, stream: *stream}
		var result app.RunResult
		result, err = service.Run(ctx, app.RunRequest{Task: task, SessionID: *sessionID, ModelID: *modelID,
			ProviderID: *providerID, Effort: *effort, AuthProfile: *authProfile, Workspace: *workspace, Role: *role, Progress: output.observe})
		if err == nil {
			err = output.finish(result.Result.FinalText)
		} else if *stream {
			err = errors.Join(err, output.endLine())
		}
	case "sessions":
		var sessions []history.Session
		sessions, err = service.Sessions(ctx)
		if err == nil {
			err = writeJSON(sessions)
		}
	case "history":
		if *sessionID == "" {
			err = fmt.Errorf("history requires --session")
			break
		}
		var messages []history.Message
		messages, err = service.Conversation(ctx, *sessionID, *fromTurn, *toTurn)
		if err == nil {
			err = writeJSON(messages)
		}
	case "inspect":
		if *executionID == "" {
			err = fmt.Errorf("inspect requires --execution")
			break
		}
		var inspection app.Inspection
		inspection, err = service.Inspect(ctx, *executionID, *fromOrdinal, *toOrdinal)
		if err == nil {
			err = writeJSON(inspection)
		}
	default:
		err = fmt.Errorf("unknown operation %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return 0
}

func serve(ctx context.Context, workspace, dbPath, configPath, credentialFile string, maxSteps int, fromTurn int64, listen string, stderr io.Writer) int {
	if dbPath == "" {
		dbPath = filepath.Join(workspace, ".hjh", "history.sqlite3")
	}
	var err error
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, err := history.Open(dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer store.Close()
	service := app.NewService(store, nil, app.RunOptions{Workspace: workspace, ConfigPath: configPath,
		CredentialFile: credentialFile, MaxSteps: maxSteps, FromTurn: fromTurn})
	manager := corerun.New(service, workspace)
	apiServer := httpapi.New(service, manager)
	defer apiServer.Close()

	listener, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	server := &http.Server{Handler: apiServer}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	fmt.Fprintf(stderr, "Core API listening at http://%s/api/v1\n", listener.Addr())

	select {
	case err = <-serveDone:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case <-ctx.Done():
		_ = server.Close()
		<-serveDone
		return 0
	}
}

// Closing the CLI input on cancellation releases the pending read before we
// return. This also handles Ctrl-C before a task has been submitted.
func readInput(ctx context.Context, input io.ReadCloser) ([]byte, error) {
	input, closeInput, err := pollableInput(input)
	if err != nil {
		return nil, err
	}
	defer closeInput()
	type result struct {
		bytes []byte
		err   error
	}
	done := make(chan result, 1)
	go func() { b, err := io.ReadAll(input); done <- result{b, err} }()
	select {
	case r := <-done:
		return r.bytes, r.err
	case <-ctx.Done():
		_ = input.Close()
		<-done
		return nil, ctx.Err()
	}
}
func extractSession(b []byte) string {
	var v struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(b, &v)
	return v.SessionID
}

func readLoginLine(ctx context.Context, input io.ReadCloser) (string, error) {
	input, closeInput, err := pollableInput(input)
	if err != nil {
		return "", err
	}
	defer closeInput()
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(input).ReadString('\n')
		done <- result{strings.TrimSpace(line), err}
	}()
	select {
	case r := <-done:
		if errors.Is(r.err, io.EOF) && r.text != "" {
			r.err = nil
		}
		return r.text, r.err
	case <-ctx.Done():
		_ = input.Close()
		<-done
		return "", ctx.Err()
	}
}

// pollableInput gives inherited file input the same cancellable read lifetime for
// task submission and the browser login callback.
func pollableInput(input io.ReadCloser) (io.ReadCloser, func(), error) {
	if file, ok := input.(*os.File); ok {
		fd, err := syscall.Dup(int(file.Fd()))
		if err != nil {
			return nil, nil, err
		}
		if err = syscall.SetNonblock(fd, true); err != nil {
			_ = syscall.Close(fd)
			return nil, nil, err
		}
		duplicate := os.NewFile(uintptr(fd), file.Name())
		return duplicate, func() { _ = duplicate.Close() }, nil
	}
	return input, func() {}, nil
}

func loginWithChatGPT(ctx context.Context, service *app.Service, workspace, providerID string, input io.ReadCloser, output io.Writer) (credentials.Status, error) {
	for {
		login, err := service.BeginLogin(ctx, workspace, providerID)
		if err != nil {
			return credentials.Status{}, err
		}
		fmt.Fprintf(output, "Continue with ChatGPT:\n%s\nPaste the browser callback URL, or press Enter after an automatic callback:\n", login.AuthorizationURL)
		callback, err := readLoginLine(ctx, input)
		if err != nil {
			return credentials.Status{}, err
		}
		status, err := service.CompleteLogin(ctx, workspace, providerID, callback)
		var invalidGrant *credentials.AuthorizationCodeInvalidGrantError
		if errors.As(err, &invalidGrant) {
			fmt.Fprintln(output, err)
			continue
		}
		return status, err
	}
}
