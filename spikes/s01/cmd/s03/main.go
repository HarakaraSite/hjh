package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"hjh/spikes/s01/internal/libraryexperiment"
)

func main() {
	tools := flag.String("tools", "s03-tools", "prepared external tools")
	out := flag.String("out", "../../dist/s03-observations", "observations directory")
	packages := flag.String("package-out", "../../dist/s03-packages", "distribution copies")
	worker := flag.Bool("worker", false, "single distributed worker")
	db := flag.String("db", "", "worker history database")
	run := flag.String("run", "", "worker identity")
	input := flag.String("input", "", "worker input JSON file")
	flag.Parse()
	modules, err := filepath.Abs(*tools)
	if err != nil {
		fail(err)
	}
	if *worker {
		data, err := os.ReadFile(*input)
		if err != nil {
			fail(err)
		}
		var value any
		if err = json.Unmarshal(data, &value); err != nil {
			fail(err)
		}
		o, err := libraryexperiment.Execute(*db, *run, modules, filepath.Join(modules, "hooks.js"), `const w = await tools.workflow(input); return {report:w.report, savedBytes:w.savedBytes, names:w.report.compatible.map(dep => dep.name)};`, value)
		if err != nil {
			fail(err)
		}
		if err = json.NewEncoder(os.Stdout).Encode(o); err != nil {
			fail(err)
		}
		return
	}
	output, err := filepath.Abs(*out)
	if err != nil {
		fail(err)
	}
	packageDir, err := filepath.Abs(*packages)
	if err != nil {
		fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	summary, err := libraryexperiment.Run(libraryexperiment.Config{Tools: modules, Out: output, Packages: packageDir, Exe: exe})
	if err != nil {
		fail(err)
	}
	fmt.Printf("All %d S-03 scenarios passed. Evidence: %s\n", len(summary.Cases), output)
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
