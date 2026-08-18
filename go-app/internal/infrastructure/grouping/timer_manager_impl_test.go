package grouping

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestTimerManager creates a test timer manager
func setupTestTimerManager(t *testing.T) (*DefaultTimerManager, *InMemoryTimerStorage, *DefaultGroupManager) {
	storage := NewInMemoryTimerStorage(nil)

	// Create mock group manager (TN-125: use storage)
	groupManager := &DefaultGroupManager{
		storage:          NewMemoryGroupStorage(&MemoryGroupStorageConfig{}),
		fingerprintIndex: make(map[string]GroupKey),
		logger:           slog.Default(),
	}

	// Pre-populate test group in storage
	ctx := context.Background()
	testGroup := &AlertGroup{
		Key:    "test-group",
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	_ = groupManager.storage.Store(ctx, testGroup)

	config := TimerManagerConfig{
		Storage:               storage,
		GroupManager:          groupManager,
		DefaultGroupWait:      30 * time.Second,
		DefaultGroupInterval:  5 * time.Minute,
		DefaultRepeatInterval: 4 * time.Hour,
		Logger:                slog.Default(),
	}

	manager, err := NewDefaultTimerManager(config)
	require.NoError(t, err)

	return manager, storage, groupManager
}

// TestNewDefaultTimerManager tests manager construction
func TestNewDefaultTimerManager(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	groupManager := &DefaultGroupManager{
		storage:          NewMemoryGroupStorage(&MemoryGroupStorageConfig{}),
		fingerprintIndex: make(map[string]GroupKey),
		logger:           slog.Default(),
	}

	config := TimerManagerConfig{
		Storage:      storage,
		GroupManager: groupManager,
	}

	manager, err := NewDefaultTimerManager(config)
	require.NoError(t, err)
	assert.NotNil(t, manager)
	assert.Equal(t, 30*time.Second, manager.config.DefaultGroupWait)
	assert.Equal(t, 5*time.Minute, manager.config.DefaultGroupInterval)
	assert.Equal(t, 4*time.Hour, manager.config.DefaultRepeatInterval)
}

// TestNewDefaultTimerManager_MissingStorage tests validation
func TestNewDefaultTimerManager_MissingStorage(t *testing.T) {
	config := TimerManagerConfig{
		GroupManager: &DefaultGroupManager{},
	}

	_, err := NewDefaultTimerManager(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "storage is required")
}

// TestNewDefaultTimerManager_NilGroupManagerAllowed verifies construction
// succeeds without a GroupManager (Task 2.2, alertmanager-parity): breaking
// the GroupManager<->TimerManager construction cycle requires the TimerManager
// to be buildable before the GroupManager exists, with SetGroupManager
// injecting it afterwards.
func TestNewDefaultTimerManager_NilGroupManagerAllowed(t *testing.T) {
	config := TimerManagerConfig{
		Storage: NewInMemoryTimerStorage(nil),
	}

	manager, err := NewDefaultTimerManager(config)
	require.NoError(t, err)
	require.NotNil(t, manager)

	manager.groupManagerMu.RLock()
	gm := manager.groupManager
	manager.groupManagerMu.RUnlock()
	assert.Nil(t, gm)
}

// TestDefaultTimerManager_SetGroupManager verifies the setter injects the
// group manager and rejects nil.
func TestDefaultTimerManager_SetGroupManager(t *testing.T) {
	manager, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage: NewInMemoryTimerStorage(nil),
		Logger:  slog.Default(),
	})
	require.NoError(t, err)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	// Rejects nil.
	err = manager.SetGroupManager(nil)
	assert.Error(t, err)

	groupManager := &DefaultGroupManager{
		storage:          NewMemoryGroupStorage(&MemoryGroupStorageConfig{}),
		fingerprintIndex: make(map[string]GroupKey),
		logger:           slog.Default(),
	}
	require.NoError(t, manager.SetGroupManager(groupManager))

	manager.groupManagerMu.RLock()
	gm := manager.groupManager
	manager.groupManagerMu.RUnlock()
	assert.Same(t, groupManager, gm)
}

// TestDefaultTimerManager_OnTimerExpired_NilGroupManager verifies that a
// timer firing before SetGroupManager is called logs and skips callback
// dispatch instead of panicking (Task 2.2).
func TestDefaultTimerManager_OnTimerExpired_NilGroupManager(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	manager, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage: storage,
		Logger:  slog.Default(),
	})
	require.NoError(t, err)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	var callbackInvoked atomic.Bool
	manager.OnTimerExpired(func(_ context.Context, _ GroupKey, _ TimerType, _ *AlertGroup) error {
		callbackInvoked.Store(true)
		return nil
	})

	ctx := context.Background()
	_, err = manager.StartTimer(ctx, "test-group", GroupWaitTimer, 20*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, getErr := manager.GetTimer(ctx, "test-group")
		return getErr != nil
	}, time.Second, 10*time.Millisecond, "timer should be removed from active state after expiring")

	assert.False(t, callbackInvoked.Load(), "callback must not run without a group manager")
}

// TestDefaultTimerManager_StartTimer tests starting timers
func TestDefaultTimerManager_StartTimer(t *testing.T) {
	manager, storage, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	timer, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 30*time.Second)
	require.NoError(t, err)
	assert.NotNil(t, timer)
	assert.Equal(t, GroupKey("test-group"), timer.GroupKey)
	assert.Equal(t, GroupWaitTimer, timer.TimerType)
	assert.Equal(t, 30*time.Second, timer.Duration)
	assert.Equal(t, TimerStateActive, timer.State)

	// Verify timer saved to storage
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, timer.GroupKey, loaded.GroupKey)
}

// TestDefaultTimerManager_StartTimer_InvalidType tests validation
func TestDefaultTimerManager_StartTimer_InvalidType(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	_, err := manager.StartTimer(ctx, "test-group", TimerType("invalid"), 30*time.Second)
	assert.Error(t, err)
	assert.IsType(t, &InvalidTimerTypeError{}, err)
}

// TestDefaultTimerManager_StartTimer_ZeroDuration tests validation
func TestDefaultTimerManager_StartTimer_ZeroDuration(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	_, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 0)
	assert.Error(t, err)
	assert.IsType(t, &InvalidDurationError{}, err)
}

// TestDefaultTimerManager_StartTimer_EmptyGroupKey tests validation
func TestDefaultTimerManager_StartTimer_EmptyGroupKey(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	_, err := manager.StartTimer(ctx, "", GroupWaitTimer, 30*time.Second)
	assert.Error(t, err)
}

// TestDefaultTimerManager_StartTimer_ReplacesExisting tests timer replacement
func TestDefaultTimerManager_StartTimer_ReplacesExisting(t *testing.T) {
	manager, storage, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Start first timer
	timer1, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 30*time.Second)
	require.NoError(t, err)

	// Start second timer (should replace first)
	timer2, err := manager.StartTimer(ctx, "test-group", GroupIntervalTimer, 5*time.Minute)
	require.NoError(t, err)

	// Verify second timer replaced first
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, GroupIntervalTimer, loaded.TimerType)
	assert.Equal(t, 5*time.Minute, loaded.Duration)
	assert.NotEqual(t, timer1.TimerType, timer2.TimerType)
}

// TestDefaultTimerManager_CancelTimer tests cancelling timers
func TestDefaultTimerManager_CancelTimer(t *testing.T) {
	manager, storage, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Start timer
	_, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 30*time.Second)
	require.NoError(t, err)

	// Cancel timer
	cancelled, err := manager.CancelTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.True(t, cancelled)

	// Verify timer deleted from storage
	_, err = storage.LoadTimer(ctx, "test-group")
	assert.ErrorIs(t, err, ErrTimerNotFound)
}

// TestDefaultTimerManager_CancelTimer_NotFound tests cancelling non-existent timer
func TestDefaultTimerManager_CancelTimer_NotFound(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	cancelled, err := manager.CancelTimer(ctx, "non-existent")
	require.NoError(t, err)
	assert.False(t, cancelled)
}

// TestDefaultTimerManager_ResetTimer tests resetting timers
func TestDefaultTimerManager_ResetTimer(t *testing.T) {
	manager, storage, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Start timer
	_, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 30*time.Second)
	require.NoError(t, err)

	// Reset timer
	timer, err := manager.ResetTimer(ctx, "test-group", GroupIntervalTimer, 5*time.Minute)
	require.NoError(t, err)
	assert.NotNil(t, timer)
	assert.Equal(t, GroupIntervalTimer, timer.TimerType)
	assert.Equal(t, 5*time.Minute, timer.Duration)

	// Verify reset count incremented
	require.NotNil(t, timer.Metadata)
	assert.Equal(t, 1, timer.Metadata.ResetCount)

	// Verify in storage
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, GroupIntervalTimer, loaded.TimerType)
}

// TestDefaultTimerManager_ResetTimer_NotFound tests resetting non-existent timer
func TestDefaultTimerManager_ResetTimer_NotFound(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	_, err := manager.ResetTimer(ctx, "non-existent", GroupWaitTimer, 30*time.Second)
	assert.Error(t, err)
	assert.IsType(t, &TimerNotFoundError{}, err)
}

// TestDefaultTimerManager_GetTimer tests retrieving timers
func TestDefaultTimerManager_GetTimer(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Start timer
	started, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 30*time.Second)
	require.NoError(t, err)

	// Get timer
	timer, err := manager.GetTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, started.GroupKey, timer.GroupKey)
	assert.Equal(t, started.TimerType, timer.TimerType)
}

// TestDefaultTimerManager_GetTimer_NotFound tests error handling
func TestDefaultTimerManager_GetTimer_NotFound(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	_, err := manager.GetTimer(ctx, "non-existent")
	assert.ErrorIs(t, err, ErrTimerNotFound)
}

// TestDefaultTimerManager_ListActiveTimers tests listing timers
func TestDefaultTimerManager_ListActiveTimers(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Start multiple timers
	_, _ = manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	_, _ = manager.StartTimer(ctx, "group-2", GroupIntervalTimer, 5*time.Minute)
	_, _ = manager.StartTimer(ctx, "group-3", RepeatIntervalTimer, 4*time.Hour)

	// List all timers
	timers, err := manager.ListActiveTimers(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, timers, 3)
}

// TestDefaultTimerManager_ListActiveTimers_WithFilters tests filtering
func TestDefaultTimerManager_ListActiveTimers_WithFilters(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Start timers
	_, _ = manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	_, _ = manager.StartTimer(ctx, "group-2", GroupWaitTimer, 30*time.Second)
	_, _ = manager.StartTimer(ctx, "group-3", GroupIntervalTimer, 5*time.Minute)

	// Filter by type
	filters := &TimerFilters{
		TimerType: ptrTimerType(GroupWaitTimer),
	}

	timers, err := manager.ListActiveTimers(ctx, filters)
	require.NoError(t, err)
	assert.Len(t, timers, 2)
	for _, timer := range timers {
		assert.Equal(t, GroupWaitTimer, timer.TimerType)
	}
}

// TestDefaultTimerManager_OnTimerExpired tests callback registration
func TestDefaultTimerManager_OnTimerExpired(t *testing.T) {
	manager, _, groupManager := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Add test group to storage (TN-125)
	testGroup := &AlertGroup{
		Key:    "test-group",
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	_ = groupManager.storage.Store(ctx, testGroup)

	// Register callback
	callbackCalled := atomic.Bool{}
	manager.OnTimerExpired(func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
		callbackCalled.Store(true)
		assert.Equal(t, GroupKey("test-group"), groupKey)
		assert.Equal(t, GroupWaitTimer, timerType)
		return nil
	})

	// Start timer with very short duration
	_, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 50*time.Millisecond)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(200 * time.Millisecond)

	// Verify callback was called
	assert.True(t, callbackCalled.Load())
}

// TestDefaultTimerManager_OnTimerExpired_MultipleCallbacks tests multiple callbacks
func TestDefaultTimerManager_OnTimerExpired_MultipleCallbacks(t *testing.T) {
	manager, _, groupManager := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Add test group to storage (TN-125)
	testGroup := &AlertGroup{
		Key:    "test-group",
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	_ = groupManager.storage.Store(ctx, testGroup)

	// Register multiple callbacks
	called1 := atomic.Bool{}
	called2 := atomic.Bool{}

	manager.OnTimerExpired(func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
		called1.Store(true)
		return nil
	})

	manager.OnTimerExpired(func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
		called2.Store(true)
		return nil
	})

	// Start and wait for expiration
	_, _ = manager.StartTimer(ctx, "test-group", GroupWaitTimer, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// Verify both callbacks called
	assert.True(t, called1.Load())
	assert.True(t, called2.Load())
}

// TestDefaultTimerManager_OnTimerExpired_CallbackPanicIsRecovered covers
// task 2.4 fix round 1, Finding 3: a panicking callback (e.g. a bug in the
// notify-stage chain, or in a caller-supplied InhibitionChecker/
// SilenceChecker) must not crash the process. If invokeCallbackSafely's
// recover() were missing, this whole test binary would crash instead of
// reporting a test failure — reaching the assertions below at all is part
// of the proof. A second, non-panicking callback registered after the
// panicking one must still run (panic isolation is per-callback, not
// per-timer-expiration).
func TestDefaultTimerManager_OnTimerExpired_CallbackPanicIsRecovered(t *testing.T) {
	manager, _, groupManager := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	testGroup := &AlertGroup{
		Key:    "test-group",
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	_ = groupManager.storage.Store(ctx, testGroup)

	panicked := atomic.Bool{}
	afterPanicCalled := atomic.Bool{}

	manager.OnTimerExpired(func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
		panicked.Store(true)
		panic("simulated callback panic (task 2.4 fix round 1, Finding 3)")
	})
	manager.OnTimerExpired(func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
		afterPanicCalled.Store(true)
		return nil
	})

	_, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 50*time.Millisecond)
	require.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	assert.True(t, panicked.Load(), "the panicking callback must have run")
	assert.True(t, afterPanicCalled.Load(), "a later callback must still run after an earlier one panics")
}

// TestDefaultTimerManager_RestoreTimers tests timer restoration
func TestDefaultTimerManager_RestoreTimers(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()
	now := time.Now()

	// Pre-populate storage with timers
	activeTimer := &GroupTimer{
		GroupKey:  "active-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second), // Future
		State:     TimerStateActive,
	}
	_ = storage.SaveTimer(ctx, activeTimer)

	expiredTimer := &GroupTimer{
		GroupKey:  "expired-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now.Add(-1 * time.Minute),
		ExpiresAt: now.Add(-30 * time.Second), // Past
		State:     TimerStateActive,
	}
	_ = storage.SaveTimer(ctx, expiredTimer)

	// Create manager and restore (TN-125: use storage)
	groupManager := &DefaultGroupManager{
		storage:          NewMemoryGroupStorage(&MemoryGroupStorageConfig{}),
		fingerprintIndex: make(map[string]GroupKey),
		logger:           slog.Default(),
	}

	// Add test groups to storage
	_ = groupManager.storage.Store(ctx, &AlertGroup{
		Key:    "active-group",
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	})
	_ = groupManager.storage.Store(ctx, &AlertGroup{
		Key:    "expired-group",
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	})

	config := TimerManagerConfig{
		Storage:      storage,
		GroupManager: groupManager,
		Logger:       slog.Default(),
	}

	manager, err := NewDefaultTimerManager(config)
	require.NoError(t, err)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	// Restore timers
	restored, missed, err := manager.RestoreTimers(ctx)
	require.NoError(t, err)

	// Verify counts
	assert.Equal(t, 1, restored) // active-group
	assert.Equal(t, 1, missed)   // expired-group
}

// TestDefaultTimerManager_GetStats tests statistics
func TestDefaultTimerManager_GetStats(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Start timers
	_, _ = manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	_, _ = manager.StartTimer(ctx, "group-2", GroupIntervalTimer, 5*time.Minute)

	// Get stats
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, 1, stats.ActiveTimers[GroupWaitTimer])
	assert.Equal(t, 1, stats.ActiveTimers[GroupIntervalTimer])
}

// TestDefaultTimerManager_Shutdown tests graceful shutdown
func TestDefaultTimerManager_Shutdown(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)

	ctx := context.Background()

	// Start some timers
	_, _ = manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	_, _ = manager.StartTimer(ctx, "group-2", GroupWaitTimer, 30*time.Second)

	// Shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := manager.Shutdown(shutdownCtx)
	assert.NoError(t, err)

	// Verify cannot start new timers after shutdown
	_, err = manager.StartTimer(ctx, "group-3", GroupWaitTimer, 30*time.Second)
	assert.ErrorIs(t, err, ErrManagerShutdown)
}

// TestDefaultTimerManager_ConcurrentOperations tests thread-safety
func TestDefaultTimerManager_ConcurrentOperations(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()
	var wg sync.WaitGroup

	// Concurrent starts
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			groupKey := GroupKey(string(rune('a' + id)))
			_, _ = manager.StartTimer(ctx, groupKey, GroupWaitTimer, 30*time.Second)
		}(i)
	}

	// Concurrent cancels
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
			groupKey := GroupKey(string(rune('a' + id)))
			_, _ = manager.CancelTimer(ctx, groupKey)
		}(i)
	}

	wg.Wait()

	// Verify no panics and manager still functional
	stats, err := manager.GetStats(ctx)
	require.NoError(t, err)
	assert.NotNil(t, stats)
}

// BenchmarkDefaultTimerManager_StartTimer benchmarks starting timers
func BenchmarkDefaultTimerManager_StartTimer(b *testing.B) {
	manager, _, _ := setupTestTimerManager(&testing.T{})
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groupKey := GroupKey(string(rune('a' + (i % 26))))
		_, _ = manager.StartTimer(ctx, groupKey, GroupWaitTimer, 30*time.Second)
	}
}

// BenchmarkDefaultTimerManager_CancelTimer benchmarks cancelling timers
func BenchmarkDefaultTimerManager_CancelTimer(b *testing.B) {
	manager, _, _ := setupTestTimerManager(&testing.T{})
	defer func() { _ = manager.Shutdown(context.Background()) }()

	ctx := context.Background()

	// Pre-populate timers
	for i := 0; i < b.N; i++ {
		groupKey := GroupKey(string(rune('a' + (i % 26))))
		_, _ = manager.StartTimer(ctx, groupKey, GroupWaitTimer, 30*time.Second)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groupKey := GroupKey(string(rune('a' + (i % 26))))
		_, _ = manager.CancelTimer(ctx, groupKey)
	}
}
