package tools

import (
	"fmt"
	"sort"

	"github.com/anthropics/agent/api"
)

type Tool interface {
	Definition() api.ToolDef
	Execute(input map[string]any) (string, error)
	RequiresConfirmation() bool
}

type Registry struct {
	tools map[string]Tool
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
	return r
}

func (r *Registry) Register(t Tool) {
	r.tools[t.Definition().Name] = t
}

func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return t, nil
}

func (r *Registry) Definitions() []api.ToolDef {
	defs := make([]api.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, t.Definition())
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
	return defs
}
