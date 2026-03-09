package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View renders the full TUI.
func (m model) View() string {
	var b strings.Builder

	w := m.width
	if w == 0 {
		w = 80
	}

	// -- Title bar -----------------------------------------------------------
	title := titleStyle.Render(" ghwatch ")
	var repoInfo string
	if m.repo.Error != "" {
		repoInfo = errorStyle.Render("  " + m.repo.Error)
	} else if m.repo.Branch != "" {
		aheadStr := ""
		if m.repo.Ahead > 0 {
			aheadStr = runningStyle.Render(fmt.Sprintf("  [%d ahead]", m.repo.Ahead))
		}
		repoInfo = dimStyle.Render(fmt.Sprintf("  %s → %s/%s",
			m.repo.Branch, m.repo.Remote, m.repo.Branch)) + aheadStr
	}
	b.WriteString(title + repoInfo)
	b.WriteString("\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	// -- State section -------------------------------------------------------
	b.WriteString("\n")
	b.WriteString(renderState(m))
	b.WriteString("\n")

	// -- Workflow section ----------------------------------------------------
	if m.workflow.ID != 0 || m.state == stateMonitoring {
		b.WriteString("\n")
		b.WriteString(renderWorkflow(m))
		b.WriteString("\n")
	}

	// -- Install log section -------------------------------------------------
	if len(m.installLog) > 0 {
		b.WriteString("\n")
		b.WriteString(renderInstallLog(m))
		b.WriteString("\n")
	}

	// -- Activity log --------------------------------------------------------
	if len(m.activityLog) > 0 {
		b.WriteString("\n")
		b.WriteString(sectionHeaderStyle.Render("Activity"))
		b.WriteString("\n")
		lines := m.activityLog
		visible := m.visibleLogLines()
		if len(lines) > visible {
			lines = lines[len(lines)-visible:]
		}
		for _, l := range lines {
			b.WriteString(logStyle.Render("  " + l))
			b.WriteString("\n")
		}
	}

	// -- Footer --------------------------------------------------------------
	b.WriteString("\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("  Ctrl+C to quit"))
	b.WriteString("\n")

	return b.String()
}

// renderState renders the current state indicator line.
func renderState(m model) string {
	spin := spinnerFrames[m.spinner]
	switch m.state {
	case stateIdle:
		if m.repo.Ahead == 0 && m.trackedSHA != "" {
			// Just finished — show the SHA we last processed.
			return "  " + successStyle.Render("✓") + dimStyle.Render(
				fmt.Sprintf(" idle — last processed %s", shortSHA(m.trackedSHA)))
		}
		return "  " + idleStyle.Render("● idle — watching for new commits")

	case statePushing:
		return "  " + runningStyle.Render(spin) +
			" pushing " + shaStyle.Render(shortSHA(m.trackedSHA)) + "..."

	case stateMonitoring:
		waitingStr := ""
		if m.workflow.ID == 0 {
			waitingStr = dimStyle.Render(" (waiting for run to appear...)")
		} else {
			waitingStr = dimStyle.Render(fmt.Sprintf(" run #%d", m.workflow.ID))
		}
		return "  " + runningStyle.Render(spin) +
			fmt.Sprintf(" monitoring %q for %s",
				m.workflowName, shaStyle.Render(shortSHA(m.trackedSHA))) +
			waitingStr

	case stateInstalling:
		return "  " + runningStyle.Render(spin) +
			" installing APK for " + shaStyle.Render(shortSHA(m.trackedSHA)) + "..."

	case stateFailed:
		return "  " + failStyle.Render("✗ failed") +
			dimStyle.Render(" — fix the issue and commit to retry")

	default:
		return "  " + dimStyle.Render(m.state.String())
	}
}

// renderWorkflow renders the workflow job list with minimal output.
// Each job shows its status icon and name, with the currently running
// step appended inline. Completed steps are not listed.
func renderWorkflow(m model) string {
	var b strings.Builder

	wr := m.workflow
	var headerStatus string
	if wr.ID == 0 {
		headerStatus = runningStyle.Render("waiting...")
	} else {
		headerStatus = renderStatus(wr.Status, wr.Conclusion)
	}

	b.WriteString(sectionHeaderStyle.Render("Workflow") +
		" " + dimStyle.Render(m.workflowName) +
		"  " + headerStatus)
	b.WriteString("\n")

	if wr.URL != "" && wr.Status == "completed" && wr.Conclusion != "success" {
		b.WriteString("  " + dimStyle.Render("url: ") + wr.URL)
		b.WriteString("\n")
		if wr.ID != 0 {
			b.WriteString("  " + dimStyle.Render(fmt.Sprintf("run id: %d", wr.ID)))
			b.WriteString("\n")
		}
	}

	for _, job := range wr.Jobs {
		jobIcon := renderStatusIcon(job.Status, job.Conclusion)
		activeStep := activeStepName(job.Steps)
		stepSuffix := ""
		if activeStep != "" {
			stepSuffix = "  " + dimStyle.Render(activeStep)
		}
		b.WriteString(fmt.Sprintf("  %s %s%s\n",
			jobIcon,
			lipgloss.NewStyle().Bold(true).Render(job.Name),
			stepSuffix))
	}

	return b.String()
}

// activeStepName returns the name of the currently in_progress step,
// or empty string if no step is actively running.
func activeStepName(steps []workflowStep) string {
	for _, s := range steps {
		if s.Status == "in_progress" {
			return s.Name
		}
	}
	return ""
}

// renderInstallLog renders the adb install step log.
func renderInstallLog(m model) string {
	var b strings.Builder
	b.WriteString(sectionHeaderStyle.Render("Install"))
	b.WriteString("\n")
	for _, line := range m.installLog {
		b.WriteString(dimStyle.Render("  " + line))
		b.WriteString("\n")
	}
	return b.String()
}

// -- Helpers -----------------------------------------------------------------

func renderStatus(status, conclusion string) string {
	icon := renderStatusIcon(status, conclusion)
	label := jobStatusLabel(status, conclusion)
	return icon + " " + label
}

func renderStatusIcon(status, conclusion string) string {
	if status == "completed" {
		switch conclusion {
		case "success":
			return successStyle.Render("✓")
		case "skipped":
			return dimStyle.Render("–")
		default:
			return failStyle.Render("✗")
		}
	}
	if status == "in_progress" {
		return runningStyle.Render("⟳")
	}
	return dimStyle.Render("·")
}

func jobStatusLabel(status, conclusion string) string {
	if status == "completed" {
		switch conclusion {
		case "success":
			return successStyle.Render("success")
		case "skipped":
			return dimStyle.Render("skipped")
		default:
			return failStyle.Render(conclusion)
		}
	}
	if status == "in_progress" {
		return runningStyle.Render("running")
	}
	return dimStyle.Render(status)
}
