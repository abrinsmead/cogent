package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultMaxRetries = 3
	defaultBackoff    = 2 * time.Second
)

// retryableStatusCode returns true for HTTP status codes that should trigger a retry.
// 429 = rate limited, 529 = overloaded (Anthropic-specific, safe to check for all).
func retryableStatusCode(code int) bool {
	return code == 429 || code == 529
}

// doRequest executes an HTTP request with retries on rate-limit/overload errors.
// The caller builds the *http.Request (including headers); this function handles
// the retry loop, body reading, and error formatting.
//
// On success (200), returns the raw response body.
// On non-retryable errors, returns an error with the status code and body.
func doRequest(ctx context.Context, client *http.Client, req *http.Request, body []byte) ([]byte, error) {
	backoff := defaultBackoff

	for attempt := range defaultMaxRetries {
		// Clone the request for each attempt (body reader is consumed on each send).
		httpReq := req.Clone(ctx)
		httpReq.Body = io.NopCloser(bytes.NewReader(body))

		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("send request: %w", err)
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if retryableStatusCode(resp.StatusCode) && attempt < defaultMaxRetries-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}

		if resp.StatusCode != http.StatusOK {
			// Try to extract a structured error message.
			var errResp ErrorResponse
			if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
				return nil, fmt.Errorf("API error (%d): %s: %s", resp.StatusCode, errResp.Error.Type, errResp.Error.Message)
			}
			return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
		}

		return respBody, nil
	}
	return nil, fmt.Errorf("request failed after %d retries", defaultMaxRetries)
}
