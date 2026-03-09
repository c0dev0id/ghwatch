//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// startGitWatcher launches the background inotify goroutine that watches
// .git/ for any change and signals gitChangeCh with a 300 ms debounce.
func startGitWatcher() {
	go runInotifyWatcher()
}

func runInotifyWatcher() {
	fd, err := syscall.InotifyInit()
	if err != nil {
		return
	}
	defer syscall.Close(fd)

	const mask uint32 = syscall.IN_CREATE |
		syscall.IN_CLOSE_WRITE |
		syscall.IN_MOVED_TO |
		syscall.IN_DELETE

	for _, dir := range collectWatchDirs() {
		syscall.InotifyAddWatch(fd, dir, mask) //nolint:errcheck
	}

	buf := make([]byte, 4096)
	var timer *time.Timer

	for {
		n, err := syscall.Read(fd, buf)
		if err != nil || n <= 0 {
			return
		}
		// Debounce rapid bursts (e.g. a commit touches HEAD + refs in quick succession).
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(300*time.Millisecond, func() {
			select {
			case gitChangeCh <- struct{}{}:
			default: // already pending, skip
			}
		})
	}
}

// collectWatchDirs returns .git and every directory under .git/refs/.
// inotify watches are not recursive, so we enumerate subdirs explicitly.
func collectWatchDirs() []string {
	dirs := []string{".git"}
	_ = filepath.Walk(".git/refs", func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	return dirs
}
