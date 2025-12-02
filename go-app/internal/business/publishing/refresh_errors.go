package publishing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Standard refresh errors.
var (
	// ErrRefreshInProgress indicates refresh already running.
	// Return 503 Service Unavailable to client.
	ErrRefreshInProgress = errors.New("refresh already in progress")

	// ErrRateLimitExceeded indicates too many refresh requests.
	// Return 429 Too Many Requests to client.
	ErrRateLimitExceeded = errors.New("rate limit exceeded: max 1 refresh per minute")

	// ErrAlreadyStarted indicates manager already started.
	// Internal error (Start() called twice).
	ErrAlreadyStarted = errors.New("refresh manager already started")

	// ErrNotStarted indicates manager not started.
	// Internal error (Stop/RefreshNow called before Start).
	ErrNotStarted = errors.New("refresh manager not started")

	// ErrShutdownTimeout indicates Stop() timeout exceeded.
	// Manager force-shutdown (may leak goroutine).
	ErrShutdownTimeout = errors.New("shutdown timeout exceeded")
)

// RefreshError wraps refresh failures with context.
//
// This error type provides comprehensive information for debugging:
//   - Operation that failed (e.g., "discover_targets")
//   - Underlying error (K8s API error, timeout, etc.)
//   - Retry attempts (how many retries were attempted)
//   - Total duration (time spent across all retries)
//   - Transient flag (true if error is retryable)
//
// Thread Safety: Immutable after creation (safe to share)
//
// Example:
//
//	err := &RefreshError{
//	    Op:        "discover_targets",
//	    Err:       errors.New("connection refused"),
//	    Retries:   3,
//	    Duration:  5 * time.Second,
//	    Transient: true,
//	}
//	log.Error("Refresh failed", "error", err)
//
//	// Check if transient (should retry later)
//	if err.Transient {
//	    log.Info("Will retry on next scheduled refresh")
//	}
type RefreshError struct {
	// Op is operation that failed (e.g., "discover_targets").
	Op string

	// Err is underlying error (wrapped).
	Err error

	// Retries is number of retry attempts (0 if first attempt).
	Retries int

	// Duration is total duration across all retries.
	Duration time.Duration

	// Transient indicates if error is transient (retry OK).
	// true: network timeout, connection refused, 503
	// false: 401, 403, parse error
	Transient bool
}

// Error implements error interface.
func (e *RefreshError) Error() string {
	transientStr := "permanent"
	if e.Transient {
		transientStr = "transient"
	}
	return fmt.Sprintf("%s failed after %d retries (%v) [%s]: %v",
		e.Op, e.Retries, e.Duration, transientStr, e.Err)
}

// Unwrap implements errors.Unwrap interface.
func (e *RefreshError) Unwrap() error {
	return e.Err
}

// ConfigError represents configuration validation error.
//
// Example:
//
//	err := &ConfigError{
//	    Field:  "Interval",
//	    Value:  -5 * time.Minute,
//	    Reason: "must be positive",
//	}
type ConfigError struct {
	// Field is config field name (e.g., "Interval").
	Field string

	// Value is invalid value.
	Value interface{}

	// Reason is why value is invalid.
	Reason string
}

// Error implements error interface.
func (e *ConfigError) Error() string {
	return fmt.Sprintf("invalid config field %q (value=%v): %s",
		e.Field, e.Value, e.Reason)
}

// classifyError classifies error as transient or permanent.
//
// Transient errors (retry OK):
//   - Network timeout
//   - Connection refused
//   - 503 Service Unavailable
//   - Context deadline exceeded
//   - DNS resolution failure
//
// Permanent errors (no retry):
//   - 401 Unauthorized
//   - 403 Forbidden
//   - Invalid configuration
//   - Parse errors (bad JSON, base64)
//   - Context cancelled (user requested)
//
// Returns:
//   - errorType: Error type for metrics (network/timeout/auth/parse/cancelled/unknown)
//   - transient: true if retryable, false otherwise
//
// Example:
//
//	err := doSomething()
//	errorType, transient := classifyError(err)
//	if transient {
//	    log.Warn("Transient error, will retry", "error_type", errorType)
//	} else {
//	    log.Error("Permanent error, no retry", "error_type", errorType)
//	}
func classifyError(err error) (errorType string, transient bool) {
	if err == nil {
		return "", false
	}

	// Context errors
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", true
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", false // User requested cancellation
	}

	// Network errors (transient)
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return "timeout", true
		}
		// Other network errors (connection refused, etc.)
		return "network", true
	}

	// DNS errors (transient)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns", true
	}

	// Authentication errors (permanent)
	if isAuthError(err) {
		return "auth", false
	}

	// Parse errors (permanent)
	if isParseError(err) {
		return "parse", false
	}

	// K8s API errors (check HTTP status code)
	if isK8sServiceUnavailable(err) {
		return "k8s_api", true
	}
	if isK8sAuthError(err) {
		return "k8s_auth", false
	}

	// Default: treat as transient (safe to retry)
	return "unknown", true
}

// isAuthError checks if error is authentication/authorization failure.
//
// Checks for:
//   - "401 Unauthorized"
//   - "403 Forbidden"
//   - "authentication failed"
//   - "invalid token"
//
// These errors are permanent (no retry).
func isAuthError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "forbidden") ||
		strings.Contains(errStr, "authentication failed") ||
		strings.Contains(errStr, "invalid token")
}

// isParseError checks if error is parsing failure.
//
// Checks for:
//   - "invalid json"
//   - "illegal base64"
//   - "unmarshal"
//   - "decode"
//
// These errors are permanent (no retry).
func isParseError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "invalid json") ||
		strings.Contains(errStr, "illegal base64") ||
		strings.Contains(errStr, "unmarshal") ||
		strings.Contains(errStr, "decode") ||
		strings.Contains(errStr, "parse error")
}

// isK8sServiceUnavailable checks if error is K8s API 503.
//
// Checks for:
//   - "503 Service Unavailable"
//   - "service unavailable"
//   - "apiserver not available"
//
// These errors are transient (retry OK).
func isK8sServiceUnavailable(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "503") ||
		strings.Contains(errStr, "service unavailable") ||
		strings.Contains(errStr, "apiserver not available")
}

// isK8sAuthError checks if error is K8s authentication failure.
//
// Checks for:
//   - "401 Unauthorized"
//   - "403 Forbidden"
//   - K8s-specific auth errors
//
// These errors are permanent (no retry).
func isK8sAuthError(err error) bool {
	if err == nil {
		return false
	}
	return isAuthError(err) // Reuse generic auth check
}

// errorString converts error to string (handles nil).
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
