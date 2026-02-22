package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anthropics/agent/api"
)

// CustomTool is a user-defined tool backed by an executable script.
// Metadata is parsed from comment directives (@tool, @description, etc.)
// and the script receives JSON input on stdin, returning output on stdout.
type CustomTool struct {
	name        string
	description string
	params      []customParam
	envVars     []customEnv
	confirm     bool
	path        string // absolute path to the executable
}

type customParam struct {
	name        string
	typ         string // "string", "number", "boolean"
	required    bool
	description string
}

type customEnv struct {
	name        string
	required    bool
	description string
}

func (t *CustomTool) Definition() api.ToolDef {
	props := make(map[string]api.Property, len(t.params))
	var required []string
	for _, p := range t.params {
		props[p.name] = api.Property{
			Type:        p.typ,
			Description: p.description,
		}
		if p.required {
			required = append(required, p.name)
		}
	}
	return api.ToolDef{
		Name:        t.name,
		Description: t.description,
		InputSchema: api.ToolInputSchema{
			Type:       "object",
			Properties: props,
			Required:   required,
		},
	}
}

func (t *CustomTool) RequiresConfirmation() bool { return t.confirm }

func (t *CustomTool) Execute(input map[string]any) (string, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal input: %w", err)
	}

	timeout := 120 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, t.path)
	cmd.Stdin = bytes.NewReader(inputJSON)
	cmd.Dir = filepath.Dir(t.path)

	// Build a clean environment: inherit user env, scrub API key, inject declared vars.
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			cmd.Env = append(cmd.Env, e)
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}
	if len(output) > 30000 {
		output = output[:30000] + "\n... (output truncated)"
	}
	output = strings.TrimRight(output, "\n")

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("tool %s timed out after %s", t.name, timeout)
	}
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			output += fmt.Sprintf("\n(exit code %d)", exitErr.ExitCode())
		}
		return output, nil
	}
	return output, nil
}

// ─── Metadata parsing ──────────────────────────────────────────────────────

// commentPrefixes are the comment markers we strip when looking for @ directives.
var commentPrefixes = []string{"#", "//", "--"}

// paramRegex matches: @param <name> <type> [required] "<description>"
// Examples:
//
//	@param title string required "Ticket title"
//	@param verbose boolean "Enable verbose output"
var paramRegex = regexp.MustCompile(
	`^@param\s+(\S+)\s+(string|number|boolean)\s+(?:(required)\s+)?(?:"([^"]*)")?\s*$`,
)

// envRegex matches: @env <NAME> [required] "<description>"
var envRegex = regexp.MustCompile(
	`^@env\s+(\S+)\s+(?:(required)\s+)?(?:"([^"]*)")?\s*$`,
)

// parseCustomTool reads an executable file and extracts @ directives from
// comment lines. Returns nil if the file contains no @tool directive.
func parseCustomTool(path string) (*CustomTool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tool := &CustomTool{
		path:    path,
		confirm: true, // custom tools require confirmation by default
	}

	scanner := bufio.NewScanner(f)
	var descLines []string

	for scanner.Scan() {
		line := scanner.Text()
		directive := extractDirective(line)
		if directive == "" {
			continue
		}

		switch {
		case strings.HasPrefix(directive, "@tool "):
			tool.name = strings.TrimSpace(strings.TrimPrefix(directive, "@tool"))

		case strings.HasPrefix(directive, "@description "):
			descLines = append(descLines, strings.TrimSpace(strings.TrimPrefix(directive, "@description")))

		case strings.HasPrefix(directive, "@param "):
			if m := paramRegex.FindStringSubmatch(directive); m != nil {
				tool.params = append(tool.params, customParam{
					name:        m[1],
					typ:         m[2],
					required:    m[3] == "required",
					description: m[4],
				})
			}

		case strings.HasPrefix(directive, "@env "):
			if m := envRegex.FindStringSubmatch(directive); m != nil {
				tool.envVars = append(tool.envVars, customEnv{
					name:        m[1],
					required:    m[2] == "required",
					description: m[3],
				})
			}

		case directive == "@confirm":
			tool.confirm = true

		case directive == "@noconfirm":
			tool.confirm = false
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Must have a @tool directive
	if tool.name == "" {
		return nil, nil
	}

	tool.description = strings.Join(descLines, " ")
	if tool.description == "" {
		tool.description = fmt.Sprintf("Custom tool: %s", tool.name)
	}

	return tool, nil
}

// extractDirective strips a comment prefix from a line and returns the
// directive (starting with @) if found, or "" otherwise.
func extractDirective(line string) string {
	trimmed := strings.TrimSpace(line)
	for _, prefix := range commentPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			after := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			if strings.HasPrefix(after, "@") {
				return after
			}
			return ""
		}
	}
	return ""
}

// ─── Discovery ──────────────────────────────────────────────────────────────

// LoadCustomTools scans a directory for executable files with @tool directives.
// Non-executable files and files without @tool are silently skipped.
// Files with missing required env vars are skipped with a warning via the
// returned warnings slice.
func LoadCustomTools(dir string) ([]*CustomTool, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // directory doesn't exist — that's fine
	}

	var tools []*CustomTool
	var warnings []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())

		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Must be executable
		if info.Mode()&0111 == 0 {
			continue
		}

		tool, err := parseCustomTool(path)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("custom tool %s: %s", entry.Name(), err))
			continue
		}
		if tool == nil {
			continue // no @tool directive
		}

		// Validate required env vars
		missing := checkRequiredEnv(tool)
		if len(missing) > 0 {
			warnings = append(warnings,
				fmt.Sprintf("skipping custom tool %q: missing required env var(s): %s",
					tool.name, strings.Join(missing, ", ")))
			continue
		}

		tools = append(tools, tool)
	}

	return tools, warnings
}

// checkRequiredEnv returns the names of any required env vars that are not set.
func checkRequiredEnv(tool *CustomTool) []string {
	var missing []string
	for _, ev := range tool.envVars {
		if ev.required {
			if os.Getenv(ev.name) == "" {
				missing = append(missing, ev.name)
			}
		}
	}
	return missing
}

// LoadDotEnv reads a simple KEY=VALUE file (one per line, # comments, no
// quoting) and sets the variables in the process environment. This is called
// before tool discovery so that @env required checks see the values.
func LoadDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // no .env file — that's fine
	}
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip surrounding quotes
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		// Only set if not already in the environment (explicit env takes precedence)
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return scanner.Err()
}

// CustomToolsExist returns true if a .cogent/tools/ directory exists
// under the given working directory or home directory.
func CustomToolsExist(cwd string) bool {
	dirs := customToolDirs(cwd)
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// customToolDirs returns the directories to scan for custom tools.
// Project-local is listed first so it takes precedence — the registry
// skips duplicate names, so the first one registered wins.
func customToolDirs(cwd string) []string {
	dirs := []string{filepath.Join(cwd, ".cogent", "tools")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".cogent", "tools"))
	}
	return dirs
}

// CustomToolsPrompt returns a short reference for the system prompt
// describing how to create custom tools. Only included when .cogent/tools/ exists.
const CustomToolsPrompt = `Custom tools can be created as executable scripts in .cogent/tools/. Format:

#!/bin/bash
# @tool <name>
# @description <what it does>
# @param <name> <type> [required] "<description>"
# @env <VAR> [required] "<description>"
# @confirm (default) or @noconfirm

Input is received as JSON on stdin. Output goes to stdout.
Scripts can be in any language (bash, python, node, etc.) — just needs a shebang and chmod +x.
Env vars can be set in .cogent/.env (KEY=VALUE, one per line, gitignored).
Tools in ~/.cogent/tools/ are available globally; .cogent/tools/ is project-local.`
