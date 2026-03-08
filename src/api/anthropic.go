package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
	anthropicDefaultModel   = "claude-opus-4-6"
)

// knownAnthropicModels holds context window size and per-token pricing (USD per MTok).
// Prices from https://docs.anthropic.com/en/docs/about-claude/models
var knownAnthropicModels = map[string]anthropicModelInfo{
	// Opus 4.6
	"claude-opus-4-6": {ContextWindow: 200000, InputPrice: 5, CacheHitPrice: 0.50, OutputPrice: 25},
	// Opus 4.5
	"claude-opus-4-5":          {ContextWindow: 200000, InputPrice: 5, CacheHitPrice: 0.50, OutputPrice: 25},
	"claude-opus-4-5-20250610": {ContextWindow: 200000, InputPrice: 5, CacheHitPrice: 0.50, OutputPrice: 25},
	// Opus 4.1
	"claude-opus-4-1":          {ContextWindow: 200000, InputPrice: 15, CacheHitPrice: 1.50, OutputPrice: 75},
	"claude-opus-4-1-20250528": {ContextWindow: 200000, InputPrice: 15, CacheHitPrice: 1.50, OutputPrice: 75},
	// Opus 4
	"claude-opus-4":          {ContextWindow: 200000, InputPrice: 15, CacheHitPrice: 1.50, OutputPrice: 75},
	"claude-opus-4-20250514": {ContextWindow: 200000, InputPrice: 15, CacheHitPrice: 1.50, OutputPrice: 75},
	// Sonnet 4.6
	"claude-sonnet-4-6": {ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15},
	// Sonnet 4.5
	"claude-sonnet-4-5":          {ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15},
	"claude-sonnet-4-5-20250610": {ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15},
	// Sonnet 4
	"claude-sonnet-4":          {ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15},
	"claude-sonnet-4-20250514": {ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15},
	// Sonnet 3.7 (deprecated)
	"claude-3-7-sonnet-20250219": {ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15},
	// Haiku 4.5
	"claude-haiku-4-5":          {ContextWindow: 200000, InputPrice: 1, CacheHitPrice: 0.10, OutputPrice: 5},
	"claude-haiku-4-5-20250610": {ContextWindow: 200000, InputPrice: 1, CacheHitPrice: 0.10, OutputPrice: 5},
	// Haiku 3.5
	"claude-3-5-haiku-20241022": {ContextWindow: 200000, InputPrice: 0.80, CacheHitPrice: 0.08, OutputPrice: 4},
	// Opus 3 (deprecated)
	"claude-3-opus-20240229": {ContextWindow: 200000, InputPrice: 15, CacheHitPrice: 1.50, OutputPrice: 75},
	// Haiku 3
	"claude-3-haiku-20240307": {ContextWindow: 200000, InputPrice: 0.25, CacheHitPrice: 0.03, OutputPrice: 1.25},
}

type anthropicModelInfo struct {
	ContextWindow int
	InputPrice    float64
	CacheHitPrice float64
	OutputPrice   float64
}

// fallback for unknown Anthropic models
var defaultAnthropicModelInfo = anthropicModelInfo{
	ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15,
}

// AnthropicProvider implements the Provider interface for Anthropic's Claude API.
type AnthropicProvider struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewAnthropicProvider creates an Anthropic provider. If model is empty, it uses
// the ANTHROPIC_MODEL env var or falls back to claude-opus-4-6.
func NewAnthropicProvider(model string) (*AnthropicProvider, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
	}
	baseURL := envOr("ANTHROPIC_BASE_URL", anthropicDefaultBaseURL)
	if err := validateAnthropicBaseURL(baseURL); err != nil {
		return nil, err
	}
	if model == "" {
		model = envOr("ANTHROPIC_MODEL", anthropicDefaultModel)
	}
	return &AnthropicProvider{
		apiKey:     key,
		baseURL:    baseURL,
		model:      model,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

func validateAnthropicBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}
	host := u.Hostname()
	if u.Scheme == "https" {
		return nil
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}
	return fmt.Errorf("ANTHROPIC_BASE_URL must use HTTPS (got %s)", raw)
}

func (p *AnthropicProvider) modelInfo() anthropicModelInfo {
	if info, ok := knownAnthropicModels[p.model]; ok {
		return info
	}
	return defaultAnthropicModelInfo
}

func (p *AnthropicProvider) Info() ModelInfo {
	info := p.modelInfo()
	return ModelInfo{
		ProviderID:          "anthropic",
		Model:               p.model,
		ContextWindow:       info.ContextWindow,
		InputPrice:          info.InputPrice,
		OutputPrice:         info.OutputPrice,
		CacheHitPrice:       info.CacheHitPrice,
		SupportsThinking:    true,
		SupportsServerTools: true,
	}
}

func (p *AnthropicProvider) CostForUsage(u Usage) float64 {
	info := p.modelInfo()
	inputToks := u.TotalInputTokens()
	outputToks := u.TotalOutputTokens()
	input := float64(inputToks) * info.InputPrice / 1e6
	output := float64(outputToks) * info.OutputPrice / 1e6
	cacheHits := float64(u.CacheReadInputTokens) * info.CacheHitPrice / 1e6
	cacheCreation := float64(u.CacheCreationInputTokens) * info.InputPrice * 1.25 / 1e6
	return input + output + cacheHits + cacheCreation
}

func (p *AnthropicProvider) NeedsClientCompaction() bool { return false }

func (p *AnthropicProvider) Compact(_ context.Context, _ string, messages []Message, _ int) ([]Message, error) {
	return messages, nil // server handles compaction
}

func (p *AnthropicProvider) SendMessage(ctx context.Context, req ProviderRequest) (*Response, error) {
	// Build system prompt as a content block array.
	var sysBlocks []SystemBlock
	if req.System != "" {
		sysBlocks = []SystemBlock{{
			Type: "text",
			Text: req.System,
		}}
	}

	// Compaction trigger at 80% of context window (min 50k per API requirement).
	info := p.modelInfo()
	trigger := info.ContextWindow * 80 / 100
	if trigger < 50000 {
		trigger = 50000
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16384
	}

	apiReq := Request{
		Model:        p.model,
		MaxTokens:    maxTokens,
		CacheControl: &CacheControl{Type: "ephemeral"},
		System:       sysBlocks,
		Messages:     req.Messages,
		Tools:        req.Tools,
		Thinking:     req.Thinking,
		ContextManagement: &ContextManagement{
			Edits: []ContextEdit{{
				Type: "compact_20260112",
				Trigger: &CompactTrigger{
					Type:  "input_tokens",
					Value: trigger,
				},
			}},
		},
	}

	// Extended thinking requires max_tokens to accommodate both thinking
	// budget and response tokens.
	if req.Thinking != nil && req.Thinking.Type == "enabled" && req.Thinking.BudgetTokens > 0 {
		minMax := req.Thinking.BudgetTokens + 8192
		if apiReq.MaxTokens < minMax {
			apiReq.MaxTokens = minMax
		}
	}

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	maxRetries := 3
	backoff := 2 * time.Second

	for attempt := range maxRetries {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-API-Key", p.apiKey)
		httpReq.Header.Set("Anthropic-Version", anthropicAPIVersion)
		httpReq.Header.Set("Anthropic-Beta", "compact-2026-01-12")

		resp, err := p.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		// Retry on rate-limit or overloaded errors
		if (resp.StatusCode == 429 || resp.StatusCode == 529) && attempt < maxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			var errResp ErrorResponse
			if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
				return nil, fmt.Errorf("API error (%d): %s: %s", resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
			}
			return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
		}

		var apiResp Response
		if err := json.Unmarshal(respBody, &apiResp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		return &apiResp, nil
	}
	return nil, fmt.Errorf("request failed after %d retries", maxRetries)
}
