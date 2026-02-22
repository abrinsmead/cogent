package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobTool_SimplePattern(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "main.go")
	touch(t, dir, "util.go")
	touch(t, dir, "README.md")

	tool := &GlobTool{}
	result, err := tool.Execute(map[string]any{
		"pattern": "*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected main.go in result, got: %s", result)
	}
	if !strings.Contains(result, "util.go") {
		t.Errorf("expected util.go in result, got: %s", result)
	}
	if strings.Contains(result, "README.md") {
		t.Errorf("should not contain README.md, got: %s", result)
	}
}

func TestGlobTool_RecursiveDoublestar(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "top.go")
	touch(t, dir, "sub/nested.go")
	touch(t, dir, "sub/deep/deeper.go")
	touch(t, dir, "sub/deep/notes.txt")

	tool := &GlobTool{}
	result, err := tool.Execute(map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "top.go") {
		t.Errorf("expected top.go in result, got: %s", result)
	}
	if !strings.Contains(result, "nested.go") {
		t.Errorf("expected nested.go in result, got: %s", result)
	}
	if !strings.Contains(result, "deeper.go") {
		t.Errorf("expected deeper.go in result, got: %s", result)
	}
	if strings.Contains(result, "notes.txt") {
		t.Errorf("should not contain notes.txt, got: %s", result)
	}
}

func TestGlobTool_NoMatches(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "file.txt")

	tool := &GlobTool{}
	result, err := tool.Execute(map[string]any{
		"pattern": "*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "No files found" {
		t.Errorf("expected 'No files found', got: %s", result)
	}
}

func TestGlobTool_SkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, ".hidden/secret.go")
	touch(t, dir, "visible.go")

	tool := &GlobTool{}
	result, err := tool.Execute(map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "visible.go") {
		t.Errorf("expected visible.go in result, got: %s", result)
	}
	if strings.Contains(result, "secret.go") {
		t.Errorf("should not contain .hidden/secret.go, got: %s", result)
	}
}

func TestGlobTool_MissingPattern(t *testing.T) {
	tool := &GlobTool{}
	_, err := tool.Execute(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

// touch creates a file (and any parent dirs) in the given base directory.
func touch(t *testing.T, base, relPath string) {
	t.Helper()
	full := filepath.Join(base, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
}
