package publishing

import (
	"fmt"
)

// RootlyAPIError represents error from Rootly API
type RootlyAPIError struct {
	StatusCode int    // HTTP status code
	Title      string // Error title
	Detail     string // Error detail message
	Source     string // JSON pointer to field (e.g., "/data/attributes/title")
}

// Error implements error interface
func (e *RootlyAPIError) Error() string {
	if e.Source != "" {
		return fmt.Sprintf("Rootly API error %d: %s - %s (field: %s)",
			e.StatusCode, e.Title, e.Detail, e.Source)
	}
	return fmt.Sprintf("Rootly API error %d: %s - %s",
		e.StatusCode, e.Title, e.Detail)
}

// IsRetryable returns true if error is transient and should be retried
func (e *RootlyAPIError) IsRetryable() bool {
	// Retry 429 (rate limit) and 5xx (server errors)
	return e.StatusCode == 429 || e.StatusCode >= 500
}

// IsRateLimit returns true if error is rate limit (429)
func (e *RootlyAPIError) IsRateLimit() bool {
	return e.StatusCode == 429
}

// IsValidation returns true if error is validation error (422)
func (e *RootlyAPIError) IsValidation() bool {
	return e.StatusCode == 422
}

// IsAuth returns true if error is authentication error (401)
func (e *RootlyAPIError) IsAuth() bool {
	return e.StatusCode == 401
}

// IsNotFound returns true if error is not found (404)
func (e *RootlyAPIError) IsNotFound() bool {
	return e.StatusCode == 404
}

// IsConflict returns true if error is conflict (409)
func (e *RootlyAPIError) IsConflict() bool {
	return e.StatusCode == 409
}

// IsForbidden returns true if error is forbidden (403)
func (e *RootlyAPIError) IsForbidden() bool {
	return e.StatusCode == 403
}

// IsBadRequest returns true if error is bad request (400)
func (e *RootlyAPIError) IsBadRequest() bool {
	return e.StatusCode == 400
}

// IsServerError returns true if error is server error (5xx)
func (e *RootlyAPIError) IsServerError() bool {
	return e.StatusCode >= 500
}

// IsClientError returns true if error is client error (4xx)
func (e *RootlyAPIError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// Helper functions for error checking

// IsRootlyAPIError checks if error is RootlyAPIError
func IsRootlyAPIError(err error) bool {
	_, ok := err.(*RootlyAPIError)
	return ok
}

// IsRetryableError checks if error should be retried
func IsRetryableError(err error) bool {
	if rootlyErr, ok := err.(*RootlyAPIError); ok {
		return rootlyErr.IsRetryable()
	}
	return false
}

// IsNotFoundError checks if error is not found
func IsNotFoundError(err error) bool {
	if rootlyErr, ok := err.(*RootlyAPIError); ok {
		return rootlyErr.IsNotFound()
	}
	return false
}

// IsConflictError checks if error is conflict
func IsConflictError(err error) bool {
	if rootlyErr, ok := err.(*RootlyAPIError); ok {
		return rootlyErr.IsConflict()
	}
	return false
}

// IsAuthError checks if error is authentication error
func IsAuthError(err error) bool {
	if rootlyErr, ok := err.(*RootlyAPIError); ok {
		return rootlyErr.IsAuth()
	}
	return false
}

// IsRateLimitError checks if error is rate limit
func IsRateLimitError(err error) bool {
	if rootlyErr, ok := err.(*RootlyAPIError); ok {
		return rootlyErr.IsRateLimit()
	}
	return false
}
