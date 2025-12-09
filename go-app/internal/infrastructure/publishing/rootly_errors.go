package publishing

import (
	"errors"

	"github.com/ipiton/AMP/pkg/httperror"
)

// Rootly API Error Types
//
// NOTE: This file is being migrated to use pkg/httperror.
// New code should use httperror.HTTPAPIError and the unified error
// functions from errors.go.

// RootlyAPIError represents error from Rootly API.
//
// Deprecated: Use httperror.HTTPAPIError with ProviderRootly instead.
// This type is kept for backward compatibility.
type RootlyAPIError = httperror.HTTPAPIError

// Helper functions for error checking
//
// NOTE: These functions are deprecated. Use the unified functions from
// pkg/httperror or the IsPublishing* functions from errors.go.

// IsRootlyAPIError checks if error is a Rootly API error.
//
// Deprecated: Use errors.As with *httperror.HTTPAPIError instead.
func IsRootlyAPIError(err error) bool {
	var httpErr *httperror.HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.Provider == ProviderRootly
	}
	return false
}

// IsRootlyRetryableError checks if error should be retried.
//
// Deprecated: Use httperror.IsRetryable or IsPublishingRetryable instead.
func IsRootlyRetryableError(err error) bool {
	return httperror.IsRetryable(err)
}

// IsRootlyNotFoundError checks if error is not found.
//
// Deprecated: Use httperror.IsNotFound or IsPublishingNotFound instead.
func IsRootlyNotFoundError(err error) bool {
	return httperror.IsNotFound(err)
}

// IsRootlyConflictError checks if error is conflict (409).
//
// Deprecated: Use AsPublishingError and check IsConflict instead.
func IsRootlyConflictError(err error) bool {
	var httpErr *httperror.HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsConflict()
	}
	return false
}

// IsRootlyAuthError checks if error is authentication error.
//
// Deprecated: Use httperror.IsAuthError or IsPublishingAuthError instead.
func IsRootlyAuthError(err error) bool {
	return httperror.IsAuthError(err)
}

// IsRootlyRateLimitError checks if error is rate limit.
//
// Deprecated: Use httperror.IsRateLimit or IsPublishingRateLimit instead.
func IsRootlyRateLimitError(err error) bool {
	return httperror.IsRateLimit(err)
}

// IsRootlyValidationError checks if error is validation error (422).
//
// Deprecated: Use AsPublishingError and check IsValidation instead.
func IsRootlyValidationError(err error) bool {
	var httpErr *httperror.HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsValidation()
	}
	return false
}

// IsRootlyServerError checks if error is server error (5xx).
//
// Deprecated: Use httperror.IsServerError or IsPublishingServerError instead.
func IsRootlyServerError(err error) bool {
	return httperror.IsServerError(err)
}
