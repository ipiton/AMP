package resilience

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
)

// classifyError classifies an error into a type for metrics labeling.
//
// Error types:
//   - "timeout": Timeout or deadline exceeded errors
//   - "network": Network connectivity errors (connection refused, reset, unreachable)
//   - "rate_limit": Rate limiting or too many requests errors
//   - "context_cancelled": Context cancellation
//   - "context_deadline": Context deadline exceeded
//   - "dns": DNS resolution errors
//   - "unknown": All other errors
//
// Returns:
//   - string: Error type label for metrics
func classifyError(err error) string {
	if err == nil {
		return "none"
	}

	// Context errors
	if errors.Is(err, context.Canceled) {
		return "context_cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline"
	}

	// DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}

	// Network operation errors
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
			return "network"
		}
		if errors.Is(opErr.Err, syscall.ECONNRESET) {
			return "network"
		}
		if errors.Is(opErr.Err, syscall.ENETUNREACH) {
			return "network"
		}
		if errors.Is(opErr.Err, syscall.EHOSTUNREACH) {
			return "network"
		}
		return "network"
	}

	// Check error message for common patterns
	errMsg := strings.ToLower(err.Error())

	// Rate limiting
	if strings.Contains(errMsg, "rate limit") ||
		strings.Contains(errMsg, "too many requests") ||
		strings.Contains(errMsg, "429") {
		return "rate_limit"
	}

	// Timeout errors
	if strings.Contains(errMsg, "timeout") ||
		strings.Contains(errMsg, "deadline exceeded") ||
		strings.Contains(errMsg, "timed out") ||
		strings.Contains(errMsg, "i/o timeout") {
		return "timeout"
	}

	// Network errors (generic)
	if strings.Contains(errMsg, "connection") ||
		strings.Contains(errMsg, "network") {
		return "network"
	}

	// Default
	return "unknown"
}
