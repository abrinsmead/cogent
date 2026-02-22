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
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	defaultModel   = "claude-opus-4-6"
)

// modelInfo holds context window size and per-token pricing (USD per MTok).
type modelInfo struct {
	ContextWindow int     // max tokens
	InputPrice    float64 // $/MTok for base input
	CacheHitPrice float64 // $/MTok for cache hits & refreshes
	OutputPrice   float64 // $/MTok for output
}

// Known model specs. Prices from https://docs.anthropic.com/en/docs/about-claude/models
// Prices are in $/MTok as listed on the pricing page.
var knownModels = map[string]modelInfo{
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

// fallback for unknown models
var defaultModelInfo = modelInfo{ContextWindow: 200000, InputPrice: 3, CacheHitPrice: 0.30, OutputPrice: 15}

type Client struct {
	apiKey     string
	baseURL    string
	model      string
	httpClient *http.Client
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func NewClient() (*Client, error) {
	key := envOr("ANTHROPIC_API_KEY", "")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
	}
	baseURL := envOr("ANTHROPIC_BASE_URL", defaultBaseURL)
	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}
	return &Client{
		apiKey:     key,
		baseURL:    baseURL,
		model:      envOr("ANTHROPIC_MODEL", defaultModel),
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}, nil
}

// validateBaseURL requires HTTPS unless the host is localhost, 127.0.0.1, or ::1.
func validateBaseURL(raw string) error {
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

func (c *Client) Model() string {
	return c.model
}

func (c *Client) info() modelInfo {
	if info, ok := knownModels[c.model]; ok {
		return info
	}
	return defaultModelInfo
}

// ContextWindow returns the max context window size for the current model.
func (c *Client) ContextWindow() int {
	return c.info().ContextWindow
}

// CostForUsage calculates the USD cost for a given usage.
// Prices in modelInfo are $/MTok, so we divide token counts by 1e6.
func (c *Client) CostForUsage(u Usage) float64 {
	info := c.info()
	input := float64(u.InputTokens) * info.InputPrice / 1e6
	output := float64(u.OutputTokens) * info.OutputPrice / 1e6
	cacheHits := float64(u.CacheReadInputTokens) * info.CacheHitPrice / 1e6
	cacheCreation := float64(u.CacheCreationInputTokens) * info.InputPrice * 1.25 / 1e6
	return input + output + cacheHits + cacheCreation
}

func (c *Client) SendMessage(system string, messages []Message, tools []ToolDef) (*Response, error) {
	return c.SendMessageCtx(context.Background(), system, messages, tools)
}

func (c *Client) SendMessageCtx(ctx context.Context, system string, messages []Message, tools []ToolDef) (*Response, error) {
	req := Request{
		Model:     c.model,
		MaxTokens: 8192,
		System:    system,
		Messages:  messages,
		Tools:     tools,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	maxRetries := 3
	backoff := 2 * time.Second

	for attempt := range maxRetries {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("X-API-Key", c.apiKey)
		httpReq.Header.Set("Anthropic-Version", apiVersion)
		resp, err := c.httpClient.Do(httpReq)
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
