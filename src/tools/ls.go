package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/agent/api"
)

type LsTool struct{}

func (t *LsTool) RequiresConfirmation() bool { return false }

func (t *LsTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "ls",
		Description: "List files and directories at a path.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"path": {
					Type:        "string",
					Description: "Directory to list (default: cwd)",
				},
			},
		},
	}
}

func (t *LsTool) Execute(input map[string]any) (string, error) {
	dir, _ := input["path"].(string)
	if dir == "" {
		dir, _ = os.Getwd()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "(empty)", nil
	}
	var b strings.Builder
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "-"
		if e.IsDir() {
			kind = "d"
		}
		fmt.Fprintf(&b, "%s %8d  %s\n", kind, info.Size(), e.Name())
	}
	return b.String(), nil
}
