package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropics/agent/api"
)

type BashTool struct{}

func (t *BashTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "bash",
		Description: "Execute a shell command.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"command": {
					Type:        "string",
					Description: "The shell command to execute",
				},
				"timeout_ms": {
					Type:        "number",
					Description: "Timeout in milliseconds (default 120000, max 600000)",
					Default:     120000,
				},
			},
			Required: []string{"command"},
		},
	}
}

func (t *BashTool) Execute(input map[string]any) (string, error) {
	command, ok := input["command"].(string)
	if !ok || command == "" {
		return "", fmt.Errorf("command is required")
	}
	timeoutMs := 120000.0
	if v, ok := input["timeout_ms"].(float64); ok && v > 0 {
		if v > 600000 {
			v = 600000
		}
		timeoutMs = v
	}
	return runShell(command, time.Duration(timeoutMs)*time.Millisecond)
}

func runShell(command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}
	if len(output) > 30000 {
		output = output[:30000] + "\n... (output truncated)"
	}
	output = strings.TrimRight(output, "\n")
	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("command timed out after %s", timeout)
	}
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			output += fmt.Sprintf("\n(exit code %d)", exitErr.ExitCode())
		}
		return output, nil // return output (including exit code) without error so the agent sees it
	}
	return output, nil
}
