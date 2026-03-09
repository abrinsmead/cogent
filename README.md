> [!CAUTION]
> This agent can execute code, modify files, and run shell commands. Use at your own risk.

# Cogent

A lightweight coding agent for the terminal with tabbed sessions and other niceties.

![CleanShot 2026-02-28 at 20 05 01](https://github.com/user-attachments/assets/08ad8952-7a3b-4396-8a27-e19cbeda6e58)

Cogent runs in your terminal and can read, edit, and create files, execute shell commands, delegate subtasks to sub-agents, and search your codebase.

## Setup

Requires at least one provider API key.

```sh
export ANTHROPIC_API_KEY="sk-ant-..."   # and/or
export OPENAI_API_KEY="sk-..."          # and/or
export GEMINI_API_KEY="..."             # and/or
export OPENROUTER_API_KEY="sk-or-..."
make install    # → /usr/local/bin/cogent
```

Or store keys persistently in `~/.cogent/settings` (global) or `.cogent/settings` (project-local, discovered by walking up from the current directory):

```
ANTHROPIC_API_KEY=sk-ant-...
COGENT_MODEL=openrouter/anthropic/claude-sonnet-4
```

Project-local settings override global settings. Explicit environment variables always take precedence over both.

## Usage

```sh
cogent                                    # interactive TUI (default)
cogent tui --prompt "explain this"        # TUI with initial prompt
cogent repl                               # interactive REPL (no full-screen UI)
cogent agent --prompt "fix the test"      # headless, auto-approves everything
```

`--prompt` is available on all three commands and sends an initial prompt immediately on startup.

| Mode | Command | Description |
|------|---------|-------------|
| `tui` | `cogent` or `cogent tui` | Full-screen terminal UI *(default)* |
| `repl` | `cogent repl` | Interactive REPL without full-screen UI |
| `agent` | `cogent agent --prompt "..."` | Single-shot, auto-approves all tool calls |

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
| `web_search` | Search the web for up-to-date information (Anthropic only — server-side tool) |

Destructive tools (`bash`, `write`, `edit`, `dispatch`) require confirmation before executing. `write` and `edit` show a diff preview at the confirmation prompt.

### Sub-agents

The `dispatch` tool lets the agent delegate subtasks to independent sub-agents. Each sub-agent runs in its own context window with the full set of tools (except dispatch — no recursion), and returns its final output to the parent when done. Sub-agents run as background goroutines without their own tabs — confirmations for destructive tools are routed to the parent session's tab. Useful for parallelizing work or isolating tasks that benefit from a fresh context.

### Permission Modes

Cycle with **Shift+Tab** in the TUI — works both when idle **and while the agent is running**, so you can switch from YOLO back to Confirm mid-execution if the agent starts doing something you want to review:

| Mode | Behaviour |
|------|-----------|
| **Plan** | Extended thinking enabled — agent explores, asks clarifying questions, and produces a structured plan. Bash allowed (with confirmation), but write/edit/dispatch are blocked. When a plan is complete, the TUI prompts "Switch to Confirm mode and execute?" — pressing Y auto-switches and begins execution. *(default)* |
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

### REPL

The REPL provides an interactive non-full-screen interface with inline confirmations and session persistence. Supported commands are `/help`, `/clear`, `/quit`, `/mode`, `/model`, `/resume`, and `/cost`.

### TUI

The TUI auto-restores previously open tabs from `.cogent/sessions/`, supports a persistent HUD mode (`Ctrl+H`), and includes a task browser for Linear when `LINEAR_API_KEY` is configured.

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
| **Ctrl+M** | Cycle through configured models (see `COGENT_MODELS`) |
| **Ctrl+H** | Cycle HUD mode (status bar → overlay → off; persists across sessions) |
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
| `/model [provider/model]` | Show or switch the current model |
| `/rename <name>` | Rename the current session tab |
| `/sessions` | List all open sessions |
| `/resume [number\|name]` | Resume a saved session that is not currently open |
| `/close` | Close the current session |
| `/tasks` | Browse tasks (also `/linear`, `/lin`) |
| `/quit` | Exit (also `/exit`, `/q`) |

#### Status Bar

The status bar at the bottom of the TUI shows:

```
 mod openai/gpt-4o  |  ctx 24k/128k  |  usd $0.45  |  pwd ~/Projects/foo  |  git main* +12/-3
```

| Field | Description |
|-------|-------------|
| **mod** | Active provider and model (e.g. `anthropic/claude-opus-4-6`, `openai/gpt-4o`) |
| **ctx** | Context window usage: tokens used / model max |
| **usd** | Cumulative cost for the session |
| **pwd** | Working directory (shortened) |
| **git** | Git branch, with `*` if there are uncommitted changes and `+N/-M` line change counts |

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

### Session Persistence

Sessions are saved to `.cogent/sessions/` and restored differently by UI mode:

- **TUI**: open tabs are auto-restored on next launch. Closing a tab keeps the session on disk so it can be reopened with `/resume`. Closing the last tab deletes that session file.
- **REPL**: the current session is saved on prompt completion, on `/quit`, and on EOF; `/resume` restores a saved session into the current REPL.

In the TUI, use `/resume` to list saved sessions that aren't currently open, and `/resume <number>` or `/resume <name>` to restore one as a new tab.

## Multi-Provider Support

Cogent supports multiple LLM providers. Each session can use a different model, and you can switch models mid-session with **Ctrl+M** or the `/model` command.

Models use `provider/model` syntax:

| Provider | Example | API Key |
|----------|---------|---------|
| **Anthropic** | `anthropic/claude-opus-4-6` | `ANTHROPIC_API_KEY` |
| **OpenAI** | `openai/gpt-4o` | `OPENAI_API_KEY` |
| **Gemini** | `gemini/gemini-2.5-pro` | `GEMINI_API_KEY` |
| **OpenRouter** | `openrouter/anthropic/claude-sonnet-4` | `OPENROUTER_API_KEY` |

Bare model names are inferred automatically: `gpt-4o` → `openai/gpt-4o`, `gemini-2.5-pro` → `gemini/gemini-2.5-pro`, `claude-sonnet-4` → `anthropic/claude-sonnet-4`.

### Context Management

- **Anthropic** uses server-side compaction (automatic).
- **OpenAI, Gemini, OpenRouter** use client-side hybrid compaction: LLM-generated summary of older messages with a sliding-window fallback.
- **OpenAI** uses the Responses API (`/v1/responses`) rather than Chat Completions.

### Provider-Specific Features

Features like extended thinking and web search are gated per provider. For example, `web_search` is only available with Anthropic, OpenAI reasoning maps to the Responses API `reasoning` mode for supported models, and Gemini thinking maps to Gemini's provider-specific thinking config when supported.

## Configuration

Settings can be stored in `~/.cogent/settings` (global) or the nearest `.cogent/settings` found by walking up from the current directory, one `KEY=VALUE` per line. Project-local settings override global. Explicit environment variables always take precedence over both.

### Model Selection

| Variable | Description |
|----------|-------------|
| `COGENT_MODEL` | Default model for new sessions (default: `anthropic/claude-opus-4-6`) |
| `COGENT_PLAN_MODEL` | Model for planning mode (falls back to `COGENT_MODEL`) |
| `COGENT_SUBAGENT_MODEL` | Model for sub-agents (falls back to `COGENT_MODEL`) |
| `COGENT_MODELS` | Comma-separated list for **Ctrl+M** cycling (e.g. `anthropic/claude-sonnet-4,openai/gpt-4o,openrouter/deepseek/deepseek-r1`) |

### Provider API Keys

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `OPENAI_API_KEY` | OpenAI API key |
| `GEMINI_API_KEY` | Google Gemini API key |
| `OPENROUTER_API_KEY` | OpenRouter API key |

### Provider-Specific

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_MODEL` | Anthropic model override (fallback for `COGENT_MODEL`) |
| `ANTHROPIC_BASE_URL` | Custom Anthropic API base URL (must be HTTPS) |
| `OPENAI_BASE_URL` | Custom OpenAI API base URL |
| `OPENAI_MODEL` | OpenAI model override (fallback for `COGENT_MODEL`) |
| `GEMINI_MODEL` | Gemini model override (fallback for `COGENT_MODEL`) |
| `OPENROUTER_BASE_URL` | Custom OpenRouter API base URL |
| `OPENROUTER_MODEL` | OpenRouter model override (fallback for `COGENT_MODEL`) |

## Security

- API keys are scrubbed from the environment of all subprocesses.
- `write` and `edit` are sandboxed to the current working directory.
- `ANTHROPIC_BASE_URL` must use HTTPS unless the host is localhost.
