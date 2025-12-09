package publishing

import (
	"errors"

	"github.com/ipiton/AMP/pkg/httperror"
)

// PagerDuty API Error Types
//
// NOTE: This file is being migrated to use pkg/httperror.
// New code should use httperror.HTTPAPIError and the unified error
// functions from errors.go.

// PagerDutyAPIError represents an error from PagerDuty Events API v2.
//
// Deprecated: Use httperror.HTTPAPIError with ProviderPagerDuty instead.
// This type is kept for backward compatibility.
type PagerDutyAPIError = httperror.HTTPAPIError

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
//
// NOTE: These functions are deprecated. Use the unified functions from
// pkg/httperror or the IsPublishing* functions from errors.go.

// IsPagerDutyRetryable returns true if the error is retryable (transient).
// Retryable errors: rate limits (429), server errors (5xx), timeouts.
//
// Deprecated: Use httperror.IsRetryable or IsPublishingRetryable instead.
func IsPagerDutyRetryable(err error) bool {
	if err == nil {
		return false
	}

	// Check for PagerDuty-specific sentinel errors
	if errors.Is(err, ErrRateLimitExceeded) ||
		errors.Is(err, ErrAPITimeout) ||
		errors.Is(err, ErrAPIConnection) {
		return true
	}

	// Delegate to unified implementation
	return httperror.IsRetryable(err)
}

// IsPagerDutyRateLimit returns true if the error is a rate limit error (429).
//
// Deprecated: Use httperror.IsRateLimit or IsPublishingRateLimit instead.
func IsPagerDutyRateLimit(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrRateLimitExceeded) {
		return true
	}

	return httperror.IsRateLimit(err)
}

// IsPagerDutyAuthError returns true if the error is an authentication error (401, 403).
//
// Deprecated: Use httperror.IsAuthError or IsPublishingAuthError instead.
func IsPagerDutyAuthError(err error) bool {
	return httperror.IsAuthError(err)
}

// IsPagerDutyBadRequest returns true if the error is a bad request error (400).
//
// Deprecated: Use AsPublishingError and check StatusCode instead.
func IsPagerDutyBadRequest(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrInvalidRequest) {
		return true
	}

	var httpErr *httperror.HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsBadRequest()
	}

	return false
}

// IsPagerDutyNotFound returns true if the error is a not found error (404).
//
// Deprecated: Use httperror.IsNotFound or IsPublishingNotFound instead.
func IsPagerDutyNotFound(err error) bool {
	return httperror.IsNotFound(err)
}

// IsPagerDutyServerError returns true if the error is a server error (5xx).
//
// Deprecated: Use httperror.IsServerError or IsPublishingServerError instead.
func IsPagerDutyServerError(err error) bool {
	return httperror.IsServerError(err)
}

// IsPagerDutyTimeout returns true if the error is a timeout error.
//
// Deprecated: Use httperror.IsTimeout or IsPublishingTimeout instead.
func IsPagerDutyTimeout(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrAPITimeout) {
		return true
	}

	return httperror.IsTimeout(err)
}

// IsPagerDutyConnectionError returns true if the error is a connection error.
//
// Deprecated: Use httperror.IsRetryableNetworkError instead.
func IsPagerDutyConnectionError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, ErrAPIConnection) {
		return true
	}

	return httperror.IsRetryableNetworkError(err)
}
