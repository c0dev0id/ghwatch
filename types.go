package main

import "time"

// -- App state machine -------------------------------------------------------

type appState int

const (
	stateIdle       appState = iota // watching for new commits
	statePushing                    // git push in progress
	stateMonitoring                 // watching GitHub Actions workflow
	stateInstalling                 // downloading + installing APK via adb
	stateFailed                     // workflow or install failure
)

func (s appState) String() string {
	switch s {
	case stateIdle:
		return "idle"
	case statePushing:
		return "pushing"
	case stateMonitoring:
		return "monitoring"
	case stateInstalling:
		return "installing"
	case stateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// -- Repository state --------------------------------------------------------

type repoState struct {
	Branch  string
	Remote  string
	Slug    string // "owner/repo" parsed from remote URL
	HeadSHA string
	Ahead   int
	Error   string
}

// -- GitHub Actions types ----------------------------------------------------

type workflowJob struct {
	Name       string
	Status     string
	Conclusion string
	URL        string
	Steps      []workflowStep
}

type workflowStep struct {
	Name       string
	Status     string
	Conclusion string
}

type workflowRun struct {
	ID         int
	Status     string
	Conclusion string
	URL        string
	Jobs       []workflowJob
	Error      string
}

// -- Message types -----------------------------------------------------------

type repoStateMsg      repoState
type workflowRunMsg    workflowRun
type tickMsg           time.Time
type gitPushMsg        struct{ err error }
type gitChangeMsg      struct{}
type workflowsCheckMsg bool // true = repo has .github/workflows/ files
type adbInstallMsg     struct {
	sha string
	err error
	log []string
}
