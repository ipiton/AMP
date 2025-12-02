package resilience

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// Common retry-related errors
var (
	// ErrMaxRetriesExceeded is returned when all retry attempts are exhausted
	ErrMaxRetriesExceeded = errors.New("maximum retry attempts exceeded")

	// ErrNonRetryable is returned when an error is explicitly non-retryable
	ErrNonRetryable = errors.New("error is not retryable")
)

// DefaultErrorChecker is a default implementation of RetryableErrorChecker
// that considers network errors, timeouts, and temporary errors as retryable.
type DefaultErrorChecker struct{}

// IsRetryable implements RetryableErrorChecker interface.
// Returns true for transient errors that should be retried.
func (c *DefaultErrorChecker) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Explicitly non-retryable errors
	if errors.Is(err, ErrNonRetryable) {
		return false
	}

	// Network errors - check for transient conditions
	if isTransientNetworkError(err) {
		return true
	}

	// Timeout errors - generally retryable
	if isTimeoutError(err) {
		return true
	}

	// Check for "temporary" interface (common in Go stdlib)
	type temporary interface {
		Temporary() bool
	}
	if te, ok := err.(temporary); ok {
		return te.Temporary()
	}

	// Default: assume error is retryable
	return true
}

// isTransientNetworkError determines if a network error is transient.
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
		// Host unreachable - might be temporary (retryable)
		if errors.Is(opErr.Err, syscall.EHOSTUNREACH) {
			return true
		}
	}

	return false
}

// isTimeoutError checks if an error represents a timeout.
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
		"timed out",
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

// HTTPErrorChecker checks if HTTP errors are retryable based on status codes.
type HTTPErrorChecker struct {
	// RetryOn5xx enables retrying on 5xx server errors
	RetryOn5xx bool

	// RetryOn429 enables retrying on 429 Too Many Requests
	RetryOn429 bool

	// RetryOn408 enables retrying on 408 Request Timeout
	RetryOn408 bool
}

// NewHTTPErrorChecker creates an HTTPErrorChecker with sensible defaults.
func NewHTTPErrorChecker() *HTTPErrorChecker {
	return &HTTPErrorChecker{
		RetryOn5xx: true,  // Server errors are transient
		RetryOn429: true,  // Rate limits should be retried
		RetryOn408: true,  // Request timeouts should be retried
	}
}

// IsRetryable implements RetryableErrorChecker for HTTP errors.
func (c *HTTPErrorChecker) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check if error contains HTTP status code in message
	errMsg := err.Error()

	// Check for 5xx errors
	if c.RetryOn5xx {
		for code := 500; code < 600; code++ {
			if strings.Contains(errMsg, fmt.Sprintf("%d", code)) ||
				strings.Contains(errMsg, fmt.Sprintf("HTTP %d", code)) {
				return true
			}
		}
	}

	// Check for 429 Too Many Requests
	if c.RetryOn429 && (strings.Contains(errMsg, "429") ||
		strings.Contains(errMsg, "Too Many Requests") ||
		strings.Contains(errMsg, "rate limit")) {
		return true
	}

	// Check for 408 Request Timeout
	if c.RetryOn408 && (strings.Contains(errMsg, "408") ||
		strings.Contains(errMsg, "Request Timeout")) {
		return true
	}

	// Fallback to default checker for non-HTTP errors
	defaultChecker := &DefaultErrorChecker{}
	return defaultChecker.IsRetryable(err)
}

// ChainedErrorChecker chains multiple error checkers together.
// Returns true if ANY checker says the error is retryable.
type ChainedErrorChecker struct {
	Checkers []RetryableErrorChecker
}

// IsRetryable implements RetryableErrorChecker.
// Returns true if any of the chained checkers returns true.
func (c *ChainedErrorChecker) IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	for _, checker := range c.Checkers {
		if checker.IsRetryable(err) {
			return true
		}
	}

	return false
}

// NeverRetryChecker always returns false (never retry).
type NeverRetryChecker struct{}

// IsRetryable implements RetryableErrorChecker.
func (c *NeverRetryChecker) IsRetryable(err error) bool {
	return false
}

// AlwaysRetryChecker always returns true (always retry).
type AlwaysRetryChecker struct{}

// IsRetryable implements RetryableErrorChecker.
func (c *AlwaysRetryChecker) IsRetryable(err error) bool {
	return err != nil
}
