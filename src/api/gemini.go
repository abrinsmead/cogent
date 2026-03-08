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
	geminiBaseURL     = "https://generativelanguage.googleapis.com/v1beta"
	geminiDefaultModel = "gemini-2.5-pro"
)

type geminiModelInfo struct {
	ContextWindow int
	InputPrice    float64 // $/MTok
	OutputPrice   float64 // $/MTok
	HasThinking   bool
}

var knownGeminiModels = map[string]geminiModelInfo{
	"gemini-2.5-pro":   {ContextWindow: 1048576, InputPrice: 1.25, OutputPrice: 10, HasThinking: true},
	"gemini-2.5-flash": {ContextWindow: 1048576, InputPrice: 0.15, OutputPrice: 0.60, HasThinking: true},
	"gemini-2.0-flash": {ContextWindow: 1048576, InputPrice: 0.10, OutputPrice: 0.40},
}

var defaultGeminiModelInfo = geminiModelInfo{ContextWindow: 1048576, InputPrice: 0.15, OutputPrice: 0.60}

// GeminiProvider implements the Provider interface for Google's Gemini API.
type GeminiProvider struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewGeminiProvider(model string) (*GeminiProvider, error) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY environment variable is required")
	}
	if model == "" {
		model = envOr("GEMINI_MODEL", geminiDefaultModel)
	}
	return &GeminiProvider{
		apiKey:     key,
		model:      model,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func (p *GeminiProvider) modelInfo() geminiModelInfo {
	if info, ok := knownGeminiModels[p.model]; ok {
		return info
	}
	return defaultGeminiModelInfo
}

func (p *GeminiProvider) Info() ModelInfo {
	info := p.modelInfo()
	return ModelInfo{
		ProviderID:          "gemini",
		Model:               p.model,
		ContextWindow:       info.ContextWindow,
		InputPrice:          info.InputPrice,
		OutputPrice:         info.OutputPrice,
		CacheHitPrice:       0,
		SupportsThinking:    info.HasThinking,
		SupportsServerTools: false,
	}
}

func (p *GeminiProvider) CostForUsage(u Usage) float64 {
	info := p.modelInfo()
	input := float64(u.TotalInputTokens()) * info.InputPrice / 1e6
	output := float64(u.TotalOutputTokens()) * info.OutputPrice / 1e6
	return input + output
}

func (p *GeminiProvider) NeedsClientCompaction() bool { return true }

func (p *GeminiProvider) Compact(ctx context.Context, system string, messages []Message, budget int) ([]Message, error) {
	return compactMessages(ctx, p, system, messages, budget)
}

// ── Gemini wire format types ────────────────────────────────────────────────

type geminiRequest struct {
	Contents          []geminiContent          `json:"contents"`
	Tools             []geminiToolSet          `json:"tools,omitempty"`
	SystemInstruction *geminiContent           `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
	ThinkingConfig    *geminiThinkingConfig    `json:"thinkingConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiToolSet struct {
	FunctionDeclarations []geminiFuncDecl `json:"functionDeclarations"`
}

type geminiFuncDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate  `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
}

// ── Translation ─────────────────────────────────────────────────────────────

func (p *GeminiProvider) SendMessage(ctx context.Context, req ProviderRequest) (*Response, error) {
	gemReq := p.buildRequest(req)

	body, err := json.Marshal(gemReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/models/%s:generateContent?key=%s", geminiBaseURL, p.model, p.apiKey)

	maxRetries := 3
	backoff := 2 * time.Second

	for attempt := range maxRetries {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

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
			return nil, fmt.Errorf("Gemini API error (%d): %s", resp.StatusCode, string(respBody))
		}

		var gemResp geminiResponse
		if err := json.Unmarshal(respBody, &gemResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}

		return p.translateResponse(gemResp), nil
	}
	return nil, fmt.Errorf("request failed after %d retries", maxRetries)
}

func (p *GeminiProvider) buildRequest(req ProviderRequest) geminiRequest {
	gemReq := geminiRequest{}

	// System instruction
	if req.System != "" {
		gemReq.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.System}},
		}
	}

	// Translate messages
	gemReq.Contents = p.translateMessages(req.Messages)

	// Translate tools
	gemReq.Tools = p.translateTools(req.Tools)

	// Generation config
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16384
	}
	gemReq.GenerationConfig = &geminiGenerationConfig{
		MaxOutputTokens: maxTokens,
	}

	// Thinking config for thinking models
	if req.Thinking != nil && req.Thinking.Type == "enabled" && p.modelInfo().HasThinking {
		gemReq.ThinkingConfig = &geminiThinkingConfig{
			ThinkingBudget: req.Thinking.BudgetTokens,
		}
	}

	return gemReq
}

func (p *GeminiProvider) translateMessages(messages []Message) []geminiContent {
	var result []geminiContent

	for _, msg := range messages {
		role := "user"
		if msg.Role == RoleAssistant {
			role = "model"
		}

		var parts []geminiPart
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					parts = append(parts, geminiPart{Text: block.Text})
				}
			case "tool_use":
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: block.Name,
						Args: block.Input,
					},
				})
			case "tool_result":
				resp := map[string]any{"result": block.Content}
				if block.IsError {
					resp["error"] = block.Content
					delete(resp, "result")
				}
				parts = append(parts, geminiPart{
					FunctionResponse: &geminiFunctionResponse{
						Name:     block.Name,
						Response: resp,
					},
				})
			// thinking, compaction, server_tool_use, web_search_tool_result — skip
			}
		}

		if len(parts) > 0 {
			result = append(result, geminiContent{Role: role, Parts: parts})
		}
	}

	return result
}

func (p *GeminiProvider) translateTools(tools []any) []geminiToolSet {
	var decls []geminiFuncDecl
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
			decls = append(decls, geminiFuncDecl{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  params,
			})
		// ServerTool — skip
		}
	}
	if len(decls) == 0 {
		return nil
	}
	return []geminiToolSet{{FunctionDeclarations: decls}}
}

func (p *GeminiProvider) translateResponse(resp geminiResponse) *Response {
	if len(resp.Candidates) == 0 {
		return &Response{
			Usage: Usage{
				InputTokens:  resp.UsageMetadata.PromptTokenCount,
				OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
			},
		}
	}

	candidate := resp.Candidates[0]
	var content []ContentBlock

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			content = append(content, TextBlock(part.Text))
		}
		if part.FunctionCall != nil {
			content = append(content, ContentBlock{
				Type:  "tool_use",
				ID:    fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, time.Now().UnixNano()),
				Name:  part.FunctionCall.Name,
				Input: part.FunctionCall.Args,
			})
		}
	}

	var stopReason StopReason
	switch candidate.FinishReason {
	case "MAX_TOKENS":
		stopReason = StopMaxTokens
	case "STOP":
		// Check if there are function calls — Gemini uses STOP even with tool calls
		for _, block := range content {
			if block.Type == "tool_use" {
				stopReason = StopToolUse
				break
			}
		}
	default:
		stopReason = ""
	}

	return &Response{
		Role:       RoleAssistant,
		Content:    content,
		StopReason: stopReason,
		Usage: Usage{
			InputTokens:  resp.UsageMetadata.PromptTokenCount,
			OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
		},
	}
}
