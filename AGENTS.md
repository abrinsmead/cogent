# Cogent — Agent Guidelines

## Project Overview

Cogent is a lightweight terminal-based coding agent powered by the Anthropic API, written in Go. It provides two UI modes (TUI and headless) and a set of built-in tools for file manipulation and shell access.

## Repository Structure

```
cogent/
├── Makefile              # build, install, clean targets
├── src/
│   ├── main.go           # entry point — flag parsing, mode detection
│   ├── go.mod
│   ├── agent/
│   │   └── agent.go      # core agent loop, permission modes, system prompt, AGENTS.md loading
│   ├── api/
│   │   ├── client.go     # Anthropic HTTP client, model pricing, retry logic
│   │   └── types.go      # request/response types, content blocks, tool definitions
│   ├── cli/
│   │   ├── cli.go        # CLI interface, ANSI helpers, diff rendering
│   │   ├── tui.go        # Bubble Tea full-screen UI (viewport, textarea, status bar)
│   │   └── headless.go   # single-shot, auto-approve, for CI/pipes
│   └── tools/
│       ├── registry.go   # tool registry
│       ├── safepath.go   # path validation (sandbox writes to cwd)
│       ├── walk.go       # directory walker
│       ├── bash.go       # shell command execution with timeout
│       ├── read.go       # read files with line numbers
│       ├── write.go      # write/create files
│       ├── edit.go       # search-and-replace (exact single match)
│       ├── glob.go       # file pattern matching
│       ├── grep.go       # regex search
│       ├── ls.go         # directory listing
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
- Each iteration: send conversation → process response blocks (text or tool_use) → collect tool results → repeat if stop_reason is `tool_use`.
- The system prompt is built once at construction: base prompt + AGENTS.md contents (if found) + per-call plan-mode suffix.
- History is trimmed to 100 messages, keeping the first message plus the most recent.

### Permission Modes

Four modes cycle via Shift+Tab: Confirm → Plan → YOLO → Terminal. Plan mode appends a read-only instruction to the system prompt. Terminal mode routes input directly to the shell.

### API Client (`api/client.go`)

- Non-streaming: single POST to `/v1/messages`, parses full response.
- Retries on 429/529 with exponential backoff (3 attempts).
- Model pricing table for cost tracking (per-MTok rates).
- Base URL validated: must be HTTPS or localhost.

### Tools

Every tool implements `tools.Tool` (Definition, Execute, RequiresConfirmation). Destructive tools (`bash`, `write`, `edit`) require confirmation. `write` and `edit` are path-sandboxed to the working directory via `safepath.go`.

### UI Modes

- **TUI** (`cli/tui.go`): Bubble Tea with viewport + textarea. Async agent calls via goroutine + message channel. Status bar shows model, mode, context, cost, git branch. Accepts an optional initial prompt via CLI args.
- **Headless** (`cli/headless.go`): Single prompt, auto-approve, returns on completion.

## Conventions

- **Go style**: standard library preferred, minimal dependencies. Direct deps: Bubble Tea stack, `x/term`, `rivo/uniseg`.
- **Error handling**: tools return `(string, error)` — errors become tool_result with `is_error: true` so the agent can recover.
- **Security**: `ANTHROPIC_API_KEY` is scrubbed from all subprocess environments. Write paths are validated against the working directory.
- **No streaming**: the API client makes synchronous requests; the TUI updates via a channel of Bubble Tea messages from callbacks.

## Testing

```sh
cd src && go test ./...
```

Currently only `tools/glob_test.go` has tests. When adding tools, add corresponding `_test.go` files.
