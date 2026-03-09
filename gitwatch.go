package main

import tea "github.com/charmbracelet/bubbletea"

// gitChangeCh carries a signal from the OS-level file watcher to the
// Bubble Tea event loop. It is buffered (capacity 1) so the goroutine
// never blocks if a signal is already pending.
var gitChangeCh = make(chan struct{}, 1)

// waitForGitChange returns a Cmd that blocks until the inotify goroutine
// signals a change, then produces a gitChangeMsg.
// On platforms without inotify support (startGitWatcher is a no-op) the
// Cmd blocks forever; the periodic tick still drives repo-state reloads.
func waitForGitChange() tea.Cmd {
	return func() tea.Msg {
		<-gitChangeCh
		return gitChangeMsg{}
	}
}
