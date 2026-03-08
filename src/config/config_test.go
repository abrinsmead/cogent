package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings")

	content := `# Global settings
ANTHROPIC_API_KEY=sk-test-12345
LINEAR_API_KEY='lin_api_abc123'
LINEAR_USERNAME="jane.doe"
EMPTY_LINE_BELOW

BARE_VALUE=hello
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Clear any existing values
	for _, key := range []string{"ANTHROPIC_API_KEY", "LINEAR_API_KEY", "LINEAR_USERNAME", "BARE_VALUE"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	if err := loadFile(path); err != nil {
		t.Fatalf("loadFile: %v", err)
	}

	tests := []struct {
		key, want string
	}{
		{"ANTHROPIC_API_KEY", "sk-test-12345"},
		{"LINEAR_API_KEY", "lin_api_abc123"},    // single quotes stripped
		{"LINEAR_USERNAME", "jane.doe"},          // double quotes stripped
		{"BARE_VALUE", "hello"},
	}
	for _, tt := range tests {
		if got := os.Getenv(tt.key); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestLoadFileExplicitEnvWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings")

	if err := os.WriteFile(path, []byte("MY_KEY=from_file\n"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MY_KEY", "from_env")

	if err := loadFile(path); err != nil {
		t.Fatalf("loadFile: %v", err)
	}

	if got := os.Getenv("MY_KEY"); got != "from_env" {
		t.Errorf("MY_KEY = %q, want %q (explicit env should win)", got, "from_env")
	}
}

func TestLoadFileMissing(t *testing.T) {
	if err := loadFile("/nonexistent/path/settings"); err != nil {
		t.Errorf("missing file should return nil, got: %v", err)
	}
}

func TestProjectLocalOverridesGlobal(t *testing.T) {
	// Set up a fake home with global settings
	globalDir := t.TempDir()
	globalSettingsDir := filepath.Join(globalDir, ".cogent")
	os.MkdirAll(globalSettingsDir, 0755)
	os.WriteFile(filepath.Join(globalSettingsDir, "settings"), []byte("MY_VAR=global_value\nGLOBAL_ONLY=from_global\n"), 0644)

	// Set up a project dir with local settings
	projectDir := t.TempDir()
	localSettingsDir := filepath.Join(projectDir, ".cogent")
	os.MkdirAll(localSettingsDir, 0755)
	os.WriteFile(filepath.Join(localSettingsDir, "settings"), []byte("MY_VAR=local_value\n"), 0644)

	// Clear env
	for _, key := range []string{"MY_VAR", "GLOBAL_ONLY"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}

	// Load project-local first (higher priority)
	if err := loadFile(filepath.Join(localSettingsDir, "settings")); err != nil {
		t.Fatal(err)
	}
	// Then global (lower priority — won't override)
	if err := loadFile(filepath.Join(globalSettingsDir, "settings")); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("MY_VAR"); got != "local_value" {
		t.Errorf("MY_VAR = %q, want %q (project-local should win over global)", got, "local_value")
	}
	if got := os.Getenv("GLOBAL_ONLY"); got != "from_global" {
		t.Errorf("GLOBAL_ONLY = %q, want %q (global-only keys should still load)", got, "from_global")
	}
}

func TestLoadProjectSettings(t *testing.T) {
	// Create a project dir with .cogent/settings
	projectDir := t.TempDir()
	settingsDir := filepath.Join(projectDir, ".cogent")
	os.MkdirAll(settingsDir, 0755)
	os.WriteFile(filepath.Join(settingsDir, "settings"), []byte("PROJECT_KEY=project_value\n"), 0644)

	// Create a subdirectory to test walk-up behavior
	subDir := filepath.Join(projectDir, "src", "pkg")
	os.MkdirAll(subDir, 0755)

	// Clear env and cd into subdirectory
	t.Setenv("PROJECT_KEY", "")
	os.Unsetenv("PROJECT_KEY")

	origDir, _ := os.Getwd()
	os.Chdir(subDir)
	defer os.Chdir(origDir)

	if err := loadProjectSettings(); err != nil {
		t.Fatal(err)
	}

	if got := os.Getenv("PROJECT_KEY"); got != "project_value" {
		t.Errorf("PROJECT_KEY = %q, want %q (should find .cogent/settings walking up)", got, "project_value")
	}
}
