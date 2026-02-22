package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/agent/api"
)

type ReadTool struct{}

func (t *ReadTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "read",
		Description: "Read file contents with line numbers.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"file_path": {
					Type:        "string",
					Description: "Absolute path to the file",
				},
				"offset": {
					Type:        "number",
					Description: "Start line (1-indexed, default 1)",
				},
				"limit": {
					Type:        "number",
					Description: "Max lines to read (default 2000)",
				},
			},
			Required: []string{"file_path"},
		},
	}
}

func (t *ReadTool) Execute(input map[string]any) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	offset := 1
	if v, ok := input["offset"].(float64); ok && v > 0 {
		offset = int(v)
	}
	limit := 2000
	if v, ok := input["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	start := offset - 1
	if start >= len(lines) {
		return "(offset beyond end of file)", nil
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for i := start; i < end; i++ {
		line := lines[i]
		if len(line) > 2000 {
			line = line[:2000] + "..."
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	return b.String(), nil
}
