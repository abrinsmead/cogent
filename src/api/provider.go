package api

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Provider is the interface all LLM backends implement. Each provider translates
// between the canonical internal message format and its wire format.
type Provider interface {
	// SendMessage sends a conversation and returns a Response in canonical format.
	SendMessage(ctx context.Context, req ProviderRequest) (*Response, error)

	// Info returns the model descriptor for the currently configured model.
	Info() ModelInfo

	// CostForUsage calculates USD cost for a usage report.
	CostForUsage(u Usage) float64

	// NeedsClientCompaction returns true if this provider lacks server-side compaction.
	NeedsClientCompaction() bool

	// Compact performs client-side compaction of the message history.
	// Returns the compacted message list. For providers with server-side
	// compaction (Anthropic), this is a no-op.
	Compact(ctx context.Context, system string, messages []Message, budget int) ([]Message, error)
}

// ModelInfo describes a model's capabilities and pricing.
type ModelInfo struct {
	ProviderID    string  // "anthropic", "openai", "gemini"
	Model         string  // e.g. "claude-opus-4-6", "gpt-4o"
	ContextWindow int     // max tokens
	InputPrice    float64 // $/MTok
	OutputPrice   float64 // $/MTok
	CacheHitPrice float64 // $/MTok (0 if no caching)

	// Capability flags
	SupportsThinking    bool
	SupportsServerTools bool // e.g. Anthropic web_search
}

// ProviderRequest is the provider-agnostic input to SendMessage.
type ProviderRequest struct {
	System    string
	Messages  []Message
	Tools     []any           // ToolDef and ServerTool mixed
	Thinking  *ThinkingConfig // nil = disabled
	MaxTokens int
}

// ModelSpec identifies a specific model on a specific provider.
type ModelSpec struct {
	Provider string // "anthropic", "openai", "gemini"
	Model    string
}

// String returns the "provider/model" representation.
func (s ModelSpec) String() string {
	return s.Provider + "/" + s.Model
}

// ParseModelSpec parses "provider/model" or a bare "model" (defaults to anthropic).
func ParseModelSpec(s string) ModelSpec {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return ModelSpec{
			Provider: strings.ToLower(s[:i]),
			Model:    s[i+1:],
		}
	}
	// Bare model name — infer provider from model prefix
	switch {
	case strings.HasPrefix(s, "gpt-") || strings.HasPrefix(s, "o1") || strings.HasPrefix(s, "o3") || strings.HasPrefix(s, "o4"):
		return ModelSpec{Provider: "openai", Model: s}
	case strings.HasPrefix(s, "gemini-"):
		return ModelSpec{Provider: "gemini", Model: s}
	default:
		return ModelSpec{Provider: "anthropic", Model: s}
	}
}

// NewProvider creates a provider for the given spec.
func NewProvider(spec ModelSpec) (Provider, error) {
	switch spec.Provider {
	case "anthropic", "":
		return NewAnthropicProvider(spec.Model)
	case "openai":
		return NewOpenAIProvider(spec.Model)
	case "gemini":
		return NewGeminiProvider(spec.Model)
	case "openrouter":
		return NewOpenRouterProvider(spec.Model)
	default:
		return nil, fmt.Errorf("unknown provider: %s", spec.Provider)
	}
}

// DefaultModelSpec returns the model spec from configuration, falling back to
// anthropic/claude-opus-4-6.
func DefaultModelSpec() ModelSpec {
	if v := os.Getenv("COGENT_MODEL"); v != "" {
		return ParseModelSpec(v)
	}
	if v := os.Getenv("ANTHROPIC_MODEL"); v != "" {
		return ParseModelSpec(v)
	}
	return ModelSpec{Provider: "anthropic", Model: "claude-opus-4-6"}
}

// PlanModelSpec returns the model spec for planning mode.
func PlanModelSpec() ModelSpec {
	if v := os.Getenv("COGENT_PLAN_MODEL"); v != "" {
		return ParseModelSpec(v)
	}
	return DefaultModelSpec()
}

// SubagentModelSpec returns the model spec for sub-agents.
func SubagentModelSpec() ModelSpec {
	if v := os.Getenv("COGENT_SUBAGENT_MODEL"); v != "" {
		return ParseModelSpec(v)
	}
	return DefaultModelSpec()
}

// ConfiguredModels returns the list of models configured for Ctrl+M cycling.
// Falls back to just the default model if COGENT_MODELS is not set.
func ConfiguredModels() []ModelSpec {
	v := os.Getenv("COGENT_MODELS")
	if v == "" {
		return []ModelSpec{DefaultModelSpec()}
	}
	parts := strings.Split(v, ",")
	var specs []ModelSpec
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			specs = append(specs, ParseModelSpec(p))
		}
	}
	if len(specs) == 0 {
		return []ModelSpec{DefaultModelSpec()}
	}
	return specs
}

// AvailableModels returns all known models grouped by provider,
// filtered to those with configured API keys.
func AvailableModels() []ModelSpec {
	var models []ModelSpec

	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		for model := range knownAnthropicModels {
			models = append(models, ModelSpec{Provider: "anthropic", Model: model})
		}
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		for model := range knownOpenAIModels {
			models = append(models, ModelSpec{Provider: "openai", Model: model})
		}
	}
	if os.Getenv("GEMINI_API_KEY") != "" {
		for model := range knownGeminiModels {
			models = append(models, ModelSpec{Provider: "gemini", Model: model})
		}
	}
	if os.Getenv("OPENROUTER_API_KEY") != "" {
		for model := range knownOpenRouterModels {
			models = append(models, ModelSpec{Provider: "openrouter", Model: model})
		}
	}

	return models
}
