package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
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

// OpenAIProvider implements the Provider interface for OpenAI's Responses API.
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

// ── OpenAI Responses API wire format types ───────────────────────────────────

type oaiResponseRequest struct {
	Model       string              `json:"model"`
	Instructions string             `json:"instructions,omitempty"`
	Input       []oaiInputItem      `json:"input,omitempty"`
	Tools       []oaiResponseTool   `json:"tools,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Reasoning   *oaiReasoning       `json:"reasoning,omitempty"`
}

type oaiReasoning struct {
	Effort string `json:"effort,omitempty"`
}

type oaiInputItem struct {
	Type      string           `json:"-"`
	Role      string           `json:"-"`
	Content   []oaiContentPart `json:"-"`
	CallID    string           `json:"-"`
	Name      string           `json:"-"`
	Arguments string           `json:"-"`
	Output    string           `json:"-"`
}

func (item oaiInputItem) MarshalJSON() ([]byte, error) {
	switch item.Type {
	case "function_call_output":
		return json.Marshal(struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		}{item.Type, item.CallID, item.Output})
	case "function_call":
		return json.Marshal(struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}{item.Type, item.CallID, item.Name, item.Arguments})
	default:
		return json.Marshal(struct {
			Role    string           `json:"role"`
			Content []oaiContentPart `json:"content"`
		}{item.Role, item.Content})
	}
}

type oaiContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type oaiResponseTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type oaiResponsesAPIResponse struct {
	Output []oaiOutputItem `json:"output"`
	Usage  oaiResponsesUsage `json:"usage"`
}

type oaiOutputItem struct {
	Type      string           `json:"type"`
	ID        string           `json:"id,omitempty"`
	Role      string           `json:"role,omitempty"`
	Content   []oaiContentPart `json:"content,omitempty"`
	CallID    string           `json:"call_id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Arguments string           `json:"arguments,omitempty"`
	Status    string           `json:"status,omitempty"`
}

type oaiResponsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (p *OpenAIProvider) SendMessage(ctx context.Context, req ProviderRequest) (*Response, error) {
	input := p.translateMessages(req.Messages)
	tools := p.translateTools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16384
	}

	oaiReq := oaiResponseRequest{
		Model:           p.model,
		Instructions:    req.System,
		Input:           input,
		Tools:           tools,
		MaxOutputTokens: maxTokens,
	}
	if req.Thinking != nil && p.modelInfo().IsReasoning {
		oaiReq.Reasoning = &oaiReasoning{Effort: "medium"}
	}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/responses", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	respBody, err := doRequest(ctx, p.httpClient, httpReq, body)
	if err != nil {
		return nil, err
	}

	var oaiResp oaiResponsesAPIResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return p.translateResponse(oaiResp), nil
}

func (p *OpenAIProvider) translateMessages(messages []Message) []oaiInputItem {
	var result []oaiInputItem

	for _, msg := range messages {
		switch msg.Role {
		case RoleUser:
			var toolResults []ContentBlock
			var text strings.Builder
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					toolResults = append(toolResults, block)
					continue
				}
				if block.Type == "text" && block.Text != "" {
					text.WriteString(block.Text)
				}
			}

			if text.Len() > 0 {
				result = append(result, oaiInputItem{
					Role: "user",
					Content: []oaiContentPart{{Type: "input_text", Text: text.String()}},
				})
			}

			for _, tr := range toolResults {
				content := tr.Content
				if tr.IsError {
					content = "Error: " + content
				}
				result = append(result, oaiInputItem{
					Type:   "function_call_output",
					CallID: tr.ToolUseID,
					Output: content,
				})
			}

		case RoleAssistant:
			var text strings.Builder
			var funcCalls []oaiInputItem
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if block.Text != "" {
						text.WriteString(block.Text)
					}
				case "tool_use":
					args, _ := json.Marshal(block.Input)
					funcCalls = append(funcCalls, oaiInputItem{
						Type:      "function_call",
						CallID:    block.ID,
						Name:      block.Name,
						Arguments: string(args),
					})
				}
			}
			if text.Len() > 0 {
				result = append(result, oaiInputItem{
					Role: "assistant",
					Content: []oaiContentPart{{Type: "output_text", Text: text.String()}},
				})
			}
			result = append(result, funcCalls...)
		}
	}

	return result
}

func (p *OpenAIProvider) translateTools(tools []any) []oaiResponseTool {
	var result []oaiResponseTool
	for _, t := range tools {
		switch td := t.(type) {
		case ToolDef:
			result = append(result, oaiResponseTool{
				Type:        "function",
				Name:        td.Name,
				Description: td.Description,
				Parameters:  translateToolParams(td),
			})
		}
	}
	return result
}

func (p *OpenAIProvider) translateResponse(resp oaiResponsesAPIResponse) *Response {
	var content []ContentBlock
	var stopReason StopReason

	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if (part.Type == "output_text" || part.Type == "text") && part.Text != "" {
					content = append(content, TextBlock(part.Text))
				}
			}
		case "function_call":
			var input map[string]any
			json.Unmarshal([]byte(item.Arguments), &input)
			if input == nil {
				input = make(map[string]any)
			}
			content = append(content, ContentBlock{
				Type:  "tool_use",
				ID:    item.CallID,
				Name:  item.Name,
				Input: input,
			})
			stopReason = StopToolUse
		case "reasoning":
			// ignore opaque reasoning summaries for now
		}
	}

	return &Response{
		Role:       RoleAssistant,
		Content:    content,
		StopReason: stopReason,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}
}
