# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial project scaffold: Go module, Bubble Tea TUI skeleton
- `types.go`: app state machine (`idle → pushing → monitoring → installing → failed`), repo state, GitHub Actions types, and all Bubble Tea message types
- `model.go`: core `model` struct with rolling activity log, `Init()` starting git watcher + repo state fetch
- `styles.go`: lipgloss styles and braille spinner frames shared across the UI
- `git.go`: `runGit` helper, `fetchRepoState` (branch / remote / ahead count), `gitPush`, periodic `tick`
- `github.go`: `runGH` helper, `fetchWorkflow` — polls `gh run list` + `gh run view` for jobs/steps
- `adb.go`: artifact download (`gh run download`), signed APK selection heuristic, binary AXML manifest parser, `adb install -r`, app launch via monkey / `am start`
- `gitwatch.go` / `gitwatch_linux.go` / `gitwatch_stub.go`: inotify-based `.git/` watcher with 300 ms debounce; no-op stub for non-Linux
- `update.go`: full auto-mode event loop — detects unpushed commits, pushes, monitors workflow, installs APK, shows failure details; resets to idle after install so next commit is picked up automatically
- `view.go`: single-pane Bubble Tea TUI — title bar with repo info, state indicator with spinner, workflow job/step tree, adb install log, rolling activity log, footer hint
- `main.go`: CLI entry point with `-workflow`, `-package`, `-artifact` flags (same interface as vibeDev)
- Makefile with `build`, `install` (to `~/.bin/`), and `clean` targets
- Repo slug (`owner/repo`) shown in TUI title bar and terminal window title
- Push-only mode: if the repo has no `.github/workflows/` files, ghwatch pushes and returns to idle without monitoring
- Real download progress bar using direct GitHub API artifact download (streams bytes via `net/http`), rendered as `[████░░░░] 61%  12.3 MB / 20.1 MB`
- Height-aware TUI layout: title bar, state line, and footer are always pinned; workflow, install, and activity sections share the remaining terminal height so the header never scrolls off-screen
- Minimal workflow job output: each job shows its status icon and name with the currently running step appended inline; completed steps are not listed
- `--discover` flag: scans `.github/workflows/` and prints each workflow's display name together with the artifact names produced by `actions/upload-artifact` steps, then exits
- `--auto` flag: after each successful workflow run behave as if the user pressed `i` — installs directly when `--artifact` is also specified, or opens the artifact picker when it is not
