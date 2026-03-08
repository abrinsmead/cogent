package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/anthropics/agent/api"
	"github.com/anthropics/agent/cli"
	"github.com/anthropics/agent/config"
)

var prompt string

func main() {
	root := &cobra.Command{
		Use:   "cogent",
		Short: "A lightweight coding agent for the terminal",
		// Bare `cogent` runs the TUI.
		RunE: runTUI,
		// Silence cobra's default error/usage printing — we handle it ourselves.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	tui := &cobra.Command{
		Use:   "tui",
		Short: "Full-screen terminal UI (default)",
		RunE:  runTUI,
	}

	repl := &cobra.Command{
		Use:   "repl",
		Short: "Interactive REPL without full-screen UI",
		RunE:  runREPL,
	}

	agent := &cobra.Command{
		Use:   "agent",
		Short: "Headless mode — auto-approves all tools",
		RunE:  runAgent,
	}

	// Add --prompt to all commands.
	for _, cmd := range []*cobra.Command{root, tui, repl, agent} {
		cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "Initial prompt to send to the agent")
	}

	root.AddCommand(tui, repl, agent)
	root.CompletionOptions.DisableDefaultCmd = true

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func setup() (cwd string, provider api.Provider, err error) {
	cwd, err = os.Getwd()
	if err != nil {
		return "", nil, fmt.Errorf("cannot determine working directory: %w", err)
	}
	if err := config.Load(); err != nil {
		return "", nil, fmt.Errorf("loading global settings: %w", err)
	}
	spec := api.DefaultModelSpec()
	provider, err = api.NewProvider(spec)
	if err != nil {
		return "", nil, fmt.Errorf("%w\nSet API keys in the environment or in ~/.cogent/settings", err)
	}
	return cwd, provider, nil
}

func runTUI(cmd *cobra.Command, args []string) error {
	cwd, provider, err := setup()
	if err != nil {
		return err
	}
	return cli.NewTUI(provider, cwd, prompt).Run()
}

func runREPL(cmd *cobra.Command, args []string) error {
	cwd, provider, err := setup()
	if err != nil {
		return err
	}
	return cli.NewInteractive(provider, cwd, prompt).Run()
}

func runAgent(cmd *cobra.Command, args []string) error {
	if prompt == "" {
		return fmt.Errorf("agent mode requires --prompt: cogent agent --prompt \"...\"")
	}
	cwd, provider, err := setup()
	if err != nil {
		return err
	}
	return cli.NewHeadless(provider, cwd, prompt).Run()
}
