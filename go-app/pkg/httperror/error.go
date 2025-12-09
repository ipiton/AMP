// Package httperror provides unified HTTP API error types for external service integrations.
//
// This package eliminates code duplication across publisher implementations (Slack, PagerDuty,
// Rootly, Webhook) by providing a single, well-tested error type with consistent classification
// methods.
//
// Usage:
//
//	// Create error from HTTP response
//	err := httperror.NewHTTPError(resp.StatusCode, "bad request", "slack")
//
//	// Check error classification
//	if httperror.IsRetryable(err) {
//	    // Retry the request
//	}
//
//	// Create specific error types
//	err := httperror.NewRateLimitError("pagerduty", 60) // retry after 60s
//	err := httperror.NewTimeoutError("rootly", cause)
//	err := httperror.NewAuthError("webhook")
package httperror

import (
	"errors"
	"fmt"
	"net/http"
)

// HTTPAPIError represents a standard HTTP API error from external services.
//
// This type unifies error handling across all publisher implementations,
// providing consistent classification methods for retry logic, metrics,
// and error reporting.
//
// Thread-safe: This type is immutable after creation.
type HTTPAPIError struct {
	// StatusCode is the HTTP status code from the API response.
	// Standard codes: 200-299 success, 400-499 client error, 500-599 server error.
	StatusCode int `json:"status_code"`

	// Message is the human-readable error message.
	// May come from API response body or be generated.
	Message string `json:"message"`

	// Provider identifies the external service (slack, pagerduty, rootly, webhook).
	// Used for metrics labeling and logging.
	Provider string `json:"provider"`

	// RetryAfter is the recommended wait time in seconds before retry.
	// Typically set for 429 (rate limit) responses via Retry-After header.
	// Zero means no specific recommendation.
	RetryAfter int `json:"retry_after,omitempty"`

	// Details contains additional error information from the API.
	// May include field-specific validation errors.
	Details []string `json:"details,omitempty"`

	// RequestID is the unique request identifier for debugging.
	// Typically from X-Request-ID or similar header.
	RequestID string `json:"request_id,omitempty"`

	// Cause is the underlying error that caused this error.
	// Used for error wrapping and unwrapping.
	Cause error `json:"-"`
}

// Error implements the error interface.
// Format: "{provider} API error {status}: {message}" or with retry info.
func (e *HTTPAPIError) Error() string {
	if e == nil {
		return "nil HTTPAPIError"
	}

	provider := e.Provider
	if provider == "" {
		provider = "unknown"
	}

	if e.RetryAfter > 0 {
		return fmt.Sprintf("%s API error %d: %s (retry after %ds)",
			provider, e.StatusCode, e.Message, e.RetryAfter)
	}

	if len(e.Details) > 0 {
		return fmt.Sprintf("%s API error %d: %s (details: %v)",
			provider, e.StatusCode, e.Message, e.Details)
	}

	return fmt.Sprintf("%s API error %d: %s", provider, e.StatusCode, e.Message)
}

// Unwrap implements errors unwrapping for error chains.
// Allows use with errors.Is and errors.As.
func (e *HTTPAPIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is implements errors.Is comparison.
// Matches if both are HTTPAPIError with same StatusCode and Provider.
func (e *HTTPAPIError) Is(target error) bool {
	if e == nil {
		return target == nil
	}

	t, ok := target.(*HTTPAPIError)
	if !ok {
		return false
	}

	return e.StatusCode == t.StatusCode && e.Provider == t.Provider
}

// ============================================================================
// Classification Methods
// ============================================================================

// IsRetryable returns true if the error is transient and the request should be retried.
//
// Retryable errors:
//   - 429 Too Many Requests (rate limit)
//   - 500 Internal Server Error
//   - 502 Bad Gateway
//   - 503 Service Unavailable
//   - 504 Gateway Timeout
//
// Non-retryable errors:
//   - 400 Bad Request (client error)
//   - 401 Unauthorized (auth error)
//   - 403 Forbidden (auth error)
//   - 404 Not Found (resource error)
func (e *HTTPAPIError) IsRetryable() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode >= http.StatusInternalServerError
}

// IsRateLimit returns true if the error indicates rate limiting (429).
func (e *HTTPAPIError) IsRateLimit() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusTooManyRequests
}

// IsServerError returns true if the error is a server-side error (5xx).
func (e *HTTPAPIError) IsServerError() bool {
	if e == nil {
		return false
	}
	return e.StatusCode >= 500 && e.StatusCode < 600
}

// IsClientError returns true if the error is a client-side error (4xx).
func (e *HTTPAPIError) IsClientError() bool {
	if e == nil {
		return false
	}
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// IsAuthError returns true if the error is an authentication/authorization error (401, 403).
func (e *HTTPAPIError) IsAuthError() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusUnauthorized ||
		e.StatusCode == http.StatusForbidden
}

// IsNotFound returns true if the error indicates resource not found (404).
func (e *HTTPAPIError) IsNotFound() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusNotFound
}

// IsTimeout returns true if the error indicates a timeout (408, 504).
func (e *HTTPAPIError) IsTimeout() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode == http.StatusGatewayTimeout
}

// IsBadRequest returns true if the error indicates bad request (400).
func (e *HTTPAPIError) IsBadRequest() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusBadRequest
}

// IsConflict returns true if the error indicates conflict (409).
func (e *HTTPAPIError) IsConflict() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusConflict
}

// IsValidation returns true if the error indicates validation failure (422).
func (e *HTTPAPIError) IsValidation() bool {
	if e == nil {
		return false
	}
	return e.StatusCode == http.StatusUnprocessableEntity
}

// Type returns a string classification of the error for metrics/logging.
//
// Returns one of: "rate_limit", "timeout", "server_error", "auth_error", "not_found",
// "validation", "bad_request", "conflict", "client_error", "unknown".
//
// Note: timeout is checked before server_error because 504 is both.
func (e *HTTPAPIError) Type() string {
	if e == nil {
		return "unknown"
	}

	switch {
	case e.IsRateLimit():
		return "rate_limit"
	case e.IsTimeout():
		return "timeout"
	case e.IsServerError():
		return "server_error"
	case e.IsAuthError():
		return "auth_error"
	case e.IsNotFound():
		return "not_found"
	case e.IsValidation():
		return "validation"
	case e.IsBadRequest():
		return "bad_request"
	case e.IsConflict():
		return "conflict"
	case e.IsClientError():
		return "client_error"
	default:
		return "unknown"
	}
}

// ============================================================================
// Factory Functions
// ============================================================================

// NewHTTPError creates a new HTTPAPIError with the given parameters.
func NewHTTPError(statusCode int, message, provider string) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: statusCode,
		Message:    message,
		Provider:   provider,
	}
}

// NewHTTPErrorWithCause creates a new HTTPAPIError wrapping an underlying error.
func NewHTTPErrorWithCause(statusCode int, message, provider string, cause error) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: statusCode,
		Message:    message,
		Provider:   provider,
		Cause:      cause,
	}
}

// NewHTTPErrorWithDetails creates a new HTTPAPIError with additional details.
func NewHTTPErrorWithDetails(statusCode int, message, provider string, details []string) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: statusCode,
		Message:    message,
		Provider:   provider,
		Details:    details,
	}
}

// NewRateLimitError creates a rate limit error (429) with retry-after information.
func NewRateLimitError(provider string, retryAfter int) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "rate limit exceeded",
		Provider:   provider,
		RetryAfter: retryAfter,
	}
}

// NewTimeoutError creates a timeout error (504) wrapping the underlying cause.
func NewTimeoutError(provider string, cause error) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: http.StatusGatewayTimeout,
		Message:    "request timeout",
		Provider:   provider,
		Cause:      cause,
	}
}

// NewAuthError creates an authentication error (401).
func NewAuthError(provider string) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: http.StatusUnauthorized,
		Message:    "authentication failed",
		Provider:   provider,
	}
}

// NewForbiddenError creates a forbidden error (403).
func NewForbiddenError(provider string) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: http.StatusForbidden,
		Message:    "access forbidden",
		Provider:   provider,
	}
}

// NewNotFoundError creates a not found error (404).
func NewNotFoundError(provider string, resource string) *HTTPAPIError {
	msg := "resource not found"
	if resource != "" {
		msg = fmt.Sprintf("%s not found", resource)
	}
	return &HTTPAPIError{
		StatusCode: http.StatusNotFound,
		Message:    msg,
		Provider:   provider,
	}
}

// NewBadRequestError creates a bad request error (400).
func NewBadRequestError(provider string, message string) *HTTPAPIError {
	if message == "" {
		message = "bad request"
	}
	return &HTTPAPIError{
		StatusCode: http.StatusBadRequest,
		Message:    message,
		Provider:   provider,
	}
}

// NewValidationError creates a validation error (422) with field details.
func NewValidationError(provider string, details []string) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: http.StatusUnprocessableEntity,
		Message:    "validation failed",
		Provider:   provider,
		Details:    details,
	}
}

// NewServerError creates an internal server error (500).
func NewServerError(provider string, cause error) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: http.StatusInternalServerError,
		Message:    "internal server error",
		Provider:   provider,
		Cause:      cause,
	}
}

// NewServiceUnavailableError creates a service unavailable error (503).
func NewServiceUnavailableError(provider string, retryAfter int) *HTTPAPIError {
	return &HTTPAPIError{
		StatusCode: http.StatusServiceUnavailable,
		Message:    "service unavailable",
		Provider:   provider,
		RetryAfter: retryAfter,
	}
}

// ============================================================================
// Standalone Functions (for errors.Is/As compatibility)
// ============================================================================

// IsRetryable checks if any error in the chain is retryable.
// Works with both HTTPAPIError and network errors.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsRetryable()
	}

	// Check for network errors
	return IsRetryableNetworkError(err)
}

// IsRateLimit checks if any error in the chain is a rate limit error.
func IsRateLimit(err error) bool {
	if err == nil {
		return false
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsRateLimit()
	}

	return false
}

// IsServerError checks if any error in the chain is a server error (5xx).
func IsServerError(err error) bool {
	if err == nil {
		return false
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsServerError()
	}

	return false
}

// IsAuthError checks if any error in the chain is an auth error.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsAuthError()
	}

	return false
}

// IsNotFound checks if any error in the chain is a not found error.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsNotFound()
	}

	return false
}

// IsTimeout checks if any error in the chain is a timeout error.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.IsTimeout()
	}

	// Also check for network timeout
	return IsNetworkTimeout(err)
}

// GetRetryAfter extracts the retry-after value from an error chain.
// Returns 0 if not found or not applicable.
func GetRetryAfter(err error) int {
	if err == nil {
		return 0
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.RetryAfter
	}

	return 0
}

// GetProvider extracts the provider name from an error chain.
// Returns "unknown" if not found.
func GetProvider(err error) string {
	if err == nil {
		return "unknown"
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		if httpErr.Provider != "" {
			return httpErr.Provider
		}
	}

	return "unknown"
}

// GetErrorType extracts the error type classification from an error chain.
// Returns "unknown" if not an HTTPAPIError.
func GetErrorType(err error) string {
	if err == nil {
		return "unknown"
	}

	var httpErr *HTTPAPIError
	if errors.As(err, &httpErr) {
		return httpErr.Type()
	}

	if IsRetryableNetworkError(err) {
		return "network_error"
	}

	return "unknown"
}
