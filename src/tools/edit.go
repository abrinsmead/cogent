package tools

import (
	"fmt"
	"os"
	"strings"

	"github.com/anthropics/agent/api"
)

type EditTool struct {
	AllowedDir string
}

func (t *EditTool) RequiresConfirmation() bool { return true }

func (t *EditTool) Definition() api.ToolDef {
	return api.ToolDef{
		Name:        "edit",
		Description: "Search-and-replace edit on a file. old_string must match exactly once.",
		InputSchema: api.ToolInputSchema{
			Type: "object",
			Properties: map[string]api.Property{
				"file_path": {
					Type:        "string",
					Description: "Absolute path to the file",
				},
				"old_string": {
					Type:        "string",
					Description: "Exact text to find",
				},
				"new_string": {
					Type:        "string",
					Description: "Replacement text",
				},
			},
			Required: []string{"file_path", "old_string", "new_string"},
		},
	}
}

func (t *EditTool) Execute(input map[string]any) (string, error) {
	filePath, _ := input["file_path"].(string)
	oldStr, _ := input["old_string"].(string)
	newStr, _ := input["new_string"].(string)
	if filePath == "" || oldStr == "" {
		return "", fmt.Errorf("file_path and old_string are required")
	}
	if oldStr == newStr {
		return "", fmt.Errorf("old_string and new_string must differ")
	}
	if t.AllowedDir != "" {
		if err := ValidatePathUnder(filePath, t.AllowedDir); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	content := string(data)
	count := strings.Count(content, oldStr)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in file")
	}
	if count > 1 {
		return "", fmt.Errorf("old_string matches %d locations — add more context", count)
	}
	newContent := strings.Replace(content, oldStr, newStr, 1)
	if err := os.WriteFile(filePath, []byte(newContent), info.Mode()); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return fmt.Sprintf("Edited %s", filePath), nil
}
