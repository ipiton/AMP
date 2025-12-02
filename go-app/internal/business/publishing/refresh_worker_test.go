package publishing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackgroundWorker_WarmupPeriod tests warmup delay before first refresh.
func TestBackgroundWorker_WarmupPeriod(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	startTime := time.Now()
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait a bit less than warmup period (10ms in test config)
	time.Sleep(5 * time.Millisecond)

	// Should have zero calls (still in warmup)
	assert.Equal(t, 0, mock.GetDiscoverCallCount(), "Expected no calls during warmup")

	// Wait for warmup to complete + small buffer
	time.Sleep(10 * time.Millisecond)

	// Should have at least one call after warmup
	elapsed := time.Since(startTime)
	assert.Greater(t, mock.GetDiscoverCallCount(), 0, "Expected call after warmup")
	assert.GreaterOrEqual(t, elapsed, 10*time.Millisecond, "Expected warmup delay")
}

// TestBackgroundWorker_PeriodicRefresh tests periodic refresh at configured interval.
func TestBackgroundWorker_PeriodicRefresh(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 3,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)
	defer manager.Stop(1 * time.Second)

	// Wait for warmup + first refresh
	time.Sleep(30 * time.Millisecond)

	// Get call count after first refresh
	firstCount := mock.GetDiscoverCallCount()
	assert.Greater(t, firstCount, 0, "Expected at least one call after warmup")

	// Wait for refresh interval (100ms in test config) + buffer
	time.Sleep(150 * time.Millisecond)

	// Should have more calls (periodic refresh)
	secondCount := mock.GetDiscoverCallCount()
	assert.Greater(t, secondCount, firstCount, "Expected periodic refresh calls")

	// Should have roughly 1-2 additional calls (depending on timing)
	callsAdded := secondCount - firstCount
	assert.LessOrEqual(t, callsAdded, 3, "Expected 1-2 periodic refresh calls in 150ms")
}

// TestBackgroundWorker_GracefulShutdown tests context cancellation during refresh.
func TestBackgroundWorker_GracefulShutdown(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount:   5,
		shouldFail:    false,
		delayDuration: 50 * time.Millisecond, // Slow refresh
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	// Wait for warmup + refresh to start
	time.Sleep(30 * time.Millisecond)

	// Stop manager (should wait for current refresh to complete)
	stopStart := time.Now()
	err = manager.Stop(500 * time.Millisecond)
	stopDuration := time.Since(stopStart)

	// Should stop gracefully without error
	require.NoError(t, err)

	// Stop should wait for refresh to complete (~50ms delay)
	// but not exceed timeout
	assert.Less(t, stopDuration, 500*time.Millisecond, "Stop should not exceed timeout")

	// Verify no more calls after stop
	finalCount := mock.GetDiscoverCallCount()
	time.Sleep(150 * time.Millisecond) // Wait > refresh interval
	assert.Equal(t, finalCount, mock.GetDiscoverCallCount(), "Expected no calls after stop")
}

// TestBackgroundWorker_CancellationDuringWarmup tests stop during warmup.
func TestBackgroundWorker_CancellationDuringWarmup(t *testing.T) {
	mock := &MockTargetDiscoveryManager{
		targetCount: 5,
		shouldFail:  false,
	}

	manager, _ := createTestManager(t, mock)

	// Start manager
	err := manager.Start()
	require.NoError(t, err)

	// Stop immediately (during warmup, before first refresh)
	time.Sleep(2 * time.Millisecond) // Small delay to ensure Start() completed
	err = manager.Stop(100 * time.Millisecond)
	require.NoError(t, err)

	// Should have zero calls (stopped during warmup)
	assert.Equal(t, 0, mock.GetDiscoverCallCount(), "Expected no calls if stopped during warmup")
}
