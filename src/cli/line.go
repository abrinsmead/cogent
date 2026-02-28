package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/anthropics/agent/agent"
)

// lineType identifies the semantic kind of a viewport line for persistence.
// Lines are stored as structured data and re-rendered on load so that
// styling does not leak into the session files.
type lineType string

const (
	lineEmpty          lineType = ""           // blank separator
	lineText           lineType = "text"       // agent text output (dim)
	lineTool           lineType = "tool"       // tool invocation: Data = "name\x00summary"
	linePrompt         lineType = "prompt"     // user prompt: Data = raw input text
	lineShellPrompt    lineType = "shell"      // terminal mode command: Data = command
	lineShellOutput    lineType = "shell_out"  // shell stdout/stderr line (dim, indented)
	lineShellError     lineType = "shell_err"  // shell non-zero exit (red, indented)
	lineInfo           lineType = "info"       // system info messages (dim)
	lineModeChange     lineType = "mode"       // mode switch: Data = mode name
	lineDiff           lineType = "diff"       // confirmation diff preview: Data = "name\x00json_input"
	lineConfirmPrompt  lineType = "confirm"    // "Allow X? [Y/n/a]": Data = "prefix\x00name\x00summary"
	lineConfirmAllow   lineType = "allow"      // "✓ allowed"
	lineConfirmDeny    lineType = "deny"       // "✗ denied"
	lineConfirmDenyInt lineType = "deny_int"   // "✗ denied (interrupted)"
	lineConfirmAlways  lineType = "always"     // "✓ always allow X": Data = tool name
	lineCompaction     lineType = "compact"    // "⚡ context compacted"
	lineError          lineType = "error"      // agent/shell error (yellow)
)

// line is a single entry in the session viewport. Stored as structured data
// for persistence; rendered to styled strings on display.
type line struct {
	Type lineType `json:"t,omitempty"`
	Data string   `json:"d,omitempty"`
}

// renderLine produces a styled string from a structured line.
func renderLine(l line) string {
	switch l.Type {
	case lineEmpty:
		return ""

	case lineText:
		return tuiStatusKey.Render("message") + " " + tuiDim.Render(l.Data)

	case lineTool:
		name, summary, _ := strings.Cut(l.Data, "\x00")
		style := tuiGreen
		switch name {
		case "bash", "write", "edit":
			style = tuiRed
		}
		return tuiDim.Render(" "+style.Render(name)) + " " + tuiDim.Render(summary)

	case linePrompt:
		return formatUserPrompt("❯ ", l.Data)

	case lineShellPrompt:
		return tuiYellow.Render("$ " + l.Data)

	case lineShellOutput:
		return tuiDim.Render("  " + l.Data)

	case lineShellError:
		return tuiRed.Render("  " + l.Data)

	case lineInfo:
		return tuiDim.Render(l.Data)

	case lineModeChange:
		mode := parseModeString(l.Data)
		var style lipgloss.Style
		switch mode {
		case agent.ModePlan:
			style = tuiModePlan
		case agent.ModeYOLO:
			style = tuiModeYOLO
		case agent.ModeTerminal:
			style = tuiModeTerminal
		default:
			style = tuiModeConfirm
		}
		return tuiDim.Render("  mode → ") + style.Render(l.Data)

	case lineDiff:
		name, jsonInput, _ := strings.Cut(l.Data, "\x00")
		input := parseDiffInput(jsonInput)
		return tuiRenderDiff(name, input)

	case lineConfirmPrompt:
		// Data = "prefix\x00name\x00summary"
		parts := strings.SplitN(l.Data, "\x00", 3)
		prefix, name, summary := "", "", ""
		if len(parts) >= 1 {
			prefix = parts[0]
		}
		if len(parts) >= 2 {
			name = parts[1]
		}
		if len(parts) >= 3 {
			summary = parts[2]
		}
		return tuiYellow.Render(fmt.Sprintf("%sAllow %s %s? [Y/n/a] ", prefix, name, summary))

	case lineConfirmAllow:
		return tuiGreen.Render("  ✓ allowed")

	case lineConfirmDeny:
		return tuiRed.Render("  ✗ denied")

	case lineConfirmDenyInt:
		return tuiRed.Render("  ✗ denied (interrupted)")

	case lineConfirmAlways:
		return tuiGreen.Render(fmt.Sprintf("  ✓ always allow %s", l.Data))

	case lineCompaction:
		return tuiDim.Render("  ⚡ context compacted")

	case lineError:
		return tuiYellow.Render("Error: " + l.Data)

	default:
		return l.Data
	}
}

// parseModeString converts a mode label back to a PermissionMode.
func parseModeString(s string) agent.PermissionMode {
	switch s {
	case "Plan":
		return agent.ModePlan
	case "YOLO":
		return agent.ModeYOLO
	case "Terminal":
		return agent.ModeTerminal
	default:
		return agent.ModeConfirm
	}
}

// parseDiffInput deserializes the JSON-encoded tool input for diff rendering.
func parseDiffInput(s string) map[string]any {
	if s == "" {
		return nil
	}
	m := make(map[string]any)
	_ = json.Unmarshal([]byte(s), &m)
	return m
}
