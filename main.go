package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: ghwatch [options]\n\nOptions:\n")
		flag.VisitAll(func(f *flag.Flag) {
			fmt.Fprintf(flag.CommandLine.Output(),
				"  --%-14s %s\n", f.Name, f.Usage)
		})
	}

	workflow := flag.String("workflow", "Build",
		"GitHub Actions workflow name to monitor (default: Build)")
	pkg := flag.String("package", "",
		"Android package for launch after install (e.g. com.example.app or com.example.app/.MainActivity); auto-detected from manifest if omitted")
	artifact := flag.String("artifact", "",
		"Artifact name to download and install (e.g. app-release-signed); omit to display workflow status only")
	flag.Parse()

	p := tea.NewProgram(initialModel(*workflow, *pkg, *artifact), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
