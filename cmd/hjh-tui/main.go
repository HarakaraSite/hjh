package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"hjh/internal/apiclient"
	"hjh/internal/buildinfo"
	"hjh/internal/corelaunch"
	"hjh/internal/tui"
)

type startupOptions struct {
	corelaunch.Options
	StartCore bool
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--version" {
		line, err := buildinfo.VersionLine()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, line)
		return 0
	}
	startup, options, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	backend, err := apiclient.New(startup.Endpoint)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	defer backend.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if startup.StartCore {
		if _, err := corelaunch.Ensure(ctx, startup.Options, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			if ctx.Err() != nil {
				return 130
			}
			return 1
		}
	}
	if err := tui.Run(ctx, backend, options, stdin, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (startupOptions, tui.Options, error) {
	flags := flag.NewFlagSet("hjh-tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "", "Core HTTP endpoint (default: http://127.0.0.1:8765 with automatic local startup)")
	startCore := flags.Bool("start-core", false, "start a local Core if needed (default: when --endpoint is omitted)")
	workspace := flags.String("workspace", "", "Core-side workspace (default: Core workspace; current directory for a new Core)")
	coreBinary := flags.String("core-binary", "", "Core executable for startup (default: hjh beside hjh-tui)")
	dbPath := flags.String("db", "", "Core startup: history database (default: WORKSPACE/.hjh/history.sqlite3)")
	configPath := flags.String("config", "", "Core startup: extension config JSON")
	credentialFile := flags.String("credential-file", "", "Core startup: override API key file")
	credentialStateDir := flags.String("credential-state-dir", "", "Core startup: credential storage root (default: XDG_CONFIG_HOME/hjh or ~/.config/hjh; records under credentials/)")
	modelFavoritesDir := flags.String("model-favorites-dir", "", "Core startup: model favorite state directory")
	sessionID := flags.String("session", "", "Session short ID (unique prefix) to resume")
	continueSession := flags.Bool("continue", false, "resume the latest Session in this workspace")
	modelID := flags.String("model", "", "model ID for the next run")
	providerID := flags.String("provider", "", "provider ID for the next run")
	effort := flags.String("effort", "", "reasoning effort for the next run")
	authProfile := flags.String("auth-profile", "", "credential profile reference")
	role := flags.String("role", "", "role for the next run")
	maxSteps := flags.Int("max-steps", 0, "model step limit; 0 is unlimited")
	fromTurn := flags.Int64("from-turn", 0, "first complete conversation turn (inclusive)")
	if err := flags.Parse(args); err != nil {
		return startupOptions{}, tui.Options{}, err
	}
	if flags.NArg() != 0 {
		return startupOptions{}, tui.Options{}, fmt.Errorf("hjh-tui does not accept positional arguments")
	}
	if *continueSession && *sessionID != "" {
		return startupOptions{}, tui.Options{}, fmt.Errorf("--session and --continue are mutually exclusive")
	}
	endpointSet, startCoreSet := false, false
	options := tui.Options{
		Workspace: *workspace, SessionID: *sessionID, Continue: *continueSession, ModelID: *modelID,
		ProviderID: *providerID, Effort: *effort, AuthProfile: *authProfile, Role: *role,
	}
	flags.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "endpoint":
			endpointSet = true
		case "start-core":
			startCoreSet = true
		case "max-steps":
			v := *maxSteps
			options.MaxSteps = &v
		case "from-turn":
			v := *fromTurn
			options.FromTurn = &v
		}
	})
	autoStart := !endpointSet
	if startCoreSet {
		autoStart = *startCore
	}
	if strings.TrimSpace(*endpoint) == "" {
		*endpoint = "http://127.0.0.1:8765"
	}
	startupWorkspace := options.Workspace
	if autoStart {
		localWorkspace := *workspace
		if localWorkspace == "" {
			var err error
			localWorkspace, err = os.Getwd()
			if err != nil {
				return startupOptions{}, tui.Options{}, err
			}
		}
		var err error
		localWorkspace, err = filepath.Abs(localWorkspace)
		if err != nil {
			return startupOptions{}, tui.Options{}, err
		}
		startupWorkspace = localWorkspace
		if *workspace != "" {
			options.Workspace = localWorkspace
		}
	}
	startup := startupOptions{StartCore: autoStart, Options: corelaunch.Options{
		Endpoint: *endpoint, Binary: *coreBinary, Workspace: startupWorkspace,
		DBPath: *dbPath, ConfigPath: *configPath, CredentialFile: *credentialFile,
		CredentialStateDir: *credentialStateDir, ModelFavoritesDir: *modelFavoritesDir,
	}}
	return startup, options, nil
}
