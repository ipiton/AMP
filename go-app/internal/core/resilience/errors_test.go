package resilience

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

// ==================== DefaultErrorChecker Tests ====================

func TestDefaultErrorChecker_NilError(t *testing.T) {
	checker := &DefaultErrorChecker{}

	if checker.IsRetryable(nil) {
		t.Error("Expected nil error to not be retryable")
	}
}

func TestDefaultErrorChecker_NonRetryableError(t *testing.T) {
	checker := &DefaultErrorChecker{}
	err := fmt.Errorf("wrapped: %w", ErrNonRetryable)

	if checker.IsRetryable(err) {
		t.Error("Expected ErrNonRetryable to not be retryable")
	}
}

func TestDefaultErrorChecker_NetworkErrors(t *testing.T) {
	checker := &DefaultErrorChecker{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "ECONNREFUSED",
			err:      &net.OpError{Err: syscall.ECONNREFUSED},
			expected: true,
		},
		{
			name:     "ECONNRESET",
			err:      &net.OpError{Err: syscall.ECONNRESET},
			expected: true,
		},
		{
			name:     "ENETUNREACH",
			err:      &net.OpError{Err: syscall.ENETUNREACH},
			expected: true,
		},
		{
			name:     "EHOSTUNREACH",
			err:      &net.OpError{Err: syscall.EHOSTUNREACH},
			expected: true,
		},
		{
			name:     "DNSError temporary",
			err:      &net.DNSError{IsTemporary: true},
			expected: true,
		},
		{
			name:     "DNSError not temporary",
			err:      &net.DNSError{IsTemporary: false},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestDefaultErrorChecker_TimeoutErrors(t *testing.T) {
	checker := &DefaultErrorChecker{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "timeout in message",
			err:      errors.New("operation timeout"),
			expected: true,
		},
		{
			name:     "deadline exceeded",
			err:      errors.New("context deadline exceeded"),
			expected: true,
		},
		{
			name:     "i/o timeout",
			err:      errors.New("i/o timeout"),
			expected: true,
		},
		{
			name:     "timed out",
			err:      errors.New("request timed out"),
			expected: true,
		},
		{
			name:     "not a timeout",
			err:      errors.New("invalid request"),
			expected: true, // Default checker retries all errors
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestDefaultErrorChecker_TemporaryInterface(t *testing.T) {
	checker := &DefaultErrorChecker{}

	// Create error implementing temporary interface
	tempErr := &temporaryError{isTemp: true}
	notTempErr := &temporaryError{isTemp: false}

	if !checker.IsRetryable(tempErr) {
		t.Error("Expected temporary error to be retryable")
	}

	if checker.IsRetryable(notTempErr) {
		t.Error("Expected non-temporary error to not be retryable")
	}
}

// Helper type implementing temporary interface
type temporaryError struct {
	isTemp bool
}

func (e *temporaryError) Error() string {
	return "temporary error"
}

func (e *temporaryError) Temporary() bool {
	return e.isTemp
}

// ==================== HTTPErrorChecker Tests ====================

func TestNewHTTPErrorChecker(t *testing.T) {
	checker := NewHTTPErrorChecker()

	if !checker.RetryOn5xx {
		t.Error("Expected RetryOn5xx to be true")
	}
	if !checker.RetryOn429 {
		t.Error("Expected RetryOn429 to be true")
	}
	if !checker.RetryOn408 {
		t.Error("Expected RetryOn408 to be true")
	}
}

func TestHTTPErrorChecker_NilError(t *testing.T) {
	checker := NewHTTPErrorChecker()

	if checker.IsRetryable(nil) {
		t.Error("Expected nil error to not be retryable")
	}
}

func TestHTTPErrorChecker_5xxErrors(t *testing.T) {
	checker := NewHTTPErrorChecker()

	tests := []struct {
		statusCode int
		retryOn5xx bool
		expected   bool
	}{
		{500, true, true},  // Internal Server Error
		{502, true, true},  // Bad Gateway
		{503, true, true},  // Service Unavailable
		{504, true, true},  // Gateway Timeout
		{500, false, true}, // Disabled but fallback to default checker (all errors retryable)
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d_retry_%v", tt.statusCode, tt.retryOn5xx), func(t *testing.T) {
			checker.RetryOn5xx = tt.retryOn5xx
			err := fmt.Errorf("HTTP %d error", tt.statusCode)

			result := checker.IsRetryable(err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", err, result, tt.expected)
			}
		})
	}
}

func TestHTTPErrorChecker_429RateLimitErrors(t *testing.T) {
	checker := NewHTTPErrorChecker()

	tests := []struct {
		name       string
		err        error
		retryOn429 bool
		expected   bool
	}{
		{
			name:       "429 enabled",
			err:        errors.New("HTTP 429 Too Many Requests"),
			retryOn429: true,
			expected:   true,
		},
		{
			name:       "429 disabled",
			err:        errors.New("HTTP 429 Too Many Requests"),
			retryOn429: false,
			expected:   true, // Falls back to default checker
		},
		{
			name:       "rate limit in message",
			err:        errors.New("rate limit exceeded"),
			retryOn429: true,
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker.RetryOn429 = tt.retryOn429
			result := checker.IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestHTTPErrorChecker_408RequestTimeout(t *testing.T) {
	checker := NewHTTPErrorChecker()

	tests := []struct {
		name       string
		err        error
		retryOn408 bool
		expected   bool
	}{
		{
			name:       "408 enabled",
			err:        errors.New("HTTP 408 Request Timeout"),
			retryOn408: true,
			expected:   true,
		},
		{
			name:       "408 disabled",
			err:        errors.New("HTTP 408 Request Timeout"),
			retryOn408: false,
			expected:   true, // Falls back to default checker (timeout)
		},
		{
			name:       "Request Timeout in message",
			err:        errors.New("Request Timeout occurred"),
			retryOn408: true,
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker.RetryOn408 = tt.retryOn408
			result := checker.IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestHTTPErrorChecker_NonHTTPErrors(t *testing.T) {
	checker := NewHTTPErrorChecker()

	// Non-HTTP errors should fall back to default checker
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "generic error",
			err:      errors.New("some error"),
			expected: true, // Default checker retries all
		},
		{
			name:     "network error",
			err:      &net.OpError{Err: syscall.ECONNREFUSED},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

// ==================== ChainedErrorChecker Tests ====================

func TestChainedErrorChecker_NilError(t *testing.T) {
	checker := &ChainedErrorChecker{
		Checkers: []RetryableErrorChecker{
			&DefaultErrorChecker{},
			NewHTTPErrorChecker(),
		},
	}

	if checker.IsRetryable(nil) {
		t.Error("Expected nil error to not be retryable")
	}
}

func TestChainedErrorChecker_AnyCheckerReturnsTrue(t *testing.T) {
	checker := &ChainedErrorChecker{
		Checkers: []RetryableErrorChecker{
			&NeverRetryChecker{},
			&AlwaysRetryChecker{}, // This one returns true
			&NeverRetryChecker{},
		},
	}

	err := errors.New("test error")
	if !checker.IsRetryable(err) {
		t.Error("Expected chained checker to retry when any checker returns true")
	}
}

func TestChainedErrorChecker_AllCheckersReturnFalse(t *testing.T) {
	checker := &ChainedErrorChecker{
		Checkers: []RetryableErrorChecker{
			&NeverRetryChecker{},
			&NeverRetryChecker{},
		},
	}

	err := errors.New("test error")
	if checker.IsRetryable(err) {
		t.Error("Expected chained checker to not retry when all checkers return false")
	}
}

func TestChainedErrorChecker_EmptyCheckers(t *testing.T) {
	checker := &ChainedErrorChecker{
		Checkers: []RetryableErrorChecker{},
	}

	err := errors.New("test error")
	if checker.IsRetryable(err) {
		t.Error("Expected empty chained checker to not retry")
	}
}

// ==================== NeverRetryChecker Tests ====================

func TestNeverRetryChecker(t *testing.T) {
	checker := &NeverRetryChecker{}

	tests := []struct {
		name string
		err  error
	}{
		{"nil error", nil},
		{"generic error", errors.New("test")},
		{"network error", &net.OpError{Err: syscall.ECONNREFUSED}},
		{"timeout error", errors.New("timeout")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if checker.IsRetryable(tt.err) {
				t.Errorf("NeverRetryChecker should always return false, got true for %v", tt.err)
			}
		})
	}
}

// ==================== AlwaysRetryChecker Tests ====================

func TestAlwaysRetryChecker(t *testing.T) {
	checker := &AlwaysRetryChecker{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false}, // nil is not retryable
		{"generic error", errors.New("test"), true},
		{"network error", &net.OpError{Err: syscall.ECONNREFUSED}, true},
		{"non-retryable error", fmt.Errorf("wrapped: %w", ErrNonRetryable), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

// ==================== Helper Functions Tests ====================

func TestIsTransientNetworkError_NilError(t *testing.T) {
	if isTransientNetworkError(nil) {
		t.Error("Expected nil error to not be transient")
	}
}

func TestIsTransientNetworkError_NonNetworkError(t *testing.T) {
	err := errors.New("generic error")
	if isTransientNetworkError(err) {
		t.Error("Expected non-network error to not be transient")
	}
}

func TestIsTimeoutError_NilError(t *testing.T) {
	if isTimeoutError(nil) {
		t.Error("Expected nil error to not be timeout")
	}
}

func TestIsTimeoutError_TimeoutInterface(t *testing.T) {
	// Create error implementing timeout interface
	timeoutErr := &timeoutError{isTimeout: true}
	notTimeoutErr := &timeoutError{isTimeout: false}

	if !isTimeoutError(timeoutErr) {
		t.Error("Expected timeout error to be detected")
	}

	// Note: notTimeoutErr.Temporary() returns false, so DefaultErrorChecker
	// won't find it via temporary interface, but isTimeoutError checks
	// the Timeout() method directly
	if isTimeoutError(notTimeoutErr) {
		t.Error("Expected non-timeout error to not be detected")
	}
}

// Helper type implementing timeout interface
type timeoutError struct {
	isTimeout bool
}

func (e *timeoutError) Error() string {
	if e.isTimeout {
		return "timeout error"
	}
	return "generic network error"
}

func (e *timeoutError) Timeout() bool {
	return e.isTimeout
}

func (e *timeoutError) Temporary() bool {
	// Always return false to avoid DefaultErrorChecker catching it via Temporary()
	return false
}

// ==================== Edge Cases ====================

func TestErrorCheckerWithWrappedErrors(t *testing.T) {
	checker := &DefaultErrorChecker{}

	// Test wrapped errors
	baseErr := errors.New("connection refused")
	wrappedErr := fmt.Errorf("failed to connect: %w", baseErr)
	doubleWrappedErr := fmt.Errorf("operation failed: %w", wrappedErr)

	// All should be retryable (default behavior)
	if !checker.IsRetryable(baseErr) {
		t.Error("Expected base error to be retryable")
	}
	if !checker.IsRetryable(wrappedErr) {
		t.Error("Expected wrapped error to be retryable")
	}
	if !checker.IsRetryable(doubleWrappedErr) {
		t.Error("Expected double-wrapped error to be retryable")
	}
}

func TestComplexChainedChecker(t *testing.T) {
	// Create a complex chained checker
	httpChecker := NewHTTPErrorChecker()
	httpChecker.RetryOn5xx = true
	httpChecker.RetryOn429 = false

	checker := &ChainedErrorChecker{
		Checkers: []RetryableErrorChecker{
			httpChecker,
			&DefaultErrorChecker{},
		},
	}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "HTTP 500",
			err:      errors.New("HTTP 500 Internal Server Error"),
			expected: true,
		},
		{
			name:     "HTTP 429 (disabled in HTTP checker, but default catches it)",
			err:      errors.New("HTTP 429 Too Many Requests"),
			expected: true,
		},
		{
			name:     "Network error",
			err:      &net.OpError{Err: syscall.ECONNREFUSED},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.IsRetryable(tt.err)
			if result != tt.expected {
				t.Errorf("IsRetryable(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

// Note: Benchmarks for error checkers are in retry_bench_test.go
