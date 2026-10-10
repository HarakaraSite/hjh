package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"hjh/spikes/s01/internal/experiment"
)

func main() {
	modules := flag.String("tools", "tools", "external JS module directory")
	out := flag.String("out", "../../docs/spikes/s01-artifacts", "observation output directory")
	flag.Parse()
	moduleDir, err := filepath.Abs(*modules)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summary, err := experiment.Run(moduleDir, *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("All %d scenarios passed. Evidence: %s\n", len(summary.Cases), *out)
}
