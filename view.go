package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// View renders the full TUI with pinned title and footer, and a
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
		behindStr := ""
		if m.repo.Behind > 0 {
			behindStr = runningStyle.Render(fmt.Sprintf("  [%d behind]", m.repo.Behind))
		}
		repoInfo = dimStyle.Render(fmt.Sprintf("  %s → %s/%s",
			m.repo.Branch, m.repo.Remote, m.repo.Branch)) + aheadStr + behindStr
	}

	topLines := []string{
		title + repoInfo,
		dividerStyle.Render(strings.Repeat("─", w)),
		"",
		renderState(m),
	}

	// -- Fixed footer lines (always visible) ---------------------------------
	footerHint := "  Ctrl+C to quit"
	switch m.state {
	case stateIdle:
		if m.workflow.ID != 0 && m.workflow.Conclusion == "success" {
			footerHint += "  ·  " + runningStyle.Render("i") + dimStyle.Render(" to install")
		}
	case stateSelectingArtifact:
		if len(m.artifactList) > 0 {
			footerHint += "  ·  " + dimStyle.Render("number to select  ·  Esc to cancel")
		}
	}
	footerLines := []string{
		"",
		dividerStyle.Render(strings.Repeat("─", w)),
		dimStyle.Render(footerHint),
	}

	// -- Variable body sections ----------------------------------------------
	workflowLines := workflowSectionLines(m)
	installLines := installSectionLines(m)
	activityLines := activitySectionLines(m, w)
	pickerLines := artifactPickerLines(m)

	// Height budget for variable body sections.
	budget := m.height - len(topLines) - len(footerLines)
	if budget < 0 {
		budget = 0
	}

	third := budget / 3
	wAlloc := min(len(workflowLines), third)
	iAlloc := min(len(installLines), third)
	pAlloc := min(len(pickerLines), third)
	aAlloc := min(len(activityLines), budget-wAlloc-iAlloc-pAlloc)
	if aAlloc < 0 {
		aAlloc = 0
	}

	var lines []string
	lines = append(lines, topLines...)
	if pAlloc > 0 {
		lines = append(lines, "")
		lines = append(lines, pickerLines[:pAlloc]...)
	}
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
		tail := activityLines[len(activityLines)-aAlloc:]
		lines = append(lines, tail...)
	}
	lines = append(lines, footerLines...)

	return strings.Join(lines, "\n")
}

// -- Section renderers -------------------------------------------------------

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

// activitySectionLines returns the activity log. Lines are clipped to w
// characters to prevent wrapping on narrow terminals.
func activitySectionLines(m model, w int) []string {
	if len(m.activityLog) == 0 {
		return nil
	}
	maxLine := w - 4 // 2 indent + 2 margin
	if maxLine < 10 {
		maxLine = 10
	}
	var lines []string
	lines = append(lines, sectionHeaderStyle.Render("Activity"))
	for _, l := range m.activityLog {
		display := "  " + l
		if len(display) > maxLine {
			display = display[:maxLine-1] + "…"
		}
		lines = append(lines, logStyle.Render(display))
	}
	return lines
}

// artifactPickerLines renders the artifact selection UI.
func artifactPickerLines(m model) []string {
	if m.state != stateSelectingArtifact {
		return nil
	}
	var lines []string
	if len(m.artifactList) == 0 {
		lines = append(lines, sectionHeaderStyle.Render("Select artifact")+
			"  "+runningStyle.Render(spinnerFrames[m.spinner])+" loading...")
		return lines
	}
	lines = append(lines, sectionHeaderStyle.Render("Select artifact"))
	for i, a := range m.artifactList {
		if i >= 9 {
			break
		}
		sizeStr := ""
		if a.Size > 0 {
			sizeStr = "  " + dimStyle.Render(formatBytes(a.Size))
		}
		lines = append(lines, fmt.Sprintf("  %s  %s%s",
			runningStyle.Render(fmt.Sprintf("%d", i+1)),
			a.Name,
			sizeStr))
	}
	return lines
}

// -- State line --------------------------------------------------------------

func renderState(m model) string {
	spin := spinnerFrames[m.spinner]
	switch m.state {
	case stateIdle:
		if m.repo.Ahead == 0 && m.trackedSHA != "" {
			return "  " + successStyle.Render("✓") + dimStyle.Render(
				" idle — last: "+shortSHA(m.trackedSHA))
		}
		return "  " + idleStyle.Render("● idle — watching for new commits")

	case statePushing:
		return "  " + runningStyle.Render(spin) +
			" pushing " + shaStyle.Render(shortSHA(m.trackedSHA)) + "..."

	case statePushFailed:
		return "  " + failStyle.Render("✗ push failed") +
			dimStyle.Render(" — watching for changes")

	case stateMonitoring:
		waitingStr := ""
		if m.workflow.ID == 0 {
			waitingStr = dimStyle.Render(" (waiting for run...)")
		} else {
			waitingStr = dimStyle.Render(fmt.Sprintf(" run #%d", m.workflow.ID))
		}
		return "  " + runningStyle.Render(spin) +
			fmt.Sprintf(" monitoring %q for %s",
				m.workflowName, shaStyle.Render(shortSHA(m.trackedSHA))) +
			waitingStr

	case stateSelectingArtifact:
		return "  " + runningStyle.Render(spin) +
			dimStyle.Render(fmt.Sprintf(" select artifact to install (run #%d)", m.workflow.ID))

	case stateInstalling:
		return "  " + runningStyle.Render(spin) +
			" installing APK for " + shaStyle.Render(shortSHA(m.trackedSHA)) + "..."

	default:
		return "  " + dimStyle.Render(m.state.String())
	}
}

// -- Helpers -----------------------------------------------------------------

func activeStepName(steps []workflowStep) string {
	for _, s := range steps {
		if s.Status == "in_progress" {
			return s.Name
		}
	}
	return ""
}

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
	bar := strings.Repeat("▒", barWidth)
	return runningStyle.Render(fmt.Sprintf("[%s]  %s", bar, formatBytes(downloaded)))
}

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

func renderStatus(status, conclusion string) string {
	return renderStatusIcon(status, conclusion) + " " + jobStatusLabel(status, conclusion)
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
