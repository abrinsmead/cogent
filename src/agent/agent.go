package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// ModePlan disallows all destructive tools — the agent can only read/plan (default).
	ModePlan PermissionMode = iota
	// ModeConfirm asks the user before executing destructive tools.
	ModeConfirm
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

// CyclePermissionMode returns the next mode: Plan → Confirm → YOLO → Terminal → Plan.
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
	provider     api.Provider
	registry     *tools.Registry
	messages     []api.Message
	system       string
	permMode     atomic.Int32    // stores PermissionMode; atomic for cross-goroutine access
	allowedTools map[string]bool // tools the user has "always allowed" for this session
	onText       func(string)
	onThinking   func(string) // called with extended thinking content
	onTool       func(string, string)
	onToolResult func(name string, result string, isError bool)
	onConfirm    func(name string, input map[string]any) ConfirmResult
	onUsage      func(api.Usage)
	onCompaction func() // called when server-side compaction occurs

	lastContextUsed int // last known context usage from provider response
}

type Option func(*Agent)

func WithTextCallback(fn func(string)) Option {
	return func(a *Agent) { a.onText = fn }
}

func WithThinkingCallback(fn func(string)) Option {
	return func(a *Agent) { a.onThinking = fn }
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

func WithCompactionCallback(fn func()) Option {
	return func(a *Agent) { a.onCompaction = fn }
}

func WithPermissionMode(m PermissionMode) Option {
	return func(a *Agent) { a.permMode.Store(int32(m)) }
}

// WithRegistry overrides the default tool registry. Useful for sub-agents
// that need a restricted tool set (e.g. no dispatch).
func WithRegistry(r *tools.Registry) Option {
	return func(a *Agent) { a.registry = r }
}

func New(provider api.Provider, cwd string, opts ...Option) *Agent {
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
		provider:     provider,
		registry:     registry,
		system:       system,
		allowedTools: make(map[string]bool),
		onText:       func(s string) {},
		onThinking:   func(s string) {},
		onTool:       func(n, s string) {},
		onToolResult: func(name, result string, isError bool) {},
		onConfirm:    func(name string, input map[string]any) ConfirmResult { return ConfirmAllow },
		onUsage:      func(u api.Usage) {},
		onCompaction: func() {},
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

// planPrompt is appended to the system prompt when plan mode is active.
// It encourages deep analysis, clarifying questions, and structured output.
const planPrompt = `

IMPORTANT: You are in PLANNING MODE. Your goal is to deeply understand the task and produce a thorough plan before any code is changed.

Approach:
1. EXPLORE — Read relevant files, search for patterns, run read-only shell commands (git log, tests, etc.) to understand the codebase and the problem.
2. CLARIFY — If the task is ambiguous, underspecified, or could be interpreted multiple ways, ask clarifying questions BEFORE proposing a plan. Do not guess at requirements.
3. ANALYZE — Consider edge cases, test implications, backward compatibility, and potential breakage.
4. PLAN — Present your plan as a structured, numbered checklist.

Constraints:
- You have access to read-only tools. Use them to explore the codebase.
- You may use bash for read-only commands (git log/diff/status, running tests, cat, find, etc.). Destructive shell commands will require user confirmation.
- Describe what file changes you would make rather than making them directly.

When you have gathered enough information, present your final plan in this format:

## Plan

1. **file/path.go** — Description of change and why
2. **file/path.go** — Description of change and why
...

### Risks / Open Questions
- Any concerns or alternatives worth noting

End with: "Ready to execute — confirm when prompted."`

func (a *Agent) loop(ctx context.Context) error {
	for i := 0; i < 50; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		system := a.system
		isPlan := a.GetPermissionMode() == ModePlan
		if isPlan {
			system += planPrompt
		}

		info := a.provider.Info()

		// Enable extended thinking in plan mode for deeper reasoning
		// (only if provider supports it).
		var thinking *api.ThinkingConfig
		if isPlan && info.SupportsThinking {
			thinking = &api.ThinkingConfig{
				Type:         "enabled",
				BudgetTokens: 10000,
			}
		}

		var tools []any
		if isPlan {
			tools = a.registry.PlanDefinitions()
		} else {
			tools = a.registry.Definitions()
		}
		// Only add server tools when the provider supports them.
		if info.SupportsServerTools {
			tools = append(tools, api.ServerTool{Type: "web_search_20250305", Name: "web_search"})
		}

		// Client-side compaction for providers without server-side compaction.
		if a.provider.NeedsClientCompaction() {
			threshold := info.ContextWindow * 80 / 100
			if a.contextUsed() > threshold {
				compacted, err := a.provider.Compact(ctx, system, a.messages, info.ContextWindow/2)
				if err == nil {
					a.messages = compacted
					a.onCompaction()
				}
			}
		}

		resp, err := a.provider.SendMessage(ctx, api.ProviderRequest{
			System:    system,
			Messages:  a.messages,
			Tools:     tools,
			Thinking:  thinking,
			MaxTokens: 16384,
		})
		if err != nil {
			return fmt.Errorf("api call: %w", err)
		}
		a.onUsage(resp.Usage)
		a.lastContextUsed = resp.Usage.ContextUsed()
		a.messages = append(a.messages, api.Message{
			Role:    api.RoleAssistant,
			Content: resp.Content,
		})

		// Check for compaction blocks in the response
		compacted := false
		for _, block := range resp.Content {
			if block.Type == "compaction" {
				compacted = true
				break
			}
		}
		if compacted {
			a.onCompaction()
		}

		// Process non-tool blocks first, collect tool_use blocks.
		var toolBlocks []api.ContentBlock
		for _, block := range resp.Content {
			switch block.Type {
			case "thinking":
				a.onThinking(block.Thinking)
			case "text":
				a.onText(block.Text)
			case "compaction":
				// Compaction blocks are kept in the message history —
				// the API will drop all content before them automatically.
			case "tool_use":
				toolBlocks = append(toolBlocks, block)
			case "server_tool_use":
				if q, ok := block.Input["query"].(string); ok {
					a.onTool("web_search", q)
				}
			case "web_search_tool_result":
				a.onToolResult("web_search", fmt.Sprintf("%d results", len(block.SearchContent)), false)
			}
		}

		// Execute tools. Concurrent tools (e.g. dispatch) run in parallel;
		// sequential tools run one at a time in order.
		toolResults, denied := a.executeTools(toolBlocks)
		if resp.StopReason == api.StopCompaction {
			// Compaction paused the response — continue to let the model
			// resume with the compacted context.
			continue
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

// executeTools processes a batch of tool_use blocks. Tools that implement
// tools.ConcurrentTool are run in parallel; all other tools run sequentially.
// Confirmations are always handled sequentially (one at a time for the UI).
// Returns the ordered tool results and whether any tool was denied.
func (a *Agent) executeTools(blocks []api.ContentBlock) ([]api.ContentBlock, bool) {
	if len(blocks) == 0 {
		return nil, false
	}

	// Phase 1: confirm all tools and identify which are concurrent.
	// We must do confirmations sequentially (UI interaction).
	type confirmedTool struct {
		block      api.ContentBlock
		tool       tools.Tool
		denied     bool // user denied
		errResult  *api.ContentBlock // pre-built error result (tool not found, blocked, etc.)
		concurrent bool
	}

	confirmed := make([]confirmedTool, len(blocks))
	denied := false

	for i, block := range blocks {
		confirmed[i].block = block

		if denied {
			r := api.ToolResultBlock(block.ID,
				"Error: tool execution skipped — user denied a previous tool in this response", true)
			confirmed[i].errResult = &r
			continue
		}

		tool, err := a.registry.Get(block.Name)
		if err != nil {
			r := api.ToolResultBlock(block.ID, fmt.Sprintf("Error: %s", err), true)
			confirmed[i].errResult = &r
			continue
		}
		confirmed[i].tool = tool

		summary := summarizeInput(block.Name, block.Input)
		a.onTool(block.Name, summary)

		// Handle confirmation
		if errMsg, wasDenied := a.confirmTool(tool, block); wasDenied {
			r := api.ToolResultBlock(block.ID, errMsg, true)
			confirmed[i].errResult = &r
			confirmed[i].denied = true
			denied = true
			continue
		}

		// Check if this tool supports concurrent execution
		if ct, ok := tool.(tools.ConcurrentTool); ok && ct.IsConcurrent() {
			confirmed[i].concurrent = true
		}
	}

	// Phase 2: execute tools. Launch concurrent tools in goroutines,
	// run sequential tools inline, then collect all results in order.
	type indexedResult struct {
		index  int
		result api.ContentBlock
	}

	results := make([]api.ContentBlock, len(blocks))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var concurrentResults []indexedResult

	for i, ct := range confirmed {
		if ct.errResult != nil {
			results[i] = *ct.errResult
			continue
		}

		if ct.concurrent {
			wg.Add(1)
			go func(idx int, t tools.Tool, b api.ContentBlock) {
				defer wg.Done()
				result, err := t.Execute(b.Input)
				var r api.ContentBlock
				if err != nil {
					a.onToolResult(b.Name, fmt.Sprintf("Error: %s", err), true)
					r = api.ToolResultBlock(b.ID, fmt.Sprintf("Error: %s", err), true)
				} else {
					a.onToolResult(b.Name, result, false)
					r = api.ToolResultBlock(b.ID, result, false)
				}
				mu.Lock()
				concurrentResults = append(concurrentResults, indexedResult{idx, r})
				mu.Unlock()
			}(i, ct.tool, ct.block)
		} else {
			// Sequential execution
			result, err := ct.tool.Execute(ct.block.Input)
			if err != nil {
				a.onToolResult(ct.block.Name, fmt.Sprintf("Error: %s", err), true)
				results[i] = api.ToolResultBlock(ct.block.ID, fmt.Sprintf("Error: %s", err), true)
			} else {
				a.onToolResult(ct.block.Name, result, false)
				results[i] = api.ToolResultBlock(ct.block.ID, result, false)
			}
		}
	}

	// Wait for all concurrent tools to finish
	wg.Wait()

	// Place concurrent results into the ordered results slice
	for _, cr := range concurrentResults {
		results[cr.index] = cr.result
	}

	return results, denied
}

// confirmTool handles the confirmation prompt for a tool call.
// Returns ("", false) if execution should proceed.
// Returns (errorMsg, true) if the user denied or the mode blocks the tool.
func (a *Agent) confirmTool(tool tools.Tool, block api.ContentBlock) (string, bool) {
	if !tool.RequiresConfirmation() || a.allowedTools[block.Name] {
		return "", false
	}

	mode := a.GetPermissionMode()
	switch mode {
	case ModePlan:
		// Plan mode allows bash (with confirmation) but blocks write/edit/dispatch.
		if block.Name != "bash" {
			return "Error: tool execution blocked — planning mode (read-only). Use write/edit only after switching to Confirm mode.", true
		}
		// Bash in plan mode — require confirmation like Confirm mode.
		switch a.onConfirm(block.Name, block.Input) {
		case ConfirmAlways:
			a.allowedTools[block.Name] = true
		case ConfirmAllow:
			// proceed
		default:
			return "Error: tool execution denied by user", true
		}
	case ModeTerminal:
		return "Error: tool execution blocked — terminal mode", true
	case ModeYOLO:
		// Auto-approve, skip confirmation callback.
	default: // ModeConfirm
		switch a.onConfirm(block.Name, block.Input) {
		case ConfirmAlways:
			a.allowedTools[block.Name] = true
		case ConfirmAllow:
			// proceed
		default: // ConfirmDeny
			return "Error: tool execution denied by user", true
		}
	}
	return "", false
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

// LastResponse returns the text from the most recent assistant message.
func (a *Agent) LastResponse() string {
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i].Role == api.RoleAssistant {
			var texts []string
			for _, block := range a.messages[i].Content {
				if block.Type == "text" {
					texts = append(texts, block.Text)
				}
			}
			if len(texts) > 0 {
				return strings.Join(texts, "\n")
			}
		}
	}
	return ""
}

// PlanReady returns true if the agent's last response signals a completed plan
// (contains the "Ready to execute" marker from the plan prompt instructions).
func (a *Agent) PlanReady() bool {
	resp := strings.ToLower(a.LastResponse())
	return strings.Contains(resp, "ready to execute")
}

func (a *Agent) SetPermissionMode(m PermissionMode) { a.permMode.Store(int32(m)) }
func (a *Agent) GetPermissionMode() PermissionMode  { return PermissionMode(a.permMode.Load()) }

// SetProvider swaps the provider on the agent (for mid-session model switching).
func (a *Agent) SetProvider(p api.Provider) { a.provider = p; a.lastContextUsed = 0 }

// GetProvider returns the current provider.
func (a *Agent) GetProvider() api.Provider { return a.provider }

// contextUsed returns the estimated context token usage.
func (a *Agent) contextUsed() int { return a.lastContextUsed }

// Registry returns the agent's tool registry for external tool registration.
func (a *Agent) Registry() *tools.Registry { return a.registry }

// AllowedTools returns the set of tool names that have been "always allowed" for this session.
func (a *Agent) AllowedTools() map[string]bool { return a.allowedTools }

// Messages returns the conversation history.
func (a *Agent) Messages() []api.Message { return a.messages }

// SetMessages replaces the conversation history. Used to restore a persisted session.
func (a *Agent) SetMessages(msgs []api.Message) { a.messages = msgs }

// SetAllowedTools replaces the "always allowed" tool set. Used to restore a persisted session.
func (a *Agent) SetAllowedTools(m map[string]bool) { a.allowedTools = m }

// AppendHistory injects a user message and an assistant message into the
// conversation history. This is used by Terminal mode so that shell commands
// and their output are visible to the agent in subsequent turns.
func (a *Agent) AppendHistory(userText, assistantText string) {
	a.messages = append(a.messages,
		api.UserMessage(userText),
		api.Message{Role: api.RoleAssistant, Content: []api.ContentBlock{api.TextBlock(assistantText)}},
	)
}
