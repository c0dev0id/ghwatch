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

		// 'i' — install APK.
		// With --artifact: install directly.
		// Without --artifact: open artifact picker to let user choose.
		case "i":
			if m.state == stateIdle {
				if m.workflow.ID == 0 || m.workflow.Conclusion != "success" {
					break
				}
				if m.artifactName != "" {
					return m, m.beginInstall(m.workflow.ID, m.artifactName,
						fmt.Sprintf("installing (run #%d, %s)...", m.workflow.ID, shortSHA(m.trackedSHA)))
				}
				// No artifact pinned — fetch list and show picker.
				m.state = stateSelectingArtifact
				m.artifactList = nil
				return m, fetchArtifactListCmd(m.repo.Slug, m.workflow.ID)
			}

		// Number keys — select an artifact in the picker.
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if m.state == stateSelectingArtifact && len(m.artifactList) > 0 {
				idx := int(msg.String()[0] - '1')
				if idx < len(m.artifactList) {
					chosen := m.artifactList[idx]
					return m, m.beginInstall(m.workflow.ID, chosen.Name,
						fmt.Sprintf("installing %q (run #%d)...", chosen.Name, m.workflow.ID))
				}
			}

		// Escape — cancel artifact picker.
		case "esc":
			if m.state == stateSelectingArtifact {
				m.state = stateIdle
				m.artifactList = nil
				return m, nil
			}
		}

	// -- Git filesystem event --------------------------------------------------
	// Re-arm the watcher and immediately re-check repo state.
	case gitChangeMsg:
		return m, tea.Batch(fetchRepoState, waitForGitChange())

	// -- Periodic tick ---------------------------------------------------------
	case tickMsg:
		m.spinner = (m.spinner + 1) % len(spinnerFrames)
		m.pollTick = (m.pollTick + 1) % pollEvery
		cmds := []tea.Cmd{tick(tickInterval)}
		switch m.state {
		case stateIdle, statePushFailed:
			// Poll repo state to catch new commits even without inotify.
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

		// -- statePushFailed transitions ----------------------------------------
		// The previous push failed. Decide whether anything has changed.
		if m.state == statePushFailed {
			if m.repo.Ahead == 0 {
				// Someone pushed (manually or force-pushed) — start monitoring.
				m.trackedSHA = m.repo.HeadSHA
				m.workflow = workflowRun{}
				m.state = stateMonitoring
				m.autoInstall = m.autoFlag
				m.addLog(fmt.Sprintf("push resolved — monitoring %s", shortSHA(m.trackedSHA)))
				cmds = append(cmds, fetchWorkflow(m.workflowName, m.trackedSHA, 0))
				return m, tea.Batch(cmds...)
			}
			if m.repo.HeadSHA != m.trackedSHA {
				// New local commit — fall through to normal push detection below.
				m.state = stateIdle
			} else {
				// Same failing commit still unpushed — wait.
				return m, tea.Batch(cmds...)
			}
		}

		// -- Normal push trigger ------------------------------------------------
		if m.state == stateIdle && m.repo.Ahead > 0 {
			m.state = statePushing
			m.trackedSHA = m.repo.HeadSHA
			m.workflow = workflowRun{}
			m.installLog = nil
			m.addLog(fmt.Sprintf("detected %d unpushed commit(s), pushing %s",
				m.repo.Ahead, shortSHA(m.repo.HeadSHA)))
			cmds = append(cmds, delayedPush)
			return m, tea.Batch(cmds...)
		}

		// -- External push detection --------------------------------------------
		// HEAD changed while ahead==0: the user pushed from outside ghwatch
		// (force push, revert, etc.). Start/switch to monitoring the new SHA.
		if m.repo.Ahead == 0 &&
			m.repo.HeadSHA != "" &&
			m.repo.HeadSHA != m.trackedSHA &&
			m.trackedSHA != "" &&
			m.hasWorkflows &&
			(m.state == stateIdle || m.state == stateMonitoring) {
			m.trackedSHA = m.repo.HeadSHA
			m.workflow = workflowRun{}
			m.installLog = nil
			m.autoInstall = m.autoFlag
			m.state = stateMonitoring
			m.addLog(fmt.Sprintf("external push detected, monitoring %s", shortSHA(m.trackedSHA)))
			cmds = append(cmds, fetchWorkflow(m.workflowName, m.trackedSHA, 0))
			return m, tea.Batch(cmds...)
		}

		// -- Startup monitor ---------------------------------------------------
		if cmd := tryStartupMonitor(&m); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	// -- Workflow presence check -----------------------------------------------
	case workflowsCheckMsg:
		m.hasWorkflows = bool(msg)
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
			m.addLog("push failed: " + msg.err.Error())
			return m, nil
		}
		if !m.hasWorkflows {
			m.addLog(fmt.Sprintf("push OK — %s (no workflows)", shortSHA(m.trackedSHA)))
			m.state = stateIdle
			return m, fetchRepoState
		}
		m.autoInstall = m.autoFlag
		m.state = stateMonitoring
		m.addLog(fmt.Sprintf("push OK — monitoring %q for %s",
			m.workflowName, shortSHA(m.trackedSHA)))
		return m, fetchWorkflow(m.workflowName, m.trackedSHA, 0)

	// -- Workflow run update ---------------------------------------------------
	case workflowRunMsg:
		// Ignore late responses that arrive after we've left stateMonitoring.
		if m.state != stateMonitoring {
			return m, nil
		}
		wr := workflowRun(msg)
		if wr.Error != "" {
			// "waiting for run" or transient API error — tick will retry.
			return m, nil
		}
		m.workflow = wr

		if wr.Status != "completed" {
			return m, nil
		}

		if wr.Conclusion == "success" {
			if m.autoInstall {
				if m.artifactName != "" {
					// Artifact pinned via --artifact — install directly.
					return m, m.beginInstall(wr.ID, m.artifactName,
						fmt.Sprintf("workflow OK (run #%d) — installing", wr.ID))
				}
				// --auto without --artifact — open picker (same as pressing 'i').
				m.addLog(fmt.Sprintf("workflow OK (run #%d) — select artifact", wr.ID))
				m.state = stateSelectingArtifact
				m.artifactList = nil
				return m, fetchArtifactListCmd(m.repo.Slug, wr.ID)
			}
			// Display-only mode: inform and go idle.
			m.addLog(fmt.Sprintf("workflow OK (run #%d)%s",
				wr.ID, installHint()))
			m.state = stateIdle
			return m, fetchRepoState
		}

		// Workflow failed — go idle; the job/step tree already shows the details.
		m.state = stateIdle
		m.addLog(fmt.Sprintf("workflow failed (run #%d)", wr.ID))
		return m, fetchRepoState

	// -- Artifact list (for picker) -------------------------------------------
	case artifactListMsg:
		if msg.Err != nil {
			m.addLog("artifact list unavailable")
			m.state = stateIdle
			return m, nil
		}
		if len(msg.Artifacts) == 0 {
			m.addLog("no release artifacts found")
			m.state = stateIdle
			return m, nil
		}
		m.artifactList = msg.Artifacts
		return m, nil

	// -- Install progress / completion ----------------------------------------
	case installProgressMsg:
		if !msg.Done {
			if msg.LogLine != "" {
				m.installLog = append(m.installLog, msg.LogLine)
			}
			if msg.Downloaded > 0 || msg.Total > 0 {
				m.downloadedBytes = msg.Downloaded
				m.totalBytes = msg.Total
			} else if msg.LogLine != "" {
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
			m.state = stateIdle
			m.addLog("install failed")
		} else {
			m.addLog("install complete ✓")
			m.state = stateIdle
		}
		return m, fetchRepoState
	}

	return m, nil
}

// tryStartupMonitor starts workflow monitoring for the HEAD commit on startup
// when the repo is fully in sync. Fires at most once (guarded by trackedSHA=="").
func tryStartupMonitor(m *model) tea.Cmd {
	if m.trackedSHA != "" || m.repo.HeadSHA == "" || !m.hasWorkflows {
		return nil
	}
	if m.state != stateIdle || m.repo.Ahead != 0 || m.repo.Behind != 0 {
		return nil
	}
	m.trackedSHA = m.repo.HeadSHA
	m.state = stateMonitoring
	m.addLog(fmt.Sprintf("startup: monitoring %s", shortSHA(m.trackedSHA)))
	return fetchWorkflow(m.workflowName, m.trackedSHA, 0)
}

// installHint returns the footer hint shown after a successful workflow in display-only mode.
func installHint() string {
	return " — press i to install"
}

// shortSHA returns the first 7 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
