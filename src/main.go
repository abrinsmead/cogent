package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/cli"
	"github.com/anthropics/agent/config"
)

const usage = `Usage: cogent [command] [flags]

Commands:
  tui     Full-screen terminal UI (default)
  repl    Interactive REPL without full-screen UI
  agent   Headless mode, auto-approves all tools (requires --prompt)

Flags:
  --prompt "..."   Initial prompt to send to the agent
`

func main() {
	// Parse subcommand.
	mode := "tui"
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "tui", "repl", "agent":
			mode = args[0]
			args = args[1:]
		case "-h", "--help", "help":
			fmt.Print(usage)
			os.Exit(0)
		}
	}

	// Parse flags after the subcommand.
	fs := flag.NewFlagSet("cogent", flag.ExitOnError)
	fs.Usage = func() { fmt.Print(usage) }
	prompt := fs.String("prompt", "", "Initial prompt to send to the agent")
	fs.Parse(args)

	if fs.NArg() > 0 {
		fatal("unexpected arguments: %s\nUse --prompt \"...\" to pass a prompt", fs.Args())
	}

	cwd, err := os.Getwd()
	if err != nil {
		fatal("cannot determine working directory: %s", err)
	}

	// Load global settings (~/.cogent/settings) before creating the API
	// client so keys like ANTHROPIC_API_KEY can come from the settings file.
	if err := config.Load(); err != nil {
		fatal("loading global settings: %s", err)
	}

	client, err := api.NewClient()
	if err != nil {
		fatal("%s\nSet ANTHROPIC_API_KEY in the environment or in ~/.cogent/settings.", err)
	}

	var c cli.CLI
	switch mode {
	case "agent":
		if *prompt == "" {
			fatal("agent mode requires a prompt: cogent agent --prompt \"...\"")
		}
		c = cli.NewHeadless(client, cwd, *prompt)
	case "repl":
		c = cli.NewInteractive(client, cwd, *prompt)
	default:
		c = cli.NewTUI(client, cwd, *prompt)
	}

	if err := c.Run(); err != nil {
		fatal("%s", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
