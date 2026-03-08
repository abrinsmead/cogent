// Package cli defines the CLI interface and shared helpers used by all UI modes.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ─── Interface ───────────────────────────────────────────────────────────────

// CLI is the interface every UI mode implements.
type CLI interface {
	Run() error
}

// ─── ANSI codes ──────────────────────────────────────────────────────────────

const (
	Reset  = "\033[0m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Cyan   = "\033[36m"
)

// ─── Shared helpers ──────────────────────────────────────────────────────────

// SummarizeConfirm returns a short description for a confirmation prompt.
func SummarizeConfirm(name string, input map[string]any) string {
	str := func(key string) string { s, _ := input[key].(string); return s }
	summary := str("file_path")
	if summary == "" {
		summary = str("command")
		if len(summary) > 80 {
			summary = summary[:80] + "..."
		}
	}
	if summary == "" {
		summary = str("task")
		if len(summary) > 80 {
			summary = summary[:80] + "..."
		}
	}
	return summary
}

// RenderDiff returns a coloured diff preview as a string.
func RenderDiff(name string, input map[string]any) string {
	str := func(key string) string { s, _ := input[key].(string); return s }
	var lines []string

	switch name {
	case "edit":
		for _, line := range strings.Split(str("old_string"), "\n") {
			lines = append(lines, fmt.Sprintf("  %s- %s%s", Red, line, Reset))
		}
		for _, line := range strings.Split(str("new_string"), "\n") {
			lines = append(lines, fmt.Sprintf("  %s+ %s%s", Green, line, Reset))
		}
	case "write":
		path := str("file_path")
		content := str("content")
		if _, err := os.Stat(path); err != nil {
			// New file — show all lines as additions.
			for _, line := range strings.Split(content, "\n") {
				lines = append(lines, fmt.Sprintf("  %s+ %s%s", Green, line, Reset))
			}
		} else {
			// Existing file — unified diff.
			cmd := exec.Command("diff", "-u", path, "-")
			cmd.Stdin = strings.NewReader(content)
			out, _ := cmd.CombinedOutput()
			for _, line := range strings.Split(string(out), "\n") {
				switch {
				case strings.HasPrefix(line, "-"):
					lines = append(lines, fmt.Sprintf("  %s%s%s", Red, line, Reset))
				case strings.HasPrefix(line, "+"):
					lines = append(lines, fmt.Sprintf("  %s%s%s", Green, line, Reset))
				case strings.HasPrefix(line, "@@"):
					lines = append(lines, fmt.Sprintf("  %s%s%s", Cyan, line, Reset))
				default:
					lines = append(lines, fmt.Sprintf("  %s%s%s", Dim, line, Reset))
				}
			}
		}
	case "bash":
		lines = append(lines, fmt.Sprintf("  %s$ %s%s", Dim, str("command"), Reset))
	}

	return strings.Join(lines, "\n")
}
