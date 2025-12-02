package publishing

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// PagerDuty Events API v2 Client
// https://developer.pagerduty.com/docs/ZG9jOjExMDI5NTgw-events-api-v2-overview

// PagerDutyEventsClient defines the interface for PagerDuty Events API v2
type PagerDutyEventsClient interface {
	// TriggerEvent sends a trigger event to PagerDuty (creates or updates an incident)
	TriggerEvent(ctx context.Context, req *TriggerEventRequest) (*EventResponse, error)

	// AcknowledgeEvent acknowledges an event in PagerDuty
	AcknowledgeEvent(ctx context.Context, req *AcknowledgeEventRequest) (*EventResponse, error)

	// ResolveEvent resolves an event in PagerDuty
	ResolveEvent(ctx context.Context, req *ResolveEventRequest) (*EventResponse, error)

	// SendChangeEvent sends a change event to PagerDuty (deployment, config change, etc.)
	SendChangeEvent(ctx context.Context, req *ChangeEventRequest) (*ChangeEventResponse, error)

	// Health checks API connectivity
	Health(ctx context.Context) error
}

// PagerDutyClientConfig holds configuration for PagerDuty Events API v2 client
type PagerDutyClientConfig struct {
	// BaseURL is the base URL for PagerDuty Events API
	// Default: https://events.pagerduty.com
	BaseURL string

	// Timeout is the HTTP client timeout
	// Default: 10s
	Timeout time.Duration

	// MaxRetries is the maximum number of retries for transient errors
	// Default: 3
	MaxRetries int

	// RateLimit is the rate limit in requests per minute
	// PagerDuty limit: 120 req/min per integration
	// Default: 120.0
	RateLimit float64
}

// PagerDutyRetryConfig holds retry configuration
type PagerDutyRetryConfig struct {
	// MaxRetries is the maximum number of retry attempts
	MaxRetries int

	// BaseBackoff is the initial backoff duration
	BaseBackoff time.Duration

	// MaxBackoff is the maximum backoff duration
	MaxBackoff time.Duration
}

// pagerDutyEventsClientImpl implements PagerDutyEventsClient
type pagerDutyEventsClientImpl struct {
	httpClient  *http.Client
	baseURL     string
	rateLimiter *rate.Limiter
	logger      *slog.Logger
	metrics     *PagerDutyMetrics
	retryConfig PagerDutyRetryConfig
}

// NewPagerDutyEventsClient creates a new PagerDuty Events API v2 client
func NewPagerDutyEventsClient(config PagerDutyClientConfig, logger *slog.Logger) PagerDutyEventsClient {
	// Set defaults
	if config.BaseURL == "" {
		config.BaseURL = "https://events.pagerduty.com"
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RateLimit == 0 {
		config.RateLimit = 120.0 // 120 req/min
	}

	// Create HTTP client with TLS 1.2+
	httpClient := &http.Client{
		Timeout: config.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		},
	}

	// Create rate limiter (requests per second)
	// 120 req/min = 2 req/sec
	rateLimiter := rate.NewLimiter(rate.Limit(config.RateLimit/60.0), 10) // Burst: 10

	return &pagerDutyEventsClientImpl{
		httpClient:  httpClient,
		baseURL:     config.BaseURL,
		rateLimiter: rateLimiter,
		logger:      logger,
		metrics:     NewPagerDutyMetrics(),
		retryConfig: PagerDutyRetryConfig{
			MaxRetries:  config.MaxRetries,
			BaseBackoff: 100 * time.Millisecond,
			MaxBackoff:  5 * time.Second,
		},
	}
}

// TriggerEvent sends a trigger event to PagerDuty
func (c *pagerDutyEventsClientImpl) TriggerEvent(ctx context.Context, req *TriggerEventRequest) (*EventResponse, error) {
	// Validate request
	if req.RoutingKey == "" {
		return nil, ErrMissingRoutingKey
	}

	// Set event action
	req.EventAction = EventActionTrigger

	// Send request
	resp, err := c.doRequest(ctx, "POST", "/v2/events", req)
	if err != nil {
		c.logger.Error("Failed to trigger PagerDuty event",
			"error", err,
			"routing_key", req.RoutingKey,
			"dedup_key", req.DedupKey,
		)
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response
	var eventResp EventResponse
	if err := json.NewDecoder(resp.Body).Decode(&eventResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Debug("PagerDuty event triggered",
		"routing_key", req.RoutingKey,
		"dedup_key", eventResp.DedupKey,
		"status", eventResp.Status,
	)

	return &eventResp, nil
}

// AcknowledgeEvent acknowledges an event in PagerDuty
func (c *pagerDutyEventsClientImpl) AcknowledgeEvent(ctx context.Context, req *AcknowledgeEventRequest) (*EventResponse, error) {
	// Validate request
	if req.RoutingKey == "" {
		return nil, ErrMissingRoutingKey
	}
	if req.DedupKey == "" {
		return nil, ErrInvalidDedupKey
	}

	// Set event action
	req.EventAction = EventActionAcknowledge

	// Send request
	resp, err := c.doRequest(ctx, "POST", "/v2/events", req)
	if err != nil {
		c.logger.Error("Failed to acknowledge PagerDuty event",
			"error", err,
			"routing_key", req.RoutingKey,
			"dedup_key", req.DedupKey,
		)
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response
	var eventResp EventResponse
	if err := json.NewDecoder(resp.Body).Decode(&eventResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Debug("PagerDuty event acknowledged",
		"routing_key", req.RoutingKey,
		"dedup_key", eventResp.DedupKey,
		"status", eventResp.Status,
	)

	return &eventResp, nil
}

// ResolveEvent resolves an event in PagerDuty
func (c *pagerDutyEventsClientImpl) ResolveEvent(ctx context.Context, req *ResolveEventRequest) (*EventResponse, error) {
	// Validate request
	if req.RoutingKey == "" {
		return nil, ErrMissingRoutingKey
	}
	if req.DedupKey == "" {
		return nil, ErrInvalidDedupKey
	}

	// Set event action
	req.EventAction = EventActionResolve

	// Send request
	resp, err := c.doRequest(ctx, "POST", "/v2/events", req)
	if err != nil {
		c.logger.Error("Failed to resolve PagerDuty event",
			"error", err,
			"routing_key", req.RoutingKey,
			"dedup_key", req.DedupKey,
		)
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response
	var eventResp EventResponse
	if err := json.NewDecoder(resp.Body).Decode(&eventResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Debug("PagerDuty event resolved",
		"routing_key", req.RoutingKey,
		"dedup_key", eventResp.DedupKey,
		"status", eventResp.Status,
	)

	return &eventResp, nil
}

// SendChangeEvent sends a change event to PagerDuty
func (c *pagerDutyEventsClientImpl) SendChangeEvent(ctx context.Context, req *ChangeEventRequest) (*ChangeEventResponse, error) {
	// Validate request
	if req.RoutingKey == "" {
		return nil, ErrMissingRoutingKey
	}

	// Send request
	resp, err := c.doRequest(ctx, "POST", "/v2/change/enqueue", req)
	if err != nil {
		c.logger.Error("Failed to send PagerDuty change event",
			"error", err,
			"routing_key", req.RoutingKey,
		)
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response
	var changeResp ChangeEventResponse
	if err := json.NewDecoder(resp.Body).Decode(&changeResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	c.logger.Debug("PagerDuty change event sent",
		"routing_key", req.RoutingKey,
		"status", changeResp.Status,
	)

	return &changeResp, nil
}

// Health checks API connectivity
func (c *pagerDutyEventsClientImpl) Health(ctx context.Context) error {
	// Simple connectivity check: try to trigger an event with minimal data
	// Note: This will fail with 400 (bad request) but confirms API is reachable
	req := &TriggerEventRequest{
		RoutingKey:  "health-check",
		EventAction: EventActionTrigger,
		Payload: TriggerEventPayload{
			Summary:  "Health check",
			Source:   "alert-history-service",
			Severity: SeverityInfo,
		},
	}

	resp, err := c.doRequestWithoutRetry(ctx, "POST", "/v2/events", req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	// Any response (even 400/401) means API is reachable
	if resp.StatusCode >= 200 && resp.StatusCode < 500 {
		return nil
	}

	return fmt.Errorf("health check failed: unexpected status %d", resp.StatusCode)
}

// doRequest performs HTTP request with retry logic
func (c *pagerDutyEventsClientImpl) doRequest(ctx context.Context, method string, endpoint string, body interface{}) (*http.Response, error) {
	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		c.metrics.RateLimitHits.Inc()
		c.logger.Warn("Rate limiter triggered", "error", err)
		return nil, ErrRateLimitExceeded
	}

	// Marshal body to JSON
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal body: %w", err)
	}

	// Build URL
	url := c.baseURL + endpoint

	// Retry loop
	var lastErr error
	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Create request
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonData))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Set headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "AlertHistory/1.0 (+github.com/ipiton/alert-history)")

		// Execute request
		start := time.Now()
		resp, err := c.httpClient.Do(req)
		duration := time.Since(start)

		// Record metrics
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		c.metrics.APIRequests.WithLabelValues(endpoint, strconv.Itoa(statusCode)).Inc()
		c.metrics.APIDuration.WithLabelValues(endpoint).Observe(duration.Seconds())

		// Check error
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed: %w", err)
			c.logger.Warn("HTTP request failed",
				"attempt", attempt+1,
				"url", url,
				"error", err,
			)

			// Retry on network errors
			if attempt < c.retryConfig.MaxRetries {
				backoff := c.calculateBackoff(attempt)
				c.logger.Debug("Retrying after backoff", "backoff", backoff, "attempt", attempt+1)
				time.Sleep(backoff)
				continue
			}
			c.metrics.APIErrors.WithLabelValues("network_error").Inc()
			return nil, lastErr
		}

		// Check status code
		if resp.StatusCode == 202 {
			// Success (202 Accepted)
			return resp, nil
		}

		// Parse error
		apiErr := c.parseError(resp)
		lastErr = apiErr

		// Decide retry based on status code
		if shouldRetry(resp.StatusCode) && attempt < c.retryConfig.MaxRetries {
			c.logger.Warn("Retryable error",
				"attempt", attempt+1,
				"status", resp.StatusCode,
				"error", apiErr,
			)
			resp.Body.Close()

			backoff := c.calculateBackoff(attempt)
			c.logger.Debug("Retrying after backoff", "backoff", backoff, "attempt", attempt+1)
			time.Sleep(backoff)
			continue
		}

		// Permanent error - no retry
		resp.Body.Close()
		c.metrics.APIErrors.WithLabelValues(apiErr.Type()).Inc()
		return nil, apiErr
	}

	return nil, fmt.Errorf("request failed after %d attempts: %w", c.retryConfig.MaxRetries+1, lastErr)
}

// doRequestWithoutRetry performs HTTP request without retry logic (used for health checks)
func (c *pagerDutyEventsClientImpl) doRequestWithoutRetry(ctx context.Context, method string, endpoint string, body interface{}) (*http.Response, error) {
	// Marshal body to JSON
	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal body: %w", err)
	}

	// Build URL
	url := c.baseURL + endpoint

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AlertHistory/1.0 (+github.com/ipiton/alert-history)")

	// Execute request
	return c.httpClient.Do(req)
}

// shouldRetry determines if the error is retryable based on HTTP status code
func shouldRetry(statusCode int) bool {
	switch statusCode {
	case 429: // Too Many Requests (rate limit)
		return true
	case 500, 502, 503, 504: // Server errors
		return true
	default:
		return false
	}
}

// calculateBackoff calculates exponential backoff duration
func (c *pagerDutyEventsClientImpl) calculateBackoff(attempt int) time.Duration {
	// Exponential backoff: 100ms * 2^attempt
	// Attempt 0: 100ms
	// Attempt 1: 200ms
	// Attempt 2: 400ms
	// Attempt 3: 800ms (capped at 5s)
	backoff := c.retryConfig.BaseBackoff * time.Duration(1<<uint(attempt))
	if backoff > c.retryConfig.MaxBackoff {
		backoff = c.retryConfig.MaxBackoff
	}
	return backoff
}

// parseError parses error response from PagerDuty API
func (c *pagerDutyEventsClientImpl) parseError(resp *http.Response) *PagerDutyAPIError {
	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &PagerDutyAPIError{
			StatusCode: resp.StatusCode,
			Message:    "failed to read error response",
			Errors:     []string{err.Error()},
		}
	}

	// Try to parse JSON error
	var errorResp struct {
		Status  string   `json:"status"`
		Message string   `json:"message"`
		Errors  []string `json:"errors"`
	}

	if err := json.Unmarshal(body, &errorResp); err != nil {
		// Failed to parse JSON, return raw body
		return &PagerDutyAPIError{
			StatusCode: resp.StatusCode,
			Message:    string(body),
			Errors:     nil,
		}
	}

	return &PagerDutyAPIError{
		StatusCode: resp.StatusCode,
		Message:    errorResp.Message,
		Errors:     errorResp.Errors,
	}
}
