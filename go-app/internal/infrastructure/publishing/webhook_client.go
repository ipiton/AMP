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
	"strconv"
	"time"
)

// WebhookHTTPClient handles HTTP requests to webhook endpoints with retry logic
type WebhookHTTPClient struct {
	httpClient  *http.Client
	retryConfig WebhookRetryConfig
	authManager *AuthManager
	logger      *slog.Logger
}

// NewWebhookHTTPClient creates a new webhook HTTP client
func NewWebhookHTTPClient(retryConfig WebhookRetryConfig, logger *slog.Logger) *WebhookHTTPClient {
	// Create HTTP client with optimized settings
	httpClient := &http.Client{
		Timeout: 10 * time.Second, // Default timeout
		Transport: &http.Transport{
			// TLS configuration
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12, // Enforce TLS 1.2+
			},
			// Connection pooling settings
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
			// HTTP/2 support
			ForceAttemptHTTP2: true,
			// Timeouts
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	return &WebhookHTTPClient{
		httpClient:  httpClient,
		retryConfig: retryConfig,
		authManager: NewAuthManager(logger),
		logger:      logger,
	}
}

// Post sends a POST request to webhook endpoint with retry logic
func (c *WebhookHTTPClient) Post(ctx context.Context, url string, payload map[string]interface{}, headers map[string]string, authConfig *AuthConfig) (*WebhookResponse, error) {
	startTime := time.Now()

	// Marshal payload to JSON
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, &WebhookError{
			Type:    ErrorTypeValidation,
			Message: fmt.Sprintf("failed to marshal payload: %v", err),
			Cause:   err,
		}
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, &WebhookError{
			Type:    ErrorTypeValidation,
			Message: fmt.Sprintf("failed to create request: %v", err),
			Cause:   err,
		}
	}

	// Set Content-Type header
	req.Header.Set("Content-Type", "application/json")

	// Set User-Agent header
	req.Header.Set("User-Agent", "Alert-History-Service/1.0")

	// Add custom headers
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Apply authentication
	if authConfig != nil {
		if err := c.authManager.ApplyAuth(req, *authConfig); err != nil {
			return nil, &WebhookError{
				Type:    ErrorTypeAuth,
				Message: fmt.Sprintf("authentication failed: %v", err),
				Cause:   err,
			}
		}
	}

	// Execute request with retry logic
	resp, err := c.doRequestWithRetry(ctx, req, payloadBytes)
	if err != nil {
		return nil, err
	}

	duration := time.Since(startTime)

	c.logger.InfoContext(ctx, "Webhook POST successful",
		slog.String("url", maskURL(url)),
		slog.Int("status_code", resp.StatusCode),
		slog.Duration("duration", duration),
		slog.Int("payload_size", len(payloadBytes)))

	return resp, nil
}

// doRequestWithRetry executes HTTP request with exponential backoff retry
func (c *WebhookHTTPClient) doRequestWithRetry(ctx context.Context, req *http.Request, bodyBytes []byte) (*WebhookResponse, error) {
	var lastErr error
	backoff := c.retryConfig.BaseBackoff

	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		// Log retry attempt
		if attempt > 0 {
			c.logger.WarnContext(ctx, "Retrying request after error",
				slog.Int("attempt", attempt),
				slog.Int("max_retries", c.retryConfig.MaxRetries),
				slog.Duration("backoff", backoff),
				slog.String("last_error", lastErr.Error()))

			// Wait before retry (with context cancellation support)
			select {
			case <-ctx.Done():
				return nil, &WebhookError{
					Type:    ErrorTypeTimeout,
					Message: "context cancelled during retry",
					Cause:   ctx.Err(),
				}
			case <-time.After(backoff):
				// Continue with retry
			}
		}

		// Clone request body (consumed on first attempt)
		if len(bodyBytes) > 0 {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Execute HTTP request
		reqStartTime := time.Now()
		resp, err := c.httpClient.Do(req)
		duration := time.Since(reqStartTime)

		// Handle network errors
		if err != nil {
			lastErr = &WebhookError{
				Type:    ErrorTypeNetwork,
				Message: fmt.Sprintf("HTTP request failed: %v", err),
				Cause:   err,
			}

			// Log network error
			c.logger.ErrorContext(ctx, "Network error",
				slog.Int("attempt", attempt),
				slog.Duration("duration", duration),
				slog.String("error", err.Error()))

			// Retry network errors if retryable
			if IsWebhookRetryableError(lastErr) && attempt < c.retryConfig.MaxRetries {
				backoff = c.calculateBackoff(backoff)
				continue
			}

			// Permanent network error or max retries exceeded
			return nil, lastErr
		}

		// Read response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			c.logger.WarnContext(ctx, "Failed to read response body",
				slog.String("error", err.Error()))
			body = []byte{}
		}

		// Check HTTP status code
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			// Success (2xx)
			c.logger.DebugContext(ctx, "Request successful",
				slog.Int("attempt", attempt),
				slog.Int("status_code", resp.StatusCode),
				slog.Duration("duration", duration),
				slog.Int("response_size", len(body)))

			return &WebhookResponse{
				StatusCode: resp.StatusCode,
				Body:       body,
				Headers:    resp.Header,
				Duration:   duration,
			}, nil
		}

		// HTTP error (4xx, 5xx)
		errorType := classifyErrorType(resp.StatusCode)
		category := classifyHTTPError(resp.StatusCode)

		lastErr = &WebhookError{
			Type:       errorType,
			Message:    fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)),
			StatusCode: resp.StatusCode,
		}

		c.logger.WarnContext(ctx, "HTTP error",
			slog.Int("attempt", attempt),
			slog.Int("status_code", resp.StatusCode),
			slog.String("error_type", errorType.String()),
			slog.String("category", category.String()),
			slog.Duration("duration", duration))

		// Check if retryable
		if category == ErrorCategoryRetryable && attempt < c.retryConfig.MaxRetries {
			// Check for Retry-After header (429 Rate Limit)
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil {
					backoff = time.Duration(seconds) * time.Second
					c.logger.InfoContext(ctx, "Rate limited, respecting Retry-After header",
						slog.Int("retry_after_seconds", seconds),
						slog.Duration("backoff", backoff))
				}
			} else {
				backoff = c.calculateBackoff(backoff)
			}

			// Continue retry loop
			continue
		}

		// Permanent error or max retries exceeded
		c.logger.ErrorContext(ctx, "Permanent error or max retries exceeded",
			slog.Int("attempt", attempt),
			slog.Int("status_code", resp.StatusCode),
			slog.String("error_type", errorType.String()))

		return nil, lastErr
	}

	// Max retries exceeded
	return nil, fmt.Errorf("max retries (%d) exceeded: %w", c.retryConfig.MaxRetries, lastErr)
}

// calculateBackoff calculates next backoff duration using exponential backoff
func (c *WebhookHTTPClient) calculateBackoff(current time.Duration) time.Duration {
	next := time.Duration(float64(current) * c.retryConfig.Multiplier)
	if next > c.retryConfig.MaxBackoff {
		return c.retryConfig.MaxBackoff
	}
	return next
}

// SetTimeout sets HTTP client timeout
func (c *WebhookHTTPClient) SetTimeout(timeout time.Duration) {
	c.httpClient.Timeout = timeout
}

// Close closes idle connections
func (c *WebhookHTTPClient) Close() {
	if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}
