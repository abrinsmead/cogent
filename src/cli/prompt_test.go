package cli

import (
	"strings"
	"testing"

	"github.com/anthropics/agent/agent"
)

func TestNewConfirmPrompt(t *testing.T) {
	reply := make(chan agent.ConfirmResult, 1)
	cm := &tuiConfirmMsg{name: "bash", input: map[string]any{"command": "ls"}, reply: reply}
	p := newConfirmPrompt(cm, "Allow bash ls? [Y/n/a] ")

	if p.kind != promptConfirm {
		t.Errorf("expected promptConfirm, got %d", p.kind)
	}
	if p.confirmMsg != cm {
		t.Error("confirmMsg not stored")
	}
	if p.message != "Allow bash ls? [Y/n/a] " {
		t.Errorf("unexpected message: %q", p.message)
	}
	hint := p.hintText()
	if hint != " y/n/a " {
		t.Errorf("unexpected hint: %q", hint)
	}
}

func TestNewPlanConfirmPrompt(t *testing.T) {
	p := newPlanConfirmPrompt()

	if p.kind != promptPlanConfirm {
		t.Errorf("expected promptPlanConfirm, got %d", p.kind)
	}
	if !strings.Contains(p.message, "Confirm mode") {
		t.Errorf("unexpected message: %q", p.message)
	}
	hint := p.hintText()
	if hint != " y/n " {
		t.Errorf("unexpected hint: %q", hint)
	}
}

func TestNewChoicePrompt(t *testing.T) {
	choices := []string{"Option A", "Option B", "Other (I'll explain)"}
	p := newChoicePrompt("Which approach?", choices)

	if p.kind != promptChoice {
		t.Errorf("expected promptChoice, got %d", p.kind)
	}
	if p.selected != 0 {
		t.Errorf("expected selected=0, got %d", p.selected)
	}
	if len(p.choices) != 3 {
		t.Errorf("expected 3 choices, got %d", len(p.choices))
	}
	if p.freeform {
		t.Error("freeform should be false initially")
	}
}

func TestChoiceNavigation(t *testing.T) {
	choices := []string{"A", "B", "C"}
	p := newChoicePrompt("Pick one:", choices)

	// Initial state
	if p.selectedChoice() != 0 {
		t.Errorf("expected 0, got %d", p.selectedChoice())
	}

	// Move down
	p.down()
	if p.selectedChoice() != 1 {
		t.Errorf("after down: expected 1, got %d", p.selectedChoice())
	}

	p.down()
	if p.selectedChoice() != 2 {
		t.Errorf("after 2 downs: expected 2, got %d", p.selectedChoice())
	}

	// Can't go past the end
	p.down()
	if p.selectedChoice() != 2 {
		t.Errorf("after 3 downs: expected 2 (clamped), got %d", p.selectedChoice())
	}

	// Move up
	p.up()
	if p.selectedChoice() != 1 {
		t.Errorf("after up: expected 1, got %d", p.selectedChoice())
	}

	// All the way up
	p.up()
	p.up()
	if p.selectedChoice() != 0 {
		t.Errorf("after multiple ups: expected 0 (clamped), got %d", p.selectedChoice())
	}
}

func TestChoiceNavigationDisabledInFreeform(t *testing.T) {
	choices := []string{"A", "B", "Other"}
	p := newChoicePrompt("Pick:", choices)
	p.selected = 2
	p.freeform = true

	// up/down should be no-ops in freeform mode
	p.up()
	if p.selectedChoice() != 2 {
		t.Errorf("up in freeform should be no-op, got %d", p.selectedChoice())
	}
	p.down()
	if p.selectedChoice() != 2 {
		t.Errorf("down in freeform should be no-op, got %d", p.selectedChoice())
	}
}

func TestChoiceSelectByNumber(t *testing.T) {
	choices := []string{"A", "B", "C"}
	p := newChoicePrompt("Pick one:", choices)

	// Valid selection
	if !p.selectByNumber(2) {
		t.Error("selectByNumber(2) should return true")
	}
	if p.selectedChoice() != 1 {
		t.Errorf("after selectByNumber(2): expected 1, got %d", p.selectedChoice())
	}

	// Out of range
	if p.selectByNumber(0) {
		t.Error("selectByNumber(0) should return false")
	}
	if p.selectByNumber(4) {
		t.Error("selectByNumber(4) should return false")
	}

	// Negative
	if p.selectByNumber(-1) {
		t.Error("selectByNumber(-1) should return false")
	}
}

func TestChoiceSelectByNumberDisabledInFreeform(t *testing.T) {
	choices := []string{"A", "B", "Other"}
	p := newChoicePrompt("Pick:", choices)
	p.freeform = true

	if p.selectByNumber(1) {
		t.Error("selectByNumber in freeform should return false")
	}
}

func TestChoiceSelectByNumberNonChoice(t *testing.T) {
	p := newPlanConfirmPrompt()
	if p.selectByNumber(1) {
		t.Error("selectByNumber on non-choice should return false")
	}
}

func TestChoiceUpDownOnNonChoice(t *testing.T) {
	// up/down should be no-ops on non-choice prompts
	p := newConfirmPrompt(nil, "test")
	p.up()
	p.down()
	// No panic — that's the test
}

func TestIsOtherSelected(t *testing.T) {
	choices := []string{"A", "B", "Other (I'll explain)"}
	p := newChoicePrompt("Pick:", choices)

	// First choice — not "other"
	if p.isOtherSelected() {
		t.Error("first choice should not be 'other'")
	}

	// Navigate to last choice
	p.down()
	p.down()
	if !p.isOtherSelected() {
		t.Error("last choice should be 'other'")
	}

	// Back up
	p.up()
	if p.isOtherSelected() {
		t.Error("second choice should not be 'other'")
	}
}

func TestRenderChoices(t *testing.T) {
	choices := []string{"Use interfaces", "Use generics", "Other (I'll explain)"}
	p := newChoicePrompt("Which pattern?", choices)

	rendered := p.renderPromptLine()
	if !strings.Contains(rendered, "Which pattern?") {
		t.Errorf("rendered should contain question: %q", rendered)
	}
	if !strings.Contains(rendered, "Use interfaces") {
		t.Errorf("rendered should contain choice 1: %q", rendered)
	}
	if !strings.Contains(rendered, "Use generics") {
		t.Errorf("rendered should contain choice 2: %q", rendered)
	}
	if !strings.Contains(rendered, "Other") {
		t.Errorf("rendered should contain choice 3: %q", rendered)
	}
}

func TestHintText(t *testing.T) {
	tests := []struct {
		name string
		p    promptModel
		want string
	}{
		{"confirm", newConfirmPrompt(nil, "test"), " y/n/a "},
		{"plan", newPlanConfirmPrompt(), " y/n "},
		{"choice2", newChoicePrompt("q", []string{"a", "b"}), " ↑/↓ enter  (1-2) "},
		{"choice5", newChoicePrompt("q", []string{"a", "b", "c", "d", "e"}), " ↑/↓ enter  (1-5) "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.p.hintText()
			if got != tt.want {
				t.Errorf("hintText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHintTextFreeform(t *testing.T) {
	p := newChoicePrompt("q", []string{"a", "Other"})
	p.freeform = true
	got := p.hintText()
	if !strings.Contains(got, "type your answer") {
		t.Errorf("freeform hint should mention typing, got %q", got)
	}
}

func TestLineTypeChoice(t *testing.T) {
	// Test rendering of lineChoice
	l := line{Type: lineChoice, Data: "Which approach?\x00Option A\x00Option B\x00Other (I'll explain)"}
	rendered := renderLine(l)
	if !strings.Contains(rendered, "Which approach?") {
		t.Errorf("lineChoice render should contain question")
	}
	if !strings.Contains(rendered, "Option A") {
		t.Errorf("lineChoice render should contain Option A")
	}
	if !strings.Contains(rendered, "1.") {
		t.Errorf("lineChoice render should contain numbered prefix")
	}
	if !strings.Contains(rendered, "Other") {
		t.Errorf("lineChoice render should contain Other option")
	}
}

func TestLineTypeChoiceSelected(t *testing.T) {
	l := line{Type: lineChoiceSelected, Data: "Option B"}
	rendered := renderLine(l)
	if !strings.Contains(rendered, "Option B") {
		t.Errorf("lineChoiceSelected render should contain selected label")
	}
	if !strings.Contains(rendered, "✓") {
		t.Errorf("lineChoiceSelected render should contain checkmark")
	}
}

func TestLineTypeChoiceSelectedFreeform(t *testing.T) {
	// Freeform responses show just like regular selections
	l := line{Type: lineChoiceSelected, Data: "I want to use a completely different approach with channels"}
	rendered := renderLine(l)
	if !strings.Contains(rendered, "channels") {
		t.Errorf("lineChoiceSelected render should contain freeform text")
	}
	if !strings.Contains(rendered, "✓") {
		t.Errorf("lineChoiceSelected render should contain checkmark")
	}
}
