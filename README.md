# Cogent

A lightweight coding agent for the terminal.

<img width="1440" height="876" alt="image" src="https://github.com/user-attachments/assets/03a85436-adc8-4182-9827-54cbd8744000" />

Cogent runs in your terminal and can read, edit, and create files, execute shell commands, delegate subtasks to sub-agents, and search your codebase.

## Setup

Requires an Anthropic API key.

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
make install    # → /usr/local/bin/cogent
```

## Usage

```sh
cogent                          # interactive TUI
cogent "explain this codebase"  # TUI with initial prompt
```

When a prompt is given and stdin is not a TTY (e.g. CI), headless mode is used automatically. You can also force it:

```sh
cogent --headless "run the test suite and fix failures"
```

| Mode | Auto-selected when | Description |
|------|------|-------------|
| `tui` | Interactive TTY | Full-screen terminal UI |
| `headless` | Piped / CI with a prompt | Single-shot, auto-approves all tool calls |

## Features

### Built-in Tools

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands (configurable timeout, default 2m, max 10m) |
| `read` | Read file contents with line numbers |
| `write` | Create or overwrite a file (creates parent directories) |
| `edit` | Search-and-replace on a file (`old_string` must match exactly once) |
| `glob` | Find files matching a pattern |
| `grep` | Search file contents with regex |
| `ls` | List files and directories |
| `dispatch` | Delegate a subtask to a sub-agent running in a separate context |

Destructive tools (`bash`, `write`, `edit`, `dispatch`) require confirmation before executing. `write` and `edit` show a diff preview at the confirmation prompt.

### Sub-agents

The `dispatch` tool lets the agent delegate subtasks to independent sub-agents. Each sub-agent runs in its own context window with the full set of tools, gets its own tab in the TUI (shown in blue with a colored status dot), and returns its final output to the parent when done. Useful for parallelizing work or isolating tasks that benefit from a fresh context.

### Permission Modes

Cycle with **Shift+Tab** in the TUI — works both when idle **and while the agent is running**, so you can switch from YOLO back to Confirm mid-execution if the agent starts doing something you want to review:

| Mode | Behaviour |
|------|-----------|
| **Plan** | Extended thinking enabled — agent explores, asks clarifying questions, and produces a structured plan. Bash allowed (with confirmation), but write/edit/dispatch are blocked. *(default)* |
| **Confirm** | Asks before destructive tools |
| **YOLO** | Auto-approves every tool call |
| **Terminal** | Pauses the agent — your input runs as shell commands |

The mode change takes effect immediately — the very next tool call will use the new mode.

### Custom Tools

Cogent supports user-defined tools as executable scripts. Place them in `.cogent/tools/` (project-local) or `~/.cogent/tools/` (global). Project-local tools take precedence over global ones with the same name.

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

#### Directives

| Directive | Description |
|-----------|-------------|
| `@tool <name>` | **(required)** Tool name as exposed to the agent |
| `@description <text>` | What the tool does (shown to the model) |
| `@param <name> <type> [required] "<desc>"` | Input parameter. Type is `string`, `number`, or `boolean` |
| `@env <VAR> [required] "<desc>"` | Environment variable dependency. Tools with missing required env vars are skipped |
| `@confirm` | Require user confirmation before running *(default)* |
| `@noconfirm` | Run without confirmation (for read-only tools) |

Comment prefixes `#`, `//`, and `--` are all recognized, so the directive format works in bash, Go/JS, SQL, and similar languages.

#### Environment Variables

Create a `.cogent/.env` file to set environment variables for custom tools:

```
LINEAR_API_KEY=lin_api_...
LINEAR_USERNAME=jane.doe
```

Variables are loaded before tool discovery, so `@env required` checks will see them. Explicit environment variables take precedence — `.env` only sets values that aren't already defined.

### TUI

#### Keyboard Shortcuts

| Key | Action |
|-----|--------|
| **Enter** | Send message / approve tool call |
| **Shift+Tab** | Cycle permission mode (works while idle or running) |
| **Ctrl+C** | Interrupt running agent, or quit when idle |
| **Ctrl+T** | New session tab |
| **Ctrl+W** | Close current session tab |
| **Shift+←/→** | Switch tabs |
| **Alt+1..9** | Jump to tab by number |
| **Tab** | Focus tab bar (←/→ to navigate, Enter to select, Esc to return) |
| **Ctrl+H** | Cycle HUD mode (status bar → overlay → off) |
| **PgUp / PgDn** | Scroll output |
| **↑ / ↓** | Scroll output (while agent is running) |
| **Mouse wheel** | Scroll output |
| **y / n / a** | Approve, deny, or always allow at confirmation prompts |

The input area auto-grows as you type (up to 10 lines).

#### Commands

| Command | Description |
|---------|-------------|
| `/help` | Show help |
| `/clear` | Clear conversation history |
| `/rename <name>` | Rename the current session tab |
| `/sessions` | List all sessions |
| `/close` | Close the current session |
| `/quit` | Exit (also `/exit`, `/q`) |

#### Status Bar

The status bar at the bottom of the TUI shows:

```
 mod claude-opus-4-6  |  ctx 24k/200k  |  usd $0.45  |  pwd ~/Projects/foo  |  git main*
```

| Field | Description |
|-------|-------------|
| **mod** | Active Anthropic model |
| **ctx** | Context window usage: tokens used / model max |
| **usd** | Cumulative cost for the session |
| **pwd** | Working directory (shortened) |
| **git** | Git branch, with `*` if there are uncommitted changes |

The permission mode is displayed as a badge above the input area, not in the status bar. You can cycle it with Shift+Tab.

### AGENTS.md

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

## Security

- `ANTHROPIC_API_KEY` is scrubbed from the environment of all subprocesses.
- `write` and `edit` are sandboxed to the current working directory.
- `ANTHROPIC_BASE_URL` must use HTTPS unless the host is localhost.
