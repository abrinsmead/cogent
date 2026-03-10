package cli

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/tools"
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
	lineModelChange    lineType = "model"      // model switch: Data = model spec string
	lineDiff           lineType = "diff"       // confirmation diff preview: Data = "name\x00json_input"
	lineConfirmPrompt  lineType = "confirm"    // "Allow X? [Y/n/a]": Data = "prefix\x00name\x00summary"
	lineConfirmAllow   lineType = "allow"      // "✓ allowed"
	lineConfirmDeny    lineType = "deny"       // "✗ denied"
	lineConfirmDenyInt lineType = "deny_int"   // "✗ denied (interrupted)"
	lineConfirmAlways  lineType = "always"     // "✓ always allow X": Data = tool name
	lineCompaction     lineType = "compact"    // "⚡ context compacted"
	lineToolsLoaded    lineType = "tools"      // active custom tools: Data = "name1, name2, ..." (prefix \x01 = confirm)
	lineSessionStart   lineType = "start"     // session start/resume: Data = "new\x00timestamp" or "resumed\x00timestamp"
	linePlanConfirm    lineType = "planconf"   // "Switch to Confirm mode and execute? [Y/n]"
	lineError          lineType = "error"      // agent/shell error (yellow)
	lineChoice         lineType = "choice"     // clarifying question: Data = "question\x00opt1\x00opt2\x00..."
	lineChoiceSelected lineType = "chosen"     // selected choice: Data = chosen label
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
		return renderMarkdown(l.Data)

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

	case lineModelChange:
		return tuiDim.Render("  model → ") + mdStyleText.Render(l.Data)

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

	case lineToolsLoaded:
		names := strings.Split(l.Data, ", ")
		var styled []string
		for _, name := range names {
			if strings.HasPrefix(name, "\x01") {
				styled = append(styled, tuiRed.Render(name[1:]))
			} else {
				styled = append(styled, tuiGreen.Render(name))
			}
		}
		return tuiDim.Render("  custom tools: ") + strings.Join(styled, tuiDim.Render(", "))

	case lineSessionStart:
		kind, ts, _ := strings.Cut(l.Data, "\x00")
		label := "session started"
		if kind == "resumed" {
			label = "session resumed"
		}
		return tuiDim.Render("  " + label + " " + ts)

	case linePlanConfirm:
		return tuiYellow.Render("Switch to Confirm mode and execute? [Y/n] ")

	case lineError:
		return tuiYellow.Render("Error: " + l.Data)

	case lineChoice:
		// Data = "question\x00opt1\x00opt2\x00..."
		parts := strings.Split(l.Data, "\x00")
		if len(parts) == 0 {
			return ""
		}
		question := parts[0]
		choices := parts[1:]
		var b strings.Builder
		b.WriteString(tuiYellow.Render(question))
		for i, c := range choices {
			b.WriteString("\n")
			prefix := fmt.Sprintf("  %d. ", i+1)
			b.WriteString(tuiDim.Render(prefix) + mdStyleText.Render(c))
		}
		return b.String()

	case lineChoiceSelected:
		return tuiGreen.Render("  ✓ " + l.Data)

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

// renderMarkdown applies lightweight markdown styling using lipgloss.
// Handles headings, bold, italic, inline code, fenced code blocks, lists, and GFM tables.
// No external dependencies — just regex + lipgloss.
var (
	mdBold       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	mdItalic     = regexp.MustCompile(`(?:^|[^*])\*([^*]+?)\*(?:[^*]|$)`)
	mdInlineCode = regexp.MustCompile("`([^`]+)`")
	mdHeading    = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	mdBullet     = regexp.MustCompile(`^(\s*)([-*+])\s+(.+)$`)
	mdNumbered   = regexp.MustCompile(`^(\s*)(\d+\.)\s+(.+)$`)
	mdTableRow   = regexp.MustCompile(`^\s*\|.*\|`)
	mdTableSep   = regexp.MustCompile(`^\s*\|[ :-]+\|`)

	mdStyleBold       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	mdStyleItalic     = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("5"))
	mdStyleInlineCode = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Background(lipgloss.Color("236"))
	mdStyleHeading    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	mdStyleCodeBlock  = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Background(lipgloss.Color("236"))
	mdStyleBullet     = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	mdStyleText       = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	mdStyleTableBorder = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	mdStyleTableHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
)

func renderMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	inCode := false
	var tableBuf []string // accumulates consecutive table lines

	flushTable := func() {
		if len(tableBuf) > 0 {
			out = append(out, renderTable(tableBuf)...)
			tableBuf = nil
		}
	}

	for _, l := range lines {
		// Fenced code blocks
		if strings.HasPrefix(l, "```") {
			flushTable()
			inCode = !inCode
			if inCode {
				out = append(out, "")
			} else {
				out = append(out, "")
			}
			continue
		}
		if inCode {
			out = append(out, "  "+mdStyleCodeBlock.Render(l))
			continue
		}

		// GFM table lines — accumulate and render as a batch
		if mdTableRow.MatchString(l) {
			tableBuf = append(tableBuf, l)
			continue
		}
		flushTable()

		// Headings
		if m := mdHeading.FindStringSubmatch(l); m != nil {
			out = append(out, mdStyleHeading.Render(m[2]))
			continue
		}

		// Bullet lists
		if m := mdBullet.FindStringSubmatch(l); m != nil {
			body := applyInlineStyles(m[3])
			out = append(out, m[1]+mdStyleBullet.Render("• ")+body)
			continue
		}

		// Numbered lists
		if m := mdNumbered.FindStringSubmatch(l); m != nil {
			body := applyInlineStyles(m[3])
			out = append(out, m[1]+mdStyleBullet.Render(m[2]+" ")+body)
			continue
		}

		out = append(out, applyInlineStyles(l))
	}
	flushTable()

	return strings.Join(out, "\n")
}

// parseTableCells splits a GFM table row into trimmed cell values.
// "| a | b | c |" → ["a", "b", "c"]
func parseTableCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	parts := strings.Split(row, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

// renderTable converts accumulated GFM table lines into box-drawn output lines.
// Expects at least a header row; the separator row (|---|---|) is detected and
// skipped. Alignment markers (:---:, ---:, :---) are parsed from the separator.
func renderTable(rows []string) []string {
	if len(rows) == 0 {
		return nil
	}

	// Parse all rows into cells, identifying the separator row.
	type parsedRow struct {
		cells    []string
		isSep    bool
		isHeader bool
	}

	var parsed []parsedRow
	sepIdx := -1
	for i, r := range rows {
		cells := parseTableCells(r)
		isSep := mdTableSep.MatchString(r) && allSeparatorCells(cells)
		pr := parsedRow{cells: cells, isSep: isSep}
		if isSep && sepIdx == -1 {
			sepIdx = i
		}
		parsed = append(parsed, pr)
	}

	// Mark header rows (everything before the separator).
	if sepIdx > 0 {
		for i := 0; i < sepIdx; i++ {
			parsed[i].isHeader = true
		}
	} else if sepIdx == -1 {
		// No separator found — treat the first row as a header.
		parsed[0].isHeader = true
	}

	// Parse alignment from separator row.
	type alignment int
	const (
		alignLeft alignment = iota
		alignCenter
		alignRight
	)
	var aligns []alignment
	if sepIdx >= 0 && sepIdx < len(parsed) {
		for _, cell := range parsed[sepIdx].cells {
			cell = strings.TrimSpace(cell)
			left := strings.HasPrefix(cell, ":")
			right := strings.HasSuffix(cell, ":")
			switch {
			case left && right:
				aligns = append(aligns, alignCenter)
			case right:
				aligns = append(aligns, alignRight)
			default:
				aligns = append(aligns, alignLeft)
			}
		}
	}

	// Determine the number of columns and max width per column.
	numCols := 0
	for _, pr := range parsed {
		if pr.isSep {
			continue
		}
		if len(pr.cells) > numCols {
			numCols = len(pr.cells)
		}
	}
	if numCols == 0 {
		return nil
	}

	colWidths := make([]int, numCols)
	for _, pr := range parsed {
		if pr.isSep {
			continue
		}
		for j := 0; j < numCols && j < len(pr.cells); j++ {
			w := lipgloss.Width(applyInlineStyles(pr.cells[j]))
			if w > colWidths[j] {
				colWidths[j] = w
			}
		}
	}

	// Ensure minimum column width of 3 for aesthetics.
	for j := range colWidths {
		if colWidths[j] < 3 {
			colWidths[j] = 3
		}
	}

	// Helper to get alignment for column j.
	getAlign := func(j int) alignment {
		if j < len(aligns) {
			return aligns[j]
		}
		return alignLeft
	}

	// Build box-drawing borders.
	border := mdStyleTableBorder
	buildHLine := func(left, mid, right, fill string) string {
		var b strings.Builder
		b.WriteString(left)
		for j, w := range colWidths {
			b.WriteString(strings.Repeat(fill, w+2)) // 1 pad each side
			if j < numCols-1 {
				b.WriteString(mid)
			}
		}
		b.WriteString(right)
		return border.Render(b.String())
	}

	topBorder := buildHLine("╭", "┬", "╮", "─")
	midBorder := buildHLine("├", "┼", "┤", "─")
	botBorder := buildHLine("╰", "┴", "╯", "─")

	// Render data rows.
	renderDataRow := func(pr parsedRow) string {
		var b strings.Builder
		b.WriteString(border.Render("│"))
		for j := 0; j < numCols; j++ {
			cell := ""
			if j < len(pr.cells) {
				cell = pr.cells[j]
			}
			var styled string
			if pr.isHeader {
				styled = mdStyleTableHeader.Render(cell)
			} else {
				styled = applyInlineStyles(cell)
			}
			cellW := lipgloss.Width(styled)
			pad := colWidths[j] - cellW
			if pad < 0 {
				pad = 0
			}
			switch getAlign(j) {
			case alignRight:
				b.WriteString(" " + strings.Repeat(" ", pad) + styled + " ")
			case alignCenter:
				lpad := pad / 2
				rpad := pad - lpad
				b.WriteString(" " + strings.Repeat(" ", lpad) + styled + strings.Repeat(" ", rpad) + " ")
			default: // alignLeft
				b.WriteString(" " + styled + strings.Repeat(" ", pad) + " ")
			}
			b.WriteString(border.Render("│"))
		}
		return b.String()
	}

	var out []string
	out = append(out, noWrapMarker+topBorder)
	needMid := false
	for _, pr := range parsed {
		if pr.isSep {
			needMid = true
			continue
		}
		if needMid {
			out = append(out, noWrapMarker+midBorder)
			needMid = false
		}
		out = append(out, noWrapMarker+renderDataRow(pr))
	}
	out = append(out, noWrapMarker+botBorder)

	return out
}

// noWrapMarker is prefixed to rendered lines that must not be soft-wrapped.
// refreshContent strips it and truncates instead of wrapping.
const noWrapMarker = "\x01"

// allSeparatorCells returns true if every cell looks like a separator (e.g. "---", ":--:", "---:").
func allSeparatorCells(cells []string) bool {
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		stripped := strings.Trim(c, ":- ")
		if stripped != "" {
			return false
		}
	}
	return true
}

// applyInlineStyles renders bold, italic, and inline code within a line.
// Inline code and bold get their own colors; all other text is purple.
func applyInlineStyles(s string) string {
	// Inline code first (so bold/italic don't match inside code spans)
	s = mdInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		inner := mdInlineCode.FindStringSubmatch(m)[1]
		return "\x00" + mdStyleInlineCode.Render(" "+inner+" ") + "\x00"
	})
	// Bold
	s = mdBold.ReplaceAllStringFunc(s, func(m string) string {
		inner := mdBold.FindStringSubmatch(m)[1]
		return "\x00" + mdStyleBold.Render(inner) + "\x00"
	})
	// Italic (single *)
	s = mdItalic.ReplaceAllStringFunc(s, func(m string) string {
		sub := mdItalic.FindStringSubmatch(m)
		prefix := ""
		suffix := ""
		if len(m) > 0 && m[0] != '*' {
			prefix = string(m[0])
		}
		if len(m) > 0 && m[len(m)-1] != '*' {
			suffix = string(m[len(m)-1])
		}
		return prefix + "\x00" + mdStyleItalic.Render(sub[1]) + "\x00" + suffix
	})

	// Split on \x00 markers and wrap plain segments in purple
	parts := strings.Split(s, "\x00")
	for i, p := range parts {
		// Even-indexed parts are plain text; odd-indexed are pre-styled spans
		if i%2 == 0 && p != "" {
			parts[i] = mdStyleText.Render(p)
		}
	}
	return strings.Join(parts, "")
}

// formatToolEntries encodes custom tool entries for lineToolsLoaded Data.
// Tools requiring confirmation are prefixed with \x01.
func formatToolEntries(entries []tools.CustomToolEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		if e.Confirm {
			parts[i] = "\x01" + e.Name
		} else {
			parts[i] = e.Name
		}
	}
	return strings.Join(parts, ", ")
}
