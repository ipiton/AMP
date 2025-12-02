package publishing

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestClassifyError_Transient tests transient error classification.
func TestClassifyError_Transient(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectedType  string
		expectedTrans bool
	}{
		{
			name:          "context deadline exceeded",
			err:           context.DeadlineExceeded,
			expectedType:  "timeout",
			expectedTrans: true,
		},
		{
			name:          "network timeout",
			err:           &net.DNSError{IsTimeout: true},
			expectedType:  "timeout",
			expectedTrans: true,
		},
		{
			name:          "dns error",
			err:           &net.DNSError{Name: "example.com"},
			expectedType:  "dns",
			expectedTrans: true,
		},
		{
			name:          "503 service unavailable",
			err:           errors.New("503 Service Unavailable"),
			expectedType:  "k8s_api",
			expectedTrans: true,
		},
		{
			name:          "connection refused",
			err:           errors.New("connection refused"),
			expectedType:  "network",
			expectedTrans: true,
		},
		{
			name:          "unknown error (default transient)",
			err:           errors.New("some unknown error"),
			expectedType:  "unknown",
			expectedTrans: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errorType, transient := classifyError(tt.err)

			assert.Equal(t, tt.expectedType, errorType, "Error type mismatch")
			assert.Equal(t, tt.expectedTrans, transient, "Transient flag mismatch")
		})
	}
}

// TestClassifyError_Permanent tests permanent error classification.
func TestClassifyError_Permanent(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		expectedType  string
		expectedTrans bool
	}{
		{
			name:          "context cancelled",
			err:           context.Canceled,
			expectedType:  "cancelled",
			expectedTrans: false,
		},
		{
			name:          "401 unauthorized",
			err:           errors.New("401 Unauthorized"),
			expectedType:  "auth",
			expectedTrans: false,
		},
		{
			name:          "403 forbidden",
			err:           errors.New("403 Forbidden"),
			expectedType:  "auth",
			expectedTrans: false,
		},
		{
			name:          "invalid token",
			err:           errors.New("authentication failed: invalid token"),
			expectedType:  "auth",
			expectedTrans: false,
		},
		{
			name:          "invalid json",
			err:           errors.New("invalid JSON in secret"),
			expectedType:  "parse",
			expectedTrans: false,
		},
		{
			name:          "illegal base64",
			err:           errors.New("illegal base64 data"),
			expectedType:  "parse",
			expectedTrans: false,
		},
		{
			name:          "unmarshal error",
			err:           errors.New("unmarshal failed"),
			expectedType:  "parse",
			expectedTrans: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errorType, transient := classifyError(tt.err)

			assert.Equal(t, tt.expectedType, errorType, "Error type mismatch")
			assert.Equal(t, tt.expectedTrans, transient, "Transient flag mismatch")
		})
	}
}

// TestRefreshError_Wrapping tests RefreshError error wrapping.
func TestRefreshError_Wrapping(t *testing.T) {
	originalErr := errors.New("k8s api unavailable")

	refreshErr := &RefreshError{
		Op:        "discover_targets",
		Err:       originalErr,
		Retries:   3,
		Duration:  5 * time.Second,
		Transient: true,
	}

	// Test Error() string
	errStr := refreshErr.Error()
	assert.Contains(t, errStr, "discover_targets")
	assert.Contains(t, errStr, "3 retries")
	assert.Contains(t, errStr, "transient")
	assert.Contains(t, errStr, "k8s api unavailable")

	// Test Unwrap()
	unwrapped := errors.Unwrap(refreshErr)
	assert.Equal(t, originalErr, unwrapped)

	// Test errors.Is()
	assert.True(t, errors.Is(refreshErr, originalErr))
}

// TestConfigError_Formatting tests ConfigError error formatting.
func TestConfigError_Formatting(t *testing.T) {
	configErr := &ConfigError{
		Field:  "Interval",
		Value:  -5 * time.Minute,
		Reason: "must be positive",
	}

	errStr := configErr.Error()
	assert.Contains(t, errStr, "Interval")
	assert.Contains(t, errStr, "-5m")
	assert.Contains(t, errStr, "must be positive")
}
