package cli

import (
	"context"
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
	rt       Runtime
}

func NewHeadless(provider api.Provider, cwd, prompt string, rt Runtime) *Headless {
	return &Headless{provider: provider, cwd: cwd, prompt: prompt, rt: rt}
}

func (h *Headless) Run() error {
	as, err := h.rt.NewSession(h.provider, h.cwd,
		agent.WithPermissionMode(agent.ModeYOLO),
		agent.WithTextCallback(func(text string) {
			fmt.Printf("%s%s%s\n\n", Dim, text, Reset)
		}),
		agent.WithToolCallback(func(name, summary string, _ map[string]any) {
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
	if err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	fmt.Printf("%s> %s%s\n", Green, h.prompt, Reset)
	return as.SendCtx(context.Background(), h.prompt)
}
