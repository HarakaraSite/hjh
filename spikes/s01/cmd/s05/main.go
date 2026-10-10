// S-05 is a distributable spike CLI using the existing runtime and Session store.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"hjh/spikes/s01/internal/history"
	"hjh/spikes/s01/internal/host"
	"hjh/spikes/s01/internal/session"
)

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: hjh create|run|adopt|inspect [flags]"))
	}
	// Resolve the invocation path without requiring /proc in a minimal Linux root.
	exe, err := exec.LookPath(os.Args[0])
	if err != nil {
		fail(err)
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		fail(err)
	}
	root := filepath.Dir(exe)
	flags := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	state := flags.String("state", filepath.Join(root, "state"), "DB, report and snapshot directory")
	settings := flags.String("settings", filepath.Join(root, "settings.json"), "current configuration JSON")
	id := flags.String("session", "demo", "Session identity")
	turn := flags.String("turn", "", "turn identity for run or adopt")
	text := flags.String("text", "catalog", "request saved in the turn")
	url := flags.String("url", "", "YAML service URL")
	bodyFile := flags.String("js", "", "optional generated JS body file; default is the S-04 workflow")
	argsFile := flags.String("args", "", "optional input arguments JSON file")
	count := flags.Int("count", 3, "requested conversation pairs and turn page size")
	after := flags.Int("after", 0, "turn ordinal cursor for inspect")
	if err = flags.Parse(os.Args[2:]); err != nil {
		fail(err)
	}
	absState, err := filepath.Abs(*state)
	if err != nil {
		fail(err)
	}
	db := filepath.Join(absState, "history.sqlite")
	var store *history.Store
	if os.Args[1] == "inspect" {
		store, err = history.OpenReadOnly(db)
	} else {
		if err = os.MkdirAll(absState, 0755); err != nil {
			fail(err)
		}
		store, err = history.Open(db)
		if err == nil {
			err = store.InitSessions()
		}
	}
	if err != nil {
		fail(err)
	}
	var result any
	switch os.Args[1] {
	case "create":
		err = store.CreateSession(*id, "S-05 Linux distribution")
		if err == nil {
			result, err = store.SessionInfo(*id)
		}
	case "run":
		result, err = run(store, exe, *settings, absState, *id, *turn, *text, *url, *bodyFile, *argsFile, *count)
	case "adopt":
		err = store.Adopt(*id, *turn, nil)
		if err == nil {
			result, err = store.SessionInfo(*id)
		}
	case "inspect":
		result, err = inspect(store, *id, *after, *count)
	default:
		err = fmt.Errorf("unknown operation: %s", os.Args[1])
	}
	closeErr := store.Close()
	if result != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if e := enc.Encode(result); err == nil {
			err = e
		}
	}
	if err != nil {
		fail(err)
	}
	if closeErr != nil {
		fail(closeErr)
	}
}

func run(store *history.Store, exe, settings, state, id, turn, text, url, bodyFile, argsFile string, count int) (any, error) {
	prepared, err := session.Prepare(store, settings, exe, filepath.Join(state, "snapshots"))
	if err != nil {
		return nil, err
	}
	body := session.GeneratedJS
	if bodyFile != "" {
		b, e := os.ReadFile(bodyFile)
		if e != nil {
			return nil, e
		}
		body = string(b)
	}
	args := map[string]any{"url": url, "path": filepath.Join(state, turn+".report.json")}
	if argsFile != "" {
		b, e := os.ReadFile(argsFile)
		if e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &args); e != nil {
			return nil, e
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h := &host.Host{}
	r, err := session.Execute(ctx, store, prepared, session.Request{SessionID: id, TurnID: turn, Text: text, Body: body, Args: args, ContextPairs: count}, h)
	return struct {
		PID             int            `json:"pid"`
		Result          session.Result `json:"result"`
		ActiveHostCalls int64          `json:"activeHostCalls"`
	}{os.Getpid(), r, h.Active.Load()}, err
}

func inspect(store *history.Store, id string, after, count int) (any, error) {
	info, err := store.SessionInfo(id)
	if err != nil {
		return nil, err
	}
	window, err := store.Conversation(id, 0, count)
	if err != nil {
		return nil, err
	}
	turns, err := store.Turns(id, after, count)
	if err != nil {
		return nil, err
	}
	configurations := map[string]any{}
	events := map[string]any{}
	for _, turn := range turns {
		configurations[turn.Configuration], err = store.Configuration(turn.Configuration)
		if err != nil {
			return nil, err
		}
		events[turn.Run], err = store.Events(turn.Run)
		if err != nil {
			return nil, err
		}
	}
	sqlite, err := store.Settings()
	if err != nil {
		return nil, err
	}
	if err = store.Integrity(); err != nil {
		return nil, err
	}
	return map[string]any{"session": info, "conversation": window, "turns": turns, "configurations": configurations, "events": events, "sqlite": sqlite, "integrity": "ok"}, nil
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
