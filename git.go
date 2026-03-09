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

func gitPush() tea.Msg {
	_, err := runGit("push")
	return gitPushMsg{err: err}
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

	unpushed := make(map[string]bool)
	upstream := remote + "/" + branch
	if _, err := runGit("rev-parse", "--verify", upstream); err == nil {
		if out, err := runGit("rev-list", upstream+"..HEAD"); err == nil {
			for _, s := range strings.Split(out, "\n") {
				s = strings.TrimSpace(s)
				if s != "" {
					unpushed[s] = true
				}
			}
		}
	} else {
		// No upstream tracked — treat all local commits as unpushed.
		if out, _ := runGit("rev-list", "HEAD"); out != "" {
			for _, s := range strings.Split(out, "\n") {
				s = strings.TrimSpace(s)
				if s != "" {
					unpushed[s] = true
				}
			}
		}
	}
	rs.Ahead = len(unpushed)

	return repoStateMsg(rs)
}
