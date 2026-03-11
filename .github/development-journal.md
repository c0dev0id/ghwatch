# Development Journal — ghwatch

## Software Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.22.2 |
| TUI framework | [Bubble Tea](https://github.com/charmbracelet/bubbletea) v0.25.0 |
| Terminal styling | [lipgloss](https://github.com/charmbracelet/lipgloss) v0.9.1 |
| Git watching | inotify (Linux) via `syscall` |
| GitHub Actions | `gh` CLI |
| ADB | `adb` CLI |

## Key Decisions

**Sibling of vibeDev, not a fork.** ghwatch shares the same stack and
lower-level helpers (adb.go, github.go, gitwatch*.go) with vibeDev but omits
all file-browser and interactive navigation UI. It is a separate module
(`ghwatch`) in its own repo.

**`--auto` decoupled from `--artifact`.** Whether to auto-act after a
successful workflow run is controlled by `--auto`. Whether to install a
specific artifact is controlled by `--artifact`. Combining them installs
directly; `--auto` alone opens the picker; neither flag means display-only.
The `autoInstall` field is set from `autoFlag`, not from `artifactName != ""`.

**`--discover` for self-documentation.** Running `ghwatch --discover` scans
`.github/workflows/` with a line-based YAML parser (no dependency) and prints
each workflow's display name and the artifact names it produces. Useful for
figuring out the right `--workflow` and `--artifact` values to pass.

**Single state machine (`appState`).** The app cycles through
`idle → pushing → monitoring → installing` and resets to `idle` on success.
On failure (`stateFailed`) the next git change or tick automatically retries
if there are unpushed commits. This keeps the loop completely hands-free.

**inotify + tick redundancy.** The Linux inotify watcher provides immediate
push detection (300 ms debounce after `.git/` changes). The periodic tick
(3 s) acts as a safety net on all platforms and also drives the workflow
polling loop while in `stateMonitoring`.

**adb.go reused verbatim from vibeDev.** The APK heuristic (score-based
selection of signed/release builds), binary AXML manifest parser, and install
flow are identical. When vibeDev updates these, ghwatch should import the same
changes.

**No migration code before v1.0.0.** No configuration persistence, database,
or on-disk state. Everything lives in the in-process model.

**Workflow polling is pull-based.** We call `gh run list --commit <sha>` on
every tick. We do not use GitHub webhooks or long-polling because the `gh` CLI
is already the required dependency and keeps the deployment story simple.

**`-package` flag accepts `pkg` or `pkg/.Activity`.** If omitted, the package
name is auto-detected from the binary AndroidManifest.xml inside the APK
(no `aapt`/`aapt2` dependency). Activity auto-detection is not possible
without external tools; launch falls back to `adb shell monkey` in that case.

## Core Features

- **Commit watcher**: detects unpushed commits via inotify + `git rev-list`
- **Auto push**: `git push` on every new local commit
- **Workflow monitor**: polls GitHub Actions until `completed`; shows job/step tree live
- **APK install**: downloads artifact with `gh run download`, selects signed APK, installs with `adb install -r`, launches app
- **Failure reporting**: on workflow failure, displays run ID and URL for copy-paste
- **Rolling activity log**: timestamped audit trail of every action inside the TUI
- **`--discover`**: scans `.github/workflows/` and prints workflow names + produced artifact names, then exits
- **`--auto`**: auto-installs (or opens artifact picker) after each successful run, equivalent to pressing `i` manually
