package cli

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// overlayModal composites a modal box centered both horizontally and vertically
// onto a base string (the full rendered TUI view).
func overlayModal(base, modal string, totalWidth, totalHeight int) string {
	baseLines := strings.Split(base, "\n")
	modalLines := strings.Split(modal, "\n")

	// Pad base to totalHeight if needed.
	for len(baseLines) < totalHeight {
		baseLines = append(baseLines, "")
	}

	vertOffset := (totalHeight - len(modalLines)) / 2
	if vertOffset < 0 {
		vertOffset = 0
	}

	for i, modalLine := range modalLines {
		row := vertOffset + i
		if row >= len(baseLines) {
			break
		}

		overlayW := lipgloss.Width(modalLine)
		startCol := (totalWidth - overlayW) / 2
		if startCol < 0 {
			startCol = 0
		}

		baseLine := baseLines[row]
		baseW := lipgloss.Width(baseLine)
		if baseW < totalWidth {
			baseLine += strings.Repeat(" ", totalWidth-baseW)
		}

		left := ansi.Truncate(baseLine, startCol, "")
		leftW := lipgloss.Width(left)
		if leftW < startCol {
			left += strings.Repeat(" ", startCol-leftW)
		}

		rightStart := startCol + overlayW
		rightPad := ""
		if rightStart < totalWidth {
			rightPad = strings.Repeat(" ", totalWidth-rightStart)
		}

		baseLines[row] = left + modalLine + rightPad
	}

	return strings.Join(baseLines, "\n")
}

// hasModal returns true if the session has any modal open.
func hasModal(s *session) bool {
	return s.state == tuiStateLinear && s.linear != nil
}

// dimView re-renders every line in dim grey (color 8), stripping existing ANSI
// styles so the background appears uniformly muted behind a modal overlay.
func dimView(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = tuiDim.Render(ansi.Strip(line))
	}
	return strings.Join(lines, "\n")
}
