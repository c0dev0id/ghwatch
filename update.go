package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// tickInterval is how often the app polls for repo / workflow state.
const tickInterval = 3 * time.Second

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// -- Terminal resize -------------------------------------------------------
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	// -- Keyboard --------------------------------------------------------------
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}

	// -- Git filesystem event --------------------------------------------------
	// Re-arm the watcher and immediately re-check repo state so new commits
	// are detected without waiting for the next tick.
	case gitChangeMsg:
		return m, tea.Batch(fetchRepoState, waitForGitChange())

	// -- Periodic tick ---------------------------------------------------------
	case tickMsg:
		m.spinner = (m.spinner + 1) % len(spinnerFrames)
		cmds := []tea.Cmd{tick(tickInterval)}
		switch m.state {
		case stateIdle, stateFailed:
			// Poll repo state so we catch new commits even without inotify.
			cmds = append(cmds, fetchRepoState)
		case stateMonitoring:
			// Poll the workflow until it completes.
			cmds = append(cmds, fetchWorkflow(m.workflowName, m.trackedSHA))
		}
		return m, tea.Batch(cmds...)

	// -- Repository state update -----------------------------------------------
	case repoStateMsg:
		prevSlug := m.repo.Slug
		m.repo = repoState(msg)
		if m.repo.Error != "" {
			return m, nil
		}
		var cmds []tea.Cmd
		// Update terminal window title whenever the slug becomes known or changes.
		if m.repo.Slug != "" && m.repo.Slug != prevSlug {
			cmds = append(cmds, tea.SetWindowTitle("ghwatch — "+m.repo.Slug))
		}
		// Trigger a push when there are unpushed commits and we are not busy.
		if (m.state == stateIdle || m.state == stateFailed) && m.repo.Ahead > 0 {
			m.state = statePushing
			m.trackedSHA = m.repo.HeadSHA
			m.workflow = workflowRun{} // clear previous workflow display
			m.installLog = nil
			m.addLog(fmt.Sprintf("detected %d unpushed commit(s) — pushing %s...",
				m.repo.Ahead, shortSHA(m.repo.HeadSHA)))
			cmds = append(cmds, gitPush)
			return m, tea.Batch(cmds...)
		}
		return m, tea.Batch(cmds...)

	// -- Workflow presence check -----------------------------------------------
	case workflowsCheckMsg:
		m.hasWorkflows = bool(msg)
		return m, nil

	// -- Push result -----------------------------------------------------------
	case gitPushMsg:
		if msg.err != nil {
			m.state = stateFailed
			m.addLog("push failed: " + msg.err.Error())
			return m, nil
		}
		if !m.hasWorkflows {
			m.addLog(fmt.Sprintf("push OK — %s (no workflows)", shortSHA(m.trackedSHA)))
			m.state = stateIdle
			return m, fetchRepoState
		}
		m.state = stateMonitoring
		m.addLog(fmt.Sprintf("push OK — monitoring workflow %q for %s",
			m.workflowName, shortSHA(m.trackedSHA)))
		// Start polling immediately rather than waiting for the next tick.
		return m, fetchWorkflow(m.workflowName, m.trackedSHA)

	// -- Workflow run update ---------------------------------------------------
	case workflowRunMsg:
		wr := workflowRun(msg)
		if wr.Error != "" {
			// "waiting for run" or transient API error — tick will retry.
			return m, nil
		}
		m.workflow = wr

		if wr.Status != "completed" {
			// Still running — nothing to do; tick will poll again.
			return m, nil
		}

		if wr.Conclusion == "success" {
			m.state = stateInstalling
			m.installLog = nil
			m.downloadedBytes = 0
			m.totalBytes = 0
			m.addLog(fmt.Sprintf("workflow succeeded (run #%d) — downloading & installing APK...", wr.ID))
			go installToChannel(wr.ID, m.trackedSHA, m.repo.Slug, m.packageName, m.artifactName, m.installProgressCh)
			return m, waitForInstallProgress(m.installProgressCh)
		}

		// Workflow failed.
		m.state = stateFailed
		m.addLog(fmt.Sprintf("workflow FAILED — run #%d", wr.ID))
		m.addLog("url: " + wr.URL)
		return m, nil

	// -- Install progress / completion ----------------------------------------
	case installProgressMsg:
		if !msg.Done {
			// Accumulate log lines.
			if msg.LogLine != "" {
				m.installLog = append(m.installLog, msg.LogLine)
			}
			// Update download progress counters (zero means download is done).
			if msg.Downloaded > 0 || msg.Total > 0 {
				m.downloadedBytes = msg.Downloaded
				m.totalBytes = msg.Total
			} else if msg.LogLine != "" {
				// A log line after zeroed counters means we moved past the download phase.
				m.downloadedBytes = 0
				m.totalBytes = 0
			}
			return m, waitForInstallProgress(m.installProgressCh)
		}
		// Install pipeline finished.
		m.installLog = msg.FinalLog
		m.downloadedBytes = 0
		m.totalBytes = 0
		if msg.Err != nil {
			m.state = stateFailed
			m.addLog("install failed: " + msg.Err.Error())
		} else {
			m.addLog("install successful! ✓")
			m.state = stateIdle
		}
		return m, fetchRepoState
	}

	return m, nil
}

// shortSHA returns the first 7 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
