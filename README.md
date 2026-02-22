# Cogent

A lightweight coding agent powered by Claude, written in Go.

Cogent runs in your terminal and can read, edit, and create files, execute shell commands, and search your codebase — all with confirmation prompts before any destructive action.

## Setup

Requires Go 1.22+ and an Anthropic API key.

```sh
export ANTHROPIC_API_KEY="sk-ant-..."
```

## Build & Install

```sh
cd src
make build      # outputs to bin/cogent
make install    # copies to /usr/local/bin/cogent
```

## Usage

**Interactive mode:**

```sh
cogent
```

**Single prompt:**

```sh
cogent "refactor the handler to use middleware"
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

## REPL Commands

| Command | Description |
|---------|-------------|
| `/help` | Show help |
| `/clear` | Clear conversation history |
| `/restart` | Rebuild and restart the agent |
| `/quit` | Exit |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | **(required)** Your Anthropic API key |
| `ANTHROPIC_MODEL` | Model to use (default: set by client) |
| `ANTHROPIC_BASE_URL` | Custom API base URL |
