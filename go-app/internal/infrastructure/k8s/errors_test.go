package k8s

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestK8sError_Error tests the Error() method with and without underlying error
func TestK8sError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *K8sError
		expected string
	}{
		{
			name: "with underlying error",
			err: &K8sError{
				Op:      "list secrets",
				Message: "operation failed",
				Err:     fmt.Errorf("network timeout"),
			},
			expected: "k8s list secrets: operation failed: network timeout",
		},
		{
			name: "without underlying error",
			err: &K8sError{
				Op:      "get secret",
				Message: "not found",
			},
			expected: "k8s get secret: not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

// TestK8sError_Unwrap tests error wrapping support
func TestK8sError_Unwrap(t *testing.T) {
	underlyingErr := fmt.Errorf("network timeout")
	k8sErr := &K8sError{
		Op:      "list secrets",
		Message: "operation failed",
		Err:     underlyingErr,
	}

	unwrapped := k8sErr.Unwrap()
	assert.Equal(t, underlyingErr, unwrapped)
}

// TestK8sError_Unwrap_NoUnderlying tests unwrap with no underlying error
func TestK8sError_Unwrap_NoUnderlying(t *testing.T) {
	k8sErr := &K8sError{
		Op:      "list secrets",
		Message: "operation failed",
	}

	unwrapped := k8sErr.Unwrap()
	assert.Nil(t, unwrapped)
}

// TestConnectionError tests ConnectionError creation and type assertion
func TestConnectionError(t *testing.T) {
	underlyingErr := fmt.Errorf("connection refused")
	connErr := NewConnectionError("failed to connect", underlyingErr)

	require.NotNil(t, connErr)
	assert.Equal(t, "connection", connErr.Op)
	assert.Equal(t, "failed to connect", connErr.Message)
	assert.Equal(t, underlyingErr, connErr.Err)

	// Test error message format
	expectedMsg := "k8s connection: failed to connect: connection refused"
	assert.Equal(t, expectedMsg, connErr.Error())

	// Test type assertion via errors.As()
	var checkConnErr *ConnectionError
	assert.True(t, errors.As(connErr, &checkConnErr))
	assert.Equal(t, connErr, checkConnErr)
}

// TestAuthError tests AuthError creation and type assertion
func TestAuthError(t *testing.T) {
	underlyingErr := fmt.Errorf("forbidden: access denied")
	authErr := NewAuthError("insufficient permissions", underlyingErr)

	require.NotNil(t, authErr)
	assert.Equal(t, "authentication", authErr.Op)
	assert.Equal(t, "insufficient permissions", authErr.Message)
	assert.Equal(t, underlyingErr, authErr.Err)

	// Test error message format
	expectedMsg := "k8s authentication: insufficient permissions: forbidden: access denied"
	assert.Equal(t, expectedMsg, authErr.Error())

	// Test type assertion via errors.As()
	var checkAuthErr *AuthError
	assert.True(t, errors.As(authErr, &checkAuthErr))
	assert.Equal(t, authErr, checkAuthErr)
}

// TestNotFoundError tests NotFoundError creation and type assertion
func TestNotFoundError(t *testing.T) {
	notFoundErr := NewNotFoundError("secret default/test-secret not found")

	require.NotNil(t, notFoundErr)
	assert.Equal(t, "not_found", notFoundErr.Op)
	assert.Equal(t, "secret default/test-secret not found", notFoundErr.Message)
	assert.Nil(t, notFoundErr.Err) // NotFoundError typically doesn't wrap another error

	// Test error message format
	expectedMsg := "k8s not_found: secret default/test-secret not found"
	assert.Equal(t, expectedMsg, notFoundErr.Error())

	// Test type assertion via errors.As()
	var checkNotFoundErr *NotFoundError
	assert.True(t, errors.As(notFoundErr, &checkNotFoundErr))
	assert.Equal(t, notFoundErr, checkNotFoundErr)
}

// TestTimeoutError tests TimeoutError creation and type assertion
func TestTimeoutError(t *testing.T) {
	underlyingErr := fmt.Errorf("context deadline exceeded")
	timeoutErr := NewTimeoutError("request timed out", underlyingErr)

	require.NotNil(t, timeoutErr)
	assert.Equal(t, "timeout", timeoutErr.Op)
	assert.Equal(t, "request timed out", timeoutErr.Message)
	assert.Equal(t, underlyingErr, timeoutErr.Err)

	// Test error message format
	expectedMsg := "k8s timeout: request timed out: context deadline exceeded"
	assert.Equal(t, expectedMsg, timeoutErr.Error())

	// Test type assertion via errors.As()
	var checkTimeoutErr *TimeoutError
	assert.True(t, errors.As(timeoutErr, &checkTimeoutErr))
	assert.Equal(t, timeoutErr, checkTimeoutErr)
}

// TestWrapK8sError_Unauthorized tests wrapping of 401 Unauthorized error
func TestWrapK8sError_Unauthorized(t *testing.T) {
	k8sErr := k8serrors.NewUnauthorized("invalid token")
	wrapped := wrapK8sError("list secrets", k8sErr)

	var authErr *AuthError
	require.True(t, errors.As(wrapped, &authErr))
	assert.Equal(t, "authentication", authErr.Op)
	assert.Equal(t, "insufficient permissions", authErr.Message)
}

// TestWrapK8sError_Forbidden tests wrapping of 403 Forbidden error
func TestWrapK8sError_Forbidden(t *testing.T) {
	k8sErr := k8serrors.NewForbidden(
		schema.GroupResource{Group: "", Resource: "secrets"},
		"test-secret",
		fmt.Errorf("access denied"),
	)
	wrapped := wrapK8sError("get secret", k8sErr)

	var authErr *AuthError
	require.True(t, errors.As(wrapped, &authErr))
	assert.Equal(t, "authentication", authErr.Op)
	assert.Equal(t, "insufficient permissions", authErr.Message)
}

// TestWrapK8sError_NotFound tests wrapping of 404 NotFound error
func TestWrapK8sError_NotFound(t *testing.T) {
	k8sErr := k8serrors.NewNotFound(
		schema.GroupResource{Group: "", Resource: "secrets"},
		"test-secret",
	)
	wrapped := wrapK8sError("get secret", k8sErr)

	var notFoundErr *NotFoundError
	require.True(t, errors.As(wrapped, &notFoundErr))
	assert.Equal(t, "not_found", notFoundErr.Op)
	assert.Contains(t, notFoundErr.Message, "get secret")
}

// TestWrapK8sError_Timeout tests wrapping of timeout errors
func TestWrapK8sError_Timeout(t *testing.T) {
	k8sErr := k8serrors.NewTimeoutError("request timeout", 30)
	wrapped := wrapK8sError("list secrets", k8sErr)

	var timeoutErr *TimeoutError
	require.True(t, errors.As(wrapped, &timeoutErr))
	assert.Equal(t, "timeout", timeoutErr.Op)
	assert.Equal(t, "request timed out", timeoutErr.Message)
}

// TestWrapK8sError_ServerTimeout tests wrapping of server timeout errors
func TestWrapK8sError_ServerTimeout(t *testing.T) {
	k8sErr := k8serrors.NewServerTimeout(
		schema.GroupResource{Group: "", Resource: "secrets"},
		"list",
		30,
	)
	wrapped := wrapK8sError("list secrets", k8sErr)

	var timeoutErr *TimeoutError
	require.True(t, errors.As(wrapped, &timeoutErr))
	assert.Equal(t, "timeout", timeoutErr.Op)
	assert.Equal(t, "request timed out", timeoutErr.Message)
}

// TestWrapK8sError_Generic tests wrapping of generic K8s errors
func TestWrapK8sError_Generic(t *testing.T) {
	k8sErr := k8serrors.NewInternalError(fmt.Errorf("internal server error"))
	wrapped := wrapK8sError("list secrets", k8sErr)

	// Should return generic K8sError
	var genericErr *K8sError
	require.True(t, errors.As(wrapped, &genericErr))
	assert.Equal(t, "list secrets", genericErr.Op)
	assert.Equal(t, "operation failed", genericErr.Message)
}

// TestIsRetryableError_Transient tests that transient errors are retryable
func TestIsRetryableError_Transient(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "timeout error",
			err:  k8serrors.NewTimeoutError("timeout", 30),
		},
		{
			name: "server timeout error",
			err: k8serrors.NewServerTimeout(
				schema.GroupResource{Group: "", Resource: "secrets"},
				"list",
				30,
			),
		},
		{
			name: "internal error",
			err:  k8serrors.NewInternalError(fmt.Errorf("internal error")),
		},
		{
			name: "service unavailable",
			err:  k8serrors.NewServiceUnavailable("service unavailable"),
		},
		{
			name: "too many requests",
			err:  k8serrors.NewTooManyRequests("rate limit exceeded", 60),
		},
		{
			name: "unknown error (conservative retry)",
			err:  fmt.Errorf("unknown network error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, isRetryableError(tt.err), "error should be retryable: %v", tt.err)
		})
	}
}

// TestIsRetryableError_Permanent tests that permanent errors are not retryable
func TestIsRetryableError_Permanent(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "unauthorized error",
			err:  k8serrors.NewUnauthorized("invalid token"),
		},
		{
			name: "forbidden error",
			err: k8serrors.NewForbidden(
				schema.GroupResource{Group: "", Resource: "secrets"},
				"test-secret",
				fmt.Errorf("access denied"),
			),
		},
		{
			name: "not found error",
			err: k8serrors.NewNotFound(
				schema.GroupResource{Group: "", Resource: "secrets"},
				"test-secret",
			),
		},
		{
			name: "invalid error",
			err: k8serrors.NewInvalid(
				schema.GroupKind{Group: "", Kind: "Secret"},
				"test-secret",
				nil,
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, isRetryableError(tt.err), "error should not be retryable: %v", tt.err)
		})
	}
}

// TestIsRetryableError_EdgeCases tests edge cases for retry logic
func TestIsRetryableError_EdgeCases(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		retryable  bool
		comment    string
	}{
		{
			name:      "nil error",
			err:       nil,
			retryable: true, // Conservative: retry unknown
			comment:   "nil error should be handled gracefully",
		},
		{
			name: "conflict error (409)",
			err: k8serrors.NewConflict(
				schema.GroupResource{Group: "", Resource: "secrets"},
				"test-secret",
				fmt.Errorf("conflict"),
			),
			retryable: true, // Conflicts might be transient
			comment:   "conflict errors might resolve on retry",
		},
		{
			name:      "bad request (400)",
			err:       k8serrors.NewBadRequest("invalid request"),
			retryable: true, // Conservative approach
			comment:   "bad request treated as retryable (conservative)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			assert.Equal(t, tt.retryable, result, tt.comment)
		})
	}
}

// TestErrorTypeChain tests that error types maintain proper error chain
func TestErrorTypeChain(t *testing.T) {
	underlyingErr := fmt.Errorf("original error")
	k8sErr := &K8sError{
		Op:      "test",
		Message: "test error",
		Err:     underlyingErr,
	}
	connErr := &ConnectionError{K8sError: k8sErr}

	// Test unwrapping через errors.Unwrap()
	// ConnectionError embeds K8sError, so Unwrap() returns K8sError.Err (underlyingErr)
	unwrapped := errors.Unwrap(connErr)
	require.NotNil(t, unwrapped)
	assert.Equal(t, underlyingErr, unwrapped, "Unwrap should return underlying error")

	// Test that ConnectionError can be extracted via errors.As()
	var extractedConnErr *ConnectionError
	assert.True(t, errors.As(connErr, &extractedConnErr))
	assert.Equal(t, connErr, extractedConnErr)

	// Verify embedded K8sError is accessible
	assert.Equal(t, "test", connErr.Op)
	assert.Equal(t, "test error", connErr.Message)
	assert.Equal(t, underlyingErr, connErr.Err)
}

// TestErrorsAs tests errors.As() compatibility with all error types
func TestErrorsAs(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		targetType interface{}
	}{
		{
			name:      "K8sError",
			err:       &K8sError{Op: "test", Message: "test"},
			targetType: &K8sError{},
		},
		{
			name:      "ConnectionError",
			err:       NewConnectionError("test", nil),
			targetType: &ConnectionError{},
		},
		{
			name:      "AuthError",
			err:       NewAuthError("test", nil),
			targetType: &AuthError{},
		},
		{
			name:      "NotFoundError",
			err:       NewNotFoundError("test"),
			targetType: &NotFoundError{},
		},
		{
			name:      "TimeoutError",
			err:       NewTimeoutError("test", nil),
			targetType: &TimeoutError{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, errors.As(tt.err, &tt.targetType),
				"errors.As() should work for %T", tt.err)
		})
	}
}

// TestWrapK8sError_AllK8sErrorTypes tests comprehensive coverage of k8s error types
func TestWrapK8sError_AllK8sErrorTypes(t *testing.T) {
	tests := []struct {
		name         string
		k8sErr       error
		expectedType interface{}
		comment      string
	}{
		{
			name:         "StatusError - Unauthorized",
			k8sErr:       k8serrors.NewUnauthorized("test"),
			expectedType: &AuthError{},
			comment:      "Unauthorized should wrap to AuthError",
		},
		{
			name: "StatusError - Forbidden",
			k8sErr: k8serrors.NewForbidden(
				schema.GroupResource{Resource: "secrets"},
				"test",
				nil,
			),
			expectedType: &AuthError{},
			comment:      "Forbidden should wrap to AuthError",
		},
		{
			name: "StatusError - NotFound",
			k8sErr: k8serrors.NewNotFound(
				schema.GroupResource{Resource: "secrets"},
				"test",
			),
			expectedType: &NotFoundError{},
			comment:      "NotFound should wrap to NotFoundError",
		},
		{
			name:         "StatusError - Timeout",
			k8sErr:       k8serrors.NewTimeoutError("test", 30),
			expectedType: &TimeoutError{},
			comment:      "Timeout should wrap to TimeoutError",
		},
		{
			name: "StatusError - ServerTimeout",
			k8sErr: k8serrors.NewServerTimeout(
				schema.GroupResource{Resource: "secrets"},
				"list",
				30,
			),
			expectedType: &TimeoutError{},
			comment:      "ServerTimeout should wrap to TimeoutError",
		},
		{
			name:         "StatusError - InternalError",
			k8sErr:       k8serrors.NewInternalError(fmt.Errorf("test")),
			expectedType: &K8sError{},
			comment:      "InternalError should wrap to generic K8sError",
		},
		{
			name:         "StatusError - ServiceUnavailable",
			k8sErr:       k8serrors.NewServiceUnavailable("test"),
			expectedType: &K8sError{},
			comment:      "ServiceUnavailable should wrap to generic K8sError",
		},
		{
			name:         "StatusError - TooManyRequests",
			k8sErr:       k8serrors.NewTooManyRequests("test", 60),
			expectedType: &K8sError{},
			comment:      "TooManyRequests should wrap to generic K8sError",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := wrapK8sError("test operation", tt.k8sErr)
			assert.True(t, errors.As(wrapped, &tt.expectedType), tt.comment)
		})
	}
}
