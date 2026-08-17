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

	"github.com/ipiton/AMP/pkg/httperror"
	"golang.org/x/time/rate"
)

// telegram_client.go - Telegram Bot API client with rate limiting and retry logic

// TelegramClient defines the interface for Telegram Bot API operations
type TelegramClient interface {
	// SendMessage sends a text message to a Telegram chat/channel/group
	// Returns TelegramResponse with the sent message details on success
	SendMessage(ctx context.Context, message *TelegramMessage) (*TelegramResponse, error)

	// Health checks if the bot token is valid and the API is reachable
	// Uses the "getMe" endpoint, which does not send any message
	Health(ctx context.Context) error
}

// HTTPTelegramClient implements TelegramClient using HTTP
// Provides rate limiting, retry logic with exponential backoff, and
// comprehensive error handling for the Telegram Bot API.
type HTTPTelegramClient struct {
	httpClient  *http.Client
	apiURL      string // Telegram Bot API base URL (e.g. https://api.telegram.org), no trailing slash
	botToken    string
	rateLimiter *rate.Limiter
	logger      *slog.Logger
}

// NewHTTPTelegramClient creates a new Telegram Bot API client.
// apiURL is the Telegram Bot API base URL (defaults to https://api.telegram.org when empty).
// botToken is the Telegram bot token issued by the BotFather bot in Telegram.
// logger is the structured logger used for debug/info/warn/error logging.
func NewHTTPTelegramClient(
	apiURL string,
	botToken string,
	logger *slog.Logger,
) TelegramClient {
	if apiURL == "" {
		apiURL = DefaultTelegramAPIURL
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &HTTPTelegramClient{
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
		apiURL:      strings.TrimRight(apiURL, "/"),
		botToken:    botToken,
		rateLimiter: rate.NewLimiter(rate.Limit(30), 5), // Telegram global limit: ~30 msg/sec, burst 5
		logger:      logger.With("component", "telegram_client"),
	}
}

// endpoint builds the full Telegram Bot API URL for the given method.
// The bot token becomes part of the URL path here; callers must never log
// the result of this function directly (log c.apiURL instead).
func (c *HTTPTelegramClient) endpoint(method string) string {
	return fmt.Sprintf("%s/bot%s/%s", c.apiURL, c.botToken, method)
}

// SendMessage sends a text message to Telegram
// Blocks until a rate limit token is available.
// Retries transient errors (429, 5xx, network) with exponential backoff.
func (c *HTTPTelegramClient) SendMessage(ctx context.Context, message *TelegramMessage) (*TelegramResponse, error) {
	c.logger.DebugContext(ctx, "Sending message to Telegram",
		slog.String("api_url", c.apiURL))

	// Rate limit check (blocks until token available)
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}

	// Build HTTP request
	body, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("sendMessage"), bytes.NewReader(body))
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

// Health checks bot token validity and API connectivity via the "getMe"
// endpoint. Unlike SendMessage, this never delivers a visible message.
func (c *HTTPTelegramClient) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("getMe"), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read health check response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return parseTelegramError(httpResp, respBody)
	}

	var tgResp TelegramResponse
	if err := json.Unmarshal(respBody, &tgResp); err != nil {
		return fmt.Errorf("failed to parse health check response: %w", err)
	}
	if !tgResp.OK {
		return httperror.NewHTTPError(httpResp.StatusCode, tgResp.Description, ProviderTelegram)
	}

	return nil
}

// doRequestWithRetry executes HTTP request with retry logic
// Retries transient errors (429, 5xx, network) up to 3 times
// Uses exponential backoff: 100ms → 200ms → 400ms → ... → 5s max
func (c *HTTPTelegramClient) doRequestWithRetry(ctx context.Context, req *http.Request, bodyBytes []byte) (*TelegramResponse, error) {
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
			if !httperror.IsRetryableNetworkError(err) {
				return nil, lastErr // Don't retry non-network-transient errors
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
		defer func() { _ = httpResp.Body.Close() }()

		// Read response body
		respBody, err := io.ReadAll(httpResp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		// Check status code
		if httpResp.StatusCode == http.StatusOK {
			// Success - parse response
			var tgResp TelegramResponse
			if err := json.Unmarshal(respBody, &tgResp); err != nil {
				return nil, fmt.Errorf("failed to parse response: %w", err)
			}

			if !tgResp.OK {
				// Telegram returned ok=false with a 200 status (rare, but defensive)
				return nil, httperror.NewHTTPError(httpResp.StatusCode, tgResp.Description, ProviderTelegram)
			}

			return &tgResp, nil
		}

		// Error - parse Telegram API error
		apiErr := parseTelegramError(httpResp, respBody)
		lastErr = apiErr

		// Check if retryable
		if !(&httperror.PublishingClassifier{}).IsRetryable(apiErr) {
			c.logger.ErrorContext(ctx, "Permanent error, not retrying",
				slog.Int("status_code", httpResp.StatusCode),
				slog.String("error", apiErr.Error()))
			return nil, apiErr
		}

		// Retry transient errors (429, 5xx)
		c.logger.WarnContext(ctx, "Retrying after transient error",
			slog.Int("attempt", i+1),
			slog.Int("status_code", httpResp.StatusCode),
			slog.String("error", apiErr.Error()))

		// Respect retry_after for 429 (rate limit)
		if apiErr.StatusCode == http.StatusTooManyRequests && apiErr.RetryAfter > 0 {
			retryAfter := time.Duration(apiErr.RetryAfter) * time.Second
			c.logger.InfoContext(ctx, "Rate limited, respecting retry_after",
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
