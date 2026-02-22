# Cogent

A lightweight coding agent powered by Claude, written in Go.

Cogent runs in your terminal and can read, edit, and create files, execute shell commands, and search your codebase — all with confirmation prompts before any destructive action.

## Setup

Requires Go 1.24+ and an Anthropic API key.

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
```

## Build & Install

```sh
make build      # outputs to bin/cogent
make install    # copies to /usr/local/bin/cogent
```

## Usage

Cogent supports three UI modes, selected with `--ui` or auto-detected:

| Mode | When | Description |
|------|------|-------------|
| `tui` | Interactive TTY, no prompt | Full-screen Bubble Tea interface with scrollable output and inline confirmation |
| `basic` | TTY with a prompt | Original line-based REPL with ANSI colours and `[Y/n]` confirmation |
| `headless` | Piped / CI, prompt given | Single-shot, auto-approves all tool calls, no interaction |

**Interactive TUI (default when no prompt given):**

```sh
cogent
```

**Single prompt (basic mode, with confirmation):**

```sh
cogent "refactor the handler to use middleware"
```

**Force a specific mode:**

```sh
cogent --ui=tui
cogent --ui=basic
cogent --ui=basic "explain this codebase"
cogent --ui=headless "fix the typo in README.md"
```

**Headless in CI / pipes:**

```sh
echo "list all TODOs" | cogent --ui=basic
cogent --ui=headless "run the test suite and fix any failures"
```

## Built-in Tools

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands |
| `read` | Read file contents with line numbers |
| `write` | Write content to a file |
| `edit` | Search-and-replace edit on a file |
| `glob` | Find files matching a pattern |
| `grep` | Search file contents with regex |
| `ls` | List files and directories |

All write operations (`bash`, `write`, `edit`) require explicit confirmation with a diff preview.

## Permission Modes

Cycle through modes with **Shift+Tab** in the TUI:

| Mode | Description |
|------|-------------|
| **Confirm** | Asks before executing destructive tools (default) |
| **Plan** | Read-only — the agent can only read and plan, no writes |
| **YOLO** | Auto-approves all tool calls without asking |
| **Terminal** | Input goes to the shell directly — run commands yourself |

## REPL Commands

Available in **tui** and **basic** modes:

| Command | Description |
|---------|-------------|
| `/help` | Show help |
| `/clear` | Clear conversation history |
| `/restart` | Rebuild and restart the agent (basic only) |
| `/quit` | Exit |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | **(required)** Your Anthropic API key |
| `ANTHROPIC_MODEL` | Model to use (default: set by client) |
| `ANTHROPIC_BASE_URL` | Custom API base URL |
