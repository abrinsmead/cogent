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
	openrouterDefaultBaseURL = "https://openrouter.ai/api"
	openrouterDefaultModel   = "anthropic/claude-sonnet-4"
)

type openrouterModelInfo struct {
	ContextWindow int
	InputPrice    float64 // $/MTok
	OutputPrice   float64 // $/MTok
}

// knownOpenRouterModels lists popular models available on OpenRouter with pricing.
// OpenRouter supports hundreds of models — unknown models get default pricing.
var knownOpenRouterModels = map[string]openrouterModelInfo{
	// Anthropic
	"anthropic/claude-opus-4":       {ContextWindow: 200000, InputPrice: 15, OutputPrice: 75},
	"anthropic/claude-sonnet-4":     {ContextWindow: 200000, InputPrice: 3, OutputPrice: 15},
	"anthropic/claude-haiku-3.5":    {ContextWindow: 200000, InputPrice: 0.80, OutputPrice: 4},
	// OpenAI
	"openai/gpt-4o":                 {ContextWindow: 128000, InputPrice: 2.50, OutputPrice: 10},
	"openai/gpt-4o-mini":            {ContextWindow: 128000, InputPrice: 0.15, OutputPrice: 0.60},
	"openai/o3":                     {ContextWindow: 200000, InputPrice: 2, OutputPrice: 8},
	"openai/o4-mini":                {ContextWindow: 200000, InputPrice: 1.10, OutputPrice: 4.40},
	// Google
	"google/gemini-2.5-pro":         {ContextWindow: 1048576, InputPrice: 1.25, OutputPrice: 10},
	"google/gemini-2.5-flash":       {ContextWindow: 1048576, InputPrice: 0.15, OutputPrice: 0.60},
	// Meta
	"meta-llama/llama-4-maverick":   {ContextWindow: 1048576, InputPrice: 0.20, OutputPrice: 0.60},
	"meta-llama/llama-4-scout":      {ContextWindow: 512000, InputPrice: 0.10, OutputPrice: 0.25},
	// DeepSeek
	"deepseek/deepseek-r1":          {ContextWindow: 128000, InputPrice: 0.55, OutputPrice: 2.19},
	"deepseek/deepseek-chat-v3":     {ContextWindow: 128000, InputPrice: 0.27, OutputPrice: 1.10},
}

var defaultOpenRouterModelInfo = openrouterModelInfo{ContextWindow: 128000, InputPrice: 3, OutputPrice: 15}

// OpenRouterProvider implements Provider using OpenRouter's OpenAI-compatible API.
// OpenRouter proxies requests to many different model providers, so it reuses
// the same wire format as the OpenAI provider.
type OpenRouterProvider struct {
	apiKey     string
	baseURL    string
	model      string // full OpenRouter model ID, e.g. "anthropic/claude-sonnet-4"
	httpClient *http.Client
}

func NewOpenRouterProvider(model string) (*OpenRouterProvider, error) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY environment variable is required")
	}
	baseURL := envOr("OPENROUTER_BASE_URL", openrouterDefaultBaseURL)
	if model == "" {
		model = envOr("OPENROUTER_MODEL", openrouterDefaultModel)
	}
	return &OpenRouterProvider{
		apiKey:     key,
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func (p *OpenRouterProvider) modelInfo() openrouterModelInfo {
	if info, ok := knownOpenRouterModels[p.model]; ok {
		return info
	}
	return defaultOpenRouterModelInfo
}

func (p *OpenRouterProvider) Info() ModelInfo {
	info := p.modelInfo()
	return ModelInfo{
		ProviderID:          "openrouter",
		Model:               p.model,
		ContextWindow:       info.ContextWindow,
		InputPrice:          info.InputPrice,
		OutputPrice:         info.OutputPrice,
		CacheHitPrice:       0,
		SupportsThinking:    false,
		SupportsServerTools: false,
	}
}

func (p *OpenRouterProvider) CostForUsage(u Usage) float64 {
	info := p.modelInfo()
	input := float64(u.TotalInputTokens()) * info.InputPrice / 1e6
	output := float64(u.TotalOutputTokens()) * info.OutputPrice / 1e6
	return input + output
}

func (p *OpenRouterProvider) NeedsClientCompaction() bool { return true }

func (p *OpenRouterProvider) Compact(ctx context.Context, system string, messages []Message, budget int) ([]Message, error) {
	return compactMessages(ctx, p, system, messages, budget)
}

func (p *OpenRouterProvider) SendMessage(ctx context.Context, req ProviderRequest) (*Response, error) {
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
			return nil, fmt.Errorf("OpenRouter API error (%d): %s", resp.StatusCode, string(respBody))
		}

		var oaiResp oaiResponse
		if err := json.Unmarshal(respBody, &oaiResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}

		return p.translateResponse(oaiResp), nil
	}
	return nil, fmt.Errorf("request failed after %d retries", maxRetries)
}

// translateMessages reuses the OpenAI wire format since OpenRouter is compatible.
func (p *OpenRouterProvider) translateMessages(system string, messages []Message) []oaiMessage {
	var result []oaiMessage

	if system != "" {
		result = append(result, oaiMessage{Role: "system", Content: system})
	}

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			var toolResults []ContentBlock
			var textBlocks []ContentBlock
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					toolResults = append(toolResults, block)
				} else {
					textBlocks = append(textBlocks, block)
				}
			}

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

// translateTools reuses the OpenAI function tool format.
func (p *OpenRouterProvider) translateTools(tools []any) []oaiTool {
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
		}
	}
	return result
}

func (p *OpenRouterProvider) translateResponse(resp oaiResponse) *Response {
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
		stopReason = ""
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
