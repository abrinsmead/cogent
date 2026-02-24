package api

import (
	"encoding/json"
	"sort"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type StopReason string

const (
	StopToolUse    StopReason = "tool_use"
	StopMaxTokens  StopReason = "max_tokens"
	StopCompaction StopReason = "compaction"
)

type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
	ToolUseID string         `json:"tool_use_id"`
	Content   string         `json:"content"`
	IsError   bool           `json:"is_error"`
	Thinking  string         `json:"thinking"`  // extended thinking content
}

// MarshalJSON produces deterministic JSON output. Go maps have random
// iteration order which changes the serialized bytes across calls. The
// Anthropic prompt-caching system uses exact byte matching, so
// non-deterministic JSON breaks cache hits between requests.
func (cb ContentBlock) MarshalJSON() ([]byte, error) {
	switch cb.Type {
	case "tool_use":
		return json.Marshal(struct {
			Type  string         `json:"type"`
			ID    string         `json:"id"`
			Name  string         `json:"name"`
			Input orderedMap     `json:"input"`
		}{
			Type:  cb.Type,
			ID:    cb.ID,
			Name:  cb.Name,
			Input: newOrderedMap(cb.Input),
		})
	case "tool_result":
		if cb.IsError {
			return json.Marshal(struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
				Content   string `json:"content"`
				IsError   bool   `json:"is_error"`
			}{cb.Type, cb.ToolUseID, cb.Content, true})
		}
		return json.Marshal(struct {
			Type      string `json:"type"`
			ToolUseID string `json:"tool_use_id"`
			Content   string `json:"content"`
		}{cb.Type, cb.ToolUseID, cb.Content})
	case "thinking":
		return json.Marshal(struct {
			Type     string `json:"type"`
			Thinking string `json:"thinking"`
		}{cb.Type, cb.Thinking})
	case "compaction":
		return json.Marshal(struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}{cb.Type, cb.Content})
	default:
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{cb.Type, cb.Text})
	}
}

// orderedMap serializes a map[string]any with keys in sorted order so
// that JSON output is deterministic across calls (critical for caching).
type orderedMap struct {
	keys []string
	vals map[string]any
}

func newOrderedMap(m map[string]any) orderedMap {
	if m == nil {
		return orderedMap{vals: map[string]any{}}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return orderedMap{keys: keys, vals: m}
}

func (o orderedMap) MarshalJSON() ([]byte, error) {
	buf := []byte{'{'}
	for i, k := range o.keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		key, _ := json.Marshal(k)
		buf = append(buf, key...)
		buf = append(buf, ':')
		val, err := json.Marshal(o.vals[k])
		if err != nil {
			return nil, err
		}
		buf = append(buf, val...)
	}
	buf = append(buf, '}')
	return buf, nil
}

func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

func ToolResultBlock(toolUseID, content string, isError bool) ContentBlock {
	return ContentBlock{Type: "tool_result", ToolUseID: toolUseID, Content: content, IsError: isError}
}

type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

func UserMessage(text string) Message {
	return Message{Role: RoleUser, Content: []ContentBlock{TextBlock(text)}}
}

func ToolResultMessage(results []ContentBlock) Message {
	return Message{Role: RoleUser, Content: results}
}

type ToolInputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"-"` // serialized by custom MarshalJSON
	Required   []string            `json:"required,omitempty"`
}

// MarshalJSON produces deterministic JSON for tool schemas by sorting
// property keys. Without this, Go's random map iteration order changes
// the serialized tool definitions between requests, breaking cache hits.
func (s ToolInputSchema) MarshalJSON() ([]byte, error) {
	type schemaAlias struct {
		Type       string             `json:"type"`
		Properties json.RawMessage    `json:"properties,omitempty"`
		Required   []string           `json:"required,omitempty"`
	}
	a := schemaAlias{Type: s.Type, Required: s.Required}
	if len(s.Properties) > 0 {
		// Build sorted properties JSON
		keys := make([]string, 0, len(s.Properties))
		for k := range s.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			key, _ := json.Marshal(k)
			buf = append(buf, key...)
			buf = append(buf, ':')
			val, err := json.Marshal(s.Properties[k])
			if err != nil {
				return nil, err
			}
			buf = append(buf, val...)
		}
		buf = append(buf, '}')
		a.Properties = buf
	}
	return json.Marshal(a)
}

type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Default     any      `json:"default,omitempty"`
}

type CacheControl struct {
	Type string `json:"type"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema ToolInputSchema `json:"input_schema"`
}

// SystemBlock is a content block within the system prompt array.
type SystemBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ThinkingConfig enables extended thinking (chain-of-thought) for the request.
type ThinkingConfig struct {
	Type         string `json:"type"`          // "enabled" or "disabled"
	BudgetTokens int    `json:"budget_tokens"` // max tokens for thinking
}

type Request struct {
	Model             string             `json:"model"`
	MaxTokens         int                `json:"max_tokens"`
	CacheControl      *CacheControl      `json:"cache_control,omitempty"`
	System            []SystemBlock      `json:"system,omitempty"`
	Messages          []Message          `json:"messages"`
	Tools             []ToolDef          `json:"tools,omitempty"`
	Thinking          *ThinkingConfig    `json:"thinking,omitempty"`
	ContextManagement *ContextManagement `json:"context_management,omitempty"`
}

// ContextManagement configures server-side context management strategies.
type ContextManagement struct {
	Edits []ContextEdit `json:"edits"`
}

// ContextEdit represents a single context management strategy.
type ContextEdit struct {
	Type    string         `json:"type"`
	Trigger *CompactTrigger `json:"trigger,omitempty"`
}

// CompactTrigger configures when compaction is triggered.
type CompactTrigger struct {
	Type  string `json:"type"`
	Value int    `json:"value"`
}

type Response struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       Role           `json:"role"`
	Content    []ContentBlock `json:"content"`
	StopReason StopReason     `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

type Usage struct {
	InputTokens              int              `json:"input_tokens"`
	OutputTokens             int              `json:"output_tokens"`
	CacheCreationInputTokens int              `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int              `json:"cache_read_input_tokens,omitempty"`
	Iterations               []UsageIteration `json:"iterations,omitempty"`
}

// UsageIteration represents token usage for a single iteration within a request.
// When compaction occurs, there will be a "compaction" iteration followed by a
// "message" iteration. The top-level Usage fields only reflect non-compaction
// iterations, so Iterations must be used for accurate total cost tracking.
type UsageIteration struct {
	Type         string `json:"type"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// ContextUsed returns the total tokens consumed by this response.
// Cached tokens (both read and creation) still occupy the context window,
// so they must be included alongside regular input and output tokens.
// When compaction iterations are present, uses the last iteration's input
// tokens as the effective context size (post-compaction).
func (u Usage) ContextUsed() int {
	if len(u.Iterations) > 0 {
		last := u.Iterations[len(u.Iterations)-1]
		return last.InputTokens + last.OutputTokens
	}
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens + u.OutputTokens
}

// TotalInputTokens returns the sum of input tokens across all iterations.
// When compaction is active, the top-level InputTokens only reflects non-compaction
// iterations, so this method aggregates across all iterations for accurate billing.
func (u Usage) TotalInputTokens() int {
	if len(u.Iterations) == 0 {
		return u.InputTokens
	}
	total := 0
	for _, it := range u.Iterations {
		total += it.InputTokens
	}
	return total
}

// TotalOutputTokens returns the sum of output tokens across all iterations.
func (u Usage) TotalOutputTokens() int {
	if len(u.Iterations) == 0 {
		return u.OutputTokens
	}
	total := 0
	for _, it := range u.Iterations {
		total += it.OutputTokens
	}
	return total
}

type ErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
