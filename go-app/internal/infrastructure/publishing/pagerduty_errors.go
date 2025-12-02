package publishing

import (
	"errors"
	"fmt"
)

// PagerDuty API Error Types

// PagerDutyAPIError represents an error from PagerDuty Events API v2
type PagerDutyAPIError struct {
	// StatusCode is the HTTP status code from the API response
	StatusCode int

	// Message is the error message from the API
	Message string

	// Errors is a list of detailed error messages from the API
	Errors []string
}

// Error implements the error interface
func (e *PagerDutyAPIError) Error() string {
	if len(e.Errors) > 0 {
		return fmt.Sprintf("PagerDuty API error %d: %s (details: %v)", e.StatusCode, e.Message, e.Errors)
	}
	return fmt.Sprintf("PagerDuty API error %d: %s", e.StatusCode, e.Message)
}

// Type returns the error type classification based on HTTP status code
func (e *PagerDutyAPIError) Type() string {
	switch e.StatusCode {
	case 400:
		return "bad_request"
	case 401:
		return "unauthorized"
	case 403:
		return "forbidden"
	case 404:
		return "not_found"
	case 429:
		return "rate_limit"
	case 500, 502, 503, 504:
		return "server_error"
	default:
		return "unknown"
	}
}

// Sentinel errors for common PagerDuty integration issues
var (
	// ErrMissingRoutingKey is returned when routing_key is missing from target configuration
	ErrMissingRoutingKey = errors.New("pagerduty: routing_key not found in target configuration")

	// ErrInvalidDedupKey is returned when dedup_key is invalid or empty
	ErrInvalidDedupKey = errors.New("pagerduty: invalid or empty dedup_key")

	// ErrEventNotTracked is returned when attempting to acknowledge/resolve an event not in cache
	ErrEventNotTracked = errors.New("pagerduty: event not tracked in cache (no dedup_key found)")

	// ErrRateLimitExceeded is returned when PagerDuty rate limit is exceeded
	ErrRateLimitExceeded = errors.New("pagerduty: rate limit exceeded (120 req/min)")

	// ErrAPITimeout is returned when API request times out
	ErrAPITimeout = errors.New("pagerduty: API request timeout")

	// ErrAPIConnection is returned when API connection fails
	ErrAPIConnection = errors.New("pagerduty: API connection failed")

	// ErrInvalidRequest is returned when request validation fails
	ErrInvalidRequest = errors.New("pagerduty: invalid request")
)

// Error Helper Functions

// IsRetryable returns true if the error is retryable (transient)
// Retryable errors: rate limits (429), server errors (5xx), timeouts
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for rate limit error
	if errors.Is(err, ErrRateLimitExceeded) {
		return true
	}

	// Check for timeout error
	if errors.Is(err, ErrAPITimeout) {
		return true
	}

	// Check for connection error
	if errors.Is(err, ErrAPIConnection) {
		return true
	}

	// Check for PagerDuty API error
	var apiErr *PagerDutyAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 429: // Rate limit
			return true
		case 500, 502, 503, 504: // Server errors
			return true
		default:
			return false
		}
	}

	return false
}

// IsRateLimit returns true if the error is a rate limit error (429)
func IsRateLimit(err error) bool {
	if err == nil {
		return false
	}

	// Check sentinel error
	if errors.Is(err, ErrRateLimitExceeded) {
		return true
	}

	// Check API error
	var apiErr *PagerDutyAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 429
	}

	return false
}

// IsPagerDutyAuthError returns true if the error is an authentication error (401, 403)
func IsPagerDutyAuthError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *PagerDutyAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 401 || apiErr.StatusCode == 403
	}

	return false
}

// IsBadRequest returns true if the error is a bad request error (400)
func IsBadRequest(err error) bool {
	if err == nil {
		return false
	}

	// Check sentinel error
	if errors.Is(err, ErrInvalidRequest) {
		return true
	}

	// Check API error
	var apiErr *PagerDutyAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 400
	}

	return false
}

// IsNotFound returns true if the error is a not found error (404)
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *PagerDutyAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 404
	}

	return false
}

// IsServerError returns true if the error is a server error (5xx)
func IsServerError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *PagerDutyAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500 && apiErr.StatusCode < 600
	}

	return false
}

// IsTimeout returns true if the error is a timeout error
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}

	return errors.Is(err, ErrAPITimeout)
}

// IsConnectionError returns true if the error is a connection error
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}

	return errors.Is(err, ErrAPIConnection)
}
