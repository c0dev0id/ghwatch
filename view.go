package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View renders the full TUI with a fixed title and footer, and a
// height-bounded body so content never pushes the title off-screen.
func (m model) View() string {
	w := m.width
	if w == 0 {
		w = 80
	}

	// -- Fixed top lines (always visible) ------------------------------------
	titleText := " ghwatch "
	if m.repo.Slug != "" {
		titleText = " ghwatch  " + m.repo.Slug + " "
	}
	title := titleStyle.Render(titleText)
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

	topLines := []string{
		title + repoInfo,
		dividerStyle.Render(strings.Repeat("─", w)),
		"",
		renderState(m),
	}

	// -- Fixed footer lines (always visible) ---------------------------------
	footerHint := "  Ctrl+C to quit"
	if m.state == stateIdle && m.workflow.ID != 0 && m.workflow.Conclusion == "success" {
		footerHint += dimStyle.Render("  ·  ") + runningStyle.Render("i") + dimStyle.Render(" to install")
	}
	footerLines := []string{
		"",
		dividerStyle.Render(strings.Repeat("─", w)),
		dimStyle.Render(footerHint),
	}

	// -- Variable body sections ----------------------------------------------
	workflowLines := workflowSectionLines(m)
	installLines := installSectionLines(m)
	activityLines := activitySectionLines(m)

	// Calculate how many lines the body sections can occupy.
	budget := m.height - len(topLines) - len(footerLines)
	if budget < 0 {
		budget = 0
	}

	// Allocate budget: workflow and install each get up to 1/3 of budget,
	// activity gets the rest (showing the most recent lines).
	third := budget / 3

	wAlloc := min(len(workflowLines), third)
	iAlloc := min(len(installLines), third)
	aAlloc := budget - wAlloc - iAlloc
	if aAlloc < 0 {
		aAlloc = 0
	}
	aAlloc = min(len(activityLines), aAlloc)

	// Build final line list.
	var lines []string
	lines = append(lines, topLines...)

	if wAlloc > 0 {
		lines = append(lines, "")
		lines = append(lines, workflowLines[:wAlloc]...)
	}
	if iAlloc > 0 {
		lines = append(lines, "")
		lines = append(lines, installLines[:iAlloc]...)
	}
	if aAlloc > 0 {
		lines = append(lines, "")
		// Show the most recent lines (tail) when the section is clamped.
		tail := activityLines[len(activityLines)-aAlloc:]
		lines = append(lines, tail...)
	}

	lines = append(lines, footerLines...)

	return strings.Join(lines, "\n")
}

// workflowSectionLines returns the workflow section as a slice of lines,
// or nil if there is nothing to show.
func workflowSectionLines(m model) []string {
	if m.workflow.ID == 0 && m.state != stateMonitoring {
		return nil
	}

	wr := m.workflow
	var headerStatus string
	if wr.ID == 0 {
		headerStatus = runningStyle.Render("waiting...")
	} else {
		headerStatus = renderStatus(wr.Status, wr.Conclusion)
	}

	var lines []string
	lines = append(lines, sectionHeaderStyle.Render("Workflow")+
		" "+dimStyle.Render(m.workflowName)+
		"  "+headerStatus)

	if wr.URL != "" && wr.Status == "completed" && wr.Conclusion != "success" {
		lines = append(lines, "  "+dimStyle.Render("url: ")+wr.URL)
		if wr.ID != 0 {
			lines = append(lines, "  "+dimStyle.Render(fmt.Sprintf("run id: %d", wr.ID)))
		}
	}

	for _, job := range wr.Jobs {
		jobIcon := renderStatusIcon(job.Status, job.Conclusion)
		activeStep := activeStepName(job.Steps)
		stepSuffix := ""
		if activeStep != "" {
			stepSuffix = "  " + dimStyle.Render(activeStep)
		}
		lines = append(lines, fmt.Sprintf("  %s %s%s",
			jobIcon,
			lipgloss.NewStyle().Bold(true).Render(job.Name),
			stepSuffix))
	}

	return lines
}

// installSectionLines returns the install section as a slice of lines,
// or nil if there is nothing to show.
func installSectionLines(m model) []string {
	if len(m.installLog) == 0 && m.downloadedBytes == 0 && m.totalBytes == 0 {
		return nil
	}

	var lines []string
	lines = append(lines, sectionHeaderStyle.Render("Install"))
	for _, line := range m.installLog {
		lines = append(lines, dimStyle.Render("  "+line))
	}
	if m.downloadedBytes > 0 || m.totalBytes > 0 {
		lines = append(lines, "  "+renderProgressBar(m.downloadedBytes, m.totalBytes, 24))
	}
	return lines
}

// activitySectionLines returns the activity log section as a slice of lines,
// or nil if there is nothing to show. The first line is always the header.
func activitySectionLines(m model) []string {
	if len(m.activityLog) == 0 {
		return nil
	}
	var lines []string
	lines = append(lines, sectionHeaderStyle.Render("Activity"))
	for _, l := range m.activityLog {
		lines = append(lines, logStyle.Render("  "+l))
	}
	return lines
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
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

// renderProgressBar renders a compact progress bar.
//
//	[████████████░░░░░░░░]  61%  12.3 MB / 20.1 MB
//	[▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒▒]  4.2 MB  (size unknown)
func renderProgressBar(downloaded, total int64, barWidth int) string {
	if total > 0 {
		pct := float64(downloaded) / float64(total)
		if pct > 1 {
			pct = 1
		}
		filled := int(pct * float64(barWidth))
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
		return runningStyle.Render(fmt.Sprintf("[%s]  %3.0f%%  %s / %s",
			bar, pct*100, formatBytes(downloaded), formatBytes(total)))
	}
	// Unknown total — show bytes downloaded with a placeholder bar.
	bar := strings.Repeat("▒", barWidth)
	return runningStyle.Render(fmt.Sprintf("[%s]  %s", bar, formatBytes(downloaded)))
}

// formatBytes formats a byte count as a human-readable string (e.g. "4.2 MB").
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
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
