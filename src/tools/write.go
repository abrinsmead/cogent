package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropics/agent/api"
)

type WriteTool struct {
	AllowedDir string
}

func (t *WriteTool) RequiresConfirmation() bool { return true }

func (t *WriteTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "write",
		Description: "Write content to a file. Creates parent directories if needed.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"file_path": {
					Type:        "string",
					Description: "Absolute path to the file",
				},
				"content": {
					Type:        "string",
					Description: "Content to write",
				},
			},
			Required: []string{"file_path", "content"},
		},
	}
}

func (t *WriteTool) Execute(input map[string]any) (string, error) {
	filePath, ok := input["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}
	content, ok := input["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}
	if t.AllowedDir != "" {
		if err := ValidatePathUnder(filePath, t.AllowedDir); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return "", fmt.Errorf("create directories: %w", err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(content), filePath), nil
}
