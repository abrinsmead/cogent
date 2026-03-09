package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/anthropics/agent/api"
)

type Tool interface {
	Definition() api.ToolDef
	Execute(input map[string]any) (string, error)
	RequiresConfirmation() bool
}

// ConcurrentTool is an optional interface for tools that can safely execute
// in parallel with other tools in the same response. When the API returns
// multiple tool_use blocks and some implement ConcurrentTool, those tools
// run simultaneously rather than sequentially.
type ConcurrentTool interface {
	IsConcurrent() bool
}

type Registry struct {
	tools       map[string]Tool
	customNames []string // names of successfully loaded custom tools
	warnings    []string // warnings from custom tool loading
}

func NewRegistry(cwd string) *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	r.Register(&BashTool{})
	r.Register(&ReadTool{})
	r.Register(&WriteTool{AllowedDir: cwd})
	r.Register(&EditTool{AllowedDir: cwd})
	r.Register(&GlobTool{})
	r.Register(&GrepTool{})
	r.Register(&LsTool{})

	// Load .env files before scanning for custom tools so that
	// required env vars declared with @env can be resolved.
	// Project-local is loaded first so it takes precedence over global
	// (LoadDotEnv skips vars already set). Explicit env vars always win
	// over both since they're already in the environment.
	_ = LoadDotEnv(filepath.Join(cwd, ".cogent", ".env"))
	if home, err := os.UserHomeDir(); err == nil {
		_ = LoadDotEnv(filepath.Join(home, ".cogent", ".env"))
	}

	// Discover custom tools: project-local first, then global.
	// Project tools take priority — if a name is already registered
	// (from built-ins or a previous directory), the duplicate is skipped.
	for _, dir := range customToolDirs(cwd) {
		custom, warnings := LoadCustomTools(dir)
		r.warnings = append(r.warnings, warnings...)
		for _, t := range custom {
			name := t.Definition().Name
			if _, exists := r.tools[name]; exists {
				r.warnings = append(r.warnings,
					fmt.Sprintf("skipping custom tool %q from %s: name already registered", name, dir))
				continue
			}
			r.Register(t)
			r.customNames = append(r.customNames, name)
		}
	}

	return r
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Definition().Name] = t
}

// RegisterTool allows external packages to add tools to the registry after
// construction. Returns false if the name is already taken.
func (r *Registry) RegisterTool(t Tool) bool {
	name := t.Definition().Name
	if _, exists := r.tools[name]; exists {
		return false
	}
	r.tools[name] = t
	return true
}

func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return t, nil
}

func (r *Registry) Definitions() []any {
	defs := make([]api.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	result := make([]any, len(defs))
	for i, d := range defs {
		result[i] = d
	}
	return result
}

// Warnings returns any warnings generated during custom tool discovery
// (e.g. missing required env vars, parse errors).
func (r *Registry) Warnings() []string {
	return r.warnings
}

// CustomToolNames returns the names of successfully loaded custom tools, sorted.
func (r *Registry) CustomToolNames() []string {
	names := make([]string, len(r.customNames))
	copy(names, r.customNames)
	sort.Strings(names)
	return names
}

// CustomToolEntry describes a loaded custom tool for display purposes.
type CustomToolEntry struct {
	Name    string
	Confirm bool
}

// CustomToolInfo returns the names and confirmation status of successfully loaded custom tools, sorted by name.
func (r *Registry) CustomToolInfo() []CustomToolEntry {
	entries := make([]CustomToolEntry, 0, len(r.customNames))
	for _, name := range r.customNames {
		t := r.tools[name]
		entries = append(entries, CustomToolEntry{Name: name, Confirm: t.RequiresConfirmation()})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries
}
