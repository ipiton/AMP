package k8s

import (
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// K8sError is the base error type for K8s client errors
type K8sError struct {
	Op      string // Operation name (e.g., "list secrets", "get secret")
	Message string // Human-readable message
	Err     error  // Underlying error
}

// Error implements the error interface
func (e *K8sError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("k8s %s: %s: %v", e.Op, e.Message, e.Err)
	}
	return fmt.Sprintf("k8s %s: %s", e.Op, e.Message)
}

// Unwrap returns the underlying error for error wrapping support
func (e *K8sError) Unwrap() error {
	return e.Err
}

// ConnectionError represents connection-related errors
type ConnectionError struct {
	*K8sError
}

// NewConnectionError creates a new connection error
func NewConnectionError(message string, err error) *ConnectionError {
	return &ConnectionError{
		K8sError: &K8sError{
			Op:      "connection",
			Message: message,
			Err:     err,
		},
	}
}

// AuthError represents authentication/authorization errors
type AuthError struct {
	*K8sError
}

// NewAuthError creates a new authentication error
func NewAuthError(message string, err error) *AuthError {
	return &AuthError{
		K8sError: &K8sError{
			Op:      "authentication",
			Message: message,
			Err:     err,
		},
	}
}

// NotFoundError represents resource not found errors
type NotFoundError struct {
	*K8sError
}

// NewNotFoundError creates a new not found error
func NewNotFoundError(message string) *NotFoundError {
	return &NotFoundError{
		K8sError: &K8sError{
			Op:      "not_found",
			Message: message,
		},
	}
}

// TimeoutError represents timeout errors
type TimeoutError struct {
	*K8sError
}

// NewTimeoutError creates a new timeout error
func NewTimeoutError(message string, err error) *TimeoutError {
	return &TimeoutError{
		K8sError: &K8sError{
			Op:      "timeout",
			Message: message,
			Err:     err,
		},
	}
}

// wrapK8sError wraps a Kubernetes API error into our custom error types
func wrapK8sError(operation string, err error) error {
	if k8serrors.IsUnauthorized(err) || k8serrors.IsForbidden(err) {
		return NewAuthError("insufficient permissions", err)
	}
	if k8serrors.IsNotFound(err) {
		return NewNotFoundError(operation + " not found")
	}
	if k8serrors.IsTimeout(err) || k8serrors.IsServerTimeout(err) {
		return NewTimeoutError("request timed out", err)
	}

	// Generic K8s error
	return &K8sError{
		Op:      operation,
		Message: "operation failed",
		Err:     err,
	}
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	// Retryable: network errors, timeouts, 5xx server errors, rate limiting
	if k8serrors.IsTimeout(err) || k8serrors.IsServerTimeout(err) {
		return true
	}
	if k8serrors.IsInternalError(err) || k8serrors.IsServiceUnavailable(err) {
		return true
	}
	if k8serrors.IsTooManyRequests(err) {
		return true
	}

	// Not retryable: auth errors, not found, invalid input
	if k8serrors.IsUnauthorized(err) || k8serrors.IsForbidden(err) {
		return false
	}
	if k8serrors.IsNotFound(err) || k8serrors.IsInvalid(err) {
		return false
	}

	// Default: retry for unknown errors (conservative approach)
	return true
}
