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
	artifactName string // empty = display-only mode; non-empty = auto-install after build

	// Whether the repo has .github/workflows/ files; set once at startup.
	// When false, push-only mode is used (no workflow monitoring or install).
	hasWorkflows bool

	// autoInstall is true when the current monitoring cycle should auto-install
	// on success. Set only when a push completes AND --artifact is specified.
	autoInstall bool

	// GitHub Actions workflow data for the tracked SHA
	workflow workflowRun

	// Spinner frame index — advances on every tick
	spinner int

	// pollTick counts ticks since the last GitHub API poll. The workflow is
	// only queried every pollEvery ticks so the spinner stays smooth at 3 s
	// intervals while the API is hit at most once every ~10 s.
	pollTick int

	// artifactList holds the list fetched for the artifact picker.
	// Populated while in stateSelectingArtifact.
	artifactList []artifactInfo

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
