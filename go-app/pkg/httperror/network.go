package httperror

import (
	"context"
	"errors"
	"net"
	"syscall"
)

// Network error sentinel values.
// These are used for consistent error checking across the codebase.
var (
	// ErrConnectionRefused indicates the remote server refused the connection.
	ErrConnectionRefused = errors.New("connection refused")

	// ErrConnectionReset indicates the connection was reset by the remote server.
	ErrConnectionReset = errors.New("connection reset")

	// ErrConnectionTimeout indicates the connection timed out.
	ErrConnectionTimeout = errors.New("connection timeout")

	// ErrDNSFailure indicates DNS resolution failed.
	ErrDNSFailure = errors.New("dns lookup failed")

	// ErrNetworkUnreachable indicates the network is unreachable.
	ErrNetworkUnreachable = errors.New("network unreachable")

	// ErrHostUnreachable indicates the host is unreachable.
	ErrHostUnreachable = errors.New("host unreachable")
)

// IsRetryableNetworkError checks if a network error is retryable.
//
// Retryable network errors:
//   - Connection refused (server not running, may come back)
//   - Connection reset (server closed connection, may recover)
//   - Connection timeout (network congestion, may clear)
//   - Context deadline exceeded (timeout, may succeed on retry)
//   - Temporary network errors (transient failures)
//
// Non-retryable network errors:
//   - DNS failures (likely configuration issue)
//   - TLS handshake errors (certificate issues)
//   - Context canceled (explicit cancellation)
//
// Example:
//
//	resp, err := http.Do(req)
//	if err != nil {
//	    if httperror.IsRetryableNetworkError(err) {
//	        // Retry with backoff
//	        time.Sleep(backoff)
//	        continue
//	    }
//	    // Don't retry
//	    return err
//	}
func IsRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Context deadline exceeded is retryable (timeout)
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Context canceled is NOT retryable (explicit cancellation)
	if errors.Is(err, context.Canceled) {
		return false
	}

	// Check for net.Error interface (Timeout, Temporary)
	var netErr net.Error
	if errors.As(err, &netErr) {
		// Timeout errors are retryable
		if netErr.Timeout() {
			return true
		}
		// Note: Temporary() is deprecated but still useful for some cases
	}

	// Check for specific syscall errors via net.OpError
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Err != nil {
			// Connection refused - server might come back
			if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
				return true
			}
			// Connection reset - server closed connection
			if errors.Is(opErr.Err, syscall.ECONNRESET) {
				return true
			}
			// Connection aborted
			if errors.Is(opErr.Err, syscall.ECONNABORTED) {
				return true
			}
			// Network unreachable - might be temporary
			if errors.Is(opErr.Err, syscall.ENETUNREACH) {
				return true
			}
			// Host unreachable - might be temporary
			if errors.Is(opErr.Err, syscall.EHOSTUNREACH) {
				return true
			}
			// Broken pipe - connection was closed
			if errors.Is(opErr.Err, syscall.EPIPE) {
				return true
			}
		}
	}

	// Check for DNS errors (generally NOT retryable - configuration issue)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		// DNS temporary failures might be retryable
		if dnsErr.Temporary() {
			return true
		}
		// DNS timeout is retryable
		if dnsErr.Timeout() {
			return true
		}
		// Other DNS errors (NXDOMAIN, etc.) are not retryable
		return false
	}

	return false
}

// IsNetworkTimeout checks if an error is specifically a network timeout.
func IsNetworkTimeout(err error) bool {
	if err == nil {
		return false
	}

	// Context deadline exceeded
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// net.Error timeout
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

// IsConnectionRefused checks if an error is a connection refused error.
func IsConnectionRefused(err error) bool {
	if err == nil {
		return false
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Err != nil {
			return errors.Is(opErr.Err, syscall.ECONNREFUSED)
		}
	}

	return false
}

// IsConnectionReset checks if an error is a connection reset error.
func IsConnectionReset(err error) bool {
	if err == nil {
		return false
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Err != nil {
			return errors.Is(opErr.Err, syscall.ECONNRESET)
		}
	}

	return false
}

// IsDNSError checks if an error is a DNS resolution error.
func IsDNSError(err error) bool {
	if err == nil {
		return false
	}

	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

// ClassifyNetworkError returns a string classification of a network error.
//
// Returns one of: "timeout", "connection_refused", "connection_reset",
// "dns_error", "network_unreachable", "canceled", "network_error", "unknown".
func ClassifyNetworkError(err error) string {
	if err == nil {
		return "unknown"
	}

	// Context errors
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}

	// Timeout via net.Error
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}

	// DNS error
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns_error"
	}

	// Specific syscall errors
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		switch {
		case errors.Is(opErr.Err, syscall.ECONNREFUSED):
			return "connection_refused"
		case errors.Is(opErr.Err, syscall.ECONNRESET):
			return "connection_reset"
		case errors.Is(opErr.Err, syscall.ECONNABORTED):
			return "connection_aborted"
		case errors.Is(opErr.Err, syscall.ENETUNREACH):
			return "network_unreachable"
		case errors.Is(opErr.Err, syscall.EHOSTUNREACH):
			return "host_unreachable"
		case errors.Is(opErr.Err, syscall.EPIPE):
			return "broken_pipe"
		}
		return "network_error"
	}

	return "unknown"
}

// WrapNetworkError wraps a network error with additional context.
// Returns an HTTPAPIError with status 0 (indicating network-level failure).
func WrapNetworkError(provider string, err error) *HTTPAPIError {
	if err == nil {
		return nil
	}

	classification := ClassifyNetworkError(err)
	message := "network error: " + classification

	return &HTTPAPIError{
		StatusCode: 0, // 0 indicates network-level error (no HTTP response)
		Message:    message,
		Provider:   provider,
		Cause:      err,
	}
}
