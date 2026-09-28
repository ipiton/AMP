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

	const warmup = 10 * time.Millisecond // WarmupPeriod in createTestManager's config

	// Start manager
	startTime := time.Now()
	err := manager.Start()
	require.NoError(t, err)
	defer func() { _ = manager.Stop(1 * time.Second) }()

	// No call may happen inside the warmup window. Sleep into the middle of
	// it (so a worker with no warmup has had time to call), then read the
	// count BEFORE the clock: if less than warmup has passed since Start, any
	// call seen was made during warmup. Under load the sleep can overshoot
	// the window -- then the check proves nothing and is skipped rather than
	// failing on a call that was legitimately made after warmup.
	time.Sleep(warmup / 2)
	calls := mock.GetDiscoverCallCount()
	if time.Since(startTime) < warmup {
		assert.Equal(t, 0, calls, "Expected no calls during warmup")
	}

	// The first call must eventually happen. Polled instead of a fixed sleep:
	// on a loaded machine (-race, full-repo parallel run, CI runner) the
	// worker can be scheduled well after warmup ends.
	assert.Eventually(t, func() bool { return mock.GetDiscoverCallCount() > 0 },
		2*time.Second, time.Millisecond, "Expected call after warmup")
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
	defer func() { _ = manager.Stop(1 * time.Second) }()

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
