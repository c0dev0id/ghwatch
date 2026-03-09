package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	workflow := flag.String("workflow", "Build",
		"GitHub Actions workflow name to monitor")
	pkg := flag.String("package", "",
		"Android package/component for launch after install (e.g. com.example.app or com.example.app/.MainActivity); auto-detected from manifest if omitted")
	artifact := flag.String("artifact", "",
		"GitHub Actions artifact name to download for install (e.g. app-signed); downloads all artifacts if omitted")
	flag.Parse()

	p := tea.NewProgram(initialModel(*workflow, *pkg, *artifact), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
