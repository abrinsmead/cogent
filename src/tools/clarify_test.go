package tools

import (
	"testing"
)

func TestClarifyToolDefinition(t *testing.T) {
	ct := &ClarifyTool{}
	def := ct.Definition()

	if def.Name != "clarify" {
		t.Errorf("expected name 'clarify', got %q", def.Name)
	}
	if _, ok := def.InputSchema.Properties["question"]; !ok {
		t.Error("missing 'question' property")
	}
	if _, ok := def.InputSchema.Properties["choices"]; !ok {
		t.Error("missing 'choices' property")
	}
	if len(def.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(def.InputSchema.Required))
	}
}

func TestClarifyToolRequiresConfirmation(t *testing.T) {
	ct := &ClarifyTool{}
	if ct.RequiresConfirmation() {
		t.Error("clarify should not require confirmation")
	}
}

func TestClarifyToolExecute(t *testing.T) {
	ct := &ClarifyTool{
		Ask: func(question string, choices []string) (string, error) {
			if question != "Which approach?" {
				t.Errorf("unexpected question: %q", question)
			}
			if len(choices) != 3 {
				t.Errorf("expected 3 choices, got %d", len(choices))
			}
			return "Option B", nil
		},
	}

	input := map[string]any{
		"question": "Which approach?",
		"choices":  []any{"Option A", "Option B", "Other"},
	}

	result, err := ct.Execute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "The user selected option 2: Option B" {
		t.Errorf("expected structured selection, got %q", result)
	}
}

func TestClarifyToolExecuteFreeform(t *testing.T) {
	ct := &ClarifyTool{
		Ask: func(question string, choices []string) (string, error) {
			return "I want to use channels instead", nil
		},
	}

	input := map[string]any{
		"question": "Which approach?",
		"choices":  []any{"Option A", "Other"},
	}

	result, err := ct.Execute(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "The user responded: I want to use channels instead" {
		t.Errorf("expected structured freeform, got %q", result)
	}
}

func TestClarifyToolExecuteDismissed(t *testing.T) {
	ct := &ClarifyTool{
		Ask: func(question string, choices []string) (string, error) {
			return "", nil // user pressed Esc
		},
	}

	input := map[string]any{
		"question": "Which approach?",
		"choices":  []any{"Option A", "Other"},
	}

	_, err := ct.Execute(input)
	if err == nil {
		t.Error("expected error when user dismisses")
	}
	if err.Error() != "question dismissed by user" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClarifyToolMissingQuestion(t *testing.T) {
	ct := &ClarifyTool{
		Ask: func(q string, c []string) (string, error) { return "x", nil },
	}
	_, err := ct.Execute(map[string]any{"choices": []any{"a", "b"}})
	if err == nil {
		t.Error("expected error for missing question")
	}
}

func TestClarifyToolTooFewChoices(t *testing.T) {
	ct := &ClarifyTool{
		Ask: func(q string, c []string) (string, error) { return "x", nil },
	}
	_, err := ct.Execute(map[string]any{"question": "q?", "choices": []any{"only one"}})
	if err == nil {
		t.Error("expected error for too few choices")
	}
}

func TestClarifyToolNilAsk(t *testing.T) {
	ct := &ClarifyTool{}
	_, err := ct.Execute(map[string]any{"question": "q?", "choices": []any{"a", "b"}})
	if err == nil {
		t.Error("expected error when Ask is nil")
	}
}
