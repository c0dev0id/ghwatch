package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const maxLogLines = 100

// -- Model -------------------------------------------------------------------

type model struct {
	// Terminal dimensions
	width, height int

	// Core state machine
	state appState

	// Repository metadata
	repo repoState

	// The commit SHA we are currently pushing / monitoring / installing for.
	trackedSHA string

	// Configuration (from CLI flags)
	workflowName string
	packageName  string
	artifactName string

	// GitHub Actions workflow data for the tracked SHA
	workflow workflowRun

	// Spinner frame index — advances on every tick
	spinner int

	// Output lines from the last adb install attempt
	installLog []string

	// Rolling activity log shown at the bottom of the TUI
	activityLog []string
}

func initialModel(workflowName, packageName, artifactName string) model {
	return model{
		workflowName: workflowName,
		packageName:  packageName,
		artifactName: artifactName,
	}
}

func (m model) Init() tea.Cmd {
	startGitWatcher()
	return tea.Batch(fetchRepoState, tick(3*time.Second), waitForGitChange())
}

// addLog appends a timestamped line to the activity log, capping at maxLogLines.
func (m *model) addLog(msg string) {
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s", ts, msg)
	m.activityLog = append(m.activityLog, line)
	if len(m.activityLog) > maxLogLines {
		m.activityLog = m.activityLog[len(m.activityLog)-maxLogLines:]
	}
}

// visibleLogLines returns how many activity-log lines fit in the current terminal.
func (m model) visibleLogLines() int {
	// Reserve: 1 title + 1 blank + 3 repo info + 1 blank + 3 state section +
	//          1 blank + workflow (variable) + 1 blank + install (variable) +
	//          1 section header + 1 footer = ~14 fixed rows minimum.
	// We just use a simple heuristic: bottom quarter of the terminal.
	v := m.height/4 - 1
	if v < 3 {
		return 3
	}
	return v
}
