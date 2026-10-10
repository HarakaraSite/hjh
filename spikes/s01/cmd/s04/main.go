package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"hjh/spikes/s01/internal/sessionexperiment"
)

func main() {
	tools := flag.String("tools", "s03-tools", "source extension directory")
	out := flag.String("out", "../../dist/s04-observations", "observation directory")
	work := flag.String("work", "../../dist/s04-work", "editable and captured configuration workspaces")
	n := flag.Int("history-turns", 120, "additional real turns for history observation")
	inspect := flag.Bool("inspect", false, "read-only Session inspection")
	worker := flag.Bool("worker", false, "single resumed Worker")
	db := flag.String("db", "", "Session SQLite database")
	id := flag.String("session", "", "Session identity")
	turn := flag.String("turn", "", "new turn identity")
	settings := flag.String("settings", "", "current settings JSON")
	url := flag.String("url", "", "YAML service URL")
	path := flag.String("path", "", "report output path")
	gate := flag.String("gate", "", "experiment adoption transaction gate")
	before := flag.Int("before", 0, "conversation position cursor; zero for latest")
	after := flag.Int("after", 0, "turn ordinal cursor")
	count := flag.Int("count", 3, "requested pairs and turn page size")
	flag.Parse()
	var result any
	var err error
	switch {
	case *inspect:
		result, err = sessionexperiment.Inspect(*db, *id, *before, *after, *count)
	case *worker:
		exe, e := os.Executable()
		if e != nil {
			fail(e)
		}
		workdir, e := filepath.Abs(*work)
		if e != nil {
			fail(e)
		}
		result, err = sessionexperiment.Worker(sessionexperiment.WorkerConfig{DB: *db, Session: *id, Turn: *turn, Settings: *settings, Exe: exe, Work: workdir, URL: *url, Path: *path, Gate: *gate, ContextPairs: *count})
	default:
		modules, e := filepath.Abs(*tools)
		if e != nil {
			fail(e)
		}
		output, e := filepath.Abs(*out)
		if e != nil {
			fail(e)
		}
		workdir, e := filepath.Abs(*work)
		if e != nil {
			fail(e)
		}
		exe, e := os.Executable()
		if e != nil {
			fail(e)
		}
		summary, e := sessionexperiment.Run(sessionexperiment.Config{Tools: modules, Out: output, Work: workdir, Exe: exe, HistoryTurns: *n})
		if e != nil {
			fail(e)
		}
		fmt.Printf("All %d S-04 scenarios passed. Evidence: %s\n", len(summary.Cases), output)
		return
	}
	if err != nil {
		fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(result); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
