package grouping

import (
	"fmt"
	"time"
)

// InvalidTimerTypeError is returned when an invalid timer type is provided.
type InvalidTimerTypeError struct {
	Type string
}

func (e *InvalidTimerTypeError) Error() string {
	return fmt.Sprintf("invalid timer type: %s (must be group_wait, group_interval, or repeat_interval)", e.Type)
}

// InvalidDurationError is returned when a timer duration is invalid.
//
// Timer durations must be positive. Zero or negative durations are rejected.
type InvalidDurationError struct {
	Duration time.Duration
}

func (e *InvalidDurationError) Error() string {
	return fmt.Sprintf("invalid timer duration: %v (must be positive)", e.Duration)
}

// TimerNotFoundError is returned when a requested timer does not exist.
type TimerNotFoundError struct {
	GroupKey GroupKey
}

func (e *TimerNotFoundError) Error() string {
	return fmt.Sprintf("timer not found for group: %s", e.GroupKey)
}

// ErrTimerNotFound is a sentinel error for timer not found cases.
// Use errors.Is(err, ErrTimerNotFound) for checking.
var ErrTimerNotFound = fmt.Errorf("timer not found")

// ManagerShutdownError is returned when operations are attempted on a shutdown manager.
type ManagerShutdownError struct{}

func (e *ManagerShutdownError) Error() string {
	return "timer manager is shutting down"
}

// ErrManagerShutdown is a sentinel error for shutdown state.
var ErrManagerShutdown = fmt.Errorf("timer manager is shutting down")

// LockAcquireError is returned when a distributed lock cannot be acquired.
//
// This typically happens in multi-instance deployments when another instance
// already holds the lock for processing a timer expiration.
type LockAcquireError struct {
	GroupKey GroupKey
	Reason   string
}

func (e *LockAcquireError) Error() string {
	return fmt.Sprintf("failed to acquire lock for group %s: %s", e.GroupKey, e.Reason)
}

// ErrLockAlreadyAcquired is a sentinel error for lock conflicts.
var ErrLockAlreadyAcquired = fmt.Errorf("lock already acquired by another process")

// TimerStorageError wraps storage-related errors (e.g., Redis failures).
type TimerStorageError struct {
	Operation string
	Err       error
}

func (e *TimerStorageError) Error() string {
	return fmt.Sprintf("timer storage error during %s: %v", e.Operation, e.Err)
}

func (e *TimerStorageError) Unwrap() error {
	return e.Err
}

// NewTimerStorageError creates a new TimerStorageError.
func NewTimerStorageError(operation string, err error) *TimerStorageError {
	return &TimerStorageError{
		Operation: operation,
		Err:       err,
	}
}
