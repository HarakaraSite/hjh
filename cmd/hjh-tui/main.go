package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"hjh/internal/apiclient"
	"hjh/internal/tui"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	endpoint, options, err := parseOptions(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	backend, err := apiclient.New(endpoint)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := tui.Run(ctx, backend, options, stdin, stdout); err != nil {
		fmt.Fprintln(stderr, err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	return 0
}

func parseOptions(args []string, stderr io.Writer) (string, tui.Options, error) {
	flags := flag.NewFlagSet("hjh-tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "", "Core HTTP endpoint (required)")
	workspace := flags.String("workspace", "", "Core-side workspace path (default: Core workspace)")
	sessionID := flags.String("session", "", "Session ID to resume")
	modelID := flags.String("model", "", "model ID for the next run")
	providerID := flags.String("provider", "", "provider ID for the next run")
	effort := flags.String("effort", "", "reasoning effort for the next run")
	authProfile := flags.String("auth-profile", "", "credential profile reference")
	role := flags.String("role", "", "role for the next run")
	maxSteps := flags.Int("max-steps", 0, "model step limit; 0 is unlimited")
	fromTurn := flags.Int64("from-turn", 0, "first complete conversation turn (inclusive)")
	if err := flags.Parse(args); err != nil {
		return "", tui.Options{}, err
	}
	if flags.NArg() != 0 {
		return "", tui.Options{}, fmt.Errorf("hjh-tui does not accept positional arguments")
	}
	if strings.TrimSpace(*endpoint) == "" {
		return "", tui.Options{}, fmt.Errorf("usage: hjh-tui --endpoint http://127.0.0.1:PORT")
	}
	options := tui.Options{
		Workspace: *workspace, SessionID: *sessionID, ModelID: *modelID,
		ProviderID: *providerID, Effort: *effort, AuthProfile: *authProfile, Role: *role,
	}
	flags.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "max-steps":
			v := *maxSteps
			options.MaxSteps = &v
		case "from-turn":
			v := *fromTurn
			options.FromTurn = &v
		}
	})
	return *endpoint, options, nil
}
