package tools

import (
	"fmt"

	"github.com/anthropics/agent/api"
)

// SpawnFunc is called by DispatchTool to create a sub-agent session.
// It receives the task prompt and returns the sub-agent's final text output.
type SpawnFunc func(task string) (string, error)

// DispatchTool delegates a subtask to a sub-agent running in a separate session.
type DispatchTool struct {
	Spawn SpawnFunc
}

func (d *DispatchTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name: "dispatch",
		Description: "Delegate a subtask to a sub-agent. The sub-agent runs in a separate session " +
			"with its own context window and full set of tools (except dispatch — no recursion). " +
			"Use this to parallelize work, handle tasks that benefit from a fresh context, or " +
			"break down complex problems. The sub-agent runs to completion and returns its final " +
			"text output. Confirmations for destructive tools are routed to the parent session.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"task": {
					Type:        "string",
					Description: "The task description / prompt to send to the sub-agent",
				},
			},
			Required: []string{"task"},
		},
	}
}

func (d *DispatchTool) Execute(input map[string]any) (string, error) {
	task, _ := input["task"].(string)
	if task == "" {
		return "", fmt.Errorf("task is required")
	}
	if d.Spawn == nil {
		return "", fmt.Errorf("dispatch is not available in this mode")
	}
	return d.Spawn(task)
}

func (d *DispatchTool) RequiresConfirmation() bool { return true }
func (d *DispatchTool) IsConcurrent() bool          { return true }
