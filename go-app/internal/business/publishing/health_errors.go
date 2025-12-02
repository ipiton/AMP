package publishing

import (
	"errors"
	"net"
	"strings"
)

// Error types for health monitoring.
var (
	// ErrNilDiscoveryManager is returned when discovery manager is nil.
	ErrNilDiscoveryManager = errors.New("discovery manager cannot be nil")

	// ErrInvalidTargetURL is returned when target URL is invalid.
	ErrInvalidTargetURL = errors.New("invalid target URL")
)

// Note: ErrAlreadyStarted, ErrNotStarted, ErrShutdownTimeout are defined in refresh_errors.go
// and are shared across publishing package components (HealthMonitor + RefreshManager).

// ErrorType classifies health check errors.
type ErrorType string

const (
	// ErrorTypeTimeout indicates connection timeout.
	ErrorTypeTimeout ErrorType = "timeout"

	// ErrorTypeDNS indicates DNS resolution failure.
	ErrorTypeDNS ErrorType = "dns"

	// ErrorTypeTLS indicates TLS handshake failure.
	ErrorTypeTLS ErrorType = "tls"

	// ErrorTypeRefused indicates connection refused.
	ErrorTypeRefused ErrorType = "refused"

	// ErrorTypeHTTP indicates HTTP error (status >= 400).
	ErrorTypeHTTP ErrorType = "http_error"

	// ErrorTypeUnknown indicates unknown error type.
	ErrorTypeUnknown ErrorType = "unknown"
)

// classifyNetworkError classifies network-level errors.
//
// This function examines network errors and returns appropriate ErrorType:
//   - Timeout errors → ErrorTypeTimeout
//   - DNS errors → ErrorTypeDNS
//   - Connection refused → ErrorTypeRefused
//   - TLS errors → ErrorTypeTLS
//   - Other errors → ErrorTypeUnknown
//
// Parameters:
//   - err: Network error from net package
//
// Returns:
//   - ErrorType: Classified error type
//
// Example:
//
//	err := net.DialTimeout("tcp", "invalid-host:443", 5*time.Second)
//	errType := classifyNetworkError(err)
//	// errType = ErrorTypeDNS
func classifyNetworkError(err error) ErrorType {
	if err == nil {
		return ErrorTypeUnknown
	}

	errStr := err.Error()

	// Timeout
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return ErrorTypeTimeout
	}

	// DNS
	if strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "dns") {
		return ErrorTypeDNS
	}

	// Connection refused
	if strings.Contains(errStr, "connection refused") {
		return ErrorTypeRefused
	}

	// TLS
	if strings.Contains(errStr, "tls") ||
		strings.Contains(errStr, "certificate") ||
		strings.Contains(errStr, "x509") {
		return ErrorTypeTLS
	}

	return ErrorTypeUnknown
}

// classifyHTTPError classifies HTTP client errors.
//
// This function examines HTTP client errors and returns appropriate ErrorType:
//   - Context deadline exceeded → ErrorTypeTimeout
//   - TLS errors → ErrorTypeTLS
//   - DNS errors → ErrorTypeDNS
//   - Other errors → ErrorTypeUnknown
//
// Parameters:
//   - err: Error from http.Client
//
// Returns:
//   - ErrorType: Classified error type
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
//	defer cancel()
//	resp, err := httpClient.Do(req.WithContext(ctx))
//	if err != nil {
//	    errType := classifyHTTPError(err)
//	}
func classifyHTTPError(err error) ErrorType {
	if err == nil {
		return ErrorTypeUnknown
	}

	errStr := err.Error()

	// Timeout (context deadline)
	if strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "timeout") {
		return ErrorTypeTimeout
	}

	// TLS
	if strings.Contains(errStr, "tls") ||
		strings.Contains(errStr, "certificate") ||
		strings.Contains(errStr, "x509") {
		return ErrorTypeTLS
	}

	// DNS
	if strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "dns") {
		return ErrorTypeDNS
	}

	return ErrorTypeUnknown
}

// sanitizeErrorMessage removes sensitive data from error messages.
//
// This function:
//   - Removes auth headers (Authorization, X-API-Key)
//   - Removes tokens and API keys
//   - Preserves error message structure
//
// Parameters:
//   - errMsg: Original error message
//
// Returns:
//   - string: Sanitized error message
//
// Example:
//
//	msg := "HTTP request failed: Authorization: Bearer secret123"
//	sanitized := sanitizeErrorMessage(msg)
//	// sanitized = "HTTP request failed: Authorization: [REDACTED]"
func sanitizeErrorMessage(errMsg string) string {
	// Remove common sensitive patterns
	patterns := []struct {
		prefix string
		suffix string
	}{
		{"Authorization:", "\n"},
		{"X-API-Key:", "\n"},
		{"Bearer ", " "},
		{"token=", "&"},
		{"api_key=", "&"},
	}

	sanitized := errMsg

	for _, p := range patterns {
		if idx := strings.Index(sanitized, p.prefix); idx != -1 {
			// Find end of sensitive data
			start := idx + len(p.prefix)
			end := strings.Index(sanitized[start:], p.suffix)
			if end == -1 {
				// No suffix found, redact to end
				sanitized = sanitized[:start] + " [REDACTED]"
			} else {
				// Redact between prefix and suffix
				sanitized = sanitized[:start] + " [REDACTED]" + sanitized[start+end:]
			}
		}
	}

	return sanitized
}
