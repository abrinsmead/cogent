package tools

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropics/agent/api"
)

type GrepTool struct{}

func (t *GrepTool) RequiresConfirmation() bool { return false }

func (t *GrepTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "grep",
		Description: "Search file contents with a regex pattern.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"pattern": {
					Type:        "string",
					Description: "Regex pattern to search for",
				},
				"path": {
					Type:        "string",
					Description: "File or directory to search (default: cwd)",
				},
				"glob": {
					Type:        "string",
					Description: "Filter files by glob (e.g. \"*.go\")",
				},
			},
			Required: []string{"pattern"},
		},
	}
}

func (t *GrepTool) Execute(input map[string]any) (string, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	searchPath, _ := input["path"].(string)
	if searchPath == "" {
		searchPath, _ = os.Getwd()
	}
	globFilter, _ := input["glob"].(string)
	info, err := os.Stat(searchPath)
	if err != nil {
		return "", err
	}
	var results strings.Builder
	matchCount := 0
	maxMatches := 500
	if !info.IsDir() {
		matchCount = searchFile(searchPath, re, &results, maxMatches)
	} else {
		filepath.Walk(searchPath, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if fi.IsDir() {
				if shouldSkipDir(path, searchPath) {
					return filepath.SkipDir
				}
				return nil
			}
			if matchCount >= maxMatches {
				return filepath.SkipAll
			}
			if globFilter != "" {
				if matched, _ := filepath.Match(globFilter, filepath.Base(path)); !matched {
					return nil
				}
			}
			matchCount += searchFile(path, re, &results, maxMatches-matchCount)
			return nil
		})
	}
	if matchCount == 0 {
		return "No matches found", nil
	}
	output := results.String()
	if matchCount >= maxMatches {
		output += fmt.Sprintf("\n... truncated at %d matches", maxMatches)
	}
	return output, nil
}

func searchFile(path string, re *regexp.Regexp, b *strings.Builder, limit int) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return 0
	}
	lines := strings.Split(string(data), "\n")
	count := 0
	for i, line := range lines {
		if count >= limit {
			break
		}
		if re.MatchString(line) {
			fmt.Fprintf(b, "%s:%d: %s\n", path, i+1, line)
			count++
		}
	}
	return count
}
