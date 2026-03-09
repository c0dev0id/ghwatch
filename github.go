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

// -- Workflow fetch -----------------------------------------------------------

// fetchWorkflow queries GitHub Actions for the workflow run associated with sha.
// It returns a workflowRunMsg. When no run is found yet, workflowRun.Error is
// set to "waiting for run" and the caller should retry on the next tick.
func fetchWorkflow(workflowName, sha string) tea.Cmd {
	return func() tea.Msg {
		wr := workflowRun{}

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
		wr.ID = r.DatabaseId
		wr.Status = r.Status
		wr.Conclusion = r.Conclusion
		wr.URL = r.URL

		// Fetch job / step details for display.
		jobOut, err := runGH("run", "view", fmt.Sprintf("%d", r.DatabaseId), "--json", "jobs")
		if err == nil {
			var jobData struct {
				Jobs []struct {
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
		}

		return workflowRunMsg(wr)
	}
}
