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
	headless := flag.Bool("headless", false, "Run in headless mode (no TUI, auto-approve, requires a prompt)")
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

	if !*headless && !detectHeadless(prompt) {
		// TUI mode
	} else {
		*headless = true
	}

	var c cli.CLI
	if *headless {
		if prompt == "" {
			fatal("headless mode requires a prompt argument")
		}
		c = cli.NewHeadless(client, cwd, prompt)
	} else {
		c = cli.NewTUI(client, cwd, prompt)
	}

	if err := c.Run(); err != nil {
		fatal("%s", err)
	}
}

// detectHeadless returns true when the environment suggests headless mode:
// a prompt is provided and stdin is not a TTY (CI/pipes). If no prompt is
// given and stdin is not a TTY either, it exits with an error.
func detectHeadless(prompt string) bool {
	tty := term.IsTerminal(int(os.Stdin.Fd()))
	if tty {
		return false
	}
	if prompt != "" {
		return true
	}
	fatal("no prompt given and stdin is not a terminal; provide a prompt or use --headless")
	return false // unreachable
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	os.Exit(1)
}
