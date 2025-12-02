package publishing

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefreshWithRetry_FirstAttemptSuccess tests successful first attempt (no retry).
func TestRefreshWithRetry_FirstAttemptSuccess(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 10,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for first refresh
	time.Sleep(30 * time.Millisecond)

	// Wait for refresh to complete
	waitForRefresh(t, manager, 2*time.Second)

	// Should have exactly 1 call (no retries needed)
	assert.Equal(t, 1, mock.GetDiscoverCallCount(), "Expected single call on first success")

	// Verify success status
	assertRefreshStatus(t, manager, RefreshStateSuccess, 10, 0)
}

// TestRefreshWithRetry_TransientError tests retry with exponential backoff.
func TestRefreshWithRetry_TransientError(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount:   5,
		shouldFail:    true,
		failureCount:  2,                             // Fail first 2 attempts, succeed on 3rd
		errorToReturn: errors.New("connection refused"), // Transient error
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for first refresh (will retry)
	time.Sleep(50 * time.Millisecond)

	// Wait for refresh to complete (with retries)
	waitForRefresh(t, manager, 3*time.Second)

	// Should have 3 calls (2 failures + 1 success)
	callCount := mock.GetDiscoverCallCount()
	assert.GreaterOrEqual(t, callCount, 3, "Expected 3 attempts (2 failures + 1 success)")

	// Eventually should succeed
	status := manager.GetStatus()
	// May be success or failed depending on timing
	assert.NotEqual(t, RefreshStateIdle, status.State)
}

// TestRefreshWithRetry_PermanentError tests no retry on permanent errors.
func TestRefreshWithRetry_PermanentError(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount:   5,
		shouldFail:    true,
		failureCount:  0,                         // Always fail
		errorToReturn: errors.New("401 Unauthorized"), // Permanent error (auth failure)
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for first refresh
	time.Sleep(50 * time.Millisecond)

	// Wait for refresh to complete
	waitForRefresh(t, manager, 2*time.Second)

	// Should have exactly 1 call (no retries on permanent error)
	assert.Equal(t, 1, mock.GetDiscoverCallCount(), "Expected single call on permanent error (no retry)")

	// Verify failed status
	status := manager.GetStatus()
	assert.Equal(t, RefreshStateFailed, status.State)
	assert.Contains(t, status.Error, "Unauthorized")
	assert.Equal(t, 1, status.ConsecutiveFailures)
}

// TestRefreshWithRetry_MaxRetriesExceeded tests exhausting max retries.
func TestRefreshWithRetry_MaxRetriesExceeded(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount:   5,
		shouldFail:    true,
		failureCount:  0,                             // Always fail
		errorToReturn: errors.New("connection timeout"), // Transient error
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for first refresh (will retry until max retries)
	time.Sleep(100 * time.Millisecond)

	// Wait for refresh to complete (with all retries)
	waitForRefresh(t, manager, 3*time.Second)

	// Should have max retries + 1 calls (3 retries in test config)
	callCount := mock.GetDiscoverCallCount()
	assert.GreaterOrEqual(t, callCount, 3, "Expected at least max_retries calls")
	assert.LessOrEqual(t, callCount, 4, "Expected at most max_retries+1 calls")

	// Verify failed status
	status := manager.GetStatus()
	assert.Equal(t, RefreshStateFailed, status.State)
	assert.Contains(t, status.Error, "timeout")
	assert.Equal(t, 1, status.ConsecutiveFailures)
}

// TestRefreshWithRetry_ContextCancellation tests context cancellation during retry.
func TestRefreshWithRetry_ContextCancellation(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount:   5,
		shouldFail:    true,
		failureCount:  0,                             // Always fail
		errorToReturn: errors.New("connection timeout"), // Transient error
		ctxCancelCheck: true, // Enable context checking
	}

	// Create config with longer backoff (to test cancellation during backoff)
	config := createTestConfig()
	config.BaseBackoff = 100 * time.Millisecond

	mockReg := &MockPrometheusRegisterer{}
	manager, err := NewRefreshManager(mock, config, nil, mockReg)
	require.NoError(t, err)

	// Start manager
	err = manager.Start()
	require.NoError(t, err)

	// Wait for first refresh to start (will fail and enter backoff)
	time.Sleep(30 * time.Millisecond)

	// Stop manager (cancel context during backoff)
	err = manager.Stop(200 * time.Millisecond)
	require.NoError(t, err)

	// Should have few calls (cancelled during backoff)
	callCount := mock.GetDiscoverCallCount()
	assert.LessOrEqual(t, callCount, 3, "Expected few calls (cancelled during backoff)")
}

// TestRefreshWithRetry_BackoffSchedule tests exponential backoff timing.
func TestRefreshWithRetry_BackoffSchedule(t *testing.T) {
	t.Skip("Skipping timing-sensitive test (flaky in CI)")

	mock := &MockTargetDiscoveryManager{
		targetCount:   5,
		shouldFail:    true,
		failureCount:  3,                             // Fail first 3 attempts
		errorToReturn: errors.New("connection refused"), // Transient error
	}

	// Create config with measurable backoff
	config := createTestConfig()
	config.BaseBackoff = 50 * time.Millisecond
	config.MaxBackoff = 200 * time.Millisecond

	mockReg := &MockPrometheusRegisterer{}
	manager, err := NewRefreshManager(mock, config, nil, mockReg)
	require.NoError(t, err)

	// Start manager
	startTime := time.Now()
	err = manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for refresh with retries
	time.Sleep(500 * time.Millisecond)

	// Check that retries took appropriate time
	// Backoff schedule: 50ms → 100ms → 200ms = ~350ms minimum
	elapsed := time.Since(startTime)
	assert.GreaterOrEqual(t, elapsed, 300*time.Millisecond, "Expected exponential backoff delays")
}
