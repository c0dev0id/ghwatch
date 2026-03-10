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

	// Whether the repo has .github/workflows/ files; set once at startup.
	// When false, push-only mode is used (no workflow monitoring or install).
	hasWorkflows bool

	// pushErr holds the error message from the last failed push, displayed
	// while in statePushFailed so the user can decide how to proceed.
	pushErr string

	// autoInstall is true when the current monitoring cycle was triggered by a
	// push (so ghwatch should auto-install on success). It is false during the
	// startup load and when the user manually triggers an install via 'i'.
	autoInstall bool

	// GitHub Actions workflow data for the tracked SHA
	workflow workflowRun

	// Spinner frame index — advances on every tick
	spinner int

	// pollTick counts ticks since the last GitHub API poll. The workflow is
	// only queried every pollEvery ticks so the spinner stays smooth at 3 s
	// intervals while the API is hit at most once every ~10 s.
	pollTick int

	// Install progress. installProgressCh is created once and reused across
	// installs; the goroutine holds a send reference, the Bubble Tea Cmd holds
	// a receive reference.
	installProgressCh chan installProgressMsg
	downloadedBytes   int64
	totalBytes        int64

	// Log lines accumulated from the current (or last) install run.
	installLog []string

	// Rolling activity log shown at the bottom of the TUI
	activityLog []string
}

func initialModel(workflowName, packageName, artifactName string) model {
	return model{
		workflowName:      workflowName,
		packageName:       packageName,
		artifactName:      artifactName,
		installProgressCh: make(chan installProgressMsg, 64),
	}
}

func (m model) Init() tea.Cmd {
	startGitWatcher()
	return tea.Batch(fetchRepoState, checkWorkflows, tick(3*time.Second), waitForGitChange())
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
