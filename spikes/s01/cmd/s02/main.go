package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"hjh/spikes/s01/internal/historyexperiment"
)

func main() {
	tools := flag.String("tools", "tools", "external JS module directory")
	out := flag.String("out", "../../dist/s02-observations", "observation output directory")
	inspect := flag.String("inspect", "", "read-only SQLite inspection")
	child := flag.String("child", "", "experiment writer mode: committed or uncommitted")
	db := flag.String("db", "", "writer SQLite path")
	run := flag.String("run", "", "run identity")
	effect := flag.String("effect", "", "writer side effect path")
	ready := flag.String("ready", "", "writer termination gate path")
	flag.Parse()
	var err error
	if *inspect != "" {
		view, e := historyexperiment.Inspect(*inspect, *run)
		err = e
		if err == nil {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			err = encoder.Encode(view)
		}
	} else {
		modules, e := filepath.Abs(*tools)
		err = e
		if err == nil {
			if *child != "" {
				err = historyexperiment.Child(*db, *run, modules, *child, *effect, *ready)
			} else {
				output, e := filepath.Abs(*out)
				err = e
				if err == nil {
					exe, e := os.Executable()
					err = e
					if err == nil {
						summary, e := historyexperiment.Run(modules, output, exe)
						err = e
						if err == nil {
							fmt.Printf("All %d S-02 scenarios passed. Evidence: %s\n", len(summary.Cases), output)
						}
					}
				}
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
