# Cogent — Agent Guidelines

## Project Overview

Cogent is a lightweight terminal-based coding agent powered by the Anthropic API, written in Go. It provides two UI modes (TUI and headless) and a set of built-in tools for file manipulation, shell access, and sub-agent delegation.

## Repository Structure

```
cogent/
├── Makefile              # build, install, clean targets
├── .cogent/
│   ├── .env              # env vars for custom tools (gitignored)
│   └── tools/            # project-local custom tool scripts
├── src/
│   ├── main.go           # entry point — flag parsing, mode detection
│   ├── go.mod
│   ├── agent/
│   │   ├── agent.go      # core agent loop, permission modes, system prompt, AGENTS.md loading
│   │   ├── env.go        # runtime environment detection (OS, arch, shell, distro)
│   │   └── env_test.go   # tests for env detection
│   ├── api/
│   │   ├── client.go     # Anthropic HTTP client, model pricing, retry logic, context compaction
│   │   └── types.go      # request/response types, content blocks, tool definitions, deterministic JSON
│   ├── cli/
│   │   ├── cli.go        # CLI interface, ANSI helpers, diff rendering
│   │   ├── tui.go        # Bubble Tea full-screen UI (viewport, textarea, status bar, tab bar)
│   │   ├── session.go    # per-session state (agent, viewport, input, sub-agent support)
│   │   └── headless.go   # single-shot, auto-approve, for CI/pipes
│   └── tools/
│       ├── registry.go   # tool registry with RegisterTool for post-construction additions
│       ├── safepath.go   # path validation (sandbox writes to cwd)
│       ├── walk.go       # directory walker
│       ├── bash.go       # shell command execution with timeout
│       ├── read.go       # read files with line numbers
│       ├── write.go      # write/create files
│       ├── edit.go       # search-and-replace (exact single match)
│       ├── glob.go       # file pattern matching
│       ├── grep.go       # regex search
│       ├── ls.go         # directory listing
│       ├── dispatch.go   # sub-agent delegation tool
│       ├── custom.go     # custom tool loader, .env parser, @ directive parser
│       ├── custom_test.go
│       └── glob_test.go  # tests for glob tool
└── bin/                  # build output (gitignored)
```

## Build & Run

```sh
make build    # → bin/cogent
make install  # → /usr/local/bin/cogent
make clean
```

Requires Go 1.24+ and `ANTHROPIC_API_KEY` set.

## Architecture

### Agent Loop (`agent/agent.go`)

- The agent sends messages to the API in a loop (max 50 iterations).
- Each iteration: send conversation → process response blocks (text, compaction, or tool_use) → collect tool results → repeat if stop_reason is `tool_use`.
- The system prompt is built once at construction: base prompt (with dynamic env description) + shell guidance + AGENTS.md contents (if found) + custom tool instructions (if `.cogent/tools/` exists).
- Plan mode appends a read-only instruction to the system prompt at each iteration.
- Server-side context compaction replaces client-side history trimming — the API automatically compacts when input tokens reach 80% of the context window.
- On `stop_reason: "compaction"`, the loop continues to let the model resume with compacted context.
- On `stop_reason: "max_tokens"`, the loop sends a "Continue from where you left off." message.

### Environment Detection (`agent/env.go`)

- `envDescription()` generates a concise runtime description for the system prompt (e.g. "macOS (arm64, zsh)", "Linux/Alpine 3.19 (amd64, BusyBox ash)").
- `shellGuidance()` returns extra POSIX-compatibility guidelines when the default shell is not bash (BusyBox ash, dash, etc.).
- Detects OS, architecture, Linux distro (via `/etc/os-release`), and shell (BusyBox check → `$SHELL` → probing).

### Permission Modes

Four modes cycle via Shift+Tab: Confirm → Plan → YOLO → Terminal.

- **Confirm**: destructive tools require user confirmation.
- **Plan**: extended thinking enabled, bash allowed with confirmation, write/edit/dispatch blocked. The agent explores the codebase, asks clarifying questions, and presents a structured plan for the user to approve before switching to Confirm mode to execute.
- **YOLO**: auto-approves everything.
- **Terminal**: user input runs as shell commands; agent tools blocked.

### Extended Thinking

- Plan mode enables extended thinking via the `thinking` API parameter (`budget_tokens: 10000`).
- The API returns `thinking` content blocks which are displayed as collapsed summaries in the TUI.
- When thinking is enabled, `max_tokens` is automatically increased to accommodate the thinking budget plus response tokens.
- The `ThinkingConfig` type in `api/types.go` controls this; the `ContentBlock` type handles `thinking` blocks.

### API Client (`api/client.go`)

- Non-streaming: single POST to `/v1/messages`, parses full response.
- Retries on 429/529 with exponential backoff (3 attempts).
- Model pricing table for cost tracking (per-MTok rates). Default model: `claude-opus-4-6`.
- Base URL validated: must be HTTPS or localhost.
- Context compaction configured via `context_management` field — triggers at 80% of context window (min 50k tokens).
- System prompt sent as a content block array with `cache_control: ephemeral`.
- `max_tokens` set to 16384 (automatically increased when extended thinking is enabled).

### Deterministic JSON Serialization (`api/types.go`)

- `ContentBlock.MarshalJSON` and `ToolInputSchema.MarshalJSON` produce deterministic JSON output by sorting map keys.
- Critical for Anthropic's prompt caching, which uses exact byte matching — random Go map iteration order would break cache hits between requests.
- `orderedMap` helper serializes `map[string]any` with sorted keys.

### Tools

Every tool implements `tools.Tool` (Definition, Execute, RequiresConfirmation). Destructive tools (`bash`, `write`, `edit`, `dispatch`) require confirmation. `write` and `edit` are path-sandboxed to the working directory via `safepath.go`.

### Dispatch Tool (`tools/dispatch.go`)

- Delegates subtasks to sub-agent sessions running in separate contexts.
- `SpawnFunc` is injected by the TUI after session creation — the tool checks for nil at execution time.
- Sub-agents inherit the parent's permission mode and run to completion, returning their final text output.
- Each sub-agent gets its own tab in the TUI, prefixed with "⤵".
- Requires confirmation by default.

### Custom Tools (`tools/custom.go`)

- User-defined tools are executable scripts with `@` directives in comments (`@tool`, `@description`, `@param`, `@env`, `@confirm`/`@noconfirm`).
- Discovered from `.cogent/tools/` (project-local, takes precedence) and `~/.cogent/tools/` (global).
- `.cogent/.env` is loaded before discovery via `LoadDotEnv` so `@env required` checks see the values. Project-local `.env` takes precedence over global; explicit env vars always win.
- Scripts receive JSON on stdin, return output on stdout. 120s execution timeout.
- `ANTHROPIC_API_KEY` is scrubbed from the subprocess environment.
- Custom tools require confirmation by default (`@confirm`); read-only tools use `@noconfirm`.
- Registry warns on duplicate tool names (built-in or previously registered).

### UI Modes

- **TUI** (`cli/tui.go`, `cli/session.go`): Bubble Tea with viewport + textarea. Supports multiple sessions as tabs. Async agent calls via goroutine + message channel. Status bar shows model, mode, context (with cache stats), last/total cost, cwd, git branch. Tab bar with visual state indicators (running, needs attention, sub-agent, done).
  - **Sessions/Tabs**: Ctrl+T new, Ctrl+W close, Tab to focus tab bar (←/→ navigate, Enter select, Esc return), Alt+1..9 jump by number.
  - **Commands**: `/help`, `/clear`, `/quit`, `/close`, `/rename <name>`, `/sessions`.
  - **Sub-agent tabs**: spawned by the dispatch tool, show "⤵" prefix, marked "✓" on completion.
  - Accepts an optional initial prompt via CLI args.
- **Headless** (`cli/headless.go`): Single prompt, auto-approve, returns on completion.

## Conventions

- **Go style**: standard library preferred, minimal dependencies. Direct deps: Bubble Tea stack, `x/term`, `rivo/uniseg`.
- **Error handling**: tools return `(string, error)` — errors become tool_result with `is_error: true` so the agent can recover.
- **Security**: `ANTHROPIC_API_KEY` is scrubbed from all subprocess environments. Write paths are validated against the working directory.
- **No streaming**: the API client makes synchronous requests; the TUI updates via a channel of Bubble Tea messages from callbacks.
- **Deterministic serialization**: all JSON output from `ContentBlock` and `ToolInputSchema` uses sorted keys for cache stability.

## Testing

```sh
cd src && go test ./...
```

Test files: `tools/glob_test.go`, `tools/custom_test.go`, `agent/env_test.go`. When adding tools or modules, add corresponding `_test.go` files.
