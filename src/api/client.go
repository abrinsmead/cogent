package api

import (
	"context"
	"os"
)

// Client is a backward-compatible wrapper around a Provider.
// New code should use Provider directly; Client exists so that the migration
// can be done incrementally.
type Client struct {
	provider Provider
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// NewClient creates a Client backed by the default provider (from config).
func NewClient() (*Client, error) {
	spec := DefaultModelSpec()
	p, err := NewProvider(spec)
	if err != nil {
		return nil, err
	}
	return &Client{provider: p}, nil
}

// Provider returns the underlying Provider.
func (c *Client) Provider() Provider { return c.provider }

func (c *Client) Model() string        { return c.provider.Info().Model }
func (c *Client) ContextWindow() int   { return c.provider.Info().ContextWindow }
func (c *Client) CostForUsage(u Usage) float64 { return c.provider.CostForUsage(u) }

func (c *Client) SendMessage(system string, messages []Message, tools []any) (*Response, error) {
	return c.SendMessageCtx(context.Background(), system, messages, tools, nil)
}

func (c *Client) SendMessageCtx(ctx context.Context, system string, messages []Message, tools []any, thinking *ThinkingConfig) (*Response, error) {
	return c.provider.SendMessage(ctx, ProviderRequest{
		System:    system,
		Messages:  messages,
		Tools:     tools,
		Thinking:  thinking,
		MaxTokens: 16384,
	})
}
