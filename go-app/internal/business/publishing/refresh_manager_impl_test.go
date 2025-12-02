package publishing

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewRefreshManager_Success tests successful manager creation.
func TestNewRefreshManager_Success(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
	}

	manager, mockReg := createTestManager(t, mock)

	// Verify manager created
	assert.NotNil(t, manager)

	// Verify metrics registered (5 metrics)
	assertMetrics(t, mockReg, 5)

	// Verify initial state
	status := manager.GetStatus()
	assert.Equal(t, RefreshStateIdle, status.State)
	assert.True(t, status.LastRefresh.IsZero())
	assert.Equal(t, 0, status.ConsecutiveFailures)
}

// TestNewRefreshManager_NilDependencies tests validation of nil dependencies.
func TestNewRefreshManager_NilDependencies(t *testing.T) {
	config := createTestConfig()
	mockReg := &MockPrometheusRegisterer{}
	logger := slog.Default()

	tests := []struct {
		name          string
		discovery     TargetDiscoveryManager
		logger        *slog.Logger
		reg           prometheus.Registerer
		expectedError string
	}{
		{
			name:          "nil discovery manager",
			discovery:     nil,
			logger:        logger,
			reg:           mockReg,
			expectedError: "discovery manager is nil",
		},
		{
			name:          "nil logger",
			discovery:     &MockTargetDiscoveryManager{},
			logger:        nil,
			reg:           mockReg,
			expectedError: "logger is nil",
		},
		{
			name:          "nil metrics registry",
			discovery:     &MockTargetDiscoveryManager{},
			logger:        logger,
			reg:           nil,
			expectedError: "metrics registry is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Try to create manager with nil dependency
			_, err := NewRefreshManager(
				tt.discovery,
				config,
				tt.logger,
				tt.reg,
			)

			// Should return error
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

// TestNewRefreshManager_InvalidConfig tests config validation.
func TestNewRefreshManager_InvalidConfig(t *testing.T) {
	mock := &MockTargetDiscoveryManager{targetCount: 5}
	mockReg := &MockPrometheusRegisterer{}
	logger := slog.Default()

	tests := []struct {
		name          string
		modifyConfig  func(*RefreshConfig)
		expectedError string
	}{
		{
			name: "negative interval",
			modifyConfig: func(c *RefreshConfig) {
				c.Interval = -1 * time.Second
			},
			expectedError: "Interval",
		},
		{
			name: "negative max retries",
			modifyConfig: func(c *RefreshConfig) {
				c.MaxRetries = -1
			},
			expectedError: "MaxRetries",
		},
		{
			name: "zero base backoff",
			modifyConfig: func(c *RefreshConfig) {
				c.BaseBackoff = 0
			},
			expectedError: "BaseBackoff",
		},
		{
			name: "max backoff < base backoff",
			modifyConfig: func(c *RefreshConfig) {
				c.BaseBackoff = 10 * time.Second
				c.MaxBackoff = 5 * time.Second
			},
			expectedError: "MaxBackoff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := createTestConfig()
			tt.modifyConfig(&config)

			_, err := NewRefreshManager(mock, config, logger, mockReg)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

// TestStartStop_Success tests normal lifecycle (start → stop).
func TestStartStop_Success(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 10,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	// Wait for first refresh (warmup + first refresh)
	time.Sleep(50 * time.Millisecond)

	// Verify at least one discovery call
	assert.Greater(t, mock.GetDiscoverCallCount(), 0, "Expected at least one discovery call")

	// Check status
	status := manager.GetStatus()
	assert.NotEqual(t, RefreshStateIdle, status.State)

	// Stop manager
	err = manager.Stop(1 * time.Second)
	require.NoError(t, err)

	// Verify no more calls after stop
	callCount := mock.GetDiscoverCallCount()
	time.Sleep(150 * time.Millisecond) // Wait > refresh interval
	assert.Equal(t, callCount, mock.GetDiscoverCallCount(), "Expected no new calls after stop")
}

// TestStartStop_AlreadyStarted tests double start error.
func TestStartStop_AlreadyStarted(t *testing.T) {
	mock := &MockTargetDiscoveryManager{targetCount: 5}
	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	// Try to start again
	err = manager.Start()
	assert.ErrorIs(t, err, ErrAlreadyStarted)

	// Cleanup
	manager.Stop(1 * time.Second)
}

// TestStartStop_NotStarted tests stop before start.
func TestStartStop_NotStarted(t *testing.T) {
	mock := &MockTargetDiscoveryManager{targetCount: 5}
	manager, _ := createTestManager(t, mock)

	// Try to stop without starting
	err := manager.Stop(1 * time.Second)
	assert.ErrorIs(t, err, ErrNotStarted)
}

// TestRefreshNow_Success tests manual refresh trigger.
func TestRefreshNow_Success(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 8,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for warmup
	time.Sleep(20 * time.Millisecond)

	// Get initial call count
	initialCalls := mock.GetDiscoverCallCount()

	// Trigger manual refresh
	err = manager.RefreshNow()
	require.NoError(t, err)

	// Wait for refresh to complete
	waitForRefresh(t, manager, 2*time.Second)

	// Verify additional discovery call
	assert.Greater(t, mock.GetDiscoverCallCount(), initialCalls, "Expected manual refresh to trigger discovery")

	// Verify status
	assertRefreshStatus(t, manager, RefreshStateSuccess, 8, 0)
}

// TestRefreshNow_RateLimit tests rate limiting (max 1 refresh per minute).
func TestRefreshNow_RateLimit(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for warmup
	time.Sleep(20 * time.Millisecond)

	// First manual refresh (should succeed)
	err = manager.RefreshNow()
	require.NoError(t, err)

	// Wait for first refresh to complete
	waitForRefresh(t, manager, 2*time.Second)

	// Second manual refresh immediately (should be rate limited)
	err = manager.RefreshNow()
	assert.ErrorIs(t, err, ErrRateLimitExceeded)

	// Wait for rate limit window to pass
	time.Sleep(60 * time.Millisecond) // RateLimitPer = 50ms in test config

	// Third manual refresh (should succeed)
	err = manager.RefreshNow()
	require.NoError(t, err)
}

// TestRefreshNow_RefreshInProgress tests concurrent refresh prevention.
func TestRefreshNow_RefreshInProgress(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount:   5,
		shouldFail:    false,
		delayDuration: 200 * time.Millisecond, // Slow refresh
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for warmup
	time.Sleep(20 * time.Millisecond)

	// Trigger first manual refresh (will take 200ms)
	err = manager.RefreshNow()
	require.NoError(t, err)

	// Immediately try second refresh (should fail - refresh in progress)
	time.Sleep(10 * time.Millisecond) // Small delay to ensure first refresh started
	err = manager.RefreshNow()
	assert.ErrorIs(t, err, ErrRefreshInProgress)
}

// TestRefreshNow_NotStarted tests manual refresh before start.
func TestRefreshNow_NotStarted(t *testing.T) {
	mock := &MockTargetDiscoveryManager{targetCount: 5}
	manager, _ := createTestManager(t, mock)

	// Try manual refresh without starting
	err := manager.RefreshNow()
	assert.ErrorIs(t, err, ErrNotStarted)
}

// TestGetStatus_Accuracy tests status reporting accuracy.
func TestGetStatus_Accuracy(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 15,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Check initial status (idle)
	status := manager.GetStatus()
	assert.Equal(t, RefreshStateIdle, status.State)
	assert.True(t, status.LastRefresh.IsZero())
	assert.Equal(t, 0, status.TargetsValid)
	assert.Equal(t, 0, status.ConsecutiveFailures)

	// Start and wait for first refresh
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	time.Sleep(50 * time.Millisecond) // Wait for warmup + first refresh

	// Wait for refresh to complete
	waitForRefresh(t, manager, 2*time.Second)

	// Check status after successful refresh
	status = manager.GetStatus()
	assert.Equal(t, RefreshStateSuccess, status.State)
	assert.False(t, status.LastRefresh.IsZero())
	assert.Equal(t, 15, status.TargetsValid)
	assert.Equal(t, 0, status.TargetsInvalid)
	assert.Equal(t, 0, status.ConsecutiveFailures)
	assert.Empty(t, status.Error)
	assert.Greater(t, status.RefreshDuration, time.Duration(0))
}

// TestGetStatus_FailureTracking tests consecutive failure tracking.
func TestGetStatus_FailureTracking(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount:  10,
		shouldFail:   true,
		failureCount: 0, // Always fail
		errorToReturn: errors.New("mock k8s api unavailable"),
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for warmup + first refresh attempt (will fail)
	time.Sleep(100 * time.Millisecond)

	// Wait for refresh to complete (failure)
	waitForRefresh(t, manager, 2*time.Second)

	// Check status after failed refresh
	status := manager.GetStatus()
	assert.Equal(t, RefreshStateFailed, status.State)
	assert.NotEmpty(t, status.Error)
	assert.Equal(t, 1, status.ConsecutiveFailures)

	// Trigger another refresh (will fail again)
	time.Sleep(60 * time.Millisecond) // Wait for rate limit
	manager.RefreshNow()
	waitForRefresh(t, manager, 2*time.Second)

	// Check consecutive failures incremented
	status = manager.GetStatus()
	assert.Equal(t, RefreshStateFailed, status.State)
	assert.Greater(t, status.ConsecutiveFailures, 1)
}

// TestGetStatus_ThreadSafety tests concurrent GetStatus calls.
func TestGetStatus_ThreadSafety(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Concurrent GetStatus calls (should not panic or race)
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				status := manager.GetStatus()
				_ = status.State
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// If we reach here without panic, test passes
}
