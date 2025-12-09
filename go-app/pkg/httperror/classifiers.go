package httperror

import (
	"context"
	"errors"
	"strings"
)

// ErrorClassifier interface defines how errors are classified for retry logic.
// This is the standard interface used by pkg/retry.Strategy.
type ErrorClassifier interface {
	IsRetryable(err error) bool
}

// StandardClassifier is the default error classifier.
// It uses the pkg/httperror.IsRetryable() function for classification.
//
// Usage:
//
//	classifier := &httperror.StandardClassifier{}
//	strategy := retry.Strategy{
//	    ErrorClassifier: classifier,
//	    // ...
//	}
type StandardClassifier struct{}

// IsRetryable checks if an error is retryable using standard HTTP/network classification.
func (c *StandardClassifier) IsRetryable(err error) bool {
	return IsRetryable(err)
}

// DatabaseClassifier classifies database errors.
// It handles DB-specific errors like deadlocks, lock timeouts, connection failures.
//
// This classifier extends StandardClassifier with database-specific patterns:
//   - Deadlock errors
//   - Lock wait timeouts
//   - Serialization failures
//   - Connection lost/reset
//   - Too many connections
//   - Database locked (SQLite)
//
// Usage:
//
//	classifier := &httperror.DatabaseClassifier{}
//	strategy := retry.Strategy{
//	    ErrorClassifier: classifier,
//	    // ...
//	}
type DatabaseClassifier struct {
	StandardClassifier
}

// IsRetryable checks if a database error is retryable.
func (c *DatabaseClassifier) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Database-specific retryable patterns
	dbPatterns := []string{
		// Lock errors
		"lock wait timeout",
		"deadlock",
		"serialization failure",
		"could not serialize access",

		// Connection errors
		"connection lost",
		"connection reset",
		"connection refused",
		"server closed the connection unexpectedly",

		// Resource errors
		"too many connections",
		"out of memory",

		// PostgreSQL specific
		"pq: ",
		"sqlstate",
		"current transaction is aborted",

		// SQLite specific
		"database is locked",
		"database busy",
		"interrupted",
	}

	for _, pattern := range dbPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// Check context errors
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Fallback to standard HTTP/network classification
	return c.StandardClassifier.IsRetryable(err)
}

// PublishingClassifier classifies publishing errors.
// It's optimized for HTTP publishing to external services (Slack, PagerDuty, etc.)
//
// This classifier handles:
//   - HTTP status codes (429, 5xx retryable; 4xx not retryable)
//   - Network errors (timeouts, connection refused, DNS)
//   - Rate limiting (429 with exponential backoff)
//
// Usage:
//
//	classifier := &httperror.PublishingClassifier{}
//	strategy := retry.Strategy{
//	    ErrorClassifier: classifier,
//	    // ...
//	}
type PublishingClassifier struct {
	StandardClassifier
}

// IsRetryable checks if a publishing error is retryable.
// Uses standard HTTP/network classification (already comprehensive).
func (c *PublishingClassifier) IsRetryable(err error) bool {
	// Publishing errors are well-handled by StandardClassifier
	// (HTTP status codes, network errors, etc.)
	return c.StandardClassifier.IsRetryable(err)
}

// K8sClassifier classifies Kubernetes API errors.
// It handles K8s-specific errors like API throttling, connection issues, timeouts.
//
// This classifier extends StandardClassifier with K8s-specific patterns:
//   - Throttling errors (429)
//   - Connection failures
//   - API server unavailable
//   - Timeout errors
//
// Usage:
//
//	classifier := &httperror.K8sClassifier{}
//	strategy := retry.Strategy{
//	    ErrorClassifier: classifier,
//	    // ...
//	}
type K8sClassifier struct {
	StandardClassifier
}

// IsRetryable checks if a Kubernetes error is retryable.
func (c *K8sClassifier) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// K8s-specific retryable patterns
	k8sPatterns := []string{
		"throttling",
		"rate limit",
		"too many requests",
		"connection refused",
		"connection reset",
		"timeout",
		"deadline exceeded",
		"server unavailable",
		"api server",
	}

	for _, pattern := range k8sPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	// Fallback to standard classification
	return c.StandardClassifier.IsRetryable(err)
}

// LLMClassifier classifies LLM (Large Language Model) API errors.
// It handles errors from OpenAI, Anthropic, and other LLM providers.
//
// This classifier handles:
//   - Rate limiting (429)
//   - Server errors (5xx)
//   - Timeout errors
//   - Circuit breaker open (not retryable)
//   - Invalid requests (not retryable)
//
// Usage:
//
//	classifier := &httperror.LLMClassifier{}
//	strategy := retry.Strategy{
//	    ErrorClassifier: classifier,
//	    // ...
//	}
type LLMClassifier struct {
	StandardClassifier
}

var (
	// ErrCircuitBreakerOpen is returned when circuit breaker is open
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")

	// ErrInvalidRequest is returned when request format is invalid
	ErrInvalidRequest = errors.New("invalid request format")

	// ErrInvalidResponse is returned when response cannot be parsed
	ErrInvalidResponse = errors.New("invalid response format")
)

// IsRetryable checks if an LLM error is retryable.
func (c *LLMClassifier) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Circuit breaker open - not retryable (fail-fast)
	if errors.Is(err, ErrCircuitBreakerOpen) {
		return false
	}

	// Invalid request/response - not retryable
	if errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrInvalidResponse) {
		return false
	}

	// Fallback to standard HTTP classification
	// (handles 429 rate limits, 5xx errors, network errors)
	return c.StandardClassifier.IsRetryable(err)
}

// CompositeClassifier combines multiple classifiers with custom logic.
// It checks classifiers in order and returns true if any classifier says the error is retryable.
//
// Usage:
//
//	classifier := &httperror.CompositeClassifier{
//	    Classifiers: []httperror.ErrorClassifier{
//	        &httperror.DatabaseClassifier{},
//	        &httperror.PublishingClassifier{},
//	    },
//	}
type CompositeClassifier struct {
	Classifiers []ErrorClassifier
}

// IsRetryable checks if an error is retryable by any of the composite classifiers.
func (c *CompositeClassifier) IsRetryable(err error) bool {
	for _, classifier := range c.Classifiers {
		if classifier.IsRetryable(err) {
			return true
		}
	}
	return false
}

// CustomClassifier allows custom error classification logic.
// It wraps a custom function that determines if an error is retryable.
//
// Usage:
//
//	classifier := &httperror.CustomClassifier{
//	    Fn: func(err error) bool {
//	        // Custom logic
//	        return strings.Contains(err.Error(), "temporary")
//	    },
//	}
type CustomClassifier struct {
	Fn func(error) bool
}

// IsRetryable checks if an error is retryable using the custom function.
func (c *CustomClassifier) IsRetryable(err error) bool {
	if c.Fn == nil {
		return false
	}
	return c.Fn(err)
}
