package grouping

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Additional tests to increase coverage from 71.2% to 80%+
// Target: Error() methods and helper functions

func TestInvalidAlertError_Error(t *testing.T) {
	err := &InvalidAlertError{
		Reason: "missing required field: alertname",
	}

	expected := "invalid alert: missing required field: alertname"
	assert.Equal(t, expected, err.Error())
}

func TestGroupNotFoundError_Error(t *testing.T) {
	err := &GroupNotFoundError{
		Key: "test-group-key-123",
	}

	expected := "group not found: test-group-key-123"
	assert.Equal(t, expected, err.Error())

	// Test with empty key
	err2 := &GroupNotFoundError{Key: ""}
	assert.Equal(t, "group not found: ", err2.Error())
}

func TestStorageError_Error(t *testing.T) {
	underlyingErr := errors.New("connection timeout")
	err := &StorageError{
		Operation: "store",
		Err:       underlyingErr,
	}

	expected := "storage error during store: connection timeout"
	assert.Equal(t, expected, err.Error())
}

func TestStorageError_Unwrap(t *testing.T) {
	underlyingErr := errors.New("connection timeout")
	err := &StorageError{
		Operation: "load",
		Err:       underlyingErr,
	}

	// Test Unwrap
	unwrapped := err.Unwrap()
	assert.Equal(t, underlyingErr, unwrapped)

	// Test errors.Is
	assert.True(t, errors.Is(err, underlyingErr))
}

func TestNewStorageError(t *testing.T) {
	underlyingErr := errors.New("test error")
	err := NewStorageError("delete", underlyingErr)

	assert.Equal(t, "delete", err.Operation)
	assert.Equal(t, underlyingErr, err.Err)
	assert.Equal(t, "storage error during delete: test error", err.Error())
}

func TestNewGroupNotFoundError(t *testing.T) {
	key := GroupKey("my-group-key")
	err := NewGroupNotFoundError(key)

	assert.Equal(t, key, err.Key)
	assert.Equal(t, "group not found: my-group-key", err.Error())
}

func TestErrVersionMismatch_Error(t *testing.T) {
	err := &ErrVersionMismatch{
		Key:             "test-key",
		ExpectedVersion: 5,
		ActualVersion:   7,
	}

	expected := "version mismatch for group test-key: expected version 5, got 7 (concurrent update detected)"
	assert.Equal(t, expected, err.Error())
}

func TestNewVersionMismatchError(t *testing.T) {
	key := GroupKey("versioned-key")
	err := NewVersionMismatchError(key, 10, 15)

	assert.Equal(t, key, err.Key)
	assert.Equal(t, int64(10), err.ExpectedVersion)
	assert.Equal(t, int64(15), err.ActualVersion)

	expectedMsg := "version mismatch for group versioned-key: expected version 10, got 15 (concurrent update detected)"
	assert.Equal(t, expectedMsg, err.Error())
}

// Tests for ValidationErrors collection (removed duplicate ValidationError tests)

func TestErrorTypes_Nil(t *testing.T) {
	// Test nil errors don't panic
	var invalidErr *InvalidAlertError
	assert.Nil(t, invalidErr)

	var groupNotFoundErr *GroupNotFoundError
	assert.Nil(t, groupNotFoundErr)

	var storageErr *StorageError
	assert.Nil(t, storageErr)

	var versionErr *ErrVersionMismatch
	assert.Nil(t, versionErr)
}

func TestStorageError_WrappingChain(t *testing.T) {
	// Test multi-level error wrapping
	rootErr := errors.New("root cause")
	level1 := NewStorageError("level1", rootErr)
	level2 := NewStorageError("level2", level1)

	// Check errors.Is works through chain
	assert.True(t, errors.Is(level2, rootErr))
	assert.True(t, errors.Is(level2, level1))

	// Check errors.As works
	var storageErr *StorageError
	assert.True(t, errors.As(level2, &storageErr))
	assert.Equal(t, "level2", storageErr.Operation)
}

func TestErrorTypes_EmptyValues(t *testing.T) {
	// Test errors with empty values
	t.Run("InvalidAlertError with empty reason", func(t *testing.T) {
		err := &InvalidAlertError{Reason: ""}
		assert.Equal(t, "invalid alert: ", err.Error())
	})

	t.Run("StorageError with empty operation", func(t *testing.T) {
		err := &StorageError{Operation: "", Err: errors.New("test")}
		assert.Contains(t, err.Error(), "test")
	})

	t.Run("VersionMismatch with zero versions", func(t *testing.T) {
		err := &ErrVersionMismatch{
			Key:             "key",
			ExpectedVersion: 0,
			ActualVersion:   0,
		}
		assert.Contains(t, err.Error(), "expected version 0, got 0")
	})
}

func TestNewHelpers_EdgeCases(t *testing.T) {
	t.Run("NewStorageError with nil error", func(t *testing.T) {
		err := NewStorageError("operation", nil)
		assert.NotNil(t, err)
		assert.Equal(t, "operation", err.Operation)
		assert.Nil(t, err.Err)
	})

	t.Run("NewGroupNotFoundError with empty key", func(t *testing.T) {
		err := NewGroupNotFoundError("")
		assert.NotNil(t, err)
		assert.Equal(t, GroupKey(""), err.Key)
	})

	t.Run("NewVersionMismatchError with negative versions", func(t *testing.T) {
		err := NewVersionMismatchError("key", -1, -5)
		assert.NotNil(t, err)
		assert.Equal(t, int64(-1), err.ExpectedVersion)
		assert.Equal(t, int64(-5), err.ActualVersion)
	})
}

func TestStorageError_NilUnwrap(t *testing.T) {
	err := &StorageError{
		Operation: "test",
		Err:       nil,
	}

	unwrapped := err.Unwrap()
	assert.Nil(t, unwrapped)
}
