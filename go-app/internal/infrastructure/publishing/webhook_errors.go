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
	ErrInvalidURL       = errors.New("invalid webhook URL")
	ErrInsecureScheme   = errors.New("URL must use HTTPS")
	ErrCredentialsInURL = errors.New("URL must not contain credentials")
	ErrBlockedHost      = errors.New("blocked hostname")

	// Payload validation errors
	ErrPayloadTooLarge = errors.New("payload exceeds size limit")
	ErrInvalidFormat   = errors.New("invalid payload format")

	// Header validation errors
	ErrTooManyHeaders      = errors.New("too many headers")
	ErrHeaderValueTooLarge = errors.New("header value too large")

	// Configuration validation errors
	ErrInvalidTimeout     = errors.New("timeout out of range")
	ErrInvalidRetryConfig = errors.New("invalid retry configuration")

	// Authentication errors
	ErrMissingAuthToken            = errors.New("missing auth token")
	ErrMissingBasicAuthCredentials = errors.New("missing basic auth credentials")
	ErrMissingAPIKey               = errors.New("missing API key")
	ErrNoCustomHeaders             = errors.New("no custom headers provided")
)

// IsWebhookRetryableError checks if a webhook error should be retried.
//
// Deprecated: Use httperror.IsRetryable or IsPublishingRetryable instead.
func IsWebhookRetryableError(err error) bool {
	return httperror.IsRetryable(err)
}

// IsWebhookPermanentError checks if an error is permanent (not retryable).
//
// Deprecated: Use !httperror.IsRetryable instead.
func IsWebhookPermanentError(err error) bool {
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
