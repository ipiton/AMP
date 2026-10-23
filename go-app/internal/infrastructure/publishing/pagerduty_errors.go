package publishing

import (
	"errors"
)

// PagerDuty API Error Types
//
// PagerDuty API errors are represented by httperror.HTTPAPIError with
// Provider set to ProviderPagerDuty. See errors.go for unified helpers
// (IsPublishingRetryable, IsPublishingRateLimit, etc.) or use pkg/httperror
// directly.

// Sentinel errors for common PagerDuty integration issues
var (
	// ErrMissingRoutingKey is returned when routing_key is missing from target configuration
	ErrMissingRoutingKey = errors.New("pagerduty: routing_key not found in target configuration")

	// ErrInvalidDedupKey is returned when dedup_key is invalid or empty
	ErrInvalidDedupKey = errors.New("pagerduty: invalid or empty dedup_key")

	// ErrRateLimitExceeded is returned when PagerDuty rate limit is exceeded
	ErrRateLimitExceeded = errors.New("pagerduty: rate limit exceeded (120 req/min)")

	// ErrAPITimeout is returned when API request times out
	ErrAPITimeout = errors.New("pagerduty: API request timeout")

	// ErrAPIConnection is returned when API connection fails
	ErrAPIConnection = errors.New("pagerduty: API connection failed")

	// ErrInvalidRequest is returned when request validation fails
	ErrInvalidRequest = errors.New("pagerduty: invalid request")
)
