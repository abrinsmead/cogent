package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	openaiDefaultBaseURL = "https://api.openai.com"
	openaiDefaultModel   = "gpt-4o"
)

type openaiModelInfo struct {
	ContextWindow int
	InputPrice    float64 // $/MTok
	OutputPrice   float64 // $/MTok
	IsReasoning   bool    // o-series models with reasoning
}

var knownOpenAIModels = map[string]openaiModelInfo{
	"gpt-4o":      {ContextWindow: 128000, InputPrice: 2.50, OutputPrice: 10},
	"gpt-4o-mini": {ContextWindow: 128000, InputPrice: 0.15, OutputPrice: 0.60},
	"gpt-4-turbo": {ContextWindow: 128000, InputPrice: 10, OutputPrice: 30},
	"o3":          {ContextWindow: 200000, InputPrice: 2, OutputPrice: 8, IsReasoning: true},
	"o3-mini":     {ContextWindow: 200000, InputPrice: 1.10, OutputPrice: 4.40, IsReasoning: true},
	"o4-mini":     {ContextWindow: 200000, InputPrice: 1.10, OutputPrice: 4.40, IsReasoning: true},
}

var defaultOpenAIModelInfo = openaiModelInfo{ContextWindow: 128000, InputPrice: 2.50, OutputPrice: 10}

// OpenAIProvider implements the Provider interface for OpenAI's Chat Completions API.
type OpenAIProvider struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

func NewOpenAIProvider(model string) (*OpenAIProvider, error) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
	}
	baseURL := envOr("OPENAI_BASE_URL", openaiDefaultBaseURL)
	if model == "" {
		model = envOr("OPENAI_MODEL", openaiDefaultModel)
	}
	return &OpenAIProvider{
		apiKey:     key,
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func (p *OpenAIProvider) modelInfo() openaiModelInfo {
	if info, ok := knownOpenAIModels[p.model]; ok {
		return info
	}
	return defaultOpenAIModelInfo
}

func (p *OpenAIProvider) Info() ModelInfo {
	info := p.modelInfo()
	return ModelInfo{
		ProviderID:          "openai",
		Model:               p.model,
		ContextWindow:       info.ContextWindow,
		InputPrice:          info.InputPrice,
		OutputPrice:         info.OutputPrice,
		CacheHitPrice:       0,
		SupportsThinking:    info.IsReasoning,
		SupportsServerTools: false,
	}
}

func (p *OpenAIProvider) CostForUsage(u Usage) float64 {
	info := p.modelInfo()
	input := float64(u.TotalInputTokens()) * info.InputPrice / 1e6
	output := float64(u.TotalOutputTokens()) * info.OutputPrice / 1e6
	return input + output
}

func (p *OpenAIProvider) NeedsClientCompaction() bool { return true }

func (p *OpenAIProvider) Compact(ctx context.Context, system string, messages []Message, budget int) ([]Message, error) {
	return compactMessages(ctx, p, system, messages, budget)
}

// ── OpenAI wire format types ────────────────────────────────────────────────

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"`               // string or null
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"` // for role:"tool"
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"` // "function"
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

type oaiTool struct {
	Type     string          `json:"type"` // "function"
	Function oaiToolFuncDef  `json:"function"`
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

// ── Translation ─────────────────────────────────────────────────────────────

func (p *OpenAIProvider) SendMessage(ctx context.Context, req ProviderRequest) (*Response, error) {
	oaiMsgs := p.translateMessages(req.System, req.Messages)
	oaiTools := p.translateTools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16384
	}

	oaiReq := oaiRequest{
		Model:     p.model,
		Messages:  oaiMsgs,
		Tools:     oaiTools,
		MaxTokens: maxTokens,
	}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	maxRetries := 3
	backoff := 2 * time.Second

	for attempt := range maxRetries {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode == 429 && attempt < maxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("OpenAI API error (%d): %s", resp.StatusCode, string(respBody))
		}

		var oaiResp oaiResponse
		if err := json.Unmarshal(respBody, &oaiResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}

		return p.translateResponse(oaiResp), nil
	}
	return nil, fmt.Errorf("request failed after %d retries", maxRetries)
}

func (p *OpenAIProvider) translateMessages(system string, messages []Message) []oaiMessage {
	var result []oaiMessage

	if system != "" {
		result = append(result, oaiMessage{Role: "system", Content: system})
	}

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			// Check if this is a tool_result message
			var toolResults []ContentBlock
			var textBlocks []ContentBlock
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					toolResults = append(toolResults, block)
				} else {
					textBlocks = append(textBlocks, block)
				}
			}

			// Emit tool result messages first (each as separate role:"tool" message)
			for _, tr := range toolResults {
				content := tr.Content
				if tr.IsError {
					content = "Error: " + content
				}
				result = append(result, oaiMessage{
					Role:       "tool",
					Content:    content,
					ToolCallID: tr.ToolUseID,
				})
			}

			// Emit remaining text content
			if len(textBlocks) > 0 {
				var text string
				for _, b := range textBlocks {
					if b.Type == "text" && b.Text != "" {
						text += b.Text
					}
				}
				if text != "" {
					result = append(result, oaiMessage{Role: "user", Content: text})
				}
			}

		case RoleAssistant:
			var content string
			var toolCalls []oaiToolCall
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					content += block.Text
				case "tool_use":
					args, _ := json.Marshal(block.Input)
					toolCalls = append(toolCalls, oaiToolCall{
						ID:   block.ID,
						Type: "function",
						Function: oaiToolFunction{
							Name:      block.Name,
							Arguments: string(args),
						},
					})
				// thinking, compaction, server_tool_use, web_search_tool_result — skip
				}
			}
			msg := oaiMessage{Role: "assistant"}
			if content != "" {
				msg.Content = content
			}
			if len(toolCalls) > 0 {
				msg.ToolCalls = toolCalls
			}
			result = append(result, msg)
		}
	}

	return result
}

func (p *OpenAIProvider) translateTools(tools []any) []oaiTool {
	var result []oaiTool
	for _, t := range tools {
		switch td := t.(type) {
		case ToolDef:
			params := map[string]any{"type": td.InputSchema.Type}
			if len(td.InputSchema.Properties) > 0 {
				props := make(map[string]any)
				for name, prop := range td.InputSchema.Properties {
					p := map[string]any{"type": prop.Type}
					if prop.Description != "" {
						p["description"] = prop.Description
					}
					if len(prop.Enum) > 0 {
						p["enum"] = prop.Enum
					}
					props[name] = p
				}
				params["properties"] = props
			}
			if len(td.InputSchema.Required) > 0 {
				params["required"] = td.InputSchema.Required
			}
			result = append(result, oaiTool{
				Type: "function",
				Function: oaiToolFuncDef{
					Name:        td.Name,
					Description: td.Description,
					Parameters:  params,
				},
			})
		// ServerTool — skip (not supported on OpenAI)
		}
	}
	return result
}

func (p *OpenAIProvider) translateResponse(resp oaiResponse) *Response {
	if len(resp.Choices) == 0 {
		return &Response{
			Usage: Usage{
				InputTokens:  resp.Usage.PromptTokens,
				OutputTokens: resp.Usage.CompletionTokens,
			},
		}
	}

	choice := resp.Choices[0]
	var content []ContentBlock

	if text, ok := choice.Message.Content.(string); ok && text != "" {
		content = append(content, TextBlock(text))
	}

	for _, tc := range choice.Message.ToolCalls {
		var input map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &input)
		if input == nil {
			input = make(map[string]any)
		}
		content = append(content, ContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	var stopReason StopReason
	switch choice.FinishReason {
	case "tool_calls":
		stopReason = StopToolUse
	case "length":
		stopReason = StopMaxTokens
	default:
		stopReason = "" // end_turn equivalent
	}

	return &Response{
		Role:       RoleAssistant,
		Content:    content,
		StopReason: stopReason,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}
}
