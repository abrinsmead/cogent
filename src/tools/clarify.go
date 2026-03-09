package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/agent/api"
)

// ClarifyFunc is called by ClarifyTool to present a question to the user.
// It receives the question and choices, and returns the user's response text.
// The last choice is always treated as a freeform "Other" option by the UI —
// if selected, the user can type a custom response.
type ClarifyFunc func(question string, choices []string) (string, error)

// ClarifyTool asks the user a clarifying question with multiple-choice options.
// The model calls this when it needs user input before proceeding.
type ClarifyTool struct {
	Ask ClarifyFunc
}

func (c *ClarifyTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name: "clarify",
		Description: "Ask the user a clarifying question when you need more information to proceed. " +
			"Present a question with 2-5 specific choices. The last choice should always be an open-ended " +
			"option like \"Other (I'll explain)\" so the user can provide a custom answer. " +
			"The user's selected choice (or freeform text) is returned. " +
			"Use this instead of guessing when requirements are ambiguous.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"question": {
					Type:        "string",
					Description: "The clarifying question to ask the user",
				},
				"choices": {
					Type:        "array",
					Description: "2-5 choices for the user to pick from. The last choice should be an open-ended option like \"Other (I'll explain)\" to allow freeform input.",
					Items:       &api.Property{Type: "string"},
				},
			},
			Required: []string{"question", "choices"},
		},
	}
}

func (c *ClarifyTool) Execute(input map[string]any) (string, error) {
	question, _ := input["question"].(string)
	if question == "" {
		return "", fmt.Errorf("question is required")
	}

	rawChoices, _ := input["choices"].([]any)
	if len(rawChoices) < 2 {
		return "", fmt.Errorf("at least 2 choices are required")
	}

	choices := make([]string, 0, len(rawChoices))
	for _, rc := range rawChoices {
		switch v := rc.(type) {
		case string:
			choices = append(choices, v)
		case json.Number:
			choices = append(choices, v.String())
		default:
			choices = append(choices, fmt.Sprintf("%v", v))
		}
	}

	if c.Ask == nil {
		return "", fmt.Errorf("clarify is not available in this mode")
	}

	result, err := c.Ask(question, choices)
	if err != nil {
		return "", err
	}

	result = strings.TrimSpace(result)

	// User dismissed the prompt without answering
	if result == "" {
		return "", fmt.Errorf("question dismissed by user")
	}

	// Check if the result matches one of the predefined choices
	for i, ch := range choices {
		if result == ch {
			return fmt.Sprintf("The user selected option %d: %s", i+1, ch), nil
		}
	}

	// Freeform response
	return fmt.Sprintf("The user responded: %s", result), nil
}

func (c *ClarifyTool) RequiresConfirmation() bool { return false }
