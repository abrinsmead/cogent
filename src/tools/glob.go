package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anthropics/agent/api"
)

type GlobTool struct{}

func (t *GlobTool) RequiresConfirmation() bool { return false }

func (t *GlobTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "glob",
		Description: "Find files matching a glob pattern.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"pattern": {
					Type:        "string",
					Description: "Glob pattern (e.g. \"**/*.go\")",
				},
				"path": {
					Type:        "string",
					Description: "Directory to search (default: cwd)",
				},
			},
			Required: []string{"pattern"},
		},
	}
}

func (t *GlobTool) Execute(input map[string]any) (string, error) {
	pattern, _ := input["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	dir, _ := input["path"].(string)
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if strings.Contains(pattern, "**") {
		return globRecursive(dir, pattern)
	}
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return "", err
	}
	// Stat each match to get mod time for sorting.
	var files []fileMatch
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, fileMatch{path: m, modTime: info.ModTime()})
	}
	return formatMatches(files), nil
}

// globRecursive handles a single "**/" in the pattern. Nested double-stars
// (e.g. "a/**/b/**/c") are not supported; only the first "**/" is expanded.
func globRecursive(root, pattern string) (string, error) {
	parts := strings.SplitN(pattern, "**/", 2)
	prefix := parts[0]
	suffix := ""
	if len(parts) > 1 {
		suffix = parts[1]
	}
	searchRoot := filepath.Join(root, prefix)
	var files []fileMatch
	filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldSkipDir(path, searchRoot) {
				return filepath.SkipDir
			}
			return nil
		}
		if suffix == "" {
			files = append(files, fileMatch{path: path, modTime: info.ModTime()})
			return nil
		}
		if matched, _ := filepath.Match(suffix, filepath.Base(path)); matched {
			files = append(files, fileMatch{path: path, modTime: info.ModTime()})
		}
		return nil
	})
	return formatMatches(files), nil
}

type fileMatch struct {
	path    string
	modTime time.Time
}

func formatMatches(files []fileMatch) string {
	if len(files) == 0 {
		return "No files found"
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})
	var b strings.Builder
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(f.path)
		if i >= 999 {
			fmt.Fprintf(&b, "\n... and %d more", len(files)-1000)
			break
		}
	}
	return b.String()
}
