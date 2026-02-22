package api

import (
	"bytes"
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
	defaultModel   = "claude-sonnet-4-20250514"
)

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

func (c *Client) SendMessage(system string, messages []Message, tools []ToolDef) (*Response, error) {
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
		httpReq, err := http.NewRequest("POST", c.baseURL+"/v1/messages", bytes.NewReader(body))
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
			time.Sleep(backoff)
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
