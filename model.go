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
	artifactName string // empty = no pinned artifact; non-empty = install this artifact directly
	autoFlag     bool   // --auto: behave as if user pressed 'i' after each successful run

	// Whether the repo has .github/workflows/ files; set once at startup.
	// When false, push-only mode is used (no workflow monitoring or install).
	hasWorkflows bool

	// autoInstall is true when the current monitoring cycle should auto-install
	// (or auto-open the picker) on success. Set when a push/external-push
	// completes AND --auto is active.
	autoInstall bool

	// pullFailed is set when git pull --rebase fails so we don't retry in a
	// tight loop. Cleared automatically when Behind drops to zero.
	pullFailed bool

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

func initialModel(workflowName, packageName, artifactName string, autoFlag bool) model {
	return model{
		workflowName:      workflowName,
		packageName:       packageName,
		artifactName:      artifactName,
		autoFlag:          autoFlag,
		installProgressCh: make(chan installProgressMsg, 64),
	}
}

func (m model) Init() tea.Cmd {
	startGitWatcher()
	return tea.Batch(fetchRepoState, checkWorkflows, tick(3*time.Second), waitForGitChange())
}

// beginInstall transitions to stateInstalling, resets progress state, and
// starts the install goroutine. The caller supplies the log message.
func (m *model) beginInstall(runID int, artifactName, logMsg string) tea.Cmd {
	m.state = stateInstalling
	m.artifactList = nil
	m.installLog = nil
	m.downloadedBytes = 0
	m.totalBytes = 0
	m.addLog(logMsg)
	go installToChannel(runID, m.trackedSHA, m.repo.Slug, m.packageName, artifactName, m.installProgressCh)
	return waitForInstallProgress(m.installProgressCh)
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
