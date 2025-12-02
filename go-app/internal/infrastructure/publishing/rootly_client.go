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
	"time"

	"golang.org/x/time/rate"
)

// RootlyIncidentsClient defines interface for Rootly Incidents API v1
type RootlyIncidentsClient interface {
	CreateIncident(ctx context.Context, req *CreateIncidentRequest) (*IncidentResponse, error)
	UpdateIncident(ctx context.Context, id string, req *UpdateIncidentRequest) (*IncidentResponse, error)
	ResolveIncident(ctx context.Context, id string, req *ResolveIncidentRequest) (*IncidentResponse, error)
}

// ClientConfig holds configuration for Rootly API client
type ClientConfig struct {
	BaseURL     string        // API base URL (https://api.rootly.com/v1)
	APIKey      string        // API key for authentication
	Timeout     time.Duration // Request timeout (default: 10s)
	RateLimit   int           // Rate limit (requests per minute, default: 60)
	RateBurst   int           // Rate limit burst (default: 10)
	RetryConfig RetryConfig   // Retry configuration
}

// RetryConfig holds retry logic configuration
type RetryConfig struct {
	MaxRetries int           // Maximum number of retries (default: 3)
	BaseDelay  time.Duration // Base delay for exponential backoff (default: 100ms)
	MaxDelay   time.Duration // Maximum delay for exponential backoff (default: 5s)
}

// defaultRootlyIncidentsClient implements RootlyIncidentsClient
type defaultRootlyIncidentsClient struct {
	httpClient  *http.Client
	baseURL     string
	apiKey      string
	rateLimiter *rate.Limiter
	retryConfig RetryConfig
	logger      *slog.Logger
}

// NewRootlyIncidentsClient creates a new Rootly API client
func NewRootlyIncidentsClient(config ClientConfig, logger *slog.Logger) RootlyIncidentsClient {
	// Set defaults
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.RateLimit == 0 {
		config.RateLimit = 60 // 60 req/min
	}
	if config.RateBurst == 0 {
		config.RateBurst = 10
	}
	if config.RetryConfig.MaxRetries == 0 {
		config.RetryConfig.MaxRetries = 3
	}
	if config.RetryConfig.BaseDelay == 0 {
		config.RetryConfig.BaseDelay = 100 * time.Millisecond
	}
	if config.RetryConfig.MaxDelay == 0 {
		config.RetryConfig.MaxDelay = 5 * time.Second
	}

	// Create rate limiter (convert req/min to req/sec)
	ratePerSecond := float64(config.RateLimit) / 60.0
	rateLimiter := rate.NewLimiter(rate.Limit(ratePerSecond), config.RateBurst)

	return &defaultRootlyIncidentsClient{
		httpClient: &http.Client{
			Timeout: config.Timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12, // TLS 1.2+
				},
			},
		},
		baseURL:     config.BaseURL,
		apiKey:      config.APIKey,
		rateLimiter: rateLimiter,
		retryConfig: config.RetryConfig,
		logger:      logger,
	}
}

// CreateIncident creates a new Rootly incident
func (c *defaultRootlyIncidentsClient) CreateIncident(
	ctx context.Context,
	req *CreateIncidentRequest,
) (*IncidentResponse, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	// Marshal request
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/incidents",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	c.setHeaders(httpReq)

	// Execute with retry
	resp, err := c.doRequestWithRetry(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response
	if resp.StatusCode != http.StatusCreated {
		return nil, c.parseError(resp)
	}

	var incidentResp IncidentResponse
	if err := json.NewDecoder(resp.Body).Decode(&incidentResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	c.logger.Debug("Rootly incident created via API",
		"incident_id", incidentResp.GetID(),
		"status_code", resp.StatusCode,
	)

	return &incidentResp, nil
}

// UpdateIncident updates an existing Rootly incident
func (c *defaultRootlyIncidentsClient) UpdateIncident(
	ctx context.Context,
	id string,
	req *UpdateIncidentRequest,
) (*IncidentResponse, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	// Marshal request
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPatch,
		c.baseURL+"/incidents/"+id,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	c.setHeaders(httpReq)

	// Execute with retry
	resp, err := c.doRequestWithRetry(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response
	if resp.StatusCode != http.StatusOK {
		return nil, c.parseError(resp)
	}

	var incidentResp IncidentResponse
	if err := json.NewDecoder(resp.Body).Decode(&incidentResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	c.logger.Debug("Rootly incident updated via API",
		"incident_id", id,
		"status_code", resp.StatusCode,
	)

	return &incidentResp, nil
}

// ResolveIncident resolves a Rootly incident
func (c *defaultRootlyIncidentsClient) ResolveIncident(
	ctx context.Context,
	id string,
	req *ResolveIncidentRequest,
) (*IncidentResponse, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}

	// Marshal request
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/incidents/"+id+"/resolve",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Set headers
	c.setHeaders(httpReq)

	// Execute with retry
	resp, err := c.doRequestWithRetry(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Parse response (200 OK or 409 Conflict if already resolved)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
		return nil, c.parseError(resp)
	}

	// If 409 Conflict, incident already resolved (not an error)
	if resp.StatusCode == http.StatusConflict {
		c.logger.Info("Rootly incident already resolved",
			"incident_id", id,
		)
		return &IncidentResponse{}, nil
	}

	var incidentResp IncidentResponse
	if err := json.NewDecoder(resp.Body).Decode(&incidentResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	c.logger.Debug("Rootly incident resolved via API",
		"incident_id", id,
		"status_code", resp.StatusCode,
	)

	return &incidentResp, nil
}

// doRequestWithRetry executes HTTP request with exponential backoff retry
func (c *defaultRootlyIncidentsClient) doRequestWithRetry(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	backoff := c.retryConfig.BaseDelay

	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		// Clone request body for retries
		var bodyBytes []byte
		if req.Body != nil {
			bodyBytes, _ = io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		// Execute request
		resp, err = c.httpClient.Do(req)

		// Success (2xx or non-retryable error)
		if err == nil && !isRetryableStatus(resp.StatusCode) {
			return resp, nil
		}

		// Permanent error (4xx except 429)
		if err == nil && isPermanentError(resp.StatusCode) {
			return resp, nil
		}

		// Last attempt
		if attempt == c.retryConfig.MaxRetries {
			if err != nil {
				return nil, fmt.Errorf("max retries exceeded: %w", err)
			}
			return resp, nil
		}

		// Log retry
		c.logger.Warn("Request failed, retrying",
			"attempt", attempt+1,
			"max_retries", c.retryConfig.MaxRetries,
			"backoff", backoff,
			"error", err,
			"status", getStatusCode(resp),
		)

		// Wait with backoff
		select {
		case <-time.After(backoff):
			// Continue
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}

		// Exponential backoff (2x multiplier)
		backoff *= 2
		if backoff > c.retryConfig.MaxDelay {
			backoff = c.retryConfig.MaxDelay
		}

		// Reset body for next attempt
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
	}

	return resp, err
}

// setHeaders sets required HTTP headers
func (c *defaultRootlyIncidentsClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "AlertHistory/1.0 (+github.com/ipiton/alert-history)")
	req.Header.Set("Accept", "application/vnd.rootly.v1+json")
}

// parseError parses Rootly API error response
func (c *defaultRootlyIncidentsClient) parseError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)

	var errorResp struct {
		Errors []struct {
			Status string `json:"status"`
			Title  string `json:"title"`
			Detail string `json:"detail"`
			Source struct {
				Pointer string `json:"pointer"`
			} `json:"source"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(body, &errorResp); err != nil {
		// Failed to parse error JSON, return generic error
		return &RootlyAPIError{
			StatusCode: resp.StatusCode,
			Title:      "Unknown Error",
			Detail:     string(body),
		}
	}

	if len(errorResp.Errors) == 0 {
		return &RootlyAPIError{
			StatusCode: resp.StatusCode,
			Title:      "Unknown Error",
			Detail:     string(body),
		}
	}

	// Return first error
	firstError := errorResp.Errors[0]
	return &RootlyAPIError{
		StatusCode: resp.StatusCode,
		Title:      firstError.Title,
		Detail:     firstError.Detail,
		Source:     firstError.Source.Pointer,
	}
}

// Helper functions

// isRetryableStatus checks if HTTP status is retryable
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// isPermanentError checks if HTTP status is permanent error
func isPermanentError(code int) bool {
	return code >= 400 && code < 500 && code != http.StatusTooManyRequests
}

// getStatusCode safely gets status code from response
func getStatusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}
