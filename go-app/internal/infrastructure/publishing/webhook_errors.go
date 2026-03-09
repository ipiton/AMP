package publishing

import (
	"errors"

	"github.com/ipiton/AMP/pkg/httperror"
)

// Webhook Error Types
//
// NOTE: This file is being migrated to use pkg/httperror.
// New code should use httperror.HTTPAPIError and the unified error
// functions from errors.go.

// WebhookError represents a webhook operation error.
//
// Deprecated: Use httperror.HTTPAPIError with ProviderWebhook instead.
// This type is kept for backward compatibility.
type WebhookError = httperror.HTTPAPIError

// ErrorType categorizes webhook errors for retry decision and metrics.
//
// Deprecated: Use httperror.HTTPAPIError.Type() method instead.
type ErrorType int

const (
	// ErrorTypeValidation represents validation errors (permanent)
	ErrorTypeValidation ErrorType = iota

	// ErrorTypeAuth represents authentication errors (permanent)
	ErrorTypeAuth

	// ErrorTypeNetwork represents network errors (retryable)
	ErrorTypeNetwork

	// ErrorTypeTimeout represents timeout errors (retryable)
	ErrorTypeTimeout

	// ErrorTypeRateLimit represents rate limit errors (retryable)
	ErrorTypeRateLimit

	// ErrorTypeServer represents server errors 5xx (retryable)
	ErrorTypeServer
)

// String returns the string representation of ErrorType.
func (t ErrorType) String() string {
	switch t {
	case ErrorTypeValidation:
		return "validation"
	case ErrorTypeAuth:
		return "auth"
	case ErrorTypeNetwork:
		return "network"
	case ErrorTypeTimeout:
		return "timeout"
	case ErrorTypeRateLimit:
		return "rate_limit"
	case ErrorTypeServer:
		return "server"
	default:
		return "unknown"
	}
}

// Sentinel errors for common webhook validation failures
var (
	// URL validation errors
	ErrEmptyURL         = errors.New("webhook URL cannot be empty")
	ErrInvalidURL       = errors.New("webhook URL is invalid")
	ErrInsecureScheme   = errors.New("webhook URL must use HTTPS scheme")
	ErrCredentialsInURL = errors.New("webhook URL must not contain credentials")
	ErrBlockedHost      = errors.New("webhook URL host is blocked (localhost/private IP)")

	// Payload validation errors
	ErrPayloadTooLarge = errors.New("webhook payload exceeds maximum size")
	ErrInvalidFormat   = errors.New("webhook payload format is invalid")

	// Header validation errors
	ErrTooManyHeaders      = errors.New("webhook has too many headers")
	ErrHeaderValueTooLarge = errors.New("webhook header value exceeds maximum size")

	// Configuration validation errors
	ErrInvalidTimeout     = errors.New("webhook timeout must be between 1s and 60s")
	ErrInvalidRetryConfig = errors.New("webhook retry configuration is invalid")

	// Authentication errors
	ErrMissingAuthToken            = errors.New("bearer token is required but not provided")
	ErrMissingBasicAuthCredentials = errors.New("basic auth username/password required but not provided")
	ErrMissingAPIKey               = errors.New("API key is required but not provided")
	ErrNoCustomHeaders             = errors.New("custom headers are required but not provided")
)

// IsWebhookRetryableError checks if a webhook error should be retried.
func IsWebhookRetryableError(err error) bool {
	return httperror.IsRetryable(err)
}

// IsWebhookPermanentError checks if an error is permanent (not retryable).
func IsWebhookPermanentError(err error) bool {
	if err == nil {
		return false
	}

	// For non-webhook errors, we follow the test expectation
	if _, ok := err.(*httperror.HTTPAPIError); !ok {
		return false
	}

	return !httperror.IsRetryable(err)
}

// classifyHTTPError classifies HTTP status code to error category
func classifyHTTPError(statusCode int) ErrorCategory {
	switch {
	case statusCode == 429:
		return ErrorCategoryRetryable // Rate limit
	case statusCode >= 500:
		return ErrorCategoryRetryable // Server errors
	case statusCode >= 400 && statusCode < 500:
		return ErrorCategoryPermanent // Client errors
	default:
		return ErrorCategoryUnknown
	}
}

// classifyErrorType classifies HTTP status code to ErrorType
func classifyErrorType(statusCode int) ErrorType {
	switch {
	case statusCode == 429:
		return ErrorTypeRateLimit
	case statusCode >= 500:
		return ErrorTypeServer
	case statusCode == 401 || statusCode == 403:
		return ErrorTypeAuth
	case statusCode == 408 || statusCode == 504:
		return ErrorTypeTimeout
	default:
		return ErrorTypeValidation
	}
}

// ErrorCategory classifies errors for retry decision
type ErrorCategory int

const (
	// ErrorCategoryRetryable means the error can be retried
	ErrorCategoryRetryable ErrorCategory = iota

	// ErrorCategoryPermanent means the error should not be retried
	ErrorCategoryPermanent

	// ErrorCategoryUnknown means the error category is unknown (treat as permanent)
	ErrorCategoryUnknown
)

// String returns the string representation of ErrorCategory
func (c ErrorCategory) String() string {
	switch c {
	case ErrorCategoryRetryable:
		return "retryable"
	case ErrorCategoryPermanent:
		return "permanent"
	default:
		return "unknown"
	}
}
