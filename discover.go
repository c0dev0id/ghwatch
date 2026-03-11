package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// discoverWorkflows scans .github/workflows/ for workflow files, then for each
// workflow queries GitHub Actions for its last successful run and lists the
// artifacts produced. Requires `gh` CLI to be authenticated.
func discoverWorkflows() {
	// Resolve repo slug from the git remote.
	remoteURL, err := runGit("remote", "get-url", "origin")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read remote URL: %v\n", err)
		return
	}
	slug := parseSlug(remoteURL)
	if slug == "" {
		fmt.Fprintln(os.Stderr, "cannot determine repo slug from remote URL")
		return
	}

	// GitHub token for direct API calls.
	token, err := runGH("auth", "token")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gh auth token: %v\n", err)
		return
	}
	token = strings.TrimSpace(token)

	// Collect workflow YAML files.
	ymlFiles, _ := filepath.Glob(filepath.Join(".github", "workflows", "*.yml"))
	yamlFiles, _ := filepath.Glob(filepath.Join(".github", "workflows", "*.yaml"))
	files := append(ymlFiles, yamlFiles...)

	if len(files) == 0 {
		fmt.Println("No workflow files found in .github/workflows/")
		return
	}

	fmt.Printf("Repo: %s\n\n", slug)
	for _, path := range files {
		name, _ := parseWorkflowFile(path)

		runID, runNum, err := lastSuccessfulRunID(name)
		if err != nil {
			fmt.Printf("  %-28s  (gh error: %v)\n", name, err)
			continue
		}
		if runID == 0 {
			fmt.Printf("  %-28s  (no successful runs)\n", name)
			continue
		}

		artifacts, err := listArtifacts(token, slug, runID, "")
		if err != nil {
			fmt.Printf("  %-28s  run #%d  (failed to list artifacts: %v)\n", name, runNum, err)
			continue
		}
		if len(artifacts) == 0 {
			fmt.Printf("  %-28s  run #%d  (no artifacts)\n", name, runNum)
			continue
		}

		fmt.Printf("  %-28s  run #%d\n", name, runNum)
		for _, a := range artifacts {
			fmt.Printf("    --artifact %-20s  %s\n", a.Name, discoverFmtSize(a.Size))
		}
	}
}

// lastSuccessfulRunID returns the databaseId and run number of the most recent
// successful run for the named workflow. Returns (0, 0, nil) when no
// successful run exists yet.
func lastSuccessfulRunID(workflowName string) (id, number int, err error) {
	out, err := runGH("run", "list",
		"--workflow", workflowName,
		"--status", "success",
		"--limit", "1",
		"--json", "databaseId,runNumber")
	if err != nil {
		return 0, 0, err
	}
	var runs []struct {
		DatabaseId int `json:"databaseId"`
		RunNumber  int `json:"runNumber"`
	}
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) == 0 {
		return 0, 0, nil
	}
	return runs[0].DatabaseId, runs[0].RunNumber, nil
}

// discoverFmtSize formats a byte count as a human-readable string.
func discoverFmtSize(b int64) string {
	switch {
	case b <= 0:
		return "?"
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	}
}

// parseWorkflowFile extracts the workflow display name from a YAML file.
// It also returns any actions/upload-artifact step names, though these are
// only used as a fallback when the GitHub API is unavailable.
func parseWorkflowFile(path string) (name string, artifacts []string) {
	// Default to the filename stem if no name: field is found.
	base := filepath.Base(path)
	name = strings.TrimSuffix(base, filepath.Ext(base))

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")

	// Top-level name: is not indented.
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") ||
			strings.HasPrefix(line, "#") {
			continue
		}
		after, ok := strings.CutPrefix(strings.TrimSpace(line), "name:")
		if !ok {
			continue
		}
		n := strings.TrimSpace(strings.Trim(after, `"'`))
		if n != "" {
			name = n
			break
		}
	}

	// Find upload-artifact steps and extract their with.name values.
	seen := make(map[string]bool)
	for i, line := range lines {
		if !strings.Contains(line, "actions/upload-artifact") {
			continue
		}
		usesIndent := lineIndent(line)
		inWith := false
		for j := i + 1; j < len(lines) && j < i+30; j++ {
			next := lines[j]
			trimmed := strings.TrimSpace(next)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if lineIndent(next) <= usesIndent {
				break // left the step block
			}
			if trimmed == "with:" {
				inWith = true
				continue
			}
			if inWith {
				after, ok := strings.CutPrefix(trimmed, "name:")
				if !ok {
					continue
				}
				n := strings.TrimSpace(strings.Trim(after, `"'`))
				if n != "" && !seen[n] {
					seen[n] = true
					artifacts = append(artifacts, n)
				}
				break
			}
		}
	}
	return
}

// lineIndent returns the number of leading spaces/tabs in a line.
func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}
