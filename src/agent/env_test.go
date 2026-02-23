package agent

import (
	"runtime"
	"testing"
)

func TestEnvDescription(t *testing.T) {
	desc := envDescription()
	if desc == "" {
		t.Fatal("envDescription() returned empty string")
	}
	// Should always contain the architecture
	if got := desc; !contains(got, runtime.GOARCH) {
		t.Errorf("envDescription() = %q, want it to contain %q", got, runtime.GOARCH)
	}
	// On macOS it should say macOS, on Linux it should say Linux
	switch runtime.GOOS {
	case "darwin":
		if !contains(desc, "macOS") {
			t.Errorf("envDescription() = %q, want it to contain 'macOS'", desc)
		}
	case "linux":
		if !contains(desc, "Linux") {
			t.Errorf("envDescription() = %q, want it to contain 'Linux'", desc)
		}
	}
}

func TestDetectShell(t *testing.T) {
	sh := detectShell()
	if sh == "" {
		t.Fatal("detectShell() returned empty string")
	}
}

func TestShellGuidance(t *testing.T) {
	// Just verify it doesn't panic; result depends on environment
	_ = shellGuidance()
}

func TestShellBaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/bin/bash", "bash"},
		{"/usr/bin/zsh", "zsh"},
		{"/usr/local/bin/fish", "fish"},
		{"bash", "bash"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := shellBaseName(tt.input); got != tt.want {
			t.Errorf("shellBaseName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLinuxDistro(t *testing.T) {
	// Just verify it doesn't panic; returns "" on non-Linux
	_ = linuxDistro()
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
