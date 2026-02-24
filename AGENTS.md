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
│       ├── safepath.go   # path validation (sandbox writes to cwd, resolves symlinks)
│       ├── walk.go       # directory walker (skips .git, node_modules, vendor, __pycache__, dot-dirs)
│       ├── bash.go       # shell command execution with timeout
│       ├── read.go       # read files with line numbers
│       ├── write.go      # write/create files
│       ├── edit.go       # search-and-replace (exact single match)
│       ├── glob.go       # file pattern matching
│       ├── grep.go       # regex search
│       ├── ls.go         # directory listing
│       ├── dispatch.go   # sub-agent delegation tool (concurrent)
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
- Each iteration: send conversation → process response blocks (text, thinking, compaction, or tool_use) → collect tool results → repeat if stop_reason is `tool_use`.
- The system prompt is built once at construction: base prompt (with dynamic env description + cwd) + shell guidance + AGENTS.md contents (if found) + custom tool instructions (if `.cogent/tools/` exists).
- `loadAgentsMD` walks **up** from cwd to filesystem root collecting all AGENTS.md files, concatenates them root-first (outermost context first).
- Plan mode appends a read-only instruction (`planPrompt`) to the system prompt at each iteration.
- Server-side context compaction replaces client-side history trimming — the API automatically compacts when input tokens reach 80% of the context window.
- On `stop_reason: "compaction"`, the loop continues to let the model resume with compacted context.
- On `stop_reason: "max_tokens"`, the loop sends a "Continue from where you left off." message.

#### Tool Execution

- **Two-phase execution**: Phase 1 confirms all tools sequentially. Phase 2 runs confirmed tools — tools implementing `ConcurrentTool` (e.g., dispatch) run in parallel goroutines, others run sequentially. Results collected in original order.
- **Denial cascading**: when a user denies a tool, all subsequent tools in the same batch are skipped with an error.
- **Always-allow**: users can press `a` during confirmation to always allow a specific tool for the rest of the session (`allowedTools` map).

### Environment Detection (`agent/env.go`)

- `envDescription()` generates a concise runtime description for the system prompt (e.g. "macOS (arm64, zsh)", "Linux/Alpine 3.19 (amd64, BusyBox ash)").
- `shellGuidance()` returns extra POSIX-compatibility guidelines when the default shell is not bash (BusyBox ash, dash, etc.).
- Detects OS, architecture, Linux distro (via `/etc/os-release`), and shell (BusyBox check → `$SHELL` → probing).

### Permission Modes

Four modes cycle via Shift+Tab: Confirm → YOLO → Plan → Terminal.

- **Confirm**: destructive tools require user confirmation.
- **Plan**: extended thinking enabled, read-only tools (read/glob/grep/ls) allowed freely, bash allowed with confirmation, write/edit/dispatch blocked. The agent explores the codebase, asks clarifying questions, and presents a structured plan.
- **YOLO**: auto-approves everything.
- **Terminal**: user input runs as shell commands via `sh -c`; agent tools blocked. Shell I/O is injected into agent conversation history via `AppendHistory` so the agent sees what was run when switching back.

### Extended Thinking

- Plan mode enables extended thinking via the `thinking` API parameter (`budget_tokens: 10000`).
- The API returns `thinking` content blocks (with opaque `signature` field required by the API) which are displayed as collapsed summaries in the TUI.
- When thinking is enabled, `max_tokens` is set to `budget_tokens + 8192` if the default 16384 would be too small.
- The `ThinkingConfig` type in `api/types.go` controls this; the `ContentBlock` type handles `thinking` blocks.

### API Client (`api/client.go`)

- Non-streaming: single POST to `/v1/messages`, parses full response. HTTP timeout: 5 minutes.
- Retries on 429/529 with exponential backoff (3 attempts, starting at 2s, doubling each retry).
- Model pricing table for cost tracking (per-MTok rates). Default model: `claude-opus-4-6`. Unknown models fall back to Sonnet-tier pricing (200k context, $3/$0.30/$15).
- Cache creation charged at 1.25× input price.
- Base URL validated: must be HTTPS or localhost. Env vars: `ANTHROPIC_API_KEY` (required), `ANTHROPIC_BASE_URL`, `ANTHROPIC_MODEL`.
- Context compaction configured via `context_management` field (type `compact_20260112`) — triggers at 80% of context window (min 50k tokens). Beta header `Anthropic-Beta: compact-2026-01-12` sent with every request.
- System prompt sent as a content block array with `cache_control: ephemeral`.
- `max_tokens` set to 16384 (automatically increased when extended thinking is enabled).

### Deterministic JSON Serialization (`api/types.go`)

- `ContentBlock.MarshalJSON` and `ToolInputSchema.MarshalJSON` produce deterministic JSON output by sorting map keys.
- Critical for Anthropic's prompt caching, which uses exact byte matching — random Go map iteration order would break cache hits between requests.
- `orderedMap` helper serializes `map[string]any` with sorted keys.
- `ContentBlock.MarshalJSON` handles five block types: `tool_use`, `tool_result`, `thinking`, `compaction`, and text (default).

### Tools

Every tool implements `tools.Tool` (Definition, Execute, RequiresConfirmation). Destructive tools (`bash`, `write`, `edit`, `dispatch`) require confirmation. `write` and `edit` are path-sandboxed to the working directory via `safepath.go` (resolves symlinks to prevent escaping).

Tools implementing `ConcurrentTool` interface (`IsConcurrent() bool`) can execute in parallel when the API returns multiple tool calls. Currently only `dispatch` is concurrent.

#### Built-in Tool Details

| Tool | Confirmation | Key Limits |
|---|---|---|
| `bash` | Yes | 120s default timeout (max 600s), 30k char output, runs via `sh -c`, non-zero exit appended as text (not error) |
| `read` | No | 2000-line default limit, lines >2000 chars truncated, 1-indexed offset |
| `write` | Yes | Path-sandboxed, creates parent dirs (0755), writes file (0644) |
| `edit` | Yes | Path-sandboxed, exactly 1 match required, old_string ≠ new_string, preserves file permissions |
| `glob` | No | Max 1000 results, sorted newest-modified first, supports `**/` recursive |
| `grep` | No | Max 500 matches, regex pattern, skips binary files (null byte in first 512 bytes) |
| `ls` | No | Shows kind (d/-), size, name; returns "(empty)" for empty dirs |
| `dispatch` | Yes | Concurrent, SpawnFunc injected by TUI post-construction |

#### Directory Walker (`walk.go`)

Skips: `.git`, `node_modules`, `vendor`, `__pycache__`, and any dot-prefixed directory (except the root itself).

### Dispatch Tool (`tools/dispatch.go`)

- Delegates subtasks to sub-agent sessions running in separate contexts.
- Implements `ConcurrentTool` — multiple dispatch calls in one batch run in parallel.
- `SpawnFunc` is injected by the TUI after session creation — the tool checks for nil at execution time.
- Sub-agents inherit the parent's permission mode and run to completion, returning their final text output.
- Each sub-agent gets its own tab in the TUI, prefixed with "(sub-agent)".
- Not available in headless mode (dispatch tool not registered).
- Requires confirmation by default.

### Custom Tools (`tools/custom.go`)

- User-defined tools are executable scripts with `@` directives in comments (`@tool`, `@description`, `@param`, `@env`, `@confirm`/`@noconfirm`).
- Supports comment prefixes: `#`, `//`, `--` (bash, Go/JS/C, SQL/Lua).
- Discovered from `.cogent/tools/` (project-local, takes precedence) and `~/.cogent/tools/` (global).
- `.cogent/.env` is loaded before discovery via `LoadDotEnv` (supports single/double quote stripping) so `@env required` checks see the values. Project-local `.env` takes precedence over global; explicit env vars always win.
- Scripts receive JSON on stdin, return output on stdout. 120s timeout, 30k char output limit.
- `ANTHROPIC_API_KEY` is scrubbed from the subprocess environment.
- Custom tools require confirmation by default (`@confirm`); read-only tools use `@noconfirm`.

### Registry (`tools/registry.go`)

- `NewRegistry(cwd)` creates registry and registers 7 built-in tools (bash, read, write, edit, glob, grep, ls). Dispatch is registered externally by the session.
- `RegisterTool(t)` returns `false` if name already taken (no-op, no warning).
- `Definitions()` returns all tool defs sorted alphabetically.
- `Warnings()` returns any custom tool loading warnings.

### UI Modes

- **TUI** (`cli/tui.go`, `cli/session.go`): Bubble Tea with viewport + textarea. Supports multiple sessions as tabs. Async agent calls via goroutine + message channel. Animated splash screen on startup (dismissible with any key).

  - **Status bar** shows: model, context used/total, total cost (USD), cwd (shortened), git branch with dirty indicator. Git branch read directly from `.git/HEAD` (no exec).

  - **HUD modes** (cycle with Ctrl+H): status bar (default) → overlay (floating top-right box with color-coded context: green <50%, yellow 50–80%, red ≥80%) → off.

  - **Tab bar** with colored dot indicators: yellow = running, red = needs attention (confirmation), blue = running sub-agent, green = completed sub-agent.

  - **Animated prompt dots** while agent is running (cycling every 400ms): "thinking..." / "planning... (extended thinking)" / "running..." depending on mode.

  - **Key bindings**:
    - Ctrl+T: new tab, Ctrl+W: close tab
    - Tab: focus tab bar (←/→ navigate, Enter select, Esc return)
    - Shift+←/→: instant tab switch (without focusing tab bar)
    - Alt+1..9: jump to tab by number
    - Shift+Tab: cycle permission mode
    - Ctrl+H: cycle HUD mode
    - Ctrl+C: interrupt agent (running) / quit (idle)
    - PgUp/PgDn: scroll viewport page, ↑/↓: scroll 3 lines (when not in input)
    - y/n/a/Enter: allow / deny / always-allow tool during confirmation

  - **Commands**: `/help`, `/clear`, `/quit` (also `/exit`, `/q`), `/close`, `/rename <name>`, `/sessions`.

  - **Sub-agent tabs**: spawned by dispatch, prefixed with "(sub-agent)", colored dot for state.

  - **Desktop notifications**: OSC 9 + terminal bell on confirmation needed and session completion.

  - **Mouse**: scroll wheel on viewport (3 lines per tick).

  - Accepts an optional initial prompt via CLI args.

- **Headless** (`cli/headless.go`): Single prompt, auto-approve, returns on completion. No dispatch tool (no sub-agent support).

## Conventions

- **Go style**: standard library preferred, minimal dependencies. Direct deps: Bubble Tea stack (`bubbles`, `bubbletea`, `lipgloss`, `x/ansi`), `x/term`, `rivo/uniseg`.
- **Error handling**: tools return `(string, error)` — errors become tool_result with `is_error: true` so the agent can recover.
- **Security**: `ANTHROPIC_API_KEY` is scrubbed from all subprocess environments (bash tool, custom tools, terminal mode). Write paths are validated against the working directory with symlink resolution.
- **No streaming**: the API client makes synchronous requests; the TUI updates via a channel of Bubble Tea messages from callbacks.
- **Deterministic serialization**: all JSON output from `ContentBlock` and `ToolInputSchema` uses sorted keys for cache stability.

## Testing

```sh
cd src && go test ./...
```

Test files: `tools/glob_test.go`, `tools/custom_test.go`, `agent/env_test.go`. When adding tools or modules, add corresponding `_test.go` files.
