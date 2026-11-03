package main

import (
	"flag"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"lazylima/internal/engine"
	"lazylima/internal/ui"
	"os"
)

var version = "dev"

func main() {
	demo := flag.Bool("demo", false, "explore with sample data; never invokes Colima or Docker")
	printVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *printVersion {
		fmt.Println("lazylima " + version)
		return
	}
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments; use --help")
		os.Exit(2)
	}
	var backend engine.Backend = engine.New()
	if *demo {
		backend = engine.NewDemo()
	}
	model := ui.New(backend, *demo)
	defer model.Close()
	if _, err := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
