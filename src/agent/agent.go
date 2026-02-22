package agent

import (
	"fmt"
	"strings"

	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/tools"
)

const systemPrompt = `You are an autonomous coding agent running on macOS.
You help users with software engineering tasks: writing code, debugging, exploring codebases, and running commands.

Guidelines:
- Read files before modifying them
- Use the appropriate tool for each task
- Be concise in your responses
- Use emojis sparingly — only when they genuinely aid clarity, never for decoration
- Current working directory: %s`

type Agent struct {
	client    *api.Client
	registry  *tools.Registry
	messages  []api.Message
	system    string
	onText    func(string)
	onTool    func(string, string)
	onConfirm func(name, summary string) bool
}

type Option func(*Agent)

func WithTextCallback(fn func(string)) Option {
	return func(a *Agent) { a.onText = fn }
}

func WithToolCallback(fn func(string, string)) Option {
	return func(a *Agent) { a.onTool = fn }
}

func WithConfirmCallback(fn func(name, summary string) bool) Option {
	return func(a *Agent) { a.onConfirm = fn }
}

func New(client *api.Client, cwd string, opts ...Option) *Agent {
	a := &Agent{
		client:    client,
		registry:  tools.NewRegistry(cwd),
		system:    fmt.Sprintf(systemPrompt, cwd),
		onText:    func(s string) {},
		onTool:    func(n, s string) {},
		onConfirm: func(name, summary string) bool { return true },
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Agent) Send(userMessage string) error {
	a.messages = append(a.messages, api.UserMessage(userMessage))
	return a.loop()
}

func (a *Agent) loop() error {
	for i := 0; i < 50; i++ {
		a.trimHistory()
		resp, err := a.client.SendMessage(a.system, a.messages, a.registry.Definitions())
		if err != nil {
			return fmt.Errorf("api call: %w", err)
		}
		a.messages = append(a.messages, api.Message{
			Role:    api.RoleAssistant,
			Content: resp.Content,
		})
		var toolResults []api.ContentBlock
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				a.onText(block.Text)
			case "tool_use":
				result := a.executeTool(block)
				toolResults = append(toolResults, result)
			}
		}
		if resp.StopReason == api.StopMaxTokens {
			// Response was truncated — ask the model to continue
			a.messages = append(a.messages, api.UserMessage("Continue from where you left off."))
			continue
		}
		if resp.StopReason != api.StopToolUse || len(toolResults) == 0 {
			return nil
		}
		a.messages = append(a.messages, api.ToolResultMessage(toolResults))
	}
	return fmt.Errorf("agent loop exceeded 50 iterations")
}

func (a *Agent) executeTool(block api.ContentBlock) api.ContentBlock {
	tool, err := a.registry.Get(block.Name)
	if err != nil {
		return api.ToolResultBlock(block.ID, fmt.Sprintf("Error: %s", err), true)
	}
	summary := summarizeInput(block.Name, block.Input)
	a.onTool(block.Name, summary)
	if tool.RequiresConfirmation() && !a.onConfirm(block.Name, summary) {
		return api.ToolResultBlock(block.ID, "Error: tool execution denied by user", true)
	}
	result, err := tool.Execute(block.Input)
	if err != nil {
		return api.ToolResultBlock(block.ID, fmt.Sprintf("Error: %s", err), true)
	}
	return api.ToolResultBlock(block.ID, result, false)
}

func summarizeInput(name string, input map[string]any) string {
	str := func(key string) string { s, _ := input[key].(string); return s }
	switch name {
	case "bash":
		if v := str("command"); v != "" {
			if len(v) > 80 {
				return v[:80] + "..."
			}
			return v
		}
	case "read", "write", "edit":
		if v := str("file_path"); v != "" {
			return v
		}
	case "glob":
		if v := str("pattern"); v != "" {
			return v
		}
	case "ls":
		if v := str("path"); v != "" {
			return v
		}
	case "grep":
		return strings.TrimSpace(str("pattern") + " " + str("glob"))
	}
	return fmt.Sprintf("%v", input)
}

func (a *Agent) Reset() { a.messages = nil }

const maxHistory = 100

// trimHistory caps conversation history at maxHistory messages, keeping the
// first message (initial user prompt) plus the most recent messages.
func (a *Agent) trimHistory() {
	if len(a.messages) <= maxHistory {
		return
	}
	keep := make([]api.Message, 0, maxHistory)
	keep = append(keep, a.messages[0])
	keep = append(keep, a.messages[len(a.messages)-(maxHistory-1):]...)
	a.messages = keep
}
