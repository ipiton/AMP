package publishing

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Test Suite: Custom Errors

func TestErrTargetNotFound_Error(t *testing.T) {
	err := &ErrTargetNotFound{
		TargetName: "test-target",
	}

	msg := err.Error()
	assert.Contains(t, msg, "test-target")
	assert.Contains(t, msg, "not found")
}

func TestNewTargetNotFoundError(t *testing.T) {
	err := NewTargetNotFoundError("my-target")

	assert.NotNil(t, err)
	assert.Equal(t, "my-target", err.TargetName)
	assert.Contains(t, err.Error(), "my-target")
}

func TestErrDiscoveryFailed_Error(t *testing.T) {
	cause := fmt.Errorf("K8s API unavailable")
	err := &ErrDiscoveryFailed{
		Namespace: "production",
		Cause:     cause,
	}

	msg := err.Error()
	assert.Contains(t, msg, "production")
	assert.Contains(t, msg, "K8s API unavailable")
}

func TestErrDiscoveryFailed_Unwrap(t *testing.T) {
	cause := fmt.Errorf("connection refused")
	err := &ErrDiscoveryFailed{
		Namespace: "default",
		Cause:     cause,
	}

	unwrapped := err.Unwrap()
	assert.Equal(t, cause, unwrapped)

	// Test with errors.Is
	assert.True(t, errors.Is(err, cause))
}

func TestNewDiscoveryFailedError(t *testing.T) {
	cause := fmt.Errorf("timeout")
	err := NewDiscoveryFailedError("staging", cause)

	assert.NotNil(t, err)
	assert.Equal(t, "staging", err.Namespace)
	assert.Equal(t, cause, err.Cause)
	assert.Contains(t, err.Error(), "staging")
	assert.Contains(t, err.Error(), "timeout")
}

func TestErrInvalidSecretFormat_Error(t *testing.T) {
	err := &ErrInvalidSecretFormat{
		SecretName: "bad-secret",
		Reason:     "missing 'config' field",
	}

	msg := err.Error()
	assert.Contains(t, msg, "bad-secret")
	assert.Contains(t, msg, "missing 'config' field")
}

func TestNewInvalidSecretFormatError(t *testing.T) {
	err := NewInvalidSecretFormatError("test-secret", "invalid JSON")

	assert.NotNil(t, err)
	assert.Equal(t, "test-secret", err.SecretName)
	assert.Equal(t, "invalid JSON", err.Reason)
	assert.Contains(t, err.Error(), "test-secret")
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestValidationError_Error(t *testing.T) {
	err := ValidationError{
		Field:   "url",
		Message: "must be valid URL",
		Value:   "not-a-url",
	}

	msg := err.Error()
	assert.Contains(t, msg, "url")
	assert.Contains(t, msg, "must be valid URL")
	assert.Contains(t, msg, "not-a-url")
}

func TestNewValidationError(t *testing.T) {
	err := NewValidationError("name", "field is required", "")

	assert.Equal(t, "name", err.Field)
	assert.Equal(t, "field is required", err.Message)
	assert.Equal(t, "", err.Value)
}
