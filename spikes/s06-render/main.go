package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
)

func main() {
	selfCheck := flag.Bool("self-check", false, "run scripted checks without opening a terminal")
	independentWriter := flag.Bool("independent-writer", false, "compare the independent normal-screen writer in a terminal")
	writerReceipt := flag.Bool("writer-receipt", false, "seal displayed live text when comparing the independent writer")
	defaultWriter := flag.Bool("default-writer", false, "compare Bubble Tea's default normal-screen renderer in a terminal")
	flag.Parse()

	if *selfCheck {
		if err := runChecks(); err != nil {
			fmt.Fprintln(os.Stderr, "self-check failed:", err)
			os.Exit(1)
		}
		fmt.Println("S06 render spike self-check: ok")
		return
	}
	if *independentWriter {
		if err := runIndependentWriterDemo(os.Stdout, *writerReceipt); err != nil {
			fmt.Fprintln(os.Stderr, "run independent writer demo:", err)
			os.Exit(1)
		}
		return
	}
	if *defaultWriter {
		if err := runDefaultWriterDemo(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "run default renderer demo:", err)
			os.Exit(1)
		}
		return
	}

	program := tea.NewProgram(newModel())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "run spike:", err)
		os.Exit(1)
	}
}
