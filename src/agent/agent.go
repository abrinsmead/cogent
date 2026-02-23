package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/tools"
)

const systemPrompt = `You are an autonomous agent running on %s.
You help users accomplish tasks by using the tools available to you.

Guidelines:
- Read files before modifying them
- Use the appropriate tool for each task
- Be concise in your responses
- Use emojis sparingly — only when they genuinely aid clarity, never for decoration
- Current working directory: %s`

// PermissionMode controls how tool confirmations are handled.
type PermissionMode int

const (
	// ModeConfirm asks the user before executing destructive tools (default).
	ModeConfirm PermissionMode = iota
	// ModePlan disallows all destructive tools — the agent can only read/plan.
	ModePlan
	// ModeYOLO auto-approves every tool invocation without asking.
	ModeYOLO
	// ModeTerminal pauses the agent — user input goes to the shell directly.
	ModeTerminal
)

const numModes = 4

func (m PermissionMode) String() string {
	switch m {
	case ModePlan:
		return "Plan"
	case ModeYOLO:
		return "YOLO"
	case ModeTerminal:
		return "Terminal"
	default:
		return "Confirm"
	}
}

// CyclePermissionMode returns the next mode: Confirm → Plan → YOLO → Terminal → Confirm.
func CyclePermissionMode(m PermissionMode) PermissionMode {
	return (m + 1) % numModes
}

// ConfirmResult represents the user's response to a tool confirmation prompt.
type ConfirmResult int

const (
	ConfirmDeny   ConfirmResult = iota // deny this tool call
	ConfirmAllow                       // allow this tool call
	ConfirmAlways                      // allow this tool call and skip future prompts for this tool
)

type Agent struct {
	client       *api.Client
	registry     *tools.Registry
	messages     []api.Message
	system       string
	permMode     atomic.Int32    // stores PermissionMode; atomic for cross-goroutine access
	allowedTools map[string]bool // tools the user has "always allowed" for this session
	onText       func(string)
	onTool       func(string, string)
	onToolResult func(name string, result string, isError bool)
	onConfirm    func(name string, input map[string]any) ConfirmResult
	onUsage      func(api.Usage)
}

type Option func(*Agent)

func WithTextCallback(fn func(string)) Option {
	return func(a *Agent) { a.onText = fn }
}

func WithToolCallback(fn func(string, string)) Option {
	return func(a *Agent) { a.onTool = fn }
}

func WithToolResultCallback(fn func(name string, result string, isError bool)) Option {
	return func(a *Agent) { a.onToolResult = fn }
}

func WithConfirmCallback(fn func(name string, input map[string]any) ConfirmResult) Option {
	return func(a *Agent) { a.onConfirm = fn }
}

func WithUsageCallback(fn func(api.Usage)) Option {
	return func(a *Agent) { a.onUsage = fn }
}

func WithPermissionMode(m PermissionMode) Option {
	return func(a *Agent) { a.permMode.Store(int32(m)) }
}

func New(client *api.Client, cwd string, opts ...Option) *Agent {
	system := fmt.Sprintf(systemPrompt, envDescription(), cwd)
	if guide := shellGuidance(); guide != "" {
		system += "\n" + guide
	}
	if extra := loadAgentsMD(cwd); extra != "" {
		system += "\n\n" + extra
	}
	registry := tools.NewRegistry(cwd)
	// Only inject custom tool instructions if .cogent/tools/ exists
	if tools.CustomToolsExist(cwd) {
		system += "\n\n" + tools.CustomToolsPrompt
	}
	a := &Agent{
		client:       client,
		registry:     registry,
		system:       system,
		allowedTools: make(map[string]bool),
		onText:       func(s string) {},
		onTool:       func(n, s string) {},
		onToolResult: func(name, result string, isError bool) {},
		onConfirm:    func(name string, input map[string]any) ConfirmResult { return ConfirmAllow },
		onUsage:      func(u api.Usage) {},
	}
	for _, opt := range opts {
		opt(a)
	}
	// Surface any custom tool warnings via the text callback
	for _, w := range registry.Warnings() {
		a.onText("⚠ " + w)
	}
	return a
}

// loadAgentsMD collects AGENTS.md files from cwd up to the filesystem root.
// In a monorepo, a subdirectory may have its own AGENTS.md with package-specific
// context while the repo root has a broader one. All found files are concatenated
// root-first so the most general context comes first and local overrides follow.
func loadAgentsMD(dir string) string {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	var found []string
	for {
		path := filepath.Join(dir, "AGENTS.md")
		data, err := os.ReadFile(path)
		if err == nil {
			content := strings.TrimSpace(string(data))
			if content != "" {
				found = append(found, content)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Reverse so root-level comes first, subdirectory-level follows.
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}
	return strings.Join(found, "\n\n")
}

func (a *Agent) Send(userMessage string) error {
	return a.SendCtx(context.Background(), userMessage)
}

func (a *Agent) SendCtx(ctx context.Context, userMessage string) error {
	a.messages = append(a.messages, api.UserMessage(userMessage))
	return a.loop(ctx)
}

func (a *Agent) loop(ctx context.Context) error {
	for i := 0; i < 50; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		a.trimHistory()
		system := a.system
		if a.GetPermissionMode() == ModePlan {
			system += "\n\nIMPORTANT: You are in planning mode. You may only use read-only tools (read, glob, grep, ls). Do NOT use bash, write, or edit. Instead, describe what changes you would make and why."
		}
		resp, err := a.client.SendMessageCtx(ctx, system, a.messages, a.registry.Definitions())
		if err != nil {
			return fmt.Errorf("api call: %w", err)
		}
		a.onUsage(resp.Usage)
		a.messages = append(a.messages, api.Message{
			Role:    api.RoleAssistant,
			Content: resp.Content,
		})
		var toolResults []api.ContentBlock
		denied := false
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				a.onText(block.Text)
			case "tool_use":
				if denied {
					// A previous tool in this response was denied — skip
					// remaining tools but still provide a result so the
					// conversation stays valid for the API.
					toolResults = append(toolResults, api.ToolResultBlock(
						block.ID, "Error: tool execution skipped — user denied a previous tool in this response", true))
					continue
				}
				result, wasDenied := a.executeTool(block)
				toolResults = append(toolResults, result)
				if wasDenied {
					denied = true
				}
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
		if denied {
			// User denied a tool — stop the loop and wait for new instructions.
			return nil
		}
	}
	return fmt.Errorf("agent loop exceeded 50 iterations")
}

// executeTool runs a single tool call. The second return value is true when the
// user denied the confirmation prompt, signalling the loop to stop.
func (a *Agent) executeTool(block api.ContentBlock) (api.ContentBlock, bool) {
	tool, err := a.registry.Get(block.Name)
	if err != nil {
		return api.ToolResultBlock(block.ID, fmt.Sprintf("Error: %s", err), true), false
	}
	summary := summarizeInput(block.Name, block.Input)
	a.onTool(block.Name, summary)

	if tool.RequiresConfirmation() && !a.allowedTools[block.Name] {
		switch a.GetPermissionMode() {
		case ModePlan, ModeTerminal:
			return api.ToolResultBlock(block.ID, "Error: tool execution blocked — planning mode (read-only)", true), true
		case ModeYOLO:
			// Auto-approve, skip confirmation callback.
		default: // ModeConfirm
			switch a.onConfirm(block.Name, block.Input) {
			case ConfirmAlways:
				a.allowedTools[block.Name] = true
			case ConfirmAllow:
				// proceed
			default: // ConfirmDeny
				return api.ToolResultBlock(block.ID, "Error: tool execution denied by user", true), true
			}
		}
	}

	result, err := tool.Execute(block.Input)
	if err != nil {
		a.onToolResult(block.Name, fmt.Sprintf("Error: %s", err), true)
		return api.ToolResultBlock(block.ID, fmt.Sprintf("Error: %s", err), true), false
	}
	a.onToolResult(block.Name, result, false)
	return api.ToolResultBlock(block.ID, result, false), false
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

func (a *Agent) Reset() {
	a.messages = nil
	a.allowedTools = make(map[string]bool)
}

func (a *Agent) SetPermissionMode(m PermissionMode) { a.permMode.Store(int32(m)) }
func (a *Agent) GetPermissionMode() PermissionMode  { return PermissionMode(a.permMode.Load()) }

// AllowedTools returns the set of tool names that have been "always allowed" for this session.
func (a *Agent) AllowedTools() map[string]bool { return a.allowedTools }

const maxHistory = 100

// trimHistory caps conversation history at maxHistory messages, keeping the
// first message (initial user prompt) plus the most recent messages. The cut
// point is adjusted forward to avoid splitting a tool_use / tool_result pair,
// which would cause the API to reject orphaned tool_result blocks.
func (a *Agent) trimHistory() {
	if len(a.messages) <= maxHistory {
		return
	}
	// Start of the tail we want to keep (index into a.messages).
	start := len(a.messages) - (maxHistory - 1)

	// Walk forward until we find a message that isn't a tool_result user
	// message, since those require the preceding assistant tool_use message.
	for start < len(a.messages) {
		msg := a.messages[start]
		if msg.Role != api.RoleUser || !isToolResultMessage(msg) {
			break
		}
		start++
	}

	keep := make([]api.Message, 0, 1+len(a.messages)-start)
	keep = append(keep, a.messages[0])
	keep = append(keep, a.messages[start:]...)
	a.messages = keep
}

// isToolResultMessage returns true if every content block in the message is a
// tool_result. This is the shape produced by ToolResultMessage().
func isToolResultMessage(msg api.Message) bool {
	if len(msg.Content) == 0 {
		return false
	}
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			return false
		}
	}
	return true
}
