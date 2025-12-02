package publishing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// slack_client.go - Slack Webhook API client with rate limiting and retry logic

// SlackWebhookClient defines the interface for Slack webhook API operations
// Provides methods for posting messages, replying in threads, and health checks
type SlackWebhookClient interface {
	// PostMessage posts a new message to Slack channel
	// Returns SlackResponse with message timestamp (ts) on success
	// Rate limited to 1 message per second per webhook URL
	PostMessage(ctx context.Context, message *SlackMessage) (*SlackResponse, error)

	// ReplyInThread replies to an existing message thread
	// threadTS is the message timestamp of the parent message
	// Sets message.ThreadTS automatically before posting
	ReplyInThread(ctx context.Context, threadTS string, message *SlackMessage) (*SlackResponse, error)

	// Health checks if the webhook URL is reachable
	// Posts a minimal test message to verify connectivity
	// Returns error if webhook is invalid or unreachable
	Health(ctx context.Context) error
}

// HTTPSlackWebhookClient implements SlackWebhookClient using HTTP
// Provides rate limiting (1 msg/sec), retry logic with exponential backoff,
// and comprehensive error handling for Slack webhook API
type HTTPSlackWebhookClient struct {
	httpClient  *http.Client
	webhookURL  string
	rateLimiter *rate.Limiter // 1 message per second
	logger      *slog.Logger
}

// NewHTTPSlackWebhookClient creates a new Slack webhook client
// webhookURL: Slack incoming webhook URL (https://hooks.slack.com/services/...)
// logger: structured logger for debug/info/warn/error logging
func NewHTTPSlackWebhookClient(
	webhookURL string,
	logger *slog.Logger,
) SlackWebhookClient {
	return &HTTPSlackWebhookClient{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12, // TLS 1.2+ required
				},
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     30 * time.Second,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
		webhookURL:  webhookURL,
		rateLimiter: rate.NewLimiter(rate.Every(1*time.Second), 1), // 1 msg/sec, burst 1
		logger:      logger.With("component", "slack_client"),
	}
}

// PostMessage posts a new message to Slack
// Blocks until rate limit token is available (max 1 msg/sec)
// Retries transient errors (429, 503, network) with exponential backoff
func (c *HTTPSlackWebhookClient) PostMessage(ctx context.Context, message *SlackMessage) (*SlackResponse, error) {
	c.logger.DebugContext(ctx, "Posting message to Slack",
		slog.String("webhook_url", maskWebhookURL(c.webhookURL)))

	// Rate limit check (blocks until token available)
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}

	// Build HTTP request
	body, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.webhookURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Execute with retry logic
	resp, err := c.doRequestWithRetry(ctx, req, body)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// ReplyInThread replies to an existing message thread
// Automatically sets message.ThreadTS to threadTS before posting
func (c *HTTPSlackWebhookClient) ReplyInThread(ctx context.Context, threadTS string, message *SlackMessage) (*SlackResponse, error) {
	c.logger.DebugContext(ctx, "Replying in thread",
		slog.String("thread_ts", threadTS))

	// Set thread_ts parameter for threading
	message.ThreadTS = threadTS

	// Use PostMessage (same endpoint, different payload)
	return c.PostMessage(ctx, message)
}

// Health checks webhook connectivity
// Posts a minimal test message to verify webhook is valid
func (c *HTTPSlackWebhookClient) Health(ctx context.Context) error {
	// Post minimal test message
	message := &SlackMessage{
		Text: "Health check",
	}

	_, err := c.PostMessage(ctx, message)
	return err
}

// doRequestWithRetry executes HTTP request with retry logic
// Retries transient errors (429, 503, network) up to 3 times
// Uses exponential backoff: 100ms → 200ms → 400ms → ... → 5s max
func (c *HTTPSlackWebhookClient) doRequestWithRetry(ctx context.Context, req *http.Request, bodyBytes []byte) (*SlackResponse, error) {
	const maxRetries = 3
	backoff := 100 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		// Clone request body for each attempt (HTTP request body is consumed on first use)
		if len(bodyBytes) > 0 {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Execute HTTP request
		httpResp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			if !isRetryableNetworkError(err) {
				return nil, lastErr // Don't retry network errors
			}
			c.logger.WarnContext(ctx, "Retrying after network error",
				slog.Int("attempt", i+1),
				slog.String("error", err.Error()))
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
			continue
		}
		defer httpResp.Body.Close()

		// Read response body
		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Check status code
		if httpResp.StatusCode == http.StatusOK {
			// Success - parse response
			var slackResp SlackResponse
			if err := json.Unmarshal(respBody, &slackResp); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}

			if !slackResp.OK {
				// Slack returned ok=false (error in response body)
				return nil, &SlackAPIError{
					StatusCode:   httpResp.StatusCode,
					ErrorMessage: slackResp.Error,
				}
			}

			return &slackResp, nil
		}

		// Error - parse Slack API error
		apiErr := parseSlackError(httpResp, respBody)
		lastErr = apiErr

		// Check if retryable
		if !IsSlackRetryableError(apiErr) {
			c.logger.ErrorContext(ctx, "Permanent error, not retrying",
				slog.Int("status_code", httpResp.StatusCode),
				slog.String("error", apiErr.Error()))
			return nil, apiErr
		}

		// Retry transient errors (429, 503)
		c.logger.WarnContext(ctx, "Retrying after transient error",
			slog.Int("attempt", i+1),
			slog.Int("status_code", httpResp.StatusCode),
			slog.String("error", apiErr.Error()))

		// Respect Retry-After header for 429 (rate limit)
		if apiErr.StatusCode == http.StatusTooManyRequests && apiErr.RetryAfter > 0 {
			retryAfter := time.Duration(apiErr.RetryAfter) * time.Second
			c.logger.InfoContext(ctx, "Rate limited, respecting Retry-After",
				slog.Duration("retry_after", retryAfter))
			select {
			case <-time.After(retryAfter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			// Exponential backoff for other errors
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			backoff *= 2
			if backoff > 5*time.Second {
				backoff = 5 * time.Second
			}
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", maxRetries, lastErr)
}

// maskWebhookURL masks sensitive webhook token for logging
// Replaces last path segment (token) with "***"
// Example: https://hooks.slack.com/services/T00/B00/XXXX → https://hooks.slack.com/services/T00/B00/***
func maskWebhookURL(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		parts[len(parts)-1] = "***"
	}
	return strings.Join(parts, "/")
}
