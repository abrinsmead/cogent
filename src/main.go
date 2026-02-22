package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/cli"
	"golang.org/x/term"
)

func main() {
	uiMode := flag.String("ui", "", "UI mode: tui, headless (default: auto-detect)")
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fatal("cannot determine working directory: %s", err)
	}
	client, err := api.NewClient()
	if err != nil {
		fatal("%s\nSet ANTHROPIC_API_KEY.", err)
	}

	prompt := strings.TrimSpace(strings.Join(flag.Args(), " "))

	mode := *uiMode
	if mode == "" {
		mode = detectMode(prompt)
	}

	var c cli.CLI
	switch mode {
	case "tui":
		c = cli.NewTUI(client, cwd, prompt)
	case "headless":
		if prompt == "" {
			fatal("headless mode requires a prompt argument")
		}
		c = cli.NewHeadless(client, cwd, prompt)
	default:
		fatal("unknown --ui mode: %q (expected tui or headless)", mode)
	}

	if err := c.Run(); err != nil {
		fatal("%s", err)
	}
}

// detectMode picks the best UI based on context:
//   - prompt given + not a TTY → headless (CI/pipes)
//   - prompt given + TTY       → tui     (interactive, prompt sent as first message)
//   - no prompt + TTY          → tui     (interactive)
//   - no prompt + not a TTY    → error
func detectMode(prompt string) string {
	tty := term.IsTerminal(int(os.Stdin.Fd()))

	if prompt != "" {
		if tty {
			return "tui"
		}
		return "headless"
	}

	if tty {
		return "tui"
	}

	fatal("no prompt given and stdin is not a terminal; provide a prompt")
	return "" // unreachable
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
