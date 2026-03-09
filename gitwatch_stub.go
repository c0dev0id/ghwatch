//go:build !linux

package main

// startGitWatcher is a no-op on non-Linux platforms.
// The periodic tick drives repo-state reloads instead.
func startGitWatcher() {}
