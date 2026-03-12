package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// -- Low-level runner --------------------------------------------------------

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// -- Tick --------------------------------------------------------------------

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// -- Push command ------------------------------------------------------------

// delayedPush waits 1 second before signalling the model to actually push.
// This avoids racing against tools (e.g. IDE formatters) that write to the
// working tree in the brief window after a commit lands.
func delayedPush() tea.Msg {
	time.Sleep(time.Second)
	return pushReadyMsg{}
}

func gitPush() tea.Msg {
	_, err := runGit("push")
	return gitPushMsg{err: err}
}

func gitForcePush() tea.Msg {
	_, err := runGit("push", "--force")
	return gitPushMsg{err: err}
}

func gitPullRebase() tea.Msg {
	_, err := runGit("pull", "--rebase")
	if err != nil {
		_, _ = runGit("rebase", "--abort") // best-effort cleanup
	}
	return gitPullRebaseMsg{err: err}
}

// -- Helpers -----------------------------------------------------------------

// countLines counts non-empty lines in command output (whitespace-trimmed).
func countLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// parseSlug extracts "owner/repo" from a remote URL, supporting both HTTPS
// (https://github.com/owner/repo.git) and SSH (git@github.com:owner/repo.git)
// formats. Returns empty string if the URL cannot be parsed.
func parseSlug(url string) string {
	url = strings.TrimSuffix(url, ".git")
	// SSH: git@github.com:owner/repo
	if i := strings.Index(url, ":"); i != -1 && !strings.HasPrefix(url, "http") {
		slug := url[i+1:]
		if strings.Count(slug, "/") == 1 {
			return slug
		}
	}
	// HTTPS: https://github.com/owner/repo
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	return ""
}

// -- Repo state fetch --------------------------------------------------------

// fetchRepoState returns a repoStateMsg with branch / remote / HEAD / ahead count.
func fetchRepoState() tea.Msg {
	rs := repoState{}

	branch, err := runGit("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		rs.Error = fmt.Sprintf("not a git repo: %v", err)
		return repoStateMsg(rs)
	}
	rs.Branch = branch

	headSHA, err := runGit("rev-parse", "HEAD")
	if err != nil {
		rs.Error = "cannot read HEAD"
		return repoStateMsg(rs)
	}
	rs.HeadSHA = headSHA

	remote, err := runGit("config", "--get", fmt.Sprintf("branch.%s.remote", branch))
	if err != nil {
		remote = "origin"
	}
	rs.Remote = remote

	if url, err := runGit("remote", "get-url", remote); err == nil {
		rs.Slug = parseSlug(url)
	}

	upstream := remote + "/" + branch
	if _, err := runGit("rev-parse", "--verify", upstream); err == nil {
		if out, err := runGit("rev-list", upstream+"..HEAD"); err == nil {
			rs.Ahead = countLines(out)
		}
		if out, err := runGit("rev-list", "HEAD.."+upstream); err == nil {
			rs.Behind = countLines(out)
		}
	} else {
		// No upstream tracked — treat all local commits as unpushed.
		if out, _ := runGit("rev-list", "HEAD"); out != "" {
			rs.Ahead = countLines(out)
		}
	}

	return repoStateMsg(rs)
}
