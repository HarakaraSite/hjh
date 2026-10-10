package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

func main() {
	selfCheck := flag.Bool("self-check", false, "run scripted checks without opening a terminal")
	flag.Parse()

	if *selfCheck {
		if err := runChecks(); err != nil {
			fmt.Fprintln(os.Stderr, "self-check failed:", err)
			os.Exit(1)
		}
		fmt.Println("S06 render spike self-check: ok")
		return
	}

	program := tea.NewProgram(newModel())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run spike:", err)
		os.Exit(1)
	}
}
