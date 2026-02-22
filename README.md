# Cogent

A lightweight coding agent written in Go.

<img width="1440" height="900" alt="image" src="https://github.com/user-attachments/assets/1dfbbbc4-cba7-43f1-9ee8-b214d6bbb449" />

Cogent runs in your terminal and can read, edit, and create files, execute shell commands, and search your codebase — all with confirmation prompts before any destructive action.

## Setup

Requires Go 1.24+ and an Anthropic API key.

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
make build      # → bin/cogent
make install    # → /usr/local/bin/cogent
```

## Usage

```sh
cogent                          # interactive TUI
cogent "explain this codebase"  # TUI with initial prompt
```

When a prompt is given and stdin is not a TTY (e.g. CI), headless mode is used automatically. You can also force it with `--ui`:

```sh
cogent --ui=headless "run the test suite and fix failures"
```

| Mode | Auto-selected when | Description |
|------|------|-------------|
| `tui` | Interactive TTY | Full-screen Bubble Tea interface |
| `headless` | Piped / CI with a prompt | Single-shot, auto-approves all tool calls |

## Tools

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands (configurable timeout, default 2m, max 10m) |
| `read` | Read file contents with line numbers |
| `write` | Create or overwrite a file (creates parent directories) |
| `edit` | Search-and-replace on a file (`old_string` must match exactly once) |
| `glob` | Find files matching a pattern |
| `grep` | Search file contents with regex |
| `ls` | List files and directories |

Destructive tools (`bash`, `write`, `edit`) show a diff preview and require confirmation before executing.

## Permission Modes

Cycle with **Shift+Tab** in the TUI:

| Mode | Behaviour |
|------|-----------|
| **Confirm** | Asks before destructive tools *(default)* |
| **Plan** | Read-only — agent can only observe and suggest |
| **YOLO** | Auto-approves every tool call |
| **Terminal** | Pauses the agent — your input runs as shell commands |

## TUI

### Keyboard

| Key | Action |
|-----|--------|
| **Enter** | Send message / approve tool call |
| **Shift+Tab** | Cycle permission mode |
| **Ctrl+C** | Interrupt running agent, or quit when idle |
| **PgUp / PgDn** | Scroll output |
| **Mouse wheel** | Scroll output |
| **y / n** | Approve or deny at confirmation prompts |

The input area auto-grows as you type (up to 10 lines).

### Status Bar

The bottom bar shows: model name, permission mode, context tokens used, cost (last + total), working directory, and git branch with dirty indicator.

### Commands

| Command | Description |
|---------|-------------|
| `/help` | Show help |
| `/clear` | Clear conversation history |
| `/quit` | Exit |

## AGENTS.md

Cogent supports the [`AGENTS.md` convention](https://agents.md/). Any `AGENTS.md` file found in the working directory or a parent directory is appended to the system prompt at startup — giving the agent project-specific context without you having to repeat it each session.

All files from cwd to the filesystem root are collected and concatenated root-first, so in a monorepo the top-level file provides broad context and deeper files add local specifics:

```
monorepo/
├── AGENTS.md          # repo-wide (loaded first)
├── services/
│   └── api/
│       └── AGENTS.md  # package-specific (loaded second)
```

## Configuration

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | **(required)** Anthropic API key |
| `ANTHROPIC_MODEL` | Model (default: `claude-opus-4-6`) |
| `ANTHROPIC_BASE_URL` | Custom API base URL |

## Custom Tools

Cogent supports user-defined tools as executable scripts. Place them in `.cogent/tools/` (project-local) or `~/.cogent/tools/` (global). Project-local tools take precedence over global ones with the same name.

### Creating a Tool

Any executable file with a `@tool` directive in its comments is picked up automatically. The script receives input as JSON on stdin and writes output to stdout.

```bash
#!/bin/bash
# @tool greet
# @description Say hello to someone
# @param name string required "Who to greet"
# @noconfirm

INPUT=$(cat)
NAME=$(echo "$INPUT" | sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
echo "Hello, $NAME!"
```

```sh
chmod +x .cogent/tools/greet
```

Scripts can be written in any language — bash, Python, Node, etc. — as long as they have a shebang and are executable.

### Directives

| Directive | Description |
|-----------|-------------|
| `@tool <name>` | **(required)** Tool name as exposed to the agent |
| `@description <text>` | What the tool does (shown to the model) |
| `@param <name> <type> [required] "<desc>"` | Input parameter. Type is `string`, `number`, or `boolean` |
| `@env <VAR> [required] "<desc>"` | Environment variable dependency. Tools with missing required env vars are skipped |
| `@confirm` | Require user confirmation before running *(default)* |
| `@noconfirm` | Run without confirmation (for read-only tools) |

Comment prefixes `#`, `//`, and `--` are all recognized, so the directive format works in bash, Go/JS, SQL, and similar languages.

### Environment Variables

Create a `.cogent/.env` file to set environment variables for custom tools:

```
LINEAR_API_KEY=lin_api_...
LINEAR_USERNAME=jane.doe
```

Variables are loaded before tool discovery, so `@env required` checks will see them. Explicit environment variables take precedence — `.env` only sets values that aren't already defined.

### Bundled Examples

This project includes several custom tools in `.cogent/tools/`:

| Tool | Description |
|------|-------------|
| `btc_price` | Get the current Bitcoin spot price via the Coinbase API |
| `linear_tickets` | List Linear tickets assigned to a user |
| `linear_ticket` | Get full details of a Linear ticket by ID |
| `linear_update_ticket` | Update a Linear ticket's status, title, priority, or assignee |

## Not Yet Implemented

Cogent is deliberately minimal. Things it doesn't do (yet):

- **MCP (Model Context Protocol)** — no support for external tool servers
- **Custom slash commands** — the only commands are `/help`, `/clear`, `/quit`
- **Session resume** — conversation history is in-memory only, lost on exit
- **Streaming** — responses arrive in full, not token-by-token
- **Multi-model / sub-agents** — single model, single agent loop
- **Image & vision input** — text only
- **Configurable system prompt** — hardcoded (aside from `AGENTS.md` injection)
- **Persistent memory** — no cross-session recall

## Security

- `ANTHROPIC_API_KEY` is scrubbed from the environment of all subprocesses.
- `write` and `edit` are sandboxed to the current working directory.
- `ANTHROPIC_BASE_URL` must use HTTPS unless the host is localhost.
