package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// compactMessages implements hybrid client-side compaction for providers
// that lack server-side compaction (OpenAI, Gemini).
//
// Strategy:
// 1. Keep the most recent N messages (by token budget)
// 2. Summarize older messages via an LLM call
// 3. Return: [summary user msg, ack assistant msg, ...recent messages]
// 4. If summarization fails, fall back to sliding window truncation
func compactMessages(ctx context.Context, provider Provider, system string, messages []Message, budget int) ([]Message, error) {
	if len(messages) <= 4 {
		return messages, nil // too few to compact
	}

	// Keep the most recent messages that fit within budget.
	// Use a rough heuristic: 4 chars ≈ 1 token.
	recentBudget := budget / 2
	var recentStart int
	tokensUsed := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := estimateMessageTokens(messages[i])
		if tokensUsed+msgTokens > recentBudget {
			recentStart = i + 1
			break
		}
		tokensUsed += msgTokens
	}

	// Ensure we keep at least the last 2 messages
	if recentStart > len(messages)-2 {
		recentStart = len(messages) - 2
	}
	if recentStart < 1 {
		recentStart = 1
	}

	oldMessages := messages[:recentStart]
	recentMessages := messages[recentStart:]

	// Try to summarize old messages
	summary, err := summarizeMessages(ctx, provider, system, oldMessages)
	if err != nil {
		// Fallback: sliding window truncation — just keep recent messages
		return recentMessages, nil
	}

	// Build compacted history: summary + recent
	compacted := make([]Message, 0, len(recentMessages)+2)
	compacted = append(compacted,
		UserMessage(fmt.Sprintf("[Conversation summary]\n%s", summary)),
		Message{Role: RoleAssistant, Content: []ContentBlock{TextBlock("Understood. I'll continue with this context.")}},
	)
	compacted = append(compacted, recentMessages...)

	return compacted, nil
}

// summarizeMessages asks the provider to summarize a set of messages.
func summarizeMessages(ctx context.Context, provider Provider, system string, messages []Message) (string, error) {
	// Build a text representation of the messages to summarize
	var sb strings.Builder
	for _, msg := range messages {
		role := "User"
		if msg.Role == RoleAssistant {
			role = "Assistant"
		}
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				sb.WriteString(role + ": " + block.Text + "\n")
			case "tool_use":
				args, _ := json.Marshal(block.Input)
				sb.WriteString(fmt.Sprintf("Assistant [tool:%s]: %s\n", block.Name, string(args)))
			case "tool_result":
				content := block.Content
				if len(content) > 500 {
					content = content[:500] + "..."
				}
				sb.WriteString(fmt.Sprintf("Tool result [%s]: %s\n", block.ToolUseID, content))
			}
		}
	}

	summaryPrompt := `Summarize the following conversation context concisely. Preserve:
- Key decisions made
- File paths that were read, written, or modified
- Important tool results and their outcomes
- Outstanding tasks or goals
- Any errors encountered and how they were resolved

Conversation:
` + sb.String()

	resp, err := provider.SendMessage(ctx, ProviderRequest{
		System:    "You are a conversation summarizer. Produce a concise summary.",
		Messages:  []Message{UserMessage(summaryPrompt)},
		MaxTokens: 4096,
	})
	if err != nil {
		return "", err
	}

	// Extract text from response
	var texts []string
	for _, block := range resp.Content {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	if len(texts) == 0 {
		return "", fmt.Errorf("empty summary response")
	}

	return strings.Join(texts, "\n"), nil
}

// estimateMessageTokens gives a rough token estimate for a message.
// Uses ~4 chars per token heuristic.
func estimateMessageTokens(msg Message) int {
	total := 0
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			total += len(block.Text) / 4
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			total += len(args) / 4
		case "tool_result":
			total += len(block.Content) / 4
		case "thinking":
			total += len(block.Thinking) / 4
		}
	}
	if total < 1 {
		total = 1
	}
	return total
}
