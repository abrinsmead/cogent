package api

// Shared OpenAI-compatible chat-completions wire types used by OpenRouter.

type oaiRequest struct {
	Model             string       `json:"model"`
	Messages          []oaiMessage `json:"messages"`
	Tools             []oaiTool    `json:"tools,omitempty"`
	MaxCompletionToks int          `json:"max_completion_tokens,omitempty"`
	Temperature       *float64     `json:"temperature,omitempty"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string         `json:"type"`
	Function oaiToolFuncDef `json:"function"`
}

type oaiToolFuncDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   oaiUsage    `json:"usage"`
}

type oaiChoice struct {
	Message      oaiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
