// Package publishing provides unified error handling for all publishers.
//
// This file consolidates error types from Slack, PagerDuty, Rootly, and Webhook
// publishers into a single, unified interface using pkg/httperror.
//
// Usage:
//
//	import "github.com/ipiton/AMP/pkg/httperror"
//	if httperror.IsRetryable(err) { ... }
//	if httperror.IsRateLimit(err) { ... }
package publishing

import (
	"errors"

	"github.com/ipiton/AMP/pkg/httperror"
)

// ============================================================================
// Provider Constants
// ============================================================================

// Provider constants for error identification
const (
	ProviderSlack     = "slack"
	ProviderPagerDuty = "pagerduty"
	ProviderRootly    = "rootly"
	ProviderWebhook   = "webhook"
	ProviderTelegram  = "telegram"
)

// ============================================================================
// Unified Error Factory Functions
// ============================================================================

// NewPublishingError creates a publishing-specific HTTP error.
func NewPublishingError(statusCode int, message, provider string) *httperror.HTTPAPIError {
	return httperror.NewHTTPError(statusCode, message, provider)
}

// NewPublishingErrorWithDetails creates a publishing error with additional details.
func NewPublishingErrorWithDetails(statusCode int, message, provider string, details []string) *httperror.HTTPAPIError {
	return httperror.NewHTTPErrorWithDetails(statusCode, message, provider, details)
}

// NewPublishingErrorWithCause creates a publishing error wrapping an underlying error.
func NewPublishingErrorWithCause(statusCode int, message, provider string, cause error) *httperror.HTTPAPIError {
	return httperror.NewHTTPErrorWithCause(statusCode, message, provider, cause)
}

// ============================================================================
// Unified Error Classification Functions
// ============================================================================

// IsPublishingRetryable checks if a publishing error should be retried.
// Works with HTTPAPIError and network errors from any provider.
func IsPublishingRetryable(err error) bool {
	return httperror.IsRetryable(err)
}

// IsPublishingRateLimit checks if a publishing error is a rate limit.
func IsPublishingRateLimit(err error) bool {
	return httperror.IsRateLimit(err)
}

// IsPublishingAuthError checks if a publishing error is an auth error.
func IsPublishingAuthError(err error) bool {
	return httperror.IsAuthError(err)
}

// IsPublishingNotFound checks if a publishing error is not found.
func IsPublishingNotFound(err error) bool {
	return httperror.IsNotFound(err)
}

// IsPublishingTimeout checks if a publishing error is a timeout.
func IsPublishingTimeout(err error) bool {
	return httperror.IsTimeout(err)
}

// IsPublishingServerError checks if a publishing error is a server error.
func IsPublishingServerError(err error) bool {
	return httperror.IsServerError(err)
}

// GetPublishingRetryAfter extracts retry-after seconds from a publishing error.
// Returns 0 if not applicable.
func GetPublishingRetryAfter(err error) int {
	return httperror.GetRetryAfter(err)
}

// GetPublishingProvider extracts the provider name from a publishing error.
// Returns "unknown" if not found.
func GetPublishingProvider(err error) string {
	return httperror.GetProvider(err)
}

// GetPublishingErrorType returns the error type classification.
func GetPublishingErrorType(err error) string {
	return httperror.GetErrorType(err)
}

// ============================================================================
// Helper: Extract HTTPAPIError from any error
// ============================================================================

// AsPublishingError extracts HTTPAPIError from an error chain.
// Returns nil if not found.
func AsPublishingError(err error) *httperror.HTTPAPIError {
	if err == nil {
		return nil
	}

	var httpErr *httperror.HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr
	}

	return nil
}

// WrapAsPublishingError wraps any error as a publishing error.
// If err is already an HTTPAPIError, returns it unchanged.
// Otherwise, wraps it as an internal server error for the given provider.
func WrapAsPublishingError(err error, provider string) *httperror.HTTPAPIError {
	if err == nil {
		return nil
	}

	// Check if already HTTPAPIError
	var httpErr *httperror.HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr
	}

	// Check for network error
	if httperror.IsRetryableNetworkError(err) {
		return httperror.WrapNetworkError(provider, err)
	}

	// Wrap as internal server error
	return httperror.NewServerError(provider, err)
}
