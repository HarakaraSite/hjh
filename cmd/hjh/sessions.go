package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"hjh/internal/corecatalog"
	"hjh/internal/history"
	"hjh/internal/historyview"
	"hjh/internal/sessionid"
)

const sessionsUsage = `Usage: hjh sessions [list] [--workspace PATH] [--db PATH] [--json]
       hjh sessions delete [--workspace PATH] [--db PATH] SESSION_ID

Delete accepts a full ID or unique short ID. Stop Core and standalone runs first.
The Session, its execution history, and its unreferenced contents/configurations
are deleted. Freed database space is reusable; the file is not compacted.
`

func runSessions(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	operation := "list"
	if len(args) > 0 && (args[0] == "list" || args[0] == "delete") {
		operation, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("sessions "+operation, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { fmt.Fprint(flags.Output(), sessionsUsage); flags.PrintDefaults() }
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	workspace := flags.String("workspace", cwd, "workspace directory")
	dbPath := flags.String("db", "", "history database (default: WORKSPACE/.hjh/history.sqlite3)")
	asJSON := flags.Bool("json", false, "write full Session IDs as JSON (list only)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if (operation == "list" && flags.NArg() != 0) || (operation == "delete" && (flags.NArg() != 1 || *asJSON)) {
		fmt.Fprint(stderr, sessionsUsage)
		return 2
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(*workspace, ".hjh", "history.sqlite3")
	}
	path, err := filepath.Abs(*dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fail := func(err error) int { fmt.Fprintln(stderr, err); return 1 }
	if operation == "delete" {
		if _, err := os.Stat(path); err != nil {
			return fail(err)
		}
		if err := requireStoppedSessionCore(ctx, path); err != nil {
			return fail(err)
		}
	}
	var store *history.Store
	if operation == "delete" {
		store, err = history.Open(path)
	} else {
		store, err = history.OpenReadOnly(path)
	}
	if err != nil {
		return fail(err)
	}
	defer store.Close()
	sessions, err := store.ListSessions(ctx)
	if err != nil {
		return fail(err)
	}
	if operation == "list" {
		if *asJSON {
			encoder := json.NewEncoder(stdout)
			encoder.SetIndent("", "  ")
			err = encoder.Encode(sessions)
		} else {
			_, err = io.WriteString(stdout, historyview.RenderSessions(sessions))
		}
		if err != nil {
			return fail(err)
		}
		return 0
	}
	id, err := sessionid.Resolve(flags.Arg(0), historySessionIDs(sessions))
	if err != nil {
		return fail(err)
	}
	if err := store.DeleteSession(ctx, id); err != nil {
		return fail(err)
	}
	if _, err := fmt.Fprintln(stdout, "Deleted Session:", sessionid.Short(id, historySessionIDs(sessions)...)); err != nil {
		return fail(err)
	}
	return 0
}

func requireStoppedSessionCore(ctx context.Context, dbPath string) error {
	entries, err := corecatalog.Entries("")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path, err := filepath.Abs(entry.DBPath)
		if err != nil {
			return err
		}
		if path != dbPath {
			continue
		}
		status, err := corecatalog.Probe(ctx, entry)
		if err != nil {
			return err
		}
		if status.State == "running" {
			return fmt.Errorf("stop Core first: hjh core stop --connect %s", entry.Endpoint)
		}
	}
	return nil
}
