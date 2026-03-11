# Cogent — Agent Guidelines

Lightweight terminal coding agent with multi-provider support (Anthropic, OpenAI, Gemini, OpenRouter), written in Go.

## Repository Structure

```
src/
├── main.go              # entry point — flag parsing, mode detection
├── agent/               # core agent loop, permission modes, system prompt, env detection
├── api/                 # provider clients (Anthropic, OpenAI, Gemini, OpenRouter), types, compaction
├── cli/                 # TUI (Bubble Tea), REPL, headless mode, session persistence, Linear integration
├── config/              # settings loader (~/.cogent/settings, .cogent/settings)
└── tools/               # tool registry, built-in tools, custom tool loader, path sandboxing
```

## Build & Test

```sh
make build    # → bin/cogent
make install  # → /usr/local/bin/cogent
cd src && go test ./...
```

Requires Go 1.24+.

## Key Conventions

- **Standard library preferred** — minimal deps. Direct deps: Bubble Tea stack (`bubbles`, `bubbletea`, `lipgloss`, `x/ansi`), `x/term`, `rivo/uniseg`.
- **Tools** return `(string, error)` — errors become `tool_result` with `is_error: true` so the agent can recover. Every tool implements `tools.Tool` (Definition, Execute, RequiresConfirmation).
- **Path sandboxing**: `write` and `edit` are sandboxed to cwd via `safepath.go` (resolves symlinks to prevent escaping).
- **Security**: API keys are scrubbed from all subprocess environments (bash, custom tools, terminal mode).
- **Deterministic JSON**: `ContentBlock` and `ToolInputSchema` marshal with sorted map keys — critical for Anthropic's prompt caching (exact byte matching).
- **No streaming**: synchronous API requests; TUI updates via Bubble Tea message channel from callbacks.
- **Directory walker** (`walk.go`): skips `.git`, `node_modules`, `vendor`, `__pycache__`, and dot-prefixed directories.

## Architecture Notes

- **Agent loop** (`agent/agent.go`): sends messages in a loop (max 50 iterations). Processes text, thinking, compaction, and tool_use blocks. Continues on `compaction` and `max_tokens` stop reasons.
- **Two-phase tool execution**: Phase 1 confirms all tools sequentially. Phase 2 runs them — `ConcurrentTool` implementations (dispatch) run in parallel, others sequentially.
- **Permission modes** cycle via Shift+Tab: Plan → Confirm → YOLO → Terminal. Plan mode enables extended thinking and blocks write/edit/dispatch.
- **Dispatch** delegates to sub-agents in separate contexts. Sub-agents run as tab-less goroutines with confirmations routed to the parent tab.
- **Custom tools**: executable scripts in `.cogent/tools/` or `~/.cogent/tools/` with `@tool`/`@description`/`@param`/`@env`/`@confirm` directives.
- **Settings precedence** (highest → lowest): env vars → project `.cogent/settings` → global `~/.cogent/settings` → project `.cogent/.env` → global `~/.cogent/.env`.
- **Providers**: Anthropic uses server-side compaction; OpenAI/Gemini/OpenRouter use client-side hybrid compaction. Each implements `api.Provider`.

## Testing

Test files: `tools/glob_test.go`, `tools/custom_test.go`, `agent/env_test.go`, `api/openai_test.go`. Add `_test.go` files when adding tools or modules.
