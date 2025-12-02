package publishing

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
)

// classifyPublishingError determines whether an error should be retried (transient) or not (permanent).
//
// Error Classification Rules:
//
// TRANSIENT (retry):
//   - HTTP 429 (Rate Limit) - retry after backoff
//   - HTTP 408 (Request Timeout) - retry immediately
//   - HTTP 502, 503, 504 (Bad Gateway, Service Unavailable, Gateway Timeout) - retry after backoff
//   - Network errors (connection refused, timeout, DNS failure)
//   - Temporary errors (net.Error with Temporary() = true)
//   - syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ETIMEDOUT
//
// PERMANENT (do NOT retry):
//   - HTTP 400 (Bad Request) - invalid payload
//   - HTTP 401 (Unauthorized) - invalid credentials
//   - HTTP 403 (Forbidden) - insufficient permissions
//   - HTTP 404 (Not Found) - invalid URL
//   - HTTP 405 (Method Not Allowed) - wrong HTTP method
//   - HTTP 422 (Unprocessable Entity) - invalid data format
//   - HTTP 5xx (except 502/503/504) - permanent server errors
//
// UNKNOWN (retry with caution):
//   - All other errors - default to transient with conservative retry
//
// Parameters:
//   - err: The error to classify
//
// Returns:
//   - QueueErrorType: QueueErrorTypeTransient, QueueErrorTypePermanent, or QueueErrorTypeUnknown
//
// Example:
//
//	errType := classifyPublishingError(err)
//	if errType == QueueErrorTypePermanent {
//	    // Send to DLQ
//	}
func classifyPublishingError(err error) QueueErrorType {
	if err == nil {
		return QueueErrorTypeUnknown
	}

	// HTTP response errors
	var httpErr interface{ StatusCode() int }
	if errors.As(err, &httpErr) {
		return classifyQueueHTTPError(httpErr.StatusCode())
	}

	// String-based HTTP error parsing (fallback)
	// Try parsing error message for HTTP status codes (more aggressive)
	errMsg := err.Error()
	if errType := classifyQueueHTTPErrorString(errMsg); errType != QueueErrorTypeUnknown {
		return errType
	}

	// Network errors (transient)
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return QueueErrorTypeTransient // Network timeout
		}
		if netErr.Temporary() {
			return QueueErrorTypeTransient // Temporary network error
		}
	}

	// DNS errors (transient)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return QueueErrorTypeTransient
	}

	// Connection refused (transient)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return QueueErrorTypeTransient
	}

	// Syscall errors
	var syscallErr syscall.Errno
	if errors.As(err, &syscallErr) {
		switch syscallErr {
		case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ETIMEDOUT:
			return QueueErrorTypeTransient
		}
	}

	// Default: UNKNOWN (retry with caution)
	return QueueErrorTypeUnknown
}

// classifyQueueHTTPError classifies errors based on HTTP status code
func classifyQueueHTTPError(statusCode int) QueueErrorType {
	switch statusCode {
	// TRANSIENT - retry
	case http.StatusRequestTimeout: // 408
		return QueueErrorTypeTransient
	case http.StatusTooManyRequests: // 429
		return QueueErrorTypeTransient
	case http.StatusBadGateway: // 502
		return QueueErrorTypeTransient
	case http.StatusServiceUnavailable: // 503
		return QueueErrorTypeTransient
	case http.StatusGatewayTimeout: // 504
		return QueueErrorTypeTransient

	// PERMANENT - do NOT retry
	case http.StatusBadRequest: // 400
		return QueueErrorTypePermanent
	case http.StatusUnauthorized: // 401
		return QueueErrorTypePermanent
	case http.StatusForbidden: // 403
		return QueueErrorTypePermanent
	case http.StatusNotFound: // 404
		return QueueErrorTypePermanent
	case http.StatusMethodNotAllowed: // 405
		return QueueErrorTypePermanent
	case http.StatusConflict: // 409
		return QueueErrorTypePermanent
	case http.StatusGone: // 410
		return QueueErrorTypePermanent
	case http.StatusUnprocessableEntity: // 422
		return QueueErrorTypePermanent

	// 5xx errors (except 502/503/504) - permanent
	default:
		if statusCode >= 500 && statusCode < 600 {
			// Unknown 5xx - permanent
			return QueueErrorTypePermanent
		}
		return QueueErrorTypeUnknown
	}
}

// classifyQueueHTTPErrorString parses error message for HTTP status codes
func classifyQueueHTTPErrorString(errMsg string) QueueErrorType {
	// Extract status code from error message
	// Common formats:
	// - "HTTP 404 Not Found"
	// - "status code: 503"
	// - "received 429 Too Many Requests"

	transientCodes := []string{"408", "429", "502", "503", "504"}
	permanentCodes := []string{"400", "401", "403", "404", "405", "409", "410", "422"}

	for _, code := range transientCodes {
		if strings.Contains(errMsg, code) {
			return QueueErrorTypeTransient
		}
	}

	for _, code := range permanentCodes {
		if strings.Contains(errMsg, code) {
			return QueueErrorTypePermanent
		}
	}

	// Unknown HTTP error
	return QueueErrorTypeUnknown
}
