// Package llm provides LLM proxy client for alert classification.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/resilience"
	"github.com/ipiton/AMP/pkg/metrics"
)

// ClassificationRequest represents the request payload to LLM API.
type ClassificationRequest struct {
	Alert  LLMAlertRequest `json:"alert"`
	Model  string          `json:"model"`
	Prompt string          `json:"prompt,omitempty"`
}

// ClassificationResponse represents the response from LLM API.
type ClassificationResponse struct {
	Classification LLMClassificationResponse `json:"classification"`
	RequestID      string                    `json:"request_id"`
	ProcessingTime string                    `json:"processing_time"`
	Error          string                    `json:"error,omitempty"`
}

// LLMClient defines the interface for LLM operations.
type LLMClient interface {
	ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
	Health(ctx context.Context) error
}

// Config holds configuration for LLM client.
type Config struct {
	BaseURL        string               `mapstructure:"base_url"`
	APIKey         string               `mapstructure:"api_key"`
	Model          string               `mapstructure:"model"`
	Timeout        time.Duration        `mapstructure:"timeout"`
	MaxRetries     int                  `mapstructure:"max_retries"`
	RetryDelay     time.Duration        `mapstructure:"retry_delay"`
	RetryBackoff   float64              `mapstructure:"retry_backoff"`
	EnableMetrics  bool                 `mapstructure:"enable_metrics"`
	CircuitBreaker CircuitBreakerConfig `mapstructure:"circuit_breaker"`
}

// DefaultConfig returns default LLM client configuration.
func DefaultConfig() Config {
	return Config{
		BaseURL:        "https://llm-proxy.b2broker.tech",
		Model:          "openai/gpt-4o",
		Timeout:        30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
		RetryBackoff:   2.0,
		EnableMetrics:  true,
		CircuitBreaker: DefaultCircuitBreakerConfig(),
	}
}

// HTTPLLMClient implements LLMClient interface using HTTP with circuit breaker protection.
type HTTPLLMClient struct {
	config         Config
	httpClient     *http.Client
	logger         *slog.Logger
	circuitBreaker *CircuitBreaker
}

// NewHTTPLLMClient creates a new HTTP LLM client with optional circuit breaker.
func NewHTTPLLMClient(config Config, logger *slog.Logger) *HTTPLLMClient {
	if logger == nil {
		logger = slog.Default()
	}

	httpClient := &http.Client{
		Timeout: config.Timeout,
	}

	// Create circuit breaker if enabled
	var cb *CircuitBreaker
	if config.CircuitBreaker.Enabled {
		cbMetrics := NewCircuitBreakerMetrics()
		var err error
		cb, err = NewCircuitBreaker(config.CircuitBreaker, logger, cbMetrics)
		if err != nil {
			logger.Error("Failed to create circuit breaker, continuing without it",
				"error", err,
			)
			cb = nil
		}
	}

	return &HTTPLLMClient{
		config:         config,
		httpClient:     httpClient,
		logger:         logger,
		circuitBreaker: cb,
	}
}

// ClassifyAlert classifies an alert using LLM API with circuit breaker and retry logic.
func (c *HTTPLLMClient) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	if alert == nil {
		return nil, fmt.Errorf("alert cannot be nil")
	}

	// If circuit breaker is disabled, use legacy logic
	if c.circuitBreaker == nil {
		return c.classifyAlertWithRetry(ctx, alert)
	}

	// Wrap retry logic in circuit breaker
	var result *core.ClassificationResult
	var lastErr error

	err := c.circuitBreaker.Call(ctx, func(ctx context.Context) error {
		var err error
		result, err = c.classifyAlertWithRetry(ctx, alert)
		lastErr = err
		return err
	})

	// If circuit breaker is open, return specific error for fallback handling
	if errors.Is(err, ErrCircuitBreakerOpen) {
		c.logger.Debug("Circuit breaker is open, skipping LLM call",
			"alert", alert.AlertName,
			"state", c.circuitBreaker.GetState(),
		)
		return nil, ErrCircuitBreakerOpen
	}

	return result, lastErr
}

// classifyAlertWithRetry implements retry logic using centralized resilience package.
// REFACTORED (TN-040): Now uses internal/core/resilience.WithRetryFunc for consistency.
func (c *HTTPLLMClient) classifyAlertWithRetry(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	// Create retry policy from config (maintains backward compatibility)
	policy := &resilience.RetryPolicy{
		MaxRetries:   c.config.MaxRetries,
		BaseDelay:    c.config.RetryDelay,
		MaxDelay:     c.config.RetryDelay * 10, // Max 10x base delay
		Multiplier:   c.config.RetryBackoff,
		Jitter:       true,
		ErrorChecker: &llmErrorChecker{},
		Logger:       c.logger,
		Metrics:      metrics.DefaultRegistry().Technical().Retry,
		OperationName: "llm_classify_alert",
	}

	// Use centralized retry mechanism with metrics
	result, err := resilience.WithRetryFunc(ctx, policy, func() (*core.ClassificationResult, error) {
		return c.classifyAlertOnce(ctx, alert)
	})

	if err != nil {
		return nil, err
	}

	c.logger.Info("Alert classified successfully",
		"alert", alert.AlertName,
		"severity", result.Severity,
		"confidence", result.Confidence,
	)

	return result, nil
}

// llmErrorChecker implements retry logic for LLM client errors.
type llmErrorChecker struct{}

func (e *llmErrorChecker) IsRetryable(err error) bool {
	return IsRetryableError(err)
}

// classifyAlertOnce performs a single classification request.
func (c *HTTPLLMClient) classifyAlertOnce(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	// Convert core.Alert to LLM API format
	llmAlert := CoreAlertToLLMRequest(alert)
	if llmAlert == nil {
		return nil, fmt.Errorf("failed to convert alert to LLM format")
	}

	// Prepare request payload
	request := ClassificationRequest{
		Alert: *llmAlert,
		Model: c.config.Model,
		Prompt: `Analyze this alert and provide classification with:
1. Severity (1=noise, 2=info, 3=warning, 4=critical)
2. Category (infrastructure, application, security, etc.)
3. Summary (brief description)
4. Confidence (0.0-1.0)
5. Reasoning (why this classification)
6. Suggestions (list of recommended actions)`,
	}

	// Marshal request to JSON
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := c.config.BaseURL + "/classify"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "alert-history-go/1.0.0")

	if c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	// Log request
	c.logger.Debug("Sending LLM classification request",
		"url", url,
		"alert", alert.AlertName,
		"model", c.config.Model,
	)

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		c.logger.Error("LLM API returned error",
			"status", resp.StatusCode,
			"body", string(body),
		)
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("LLM API error: status %d, body: %s", resp.StatusCode, string(body)),
		}
	}

	// Parse response
	var response ClassificationResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for API error
	if response.Error != "" {
		return nil, fmt.Errorf("LLM API error: %s", response.Error)
	}

	// Convert LLM response to core.ClassificationResult
	result, err := LLMResponseToCoreClassification(&response.Classification)
	if err != nil {
		return nil, fmt.Errorf("failed to convert LLM response: %w", err)
	}

	// Add processing time to result
	if response.ProcessingTime != "" {
		if processingTime, err := ParseProcessingTime(response.ProcessingTime); err == nil {
			result.ProcessingTime = processingTime
		}
	}

	return result, nil
}

// Health checks if the LLM service is available.
func (c *HTTPLLMClient) Health(ctx context.Context) error {
	url := c.config.BaseURL + "/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	req.Header.Set("User-Agent", "alert-history-go/1.0.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("LLM service unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// GetCircuitBreakerState returns current circuit breaker state.
// Returns StateClosed if circuit breaker is disabled.
func (c *HTTPLLMClient) GetCircuitBreakerState() CircuitBreakerState {
	if c.circuitBreaker == nil {
		return StateClosed // No circuit breaker = always closed
	}
	return c.circuitBreaker.GetState()
}

// GetCircuitBreakerStats returns circuit breaker statistics.
// Returns empty stats if circuit breaker is disabled.
func (c *HTTPLLMClient) GetCircuitBreakerStats() CircuitBreakerStats {
	if c.circuitBreaker == nil {
		return CircuitBreakerStats{State: StateClosed}
	}
	return c.circuitBreaker.GetStats()
}

// MockLLMClient implements LLMClient interface for testing.
type MockLLMClient struct {
	ClassifyAlertFunc func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error)
	HealthFunc        func(ctx context.Context) error
}

// NewMockLLMClient creates a new mock LLM client.
func NewMockLLMClient() *MockLLMClient {
	return &MockLLMClient{
		ClassifyAlertFunc: func(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
			// Default mock response
			return &core.ClassificationResult{
				Severity:        core.SeverityWarning,
				Confidence:      0.85,
				Reasoning:       "This is a mock classification for testing purposes",
				Recommendations: []string{"Check system resources", "Review logs"},
				ProcessingTime:  0.1,
				Metadata: map[string]any{
					"category": "infrastructure",
					"summary":  "Mock classification for " + alert.AlertName,
				},
			}, nil
		},
		HealthFunc: func(ctx context.Context) error {
			return nil
		},
	}
}

// ClassifyAlert implements LLMClient interface.
func (m *MockLLMClient) ClassifyAlert(ctx context.Context, alert *core.Alert) (*core.ClassificationResult, error) {
	if m.ClassifyAlertFunc != nil {
		return m.ClassifyAlertFunc(ctx, alert)
	}
	return nil, fmt.Errorf("ClassifyAlertFunc not implemented")
}

// Health implements LLMClient interface.
func (m *MockLLMClient) Health(ctx context.Context) error {
	if m.HealthFunc != nil {
		return m.HealthFunc(ctx)
	}
	return fmt.Errorf("HealthFunc not implemented")
}
