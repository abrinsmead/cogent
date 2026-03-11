package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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

// promptModel represents an active user feedback prompt. It is shown inline
// in the viewport (pushing text up). The three kinds cover:
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
