package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// promptKind identifies the type of prompt shown to the user.
type promptKind int

const (
	// promptConfirm is the standard tool confirmation (y/n/a).
	promptConfirm promptKind = iota
	// promptPlanConfirm asks the user to switch to Confirm mode and execute.
	promptPlanConfirm
	// promptChoice presents a numbered list of options.
	// The last choice is always a freeform "Other" option.
	promptChoice
)

// promptModel represents an active user feedback prompt. It is shown as a
// floating box above the input line. The three kinds cover:
//   - Tool confirmations in Confirm mode (y/n/a)
//   - Plan-ready confirmation (y/n)
//   - Multiple-choice clarifying questions (last option = freeform)
type promptModel struct {
	kind    promptKind
	message string // human-readable question / context

	// For promptConfirm — carried from tuiConfirmMsg
	confirmMsg *tuiConfirmMsg

	// For promptChoice
	choices  []string // option labels (1-indexed for display)
	selected int      // 0-based index of the currently highlighted choice
	freeform bool     // true when the user is typing a custom response (last choice selected)
}

// ── Constructors ────────────────────────────────────────────────────────────

// newConfirmPrompt creates a tool confirmation prompt (y/n/a).
// The diff is not included in the floating box — it's already shown as a
// viewport line (lineDiff) above the confirmation, so duplicating it here
// would make the box unnecessarily large.
func newConfirmPrompt(msg *tuiConfirmMsg, displayText string) promptModel {
	return promptModel{
		kind:       promptConfirm,
		message:    displayText,
		confirmMsg: msg,
	}
}

// newPlanConfirmPrompt creates a plan-ready confirmation prompt (y/n).
func newPlanConfirmPrompt() promptModel {
	return promptModel{
		kind:    promptPlanConfirm,
		message: "Switch to Confirm mode and execute?",
	}
}

// newChoicePrompt creates a multiple-choice prompt.
// The last choice is treated as the freeform "Other" option.
func newChoicePrompt(question string, choices []string) promptModel {
	return promptModel{
		kind:     promptChoice,
		message:  question,
		choices:  choices,
		selected: 0,
	}
}

// ── Navigation (for choice prompts) ─────────────────────────────────────────

func (p *promptModel) up() {
	if p.kind != promptChoice || len(p.choices) == 0 || p.freeform {
		return
	}
	if p.selected > 0 {
		p.selected--
	}
}

func (p *promptModel) down() {
	if p.kind != promptChoice || len(p.choices) == 0 || p.freeform {
		return
	}
	if p.selected < len(p.choices)-1 {
		p.selected++
	}
}

// selectByNumber selects a choice by its 1-based number. Returns true if valid.
func (p *promptModel) selectByNumber(n int) bool {
	if p.kind != promptChoice || p.freeform {
		return false
	}
	if n >= 1 && n <= len(p.choices) {
		p.selected = n - 1
		return true
	}
	return false
}

// selectedChoice returns the 0-based index of the current selection.
func (p *promptModel) selectedChoice() int {
	return p.selected
}

// isOtherSelected returns true if the last (freeform) choice is highlighted.
func (p *promptModel) isOtherSelected() bool {
	return p.kind == promptChoice && len(p.choices) > 0 && p.selected == len(p.choices)-1
}

// ── Rendering ───────────────────────────────────────────────────────────────

// renderPromptLine produces the viewport line for this prompt.
func (p *promptModel) renderPromptLine() string {
	switch p.kind {
	case promptConfirm:
		return tuiYellow.Render(p.message)
	case promptPlanConfirm:
		return tuiYellow.Render(p.message + " [Y/n] ")
	case promptChoice:
		return p.renderChoices()
	default:
		return p.message
	}
}

// renderChoices builds the styled list of choices with a highlighted selection.
func (p *promptModel) renderChoices() string {
	var b strings.Builder
	b.WriteString(tuiYellow.Render(p.message))
	b.WriteString("\n")

	choiceActive := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("4"))

	choiceInactive := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	choiceDim := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	for i, c := range p.choices {
		prefix := fmt.Sprintf("  %d. ", i+1)
		if i == p.selected {
			b.WriteString(choiceDim.Render(prefix) + choiceActive.Render(" "+c+" "))
		} else {
			b.WriteString(choiceDim.Render(prefix) + choiceInactive.Render(c))
		}
		if i < len(p.choices)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// hintText returns the short key hint shown in the prompt bar.
func (p *promptModel) hintText() string {
	switch p.kind {
	case promptConfirm:
		return " y/n/a "
	case promptPlanConfirm:
		return " y/n "
	case promptChoice:
		if p.freeform {
			return " type your answer, enter to submit "
		}
		return fmt.Sprintf(" ↑/↓ enter  (1-%d) ", len(p.choices))
	default:
		return ""
	}
}

// ── Floating confirmation box ─────────────────────────────────────────────

var (
	promptBoxBorder = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3"))
)

// renderFloatingBox builds a rounded-corner yellow-bordered box with the
// prompt content inside. maxWidth is the maximum outer width of the box.
func (p *promptModel) renderFloatingBox(maxWidth int) string {
	if maxWidth < 10 {
		maxWidth = 10
	}

	// Build inner content lines.
	innerWidth := maxWidth - 4 // 2 border chars + 2 padding spaces
	if innerWidth < 6 {
		innerWidth = 6
	}

	var contentLines []string

	switch p.kind {
	case promptConfirm:
		contentLines = p.confirmBoxContent(innerWidth)
	case promptPlanConfirm:
		contentLines = []string{tuiYellow.Render(p.message)}
	case promptChoice:
		contentLines = p.choiceBoxContent(innerWidth)
	}

	// Add key hint line at the bottom.
	hint := p.hintText()
	if hint != "" {
		contentLines = append(contentLines, tuiDim.Render(hint))
	}

	// Use full inner width so the box matches the input bar width.
	actualWidth := innerWidth

	// Build the box.
	border := promptBoxBorder
	top := border.Render("╭" + strings.Repeat("─", actualWidth+2) + "╮")
	bot := border.Render("╰" + strings.Repeat("─", actualWidth+2) + "╯")
	left := border.Render("│") + " "
	right := " " + border.Render("│")

	var b strings.Builder
	b.WriteString(top)
	b.WriteString("\n")
	for _, cl := range contentLines {
		clW := lipgloss.Width(cl)
		padded := cl
		if clW < actualWidth {
			padded += strings.Repeat(" ", actualWidth-clW)
		} else if clW > actualWidth {
			padded = ansi.Truncate(cl, actualWidth, "…")
		}
		b.WriteString(left + padded + right + "\n")
	}
	b.WriteString(bot)
	return b.String()
}

// confirmBoxContent builds the inner lines for a tool confirmation box.
func (p *promptModel) confirmBoxContent(maxWidth int) []string {
	if p.confirmMsg == nil {
		return []string{tuiYellow.Render(p.message)}
	}

	cm := p.confirmMsg

	// Question line only — the diff is already shown as a viewport line above.
	summary := SummarizeConfirm(cm.name, cm.input)
	prefix := ""
	if cm.subAgent {
		prefix = "(sub-agent) "
	}
	question := fmt.Sprintf("%sAllow %s %s?", prefix, cm.name, summary)
	return []string{tuiYellow.Render(question)}
}

// choiceBoxContent builds the inner lines for a clarifying question box.
func (p *promptModel) choiceBoxContent(maxWidth int) []string {
	var lines []string
	lines = append(lines, tuiYellow.Render(p.message))

	choiceActive := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("15")).
		Background(lipgloss.Color("4"))

	choiceInactive := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	choiceDim := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	for i, c := range p.choices {
		prefix := fmt.Sprintf("  %d. ", i+1)
		var line string
		if i == p.selected {
			line = choiceDim.Render(prefix) + choiceActive.Render(" "+c+" ")
		} else {
			line = choiceDim.Render(prefix) + choiceInactive.Render(c)
		}
		if lipgloss.Width(line) > maxWidth {
			line = ansi.Truncate(line, maxWidth, "…")
		}
		lines = append(lines, line)
	}
	return lines
}

// overlayBottomCenter composites a floating box centered horizontally at the
// bottom of the viewport content, anchored 1 row above the bottom edge.
func overlayBottomCenter(base, box string, baseWidth, baseHeight int) string {
	boxLines := strings.Split(box, "\n")
	boxWidth := 0
	for _, bl := range boxLines {
		w := lipgloss.Width(bl)
		if w > boxWidth {
			boxWidth = w
		}
	}
	startCol := (baseWidth - boxWidth) / 2
	if startCol < 0 {
		startCol = 0
	}
	return overlayBottom(base, box, baseWidth, baseHeight, startCol)
}

// overlayBottomLeft composites a floating box left-aligned at the bottom of
// the viewport content, anchored 1 row above the bottom edge.
func overlayBottomLeft(base, box string, baseWidth, baseHeight int) string {
	return overlayBottom(base, box, baseWidth, baseHeight, 0)
}

// overlayBottom composites a floating box at the bottom of the viewport content,
// anchored 1 row above the bottom edge, starting at the given column.
func overlayBottom(base, box string, baseWidth, baseHeight, startCol int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")

	// Pad base to full height.
	for len(baseLines) < baseHeight {
		baseLines = append(baseLines, "")
	}

	// Position: bottom of viewport.
	boxHeight := len(boxLines)
	startRow := baseHeight - boxHeight - 1
	if startRow < 0 {
		startRow = 0
	}

	for i, boxLine := range boxLines {
		row := startRow + i
		if row >= len(baseLines) {
			break
		}

		overlayW := lipgloss.Width(boxLine)

		baseLine := baseLines[row]
		baseW := lipgloss.Width(baseLine)
		if baseW < baseWidth {
			baseLine += strings.Repeat(" ", baseWidth-baseW)
		}

		left := ansi.Truncate(baseLine, startCol, "")
		leftW := lipgloss.Width(left)
		if leftW < startCol {
			left += strings.Repeat(" ", startCol-leftW)
		}

		rightStart := startCol + overlayW
		var rightPart string
		if rightStart < baseWidth {
			rightPart = ansi.TruncateLeft(baseLine, rightStart, "")
			rpW := lipgloss.Width(rightPart)
			if rpW < baseWidth-rightStart {
				rightPart += strings.Repeat(" ", baseWidth-rightStart-rpW)
			}
		}

		baseLines[row] = left + boxLine + rightPart
	}

	return strings.Join(baseLines, "\n")
}
