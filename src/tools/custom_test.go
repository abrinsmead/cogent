package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCustomTool(t *testing.T) {
	dir := t.TempDir()

	t.Run("basic bash tool", func(t *testing.T) {
		path := filepath.Join(dir, "deploy")
		content := `#!/bin/bash
# @tool deploy
# @description Deploy the app to a target environment
# @param environment string required "Target environment"
# @param dry_run boolean "Dry run mode"
# @confirm
`
		writeExecutable(t, path, content)

		tool, err := parseCustomTool(path)
		if err != nil {
			t.Fatal(err)
		}
		if tool == nil {
			t.Fatal("expected tool, got nil")
		}
		if tool.name != "deploy" {
			t.Errorf("name = %q, want %q", tool.name, "deploy")
		}
		if tool.description != "Deploy the app to a target environment" {
			t.Errorf("description = %q", tool.description)
		}
		if len(tool.params) != 2 {
			t.Fatalf("params = %d, want 2", len(tool.params))
		}
		if !tool.params[0].required {
			t.Error("param 'environment' should be required")
		}
		if tool.params[1].required {
			t.Error("param 'dry_run' should not be required")
		}
		if !tool.confirm {
			t.Error("tool should require confirmation")
		}

		def := tool.Definition()
		if def.Name != "deploy" {
			t.Errorf("Definition().Name = %q", def.Name)
		}
		if len(def.InputSchema.Required) != 1 || def.InputSchema.Required[0] != "environment" {
			t.Errorf("Required = %v, want [environment]", def.InputSchema.Required)
		}
	})

	t.Run("python tool with env", func(t *testing.T) {
		path := filepath.Join(dir, "jira")
		content := `#!/usr/bin/env python3
# @tool jira_create
# @description Create a Jira ticket
# @param title string required "Ticket title"
# @env JIRA_TOKEN required "API token"
# @env JIRA_URL required "Base URL"
# @noconfirm
`
		writeExecutable(t, path, content)

		tool, err := parseCustomTool(path)
		if err != nil {
			t.Fatal(err)
		}
		if tool == nil {
			t.Fatal("expected tool, got nil")
		}
		if tool.name != "jira_create" {
			t.Errorf("name = %q", tool.name)
		}
		if len(tool.envVars) != 2 {
			t.Fatalf("envVars = %d, want 2", len(tool.envVars))
		}
		if !tool.envVars[0].required {
			t.Error("JIRA_TOKEN should be required")
		}
		if tool.confirm {
			t.Error("tool should not require confirmation (@noconfirm)")
		}
	})

	t.Run("double-slash comments", func(t *testing.T) {
		path := filepath.Join(dir, "nodescript")
		content := `#!/usr/bin/env node
// @tool format_json
// @description Format JSON input
// @param input string required "JSON string"
// @noconfirm
`
		writeExecutable(t, path, content)

		tool, err := parseCustomTool(path)
		if err != nil {
			t.Fatal(err)
		}
		if tool == nil {
			t.Fatal("expected tool, got nil")
		}
		if tool.name != "format_json" {
			t.Errorf("name = %q", tool.name)
		}
	})

	t.Run("no tool directive returns nil", func(t *testing.T) {
		path := filepath.Join(dir, "plain")
		content := `#!/bin/bash
# This is just a regular script
echo "hello"
`
		writeExecutable(t, path, content)

		tool, err := parseCustomTool(path)
		if err != nil {
			t.Fatal(err)
		}
		if tool != nil {
			t.Error("expected nil for file without @tool directive")
		}
	})

	t.Run("default description", func(t *testing.T) {
		path := filepath.Join(dir, "minimal")
		content := `#!/bin/bash
# @tool minimal_tool
`
		writeExecutable(t, path, content)

		tool, err := parseCustomTool(path)
		if err != nil {
			t.Fatal(err)
		}
		if tool == nil {
			t.Fatal("expected tool, got nil")
		}
		if tool.description != "Custom tool: minimal_tool" {
			t.Errorf("description = %q, want default", tool.description)
		}
	})
}

func TestExtractDirective(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"# @tool deploy", "@tool deploy"},
		{"// @tool deploy", "@tool deploy"},
		{"-- @tool deploy", "@tool deploy"},
		{"  # @param x string", "@param x string"},
		{"# not a directive", ""},
		{"no comment prefix @tool test", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractDirective(tt.line)
		if got != tt.want {
			t.Errorf("extractDirective(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestCheckRequiredEnv(t *testing.T) {
	tool := &CustomTool{
		envVars: []customEnv{
			{name: "TEST_REQUIRED_VAR", required: true},
			{name: "TEST_OPTIONAL_VAR", required: false},
		},
	}

	// Not set → should be missing
	os.Unsetenv("TEST_REQUIRED_VAR")
	missing := checkRequiredEnv(tool)
	if len(missing) != 1 || missing[0] != "TEST_REQUIRED_VAR" {
		t.Errorf("missing = %v, want [TEST_REQUIRED_VAR]", missing)
	}

	// Set → should pass
	os.Setenv("TEST_REQUIRED_VAR", "value")
	defer os.Unsetenv("TEST_REQUIRED_VAR")
	missing = checkRequiredEnv(tool)
	if len(missing) != 0 {
		t.Errorf("missing = %v, want []", missing)
	}
}

func TestLoadCustomTools(t *testing.T) {
	dir := t.TempDir()

	// Create a valid tool
	writeExecutable(t, filepath.Join(dir, "good"), `#!/bin/bash
# @tool good_tool
# @description A good tool
# @param name string required "Name"
`)

	// Create a non-executable file with @tool (should be skipped)
	os.WriteFile(filepath.Join(dir, "noexec"), []byte(`#!/bin/bash
# @tool skipped
`), 0644)

	// Create a file without @tool (should be skipped)
	writeExecutable(t, filepath.Join(dir, "plain"), `#!/bin/bash
echo hello
`)

	// Create a tool with missing required env
	writeExecutable(t, filepath.Join(dir, "needsenv"), `#!/bin/bash
# @tool needs_env
# @env COGENT_TEST_MISSING_VAR_12345 required "A var"
`)
	os.Unsetenv("COGENT_TEST_MISSING_VAR_12345")

	tools, warnings := LoadCustomTools(dir)

	if len(tools) != 1 {
		t.Errorf("got %d tools, want 1", len(tools))
	}
	if len(tools) > 0 && tools[0].name != "good_tool" {
		t.Errorf("tool name = %q, want good_tool", tools[0].name)
	}

	// Should have a warning about the missing env var
	if len(warnings) != 1 {
		t.Errorf("got %d warnings, want 1: %v", len(warnings), warnings)
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	content := `# comment
COGENT_TEST_A=hello
COGENT_TEST_B="quoted value"
COGENT_TEST_C='single quoted'
`
	os.WriteFile(envFile, []byte(content), 0644)

	// Clear any existing values
	os.Unsetenv("COGENT_TEST_A")
	os.Unsetenv("COGENT_TEST_B")
	os.Unsetenv("COGENT_TEST_C")

	if err := LoadDotEnv(envFile); err != nil {
		t.Fatal(err)
	}

	defer func() {
		os.Unsetenv("COGENT_TEST_A")
		os.Unsetenv("COGENT_TEST_B")
		os.Unsetenv("COGENT_TEST_C")
	}()

	if v := os.Getenv("COGENT_TEST_A"); v != "hello" {
		t.Errorf("COGENT_TEST_A = %q, want hello", v)
	}
	if v := os.Getenv("COGENT_TEST_B"); v != "quoted value" {
		t.Errorf("COGENT_TEST_B = %q, want 'quoted value'", v)
	}
	if v := os.Getenv("COGENT_TEST_C"); v != "single quoted" {
		t.Errorf("COGENT_TEST_C = %q, want 'single quoted'", v)
	}
}

func TestLoadDotEnvNoOverride(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")

	os.WriteFile(envFile, []byte("COGENT_TEST_EXISTING=from_file\n"), 0644)

	os.Setenv("COGENT_TEST_EXISTING", "from_env")
	defer os.Unsetenv("COGENT_TEST_EXISTING")

	if err := LoadDotEnv(envFile); err != nil {
		t.Fatal(err)
	}

	if v := os.Getenv("COGENT_TEST_EXISTING"); v != "from_env" {
		t.Errorf("COGENT_TEST_EXISTING = %q, want from_env (should not override)", v)
	}
}

func TestLoadDotEnvMissing(t *testing.T) {
	// Non-existent file should not error
	err := LoadDotEnv("/nonexistent/path/.env")
	if err != nil {
		t.Errorf("expected nil error for missing .env, got %v", err)
	}
}

func TestCustomToolExecution(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "echo_tool")

	// Create a tool that reads JSON from stdin and echoes a field
	content := `#!/bin/bash
# @tool echo_tool
# @description Echoes the message param
# @param message string required "Message to echo"
input=$(cat)
echo "$input" | grep -o '"message":"[^"]*"' | cut -d'"' -f4
`
	writeExecutable(t, path, content)

	tool, err := parseCustomTool(path)
	if err != nil {
		t.Fatal(err)
	}
	if tool == nil {
		t.Fatal("expected tool")
	}

	result, err := tool.Execute(map[string]any{"message": "hello world"})
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello world" {
		t.Errorf("result = %q, want 'hello world'", result)
	}
}

func TestCustomToolExecutionError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fail_tool")

	content := `#!/bin/bash
# @tool fail_tool
# @description A tool that fails
echo "some output"
exit 1
`
	writeExecutable(t, path, content)

	tool, err := parseCustomTool(path)
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(map[string]any{})
	if err != nil {
		t.Fatal("expected no error (exit code should be in output)")
	}
	if result == "" {
		t.Error("expected some output")
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}
