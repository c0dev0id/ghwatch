package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// discoverWorkflows scans .github/workflows/ for YAML files, extracts each
// workflow's display name and the artifact names produced by
// actions/upload-artifact steps, then prints a summary to stdout.
func discoverWorkflows() {
	ymlFiles, _ := filepath.Glob(filepath.Join(".github", "workflows", "*.yml"))
	yamlFiles, _ := filepath.Glob(filepath.Join(".github", "workflows", "*.yaml"))
	files := append(ymlFiles, yamlFiles...)

	if len(files) == 0 {
		fmt.Println("No workflow files found in .github/workflows/")
		return
	}

	fmt.Println("Workflows:")
	for _, path := range files {
		name, artifacts := parseWorkflowFile(path)
		artifactsStr := "(no artifacts)"
		if len(artifacts) > 0 {
			artifactsStr = strings.Join(artifacts, ", ")
		}
		fmt.Printf("  %-28s  %s\n", name, artifactsStr)
	}
}

// parseWorkflowFile extracts the workflow display name and the artifact names
// uploaded by actions/upload-artifact steps. It uses simple line-based
// parsing instead of a full YAML parser so no extra dependency is needed.
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
