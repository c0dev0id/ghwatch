package main

import "github.com/charmbracelet/lipgloss"

// -- Styles ------------------------------------------------------------------

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 1)

	successStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("34"))

	failStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("196"))

	runningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	shaStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	sectionHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("62"))

	logStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	dividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238"))

	idleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))
)

// spinnerFrames is the braille spinner sequence used during active operations.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
