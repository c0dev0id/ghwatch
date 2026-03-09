package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// -- Low-level runner --------------------------------------------------------

func runGH(args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// -- Workflow detection ------------------------------------------------------

// checkWorkflows reports whether the current repo has any tracked workflow
// files under .github/workflows/.
func checkWorkflows() tea.Msg {
	out, _ := runGit("ls-files", ".github/workflows/")
	return workflowsCheckMsg(strings.TrimSpace(out) != "")
}

// -- Workflow fetch -----------------------------------------------------------

// fetchWorkflow queries GitHub Actions for the workflow run associated with sha.
//
// If knownRunID > 0 the initial "run list" lookup is skipped and only
// "run view" is called — saving one API request per poll once the run has
// been discovered.
//
// Returns a workflowRunMsg. When no run is found yet, workflowRun.Error is
// set to "waiting for run" and the caller should retry on the next poll.
func fetchWorkflow(workflowName, sha string, knownRunID int) tea.Cmd {
	return func() tea.Msg {
		wr := workflowRun{}

		runID := knownRunID
		if runID == 0 {
			// Discover which run belongs to this commit.
			out, err := runGH("run", "list",
				"--commit", sha,
				"--workflow", workflowName,
				"--json", "databaseId,status,conclusion,url",
				"--limit", "1")
			if err != nil {
				wr.Error = fmt.Sprintf("gh: %v", err)
				return workflowRunMsg(wr)
			}

			var runs []struct {
				DatabaseId int    `json:"databaseId"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				URL        string `json:"url"`
			}
			if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) == 0 {
				wr.Error = "waiting for run"
				return workflowRunMsg(wr)
			}

			r := runs[0]
			runID = r.DatabaseId
			wr.Status = r.Status
			wr.Conclusion = r.Conclusion
			wr.URL = r.URL
		}

		wr.ID = runID

		// Fetch job / step details plus authoritative status in a single call.
		jobOut, err := runGH("run", "view", fmt.Sprintf("%d", runID),
			"--json", "status,conclusion,url,jobs")
		if err != nil {
			wr.Error = fmt.Sprintf("gh run view: %v", err)
			return workflowRunMsg(wr)
		}

		var jobData struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			URL        string `json:"url"`
			Jobs       []struct {
				Name       string `json:"name"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
				URL        string `json:"url"`
				Steps      []struct {
					Name       string `json:"name"`
					Status     string `json:"status"`
					Conclusion string `json:"conclusion"`
				} `json:"steps"`
			} `json:"jobs"`
		}
		if json.Unmarshal([]byte(jobOut), &jobData) == nil {
			wr.Status = jobData.Status
			wr.Conclusion = jobData.Conclusion
			wr.URL = jobData.URL
			for _, j := range jobData.Jobs {
				job := workflowJob{
					Name:       j.Name,
					Status:     j.Status,
					Conclusion: j.Conclusion,
					URL:        j.URL,
				}
				for _, s := range j.Steps {
					job.Steps = append(job.Steps, workflowStep{
						Name:       s.Name,
						Status:     s.Status,
						Conclusion: s.Conclusion,
					})
				}
				wr.Jobs = append(wr.Jobs, job)
			}
		}

		return workflowRunMsg(wr)
	}
}
