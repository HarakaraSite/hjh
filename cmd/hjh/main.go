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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"hjh/internal/api"

	"hjh/internal/app"
	"hjh/internal/buildinfo"
	"hjh/internal/corecatalog"
	"hjh/internal/corerun"
	"hjh/internal/credentials"
	"hjh/internal/history"
	"hjh/internal/httpapi"
	"hjh/internal/sessionid"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
func run(args []string, stdin io.ReadCloser, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		line, err := buildinfo.VersionLine()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		_, err = fmt.Fprintln(stdout, line)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprint(stdout, cliUsage)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, cliUsage)
		return 2
	}
	if args[0] == "tui" {
		fmt.Fprintln(stderr, "the TUI is a separate hjh-tui executable")
		return 2
	}
	ctx, cancel := cliSignalContext(context.Background())
	defer cancel()
	if args[0] == "core" {
		return runCore(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "history" {
		return runHistory(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "sessions" {
		return runSessions(ctx, args[1:], stdout, stderr)
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(stderr)
	connect := ""
	if args[0] == "run" {
		f.StringVar(&connect, "connect", "", "send the task to an existing Core HTTP endpoint")
		f.Usage = func() {
			fmt.Fprintln(f.Output(), "Usage: hjh run [options] TASK (or supply TASK on stdin)")
			f.PrintDefaults()
			fmt.Fprintln(f.Output(), "--session accepts a full Session ID or a unique case-insensitive prefix (8 characters by default).")
			fmt.Fprintln(f.Output(), "\nWith --connect, omitted workspace/max-steps/from-turn use Core settings.")
			fmt.Fprintln(f.Output(), "Relative workspace paths are Core-side; resuming uses the saved Session workspace.")
			fmt.Fprintln(f.Output(), "DB/config/credential/favorites paths belong to Core startup (hjh serve).")
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	workspace := f.String("workspace", cwd, "workspace directory")
	dbPath := f.String("db", "", "history database (default: WORKSPACE/.hjh/history.sqlite3)")
	sessionID := f.String("session", "", "Session ID or unique case-insensitive prefix to resume (8 characters by default)")
	executionID := f.String("execution", "", "execution ID to inspect")
	modelID := f.String("model", "", "model ID on the selected connection")
	providerID := f.String("provider", "", "provider ID from external config")
	authProfile := f.String("auth-profile", "", "credential profile reference")
	role := f.String("role", "", "role for the next run")
	effort := f.String("effort", "", "reasoning effort (default: auto)")
	configPath := f.String("config", "", "extension config JSON (default: WORKSPACE/.hjh/extensions/config.json)")
	credentialFile := f.String("credential-file", "", "override API key file for the selected credential profile")
	credentialStateDir := f.String("credential-state-dir", "", "credential storage root (default: XDG_CONFIG_HOME/hjh or ~/.config/hjh; records under credentials/)")
	modelFavoritesDir := f.String("model-favorites-dir", "", "favorite state directory (default: XDG_STATE_HOME/hjh/model-favorites or ~/.local/state/hjh/model-favorites)")
	maxSteps := f.Int("max-steps", 0, "model step limit; 0 is unlimited")
	stream := f.Bool("stream", false, "display assistant text on stdout as it arrives")
	fromTurn := f.Int64("from-turn", 0, "first complete conversation turn (inclusive)")
	fromOrdinal := f.Int64("from-ordinal", 0, "first execution record/message ordinal (inclusive)")
	toOrdinal := f.Int64("to-ordinal", -1, "last execution record/message ordinal (inclusive)")
	listen := f.String("listen", "127.0.0.1:8765", "HTTP API listen address for serve")
	if err = f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if args[0] == "run" && connect != "" {
		explicit := make(map[string]bool)
		f.Visit(func(flag *flag.Flag) { explicit[flag.Name] = true })
		for _, name := range []string{"db", "config", "credential-file", "credential-state-dir", "model-favorites-dir", "listen"} {
			if explicit[name] {
				fmt.Fprintf(stderr, "--%s belongs to Core startup; configure hjh serve before using run --connect\n", name)
				return 2
			}
		}
		task, taskErr := runTask(ctx, f.Args(), stdin)
		if taskErr != nil {
			fmt.Fprintln(stderr, taskErr)
			if ctx.Err() != nil {
				return 130
			}
			return 1
		}
		request := api.StartRunRequest{Task: task, SessionID: *sessionID, ModelID: *modelID,
			ProviderID: *providerID, Effort: *effort, AuthProfile: *authProfile, Role: *role}
		if explicit["workspace"] {
			request.Workspace = *workspace
		}
		if explicit["max-steps"] {
			request.MaxSteps = maxSteps
		}
		if explicit["from-turn"] {
			request.FromTurn = fromTurn
		}
		return runConnected(ctx, connect, request, *stream, stdout, stderr)
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
		ModelFavoritesDir: *modelFavoritesDir, CredentialStateDir: *credentialStateDir,
		MaxSteps: *maxSteps, FromTurn: *fromTurn}
	if args[0] == "serve" {
		return serve(ctx, *workspace, *dbPath, *configPath, *credentialFile, *modelFavoritesDir, *credentialStateDir, *maxSteps, *fromTurn, *listen, stderr)
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
			value, err = service.Models(ctx, *workspace, *providerID, *authProfile)
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
				value, err = service.AuthStatus(ctx, *workspace, *providerID, *authProfile)
				if err == nil {
					err = writeJSON(value)
				}
			case "login":
				value, loginErr := loginWithChatGPT(ctx, service, *workspace, *providerID, *authProfile, stdin, stderr)
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
	service := app.NewService(store, nil, options)
	switch args[0] {
	case "run":
		var task string
		task, err = runTask(ctx, f.Args(), stdin)
		if err != nil {
			break
		}
		var sessions []history.Session
		sessions, err = service.Sessions(ctx)
		if err != nil {
			break
		}
		knownSessionIDs := historySessionIDs(sessions)
		if *sessionID != "" {
			*sessionID, err = sessionid.Resolve(*sessionID, knownSessionIDs)
			if err != nil {
				break
			}
		}
		output := runOutput{stdout: stdout, stderr: stderr, stream: *stream, knownSessionIDs: knownSessionIDs}
		var result app.RunResult
		result, err = service.Run(ctx, app.RunRequest{Task: task, SessionID: *sessionID, ModelID: *modelID,
			ProviderID: *providerID, Effort: *effort, AuthProfile: *authProfile, Workspace: *workspace, Role: *role, Progress: output.observe})
		if noticeErr := writeSnapshotNotice(stderr, result.SnapshotSave, result.FailedChildSnapshotExecutionIDs); noticeErr != nil {
			err = errors.Join(err, noticeErr)
		}
		if err == nil {
			err = output.finish(result.Result.FinalText)
		} else if *stream {
			err = errors.Join(err, output.endLine())
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
	if closeErr := service.Close(); closeErr != nil {
		fmt.Fprintf(stderr, "close service: %v\n", closeErr)
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

func serve(ctx context.Context, workspace, dbPath, configPath, credentialFile, modelFavoritesDir, credentialStateDir string, maxSteps int, fromTurn int64, listen string, stderr io.Writer) int {
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer listener.Close()
	if dbPath == "" {
		dbPath = filepath.Join(workspace, ".hjh", "history.sqlite3")
	}
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
		CredentialFile: credentialFile, ModelFavoritesDir: modelFavoritesDir, CredentialStateDir: credentialStateDir, MaxSteps: maxSteps, FromTurn: fromTurn})
	manager := corerun.New(service, workspace)
	apiServer := httpapi.New(service, manager)
	apiServerClosed := false
	closeAPIServer := func() error {
		if apiServerClosed {
			return nil
		}
		apiServerClosed = true
		return apiServer.Close()
	}
	defer func() {
		if closeErr := closeAPIServer(); closeErr != nil {
			fmt.Fprintf(stderr, "close Core: %v\n", closeErr)
		}
	}()
	endpoint := coreEndpoint(listener.Addr())
	unregister, err := corecatalog.Register(corecatalog.Entry{CoreID: manager.Core().CoreID,
		Endpoint: endpoint, Workspace: workspace, PID: os.Getpid(),
		StartedAt: time.Now().UTC().Format(time.RFC3339Nano), DBPath: dbPath})
	if err != nil {
		fmt.Fprintf(stderr, "register Core: %v\n", err)
		return 1
	}
	defer func() {
		if err := unregister(); err != nil {
			fmt.Fprintf(stderr, "remove Core registration: %v\n", err)
		}
	}()

	server := &http.Server{Handler: apiServer}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	fmt.Fprintf(stderr, "Core API listening at http://%s/api/v2\n", listener.Addr())

	select {
	case err = <-serveDone:
		closeErr := closeAPIServer()
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintln(stderr, err)
			if closeErr != nil {
				fmt.Fprintf(stderr, "close Core: %v\n", closeErr)
			}
			return 1
		}
		if closeErr != nil {
			fmt.Fprintf(stderr, "close Core: %v\n", closeErr)
			return 1
		}
		return 0
	case <-apiServer.ShutdownRequested():
		closeErr := closeAPIServer()
		_ = server.Close()
		<-serveDone
		if closeErr != nil {
			fmt.Fprintln(stderr, closeErr)
			return 1
		}
		return 0
	case <-ctx.Done():
		_ = manager.BeginShutdown(manager.Core().CoreID)
		closeErr := closeAPIServer()
		_ = server.Close()
		<-serveDone
		if closeErr != nil {
			fmt.Fprintln(stderr, closeErr)
			return 1
		}
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

func loginWithChatGPT(ctx context.Context, service *app.Service, workspace, providerID, profile string, input io.ReadCloser, output io.Writer) (credentials.Status, error) {
	profiles, err := service.AuthProfiles(ctx, api.SelectionRequest{Workspace: workspace, ProviderID: providerID, AuthProfile: profile})
	if err != nil {
		return credentials.Status{}, err
	}
	target := profiles.Target
	accounts, err := service.AuthAccounts(ctx, workspace, target.ProviderID, target.AuthProfile)
	if err != nil {
		return credentials.Status{}, err
	}
	request := api.AuthRequest{CoreID: "cli:" + uuid.NewString(), Workspace: workspace, ProviderID: target.ProviderID,
		AuthProfile: target.AuthProfile, AccountID: accounts.SelectedAccountID, AttemptID: uuid.NewString()}
	attempt, err := service.BeginAuthAttempt(ctx, request)
	if err != nil {
		return credentials.Status{}, err
	}
	request.AttemptID = attempt.AttemptID
	defer func() { _, _ = service.CancelAuthAttempt(context.Background(), request) }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	printed := false
	var callbackDone chan struct {
		value string
		err   error
	}
	var readCancel context.CancelFunc
	defer func() {
		if readCancel != nil {
			readCancel()
		}
	}()
	for {
		if attempt.Terminal() {
			if attempt.Phase == "completed" {
				_ = service.DeleteAuthAttempt(context.Background(), request.CoreID, request.AttemptID)
				return service.AuthStatus(ctx, workspace, target.ProviderID, target.AuthProfile)
			}
			if attempt.Error != nil {
				if attempt.Error.Code == "authorization_code_invalid_grant" {
					fmt.Fprintln(output, attempt.Error)
					if readCancel != nil {
						readCancel()
					}
					_ = service.DeleteAuthAttempt(ctx, request.CoreID, request.AttemptID)
					request.AttemptID = uuid.NewString()
					request.CallbackURL = ""
					attempt, err = service.BeginAuthAttempt(ctx, request)
					if err != nil {
						return credentials.Status{}, err
					}
					request.AttemptID = attempt.AttemptID
					printed = false
					callbackDone = nil
					continue
				}
				return credentials.Status{}, attempt.Error
			}
			return credentials.Status{}, fmt.Errorf("ChatGPT authorization %s", attempt.Phase)
		}
		if printed && callbackDone == nil && attempt.Phase == "ready" && attempt.Error != nil {
			fmt.Fprintln(output, attempt.Error)
			printed = false
		}
		if !printed && (attempt.Phase == "ready" || attempt.Phase == "waiting_callback") {
			printed = true
			fmt.Fprintf(output, "Continue with ChatGPT:\n%s\nPaste the browser callback URL, or press Enter after an automatic callback:\n", attempt.AuthorizationURL)
			callbackDone = make(chan struct {
				value string
				err   error
			}, 1)
			readCtx, cancelRead := context.WithCancel(ctx)
			readCancel = cancelRead
			readResults := callbackDone
			go func() {
				defer cancelRead()
				value, err := readLoginLine(readCtx, input)
				readResults <- struct {
					value string
					err   error
				}{value, err}
			}()
		}
		select {
		case <-ctx.Done():
			return credentials.Status{}, ctx.Err()
		case callback := <-callbackDone:
			callbackDone = nil
			if readCancel != nil {
				readCancel()
				readCancel = nil
			}
			if callback.err != nil {
				return credentials.Status{}, callback.err
			}
			request.CallbackURL = callback.value
			attempt, err = service.CompleteAuthAttempt(ctx, request)
			if err != nil {
				fmt.Fprintln(output, err)
				printed = false
				continue
			}
		case <-ticker.C:
			attempt, err = service.AuthAttempt(ctx, request.CoreID, request.AttemptID)
			if err != nil {
				return credentials.Status{}, err
			}
		}
	}
}
