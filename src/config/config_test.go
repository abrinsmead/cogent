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
