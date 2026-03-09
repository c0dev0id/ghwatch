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
		m.repo = repoState(msg)
		if m.repo.Error != "" {
			return m, nil
		}
		// Trigger a push when there are unpushed commits and we are not busy.
		if (m.state == stateIdle || m.state == stateFailed) && m.repo.Ahead > 0 {
			m.state = statePushing
			m.trackedSHA = m.repo.HeadSHA
			m.workflow = workflowRun{} // clear previous workflow display
			m.installLog = nil
			m.addLog(fmt.Sprintf("detected %d unpushed commit(s) — pushing %s...",
				m.repo.Ahead, shortSHA(m.repo.HeadSHA)))
			return m, gitPush
		}
		return m, nil

	// -- Push result -----------------------------------------------------------
	case gitPushMsg:
		if msg.err != nil {
			m.state = stateFailed
			m.addLog("push failed: " + msg.err.Error())
			return m, nil
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
			m.addLog(fmt.Sprintf("workflow succeeded (run #%d) — downloading & installing APK...", wr.ID))
			return m, installFromRun(wr.ID, m.trackedSHA, m.packageName, m.artifactName)
		}

		// Workflow failed.
		m.state = stateFailed
		m.addLog(fmt.Sprintf("workflow FAILED — run #%d", wr.ID))
		m.addLog("url: " + wr.URL)
		return m, nil

	// -- ADB install result ---------------------------------------------------
	case adbInstallMsg:
		m.installLog = msg.log
		if msg.err != nil {
			m.state = stateFailed
			m.addLog("install failed: " + msg.err.Error())
		} else {
			m.addLog("install successful! ✓")
			// Return to idle so the next commit is picked up automatically.
			m.state = stateIdle
		}
		// Re-check repo state immediately — a new commit may have landed.
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
