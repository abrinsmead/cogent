package cli

import (
	"context"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/anthropics/agent/api"
)

// tuiSuggestionMsg delivers a completed suggestion to the TUI.
type tuiSuggestionMsg struct {
	sessionID  int
	suggestion string
}

const suggestSystemPrompt = `You are an input prediction engine for a coding assistant called Cogent. Your job is to predict what the user will type next as their instruction to the coding agent.

You will be given the recent conversation history between the user and the coding agent. Based on this context, predict the most likely next user message.

Rules:
- Output ONLY the predicted text, nothing else
- Do not add quotes, prefixes, or explanations
- Keep predictions concise and actionable (1-2 sentences typically)
- Predict follow-up instructions, bug fixes, refinements, or next steps
- If unsure, predict a short, general follow-up like "looks good, now..." or a relevant next step
- Consider the user's input history for patterns in how they phrase requests`

// suggestionEngine manages background LLM calls for input suggestions.
type suggestionEngine struct {
	provider api.Provider

	mu       sync.Mutex
	cancelFn context.CancelFunc // cancel in-flight suggestion request
}

// newSuggestionEngine creates a suggestion engine with the given provider.
// Returns nil if the provider is nil (feature disabled).
func newSuggestionEngine(provider api.Provider) *suggestionEngine {
	if provider == nil {
		return nil
	}
	return &suggestionEngine{provider: provider}
}

// requestSuggestion fires a background API call to predict the next user input.
// It sends the result as a tuiSuggestionMsg through msgCh.
// The caller should provide the last few messages and recent input history for context.
func (e *suggestionEngine) requestSuggestion(
	sessionID int,
	messages []api.Message,
	inputHistory []string,
	msgCh chan tea.Msg,
) tea.Cmd {
	e.mu.Lock()
	// Cancel any in-flight request
	if e.cancelFn != nil {
		e.cancelFn()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFn = cancel
	e.mu.Unlock()

	return func() tea.Msg {
		suggestion := e.predict(ctx, messages, inputHistory)
		if suggestion == "" || ctx.Err() != nil {
			return nil
		}
		msgCh <- sessionMsg{
			sessionID: sessionID,
			inner:     tuiSuggestionMsg{sessionID: sessionID, suggestion: suggestion},
		}
		return nil
	}
}

// cancel cancels any in-flight suggestion request.
func (e *suggestionEngine) cancel() {
	e.mu.Lock()
	if e.cancelFn != nil {
		e.cancelFn()
		e.cancelFn = nil
	}
	e.mu.Unlock()
}

// predict makes the LLM call and returns the suggestion text.
func (e *suggestionEngine) predict(ctx context.Context, messages []api.Message, inputHistory []string) string {
	// Build the request with the last few messages for context.
	// Take at most 4 messages to keep costs low.
	contextMsgs := messages
	if len(contextMsgs) > 4 {
		contextMsgs = contextMsgs[len(contextMsgs)-4:]
	}

	// Simplify messages: strip tool_use/tool_result blocks, keep only text.
	var simplified []api.Message
	for _, msg := range contextMsgs {
		var textBlocks []api.ContentBlock
		for _, block := range msg.Content {
			if block.Type == "text" && block.Text != "" {
				// Truncate long text blocks for cost control
				text := block.Text
				if len(text) > 500 {
					text = text[:500] + "..."
				}
				textBlocks = append(textBlocks, api.ContentBlock{Type: "text", Text: text})
			}
		}
		if len(textBlocks) > 0 {
			simplified = append(simplified, api.Message{
				Role:    msg.Role,
				Content: textBlocks,
			})
		}
	}

	if len(simplified) == 0 {
		return ""
	}

	// Add input history as context if available
	system := suggestSystemPrompt
	if len(inputHistory) > 0 {
		// Include the last 5 history entries
		histStart := len(inputHistory) - 5
		if histStart < 0 {
			histStart = 0
		}
		recent := inputHistory[histStart:]
		system += "\n\nRecent user inputs (oldest to newest):\n"
		for _, h := range recent {
			system += "- " + h + "\n"
		}
	}

	// Append a user message asking for prediction
	simplified = append(simplified, api.UserMessage(
		"Based on the conversation above, predict the single most likely next user message. Output ONLY the predicted text."))

	resp, err := e.provider.SendMessage(ctx, api.ProviderRequest{
		System:    system,
		Messages:  simplified,
		MaxTokens: 100,
	})
	if err != nil || resp == nil {
		return ""
	}

	// Extract the text response
	var texts []string
	for _, block := range resp.Content {
		if block.Type == "text" && block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	result := strings.TrimSpace(strings.Join(texts, " "))

	// Clean up: remove quotes that the model might add despite instructions
	if len(result) >= 2 {
		if (result[0] == '"' && result[len(result)-1] == '"') ||
			(result[0] == '\'' && result[len(result)-1] == '\'') {
			result = result[1 : len(result)-1]
		}
	}

	return result
}
