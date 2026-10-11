package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"hjh/internal/apiclient"
	"hjh/internal/corecatalog"
)

const coreUsage = `Usage: hjh core list [--workspace PATH] [--json]
       hjh core status [--core ID_OR_PREFIX | --connect URL] [--json]
       hjh core stop [--core ID_OR_PREFIX | --connect URL]

Queries do not start a Core. Without a status/stop target, show registrations.
Stop waits for cancellation and final saves before the Core exits.
`

func runCore(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, coreUsage)
		return 0
	}
	operation := args[0]
	if operation != "list" && operation != "status" && operation != "stop" {
		fmt.Fprintf(stderr, "unknown Core operation %q\n%s", operation, coreUsage)
		return 2
	}
	flags := flag.NewFlagSet("core "+operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), coreUsage); flags.PrintDefaults() }
	selector := flags.String("core", "", "Core ID or unique prefix")
	endpoint := flags.String("connect", "", "Core HTTP endpoint")
	workspace := flags.String("workspace", "", "filter local Core registrations by workspace")
	asJSON := flags.Bool("json", false, "output JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*selector != "" && *endpoint != "") || (operation == "list" && (*selector != "" || *endpoint != "")) {
		fmt.Fprint(stderr, coreUsage)
		return 2
	}
	if *workspace != "" {
		absolute, err := filepath.Abs(*workspace)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		*workspace = absolute
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}
	entries := []corecatalog.Entry{}
	var err error
	if *endpoint == "" {
		entries, err = corecatalog.Entries(*workspace)
		if err != nil {
			return fail(err)
		}
	}
	if operation == "list" || (*selector == "" && *endpoint == "") {
		statuses := make([]corecatalog.Status, 0, len(entries))
		for _, entry := range entries {
			status, err := corecatalog.Probe(ctx, entry)
			if err != nil {
				return fail(err)
			}
			if ctx.Err() != nil {
				return fail(ctx.Err())
			}
			statuses = append(statuses, status)
		}
		if err := writeCoreStatuses(stdout, statuses, *asJSON); err != nil {
			return fail(err)
		}
		if operation == "stop" {
			fmt.Fprintln(stderr, "Select a Core to stop: hjh core stop --core ID_OR_PREFIX or --connect URL")
		}
		return 0
	}
	entry := corecatalog.Entry{Endpoint: *endpoint}
	if *selector != "" {
		entry, err = corecatalog.Select(entries, *selector)
		if err != nil {
			return fail(err)
		}
	}
	client, err := apiclient.New(entry.Endpoint)
	if err != nil {
		return fail(err)
	}
	defer client.Close()
	core, err := client.Core(ctx)
	if err != nil {
		return fail(err)
	}
	if entry.CoreID != "" && entry.CoreID != core.CoreID {
		return fail(fmt.Errorf("Core %s has stopped; %s now serves another Core (%s)", entry.CoreID, entry.Endpoint, core.CoreID))
	}
	if operation == "stop" {
		if err := client.ShutdownCore(ctx, core.CoreID); err != nil {
			return fail(err)
		}
		if *asJSON {
			return writeCoreResult(stdout, map[string]any{"coreId": core.CoreID, "state": "stopped"}, fail)
		}
		_, err := fmt.Fprintf(stdout, "Core %s stopped.\n", core.CoreID)
		if err != nil {
			return fail(err)
		}
		return 0
	}
	status := corecatalog.Current(entry, core)
	runtime, err := client.CoreStatus(ctx)
	if err != nil {
		return fail(err)
	}
	if runtime.CoreID != core.CoreID {
		return fail(fmt.Errorf("Core changed while reading status; query its current identity again"))
	}
	status.ActiveRuns, status.Phase = runtime.ActiveRuns, runtime.Phase
	sessions, err := client.Sessions(ctx)
	if err != nil {
		return fail(err)
	}
	count := len(sessions)
	status.Sessions = &count
	if *asJSON {
		return writeCoreResult(stdout, status, fail)
	}
	if err := writeCoreStatuses(stdout, []corecatalog.Status{status}, false); err != nil {
		return fail(err)
	}
	_, err = fmt.Fprintf(stdout, "Phase: %s · Sessions: %d · active runs: %d\n", status.Phase, count, len(status.ActiveRuns))
	for _, active := range status.ActiveRuns {
		if err == nil {
			_, err = fmt.Fprintf(stdout, "Run %s · %s · Session %s\n", active.RunID, active.Phase, active.SessionID)
		}
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func writeCoreResult(out io.Writer, value any, fail func(error) int) int {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fail(err)
	}
	return 0
}

func writeCoreStatuses(out io.Writer, statuses []corecatalog.Status, asJSON bool) error {
	if asJSON {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(statuses)
	}
	if len(statuses) == 0 {
		_, err := fmt.Fprintln(out, "No registered Cores.")
		return err
	}
	for _, status := range statuses {
		if _, err := fmt.Fprintf(out, "%s · %s\n  URL: %s\n  Workspace: %s\n", status.CoreID, status.State, status.Endpoint, status.Workspace); err != nil {
			return err
		}
		if status.PID != 0 {
			if _, err := fmt.Fprintf(out, "  PID: %d · started: %s\n", status.PID, status.StartedAt); err != nil {
				return err
			}
		}
		if status.Error != "" {
			if _, err := fmt.Fprintln(out, "  "+status.Error); err != nil {
				return err
			}
		}
	}
	return nil
}
