package cli

import (
	"fmt"
	"strings"

	"github.com/anthropics/agent/agent"
	"github.com/anthropics/agent/api"
)

// Headless runs a single prompt with no interactive confirmation — every tool
// invocation is automatically approved. Designed for CI, pipes, and scripts.
type Headless struct {
	provider api.Provider
	cwd      string
	prompt   string
}

func NewHeadless(provider api.Provider, cwd, prompt string) *Headless {
	return &Headless{provider: provider, cwd: cwd, prompt: prompt}
}

func (h *Headless) Run() error {
	ag := agent.New(h.provider, h.cwd,
		agent.WithPermissionMode(agent.ModeYOLO),
		agent.WithTextCallback(func(text string) {
			fmt.Printf("%s%s%s\n\n", Dim, text, Reset)
		}),
		agent.WithToolCallback(func(name, summary string) {
			color := Green
			switch name {
			case "bash", "write", "edit":
				color = Red
			}
			fmt.Printf("%s%s %s%s %s%s\n", Dim, color, name, Reset, Dim, summary+Reset)
		}),
		agent.WithToolResultCallback(func(name, result string, isError bool) {
			if result == "" {
				return
			}
			const maxLines = 3
			lines := strings.Split(result, "\n")
			truncated := len(lines) > maxLines
			if truncated {
				lines = lines[:maxLines]
			}
			color := Dim
			if isError {
				color = Red
			}
			for _, line := range lines {
				fmt.Printf("%s  %s%s\n", color, line, Reset)
			}
			if truncated {
				fmt.Printf("%s%s  ... output truncated (%d lines shown)%s\n", Bold, Yellow, maxLines, Reset)
			}
		}),
	)

	fmt.Printf("%s> %s%s\n", Green, h.prompt, Reset)
	return ag.Send(h.prompt)
}
