package main

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// tickInterval controls how often the spinner advances and repo state is polled.
const tickInterval = 3 * time.Second

// pollEvery is how many ticks to skip between GitHub API workflow polls.
// At tickInterval=3 s this gives one API call every ~10 s instead of every 3 s.
const pollEvery = 3

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

		// 'f' force-pushes after a push failure.
		case "f":
			if m.state == statePushFailed {
				m.state = statePushing
				m.addLog("force pushing " + shortSHA(m.trackedSHA) + "...")
				return m, gitForcePush
			}

		// 'r' retries a normal push after a push failure.
		case "r":
			if m.state == statePushFailed {
				m.state = statePushing
				m.addLog("retrying push " + shortSHA(m.trackedSHA) + "...")
				return m, gitPush
			}

		// 'i' manually triggers the APK install for the last successful workflow.
		case "i":
			if m.state == stateIdle &&
				m.workflow.ID != 0 &&
				m.workflow.Conclusion == "success" {
				m.state = stateInstalling
				m.installLog = nil
				m.downloadedBytes = 0
				m.totalBytes = 0
				m.addLog(fmt.Sprintf("manual install triggered (run #%d, %s)...",
					m.workflow.ID, shortSHA(m.trackedSHA)))
				go installToChannel(m.workflow.ID, m.trackedSHA, m.repo.Slug,
					m.packageName, m.artifactName, m.installProgressCh)
				return m, waitForInstallProgress(m.installProgressCh)
			}
		}

	// -- Git filesystem event --------------------------------------------------
	// Re-arm the watcher and immediately re-check repo state so new commits
	// are detected without waiting for the next tick.
	case gitChangeMsg:
		return m, tea.Batch(fetchRepoState, waitForGitChange())

	// -- Periodic tick ---------------------------------------------------------
	case tickMsg:
		m.spinner = (m.spinner + 1) % len(spinnerFrames)
		m.pollTick = (m.pollTick + 1) % pollEvery
		cmds := []tea.Cmd{tick(tickInterval)}
		switch m.state {
		case stateIdle, stateFailed:
			// Poll repo state so we catch new commits even without inotify.
			cmds = append(cmds, fetchRepoState)
		case stateMonitoring:
			// Poll the workflow at a reduced rate to be gentle on the API.
			if m.pollTick == 0 {
				cmds = append(cmds, fetchWorkflow(m.workflowName, m.trackedSHA, m.workflow.ID))
			}
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
			cmds = append(cmds, delayedPush)
			return m, tea.Batch(cmds...)
		}
		// On startup, load the workflow status of the current HEAD commit.
		if cmd := tryStartupMonitor(&m); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	// -- Workflow presence check -----------------------------------------------
	case workflowsCheckMsg:
		m.hasWorkflows = bool(msg)
		// On startup, load the workflow status of the current HEAD commit.
		if cmd := tryStartupMonitor(&m); cmd != nil {
			return m, cmd
		}
		return m, nil

	// -- Pre-push delay elapsed ------------------------------------------------
	case pushReadyMsg:
		return m, gitPush

	// -- Push result -----------------------------------------------------------
	case gitPushMsg:
		if msg.err != nil {
			m.state = statePushFailed
			m.pushErr = msg.err.Error()
			m.addLog("push failed: " + msg.err.Error())
			return m, nil
		}
		if !m.hasWorkflows {
			m.addLog(fmt.Sprintf("push OK — %s (no workflows)", shortSHA(m.trackedSHA)))
			m.state = stateIdle
			return m, fetchRepoState
		}
		m.autoInstall = true
		m.state = stateMonitoring
		m.addLog(fmt.Sprintf("push OK — monitoring workflow %q for %s",
			m.workflowName, shortSHA(m.trackedSHA)))
		// Start polling immediately rather than waiting for the next tick.
		return m, fetchWorkflow(m.workflowName, m.trackedSHA, 0)

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
			if m.autoInstall {
				// Triggered by a push: auto-download and install.
				m.state = stateInstalling
				m.installLog = nil
				m.downloadedBytes = 0
				m.totalBytes = 0
				m.addLog(fmt.Sprintf("workflow succeeded (run #%d) — downloading & installing APK...", wr.ID))
				go installToChannel(wr.ID, m.trackedSHA, m.repo.Slug, m.packageName, m.artifactName, m.installProgressCh)
				return m, waitForInstallProgress(m.installProgressCh)
			}
			// Startup load: just display the result; let the user press 'i' to install.
			m.addLog(fmt.Sprintf("workflow succeeded (run #%d) — press 'i' to install", wr.ID))
			m.state = stateIdle
			return m, fetchRepoState
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
		m.autoInstall = false
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

// tryStartupMonitor starts workflow monitoring for the HEAD commit when we
// first launch and the repo is fully in sync with remote. It fires at most
// once (guarded by trackedSHA == ""). Returns nil if the conditions are not
// yet met or have already been satisfied.
func tryStartupMonitor(m *model) tea.Cmd {
	if m.trackedSHA != "" || m.repo.HeadSHA == "" || !m.hasWorkflows {
		return nil
	}
	if m.state != stateIdle || m.repo.Ahead != 0 {
		return nil
	}
	m.trackedSHA = m.repo.HeadSHA
	m.state = stateMonitoring
	m.addLog(fmt.Sprintf("startup: loading workflow status for %s...", shortSHA(m.trackedSHA)))
	return fetchWorkflow(m.workflowName, m.trackedSHA, 0)
}

// shortSHA returns the first 7 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
