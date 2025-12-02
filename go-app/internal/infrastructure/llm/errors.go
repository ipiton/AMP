package llm

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// Common errors for LLM client operations
var (
	// ErrCircuitBreakerOpen is returned when circuit breaker is open
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")

	// ErrInvalidRequest is returned when request format is invalid
	ErrInvalidRequest = errors.New("invalid request format")

	// ErrInvalidResponse is returned when response cannot be parsed
	ErrInvalidResponse = errors.New("invalid response format")
)

// HTTPError represents an HTTP error with status code
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// IsRetryableError determines if an error should be retried by retry logic.
// 150% Enhancement: Sophisticated error classification
func IsRetryableError(err error) bool {
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

	// HTTP errors
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		// 4xx errors (except 429 rate limit) - not retryable
		if httpErr.StatusCode >= 400 && httpErr.StatusCode < 500 {
			return httpErr.StatusCode == 429 // Only retry rate limits
		}
		// 5xx errors - retryable (transient server errors)
		return httpErr.StatusCode >= 500
	}

	// Network errors - classify transient vs permanent
	return isTransientNetworkError(err)
}

// isTransientNetworkError determines if network error is transient and retryable.
// 150% Enhancement: Detailed network error classification
func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// DNS errors - temporary failures are retryable
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.Temporary()
	}

	// Operation errors - check for specific syscall errors
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// Connection refused - service might be restarting (retryable)
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return true
		}
		// Connection reset - transient network issue (retryable)
		if errors.Is(opErr.Err, syscall.ECONNRESET) {
			return true
		}
		// Network unreachable - might be temporary (retryable)
		if errors.Is(opErr.Err, syscall.ENETUNREACH) {
			return true
		}
	}

	// Timeout errors - always retryable
	if isTimeoutError(err) {
		return true
	}

	// Generic check for "temporary" errors
	type temporary interface {
		Temporary() bool
	}
	if te, ok := err.(temporary); ok {
		return te.Temporary()
	}

	// Default: don't retry unknown errors
	return false
}

// isTimeoutError checks if error is a timeout.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}

	// Check error message for timeout indicators
	errMsg := err.Error()
	timeoutIndicators := []string{
		"timeout",
		"deadline exceeded",
		"context deadline exceeded",
		"i/o timeout",
	}

	for _, indicator := range timeoutIndicators {
		if strings.Contains(strings.ToLower(errMsg), indicator) {
			return true
		}
	}

	// Check for timeout interface
	type timeout interface {
		Timeout() bool
	}
	if te, ok := err.(timeout); ok {
		return te.Timeout()
	}

	return false
}

// IsNonRetryableError is exported for backward compatibility.
// Deprecated: Use IsRetryableError instead.
func IsNonRetryableError(err error) bool {
	return !IsRetryableError(err)
}

// ClassifyError classifies error into categories for metrics and logging.
// 150% Enhancement: Error pattern analysis
func ClassifyError(err error) string {
	if err == nil {
		return "success"
	}

	if errors.Is(err, ErrCircuitBreakerOpen) {
		return "circuit_breaker_open"
	}

	if errors.Is(err, ErrInvalidRequest) {
		return "invalid_request"
	}

	if errors.Is(err, ErrInvalidResponse) {
		return "invalid_response"
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.StatusCode == 429 {
			return "rate_limit"
		}
		if httpErr.StatusCode >= 500 {
			return "server_error"
		}
		if httpErr.StatusCode >= 400 {
			return "client_error"
		}
	}

	if isTimeoutError(err) {
		return "timeout"
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return "network_error"
	}

	return "unknown_error"
}
