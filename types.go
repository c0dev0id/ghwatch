package main

import "time"

// -- App state machine -------------------------------------------------------

type appState int

const (
	stateIdle              appState = iota // watching for new commits
	statePulling                           // git pull --rebase in progress
	statePushing                           // git push in progress
	stateMonitoring                        // watching GitHub Actions workflow
	stateInstalling                        // downloading + installing APK via adb
	statePushFailed                        // push failed — watching for HEAD change
	stateSelectingArtifact                 // showing artifact picker, waiting for user choice
)

func (s appState) String() string {
	switch s {
	case stateIdle:
		return "idle"
	case statePulling:
		return "pulling"
	case statePushing:
		return "pushing"
	case stateMonitoring:
		return "monitoring"
	case stateInstalling:
		return "installing"
	case statePushFailed:
		return "push failed"
	case stateSelectingArtifact:
		return "selecting artifact"
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
	Behind  int
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

type repoStateMsg repoState
type workflowRunMsg workflowRun
type tickMsg time.Time
type gitPushMsg struct{ err error }
type gitPullRebaseMsg struct{ err error }
type gitChangeMsg struct{}
type workflowsCheckMsg bool // true = repo has .github/workflows/ files
type pushReadyMsg struct{}

// artifactListMsg carries the result of a GitHub artifact list fetch.
type artifactListMsg struct {
	Artifacts []artifactInfo
	Err       error
}

// installProgressMsg is sent repeatedly while an install is in progress.
// When Done is true the install has finished (check Err for failure).
type installProgressMsg struct {
	Downloaded int64    // bytes downloaded so far
	Total      int64    // total bytes to download (0 = unknown)
	LogLine    string   // new log line to append (empty = progress-only update)
	Done       bool     // true when the entire install pipeline has finished
	Err        error    // non-nil on failure (only valid when Done == true)
	FinalLog   []string // full log (only populated when Done == true)
}
