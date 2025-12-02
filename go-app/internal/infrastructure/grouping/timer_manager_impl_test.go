package grouping

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
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
	groupManager.storage.Store(ctx, testGroup)

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

// TestNewDefaultTimerManager_MissingGroupManager tests validation
func TestNewDefaultTimerManager_MissingGroupManager(t *testing.T) {
	config := TimerManagerConfig{
		Storage: NewInMemoryTimerStorage(nil),
	}

	_, err := NewDefaultTimerManager(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "group manager is required")
}

// TestDefaultTimerManager_StartTimer tests starting timers
func TestDefaultTimerManager_StartTimer(t *testing.T) {
	manager, storage, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

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
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	_, err := manager.StartTimer(ctx, "test-group", TimerType("invalid"), 30*time.Second)
	assert.Error(t, err)
	assert.IsType(t, &InvalidTimerTypeError{}, err)
}

// TestDefaultTimerManager_StartTimer_ZeroDuration tests validation
func TestDefaultTimerManager_StartTimer_ZeroDuration(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	_, err := manager.StartTimer(ctx, "test-group", GroupWaitTimer, 0)
	assert.Error(t, err)
	assert.IsType(t, &InvalidDurationError{}, err)
}

// TestDefaultTimerManager_StartTimer_EmptyGroupKey tests validation
func TestDefaultTimerManager_StartTimer_EmptyGroupKey(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	_, err := manager.StartTimer(ctx, "", GroupWaitTimer, 30*time.Second)
	assert.Error(t, err)
}

// TestDefaultTimerManager_StartTimer_ReplacesExisting tests timer replacement
func TestDefaultTimerManager_StartTimer_ReplacesExisting(t *testing.T) {
	manager, storage, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

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
	defer manager.Shutdown(context.Background())

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
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	cancelled, err := manager.CancelTimer(ctx, "non-existent")
	require.NoError(t, err)
	assert.False(t, cancelled)
}

// TestDefaultTimerManager_ResetTimer tests resetting timers
func TestDefaultTimerManager_ResetTimer(t *testing.T) {
	manager, storage, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

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
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	_, err := manager.ResetTimer(ctx, "non-existent", GroupWaitTimer, 30*time.Second)
	assert.Error(t, err)
	assert.IsType(t, &TimerNotFoundError{}, err)
}

// TestDefaultTimerManager_GetTimer tests retrieving timers
func TestDefaultTimerManager_GetTimer(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

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
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	_, err := manager.GetTimer(ctx, "non-existent")
	assert.ErrorIs(t, err, ErrTimerNotFound)
}

// TestDefaultTimerManager_ListActiveTimers tests listing timers
func TestDefaultTimerManager_ListActiveTimers(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	// Start multiple timers
	manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	manager.StartTimer(ctx, "group-2", GroupIntervalTimer, 5*time.Minute)
	manager.StartTimer(ctx, "group-3", RepeatIntervalTimer, 4*time.Hour)

	// List all timers
	timers, err := manager.ListActiveTimers(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, timers, 3)
}

// TestDefaultTimerManager_ListActiveTimers_WithFilters tests filtering
func TestDefaultTimerManager_ListActiveTimers_WithFilters(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	// Start timers
	manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	manager.StartTimer(ctx, "group-2", GroupWaitTimer, 30*time.Second)
	manager.StartTimer(ctx, "group-3", GroupIntervalTimer, 5*time.Minute)

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
	defer manager.Shutdown(context.Background())

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
	groupManager.storage.Store(ctx, testGroup)

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
	defer manager.Shutdown(context.Background())

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
	groupManager.storage.Store(ctx, testGroup)

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
	manager.StartTimer(ctx, "test-group", GroupWaitTimer, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// Verify both callbacks called
	assert.True(t, called1.Load())
	assert.True(t, called2.Load())
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
	storage.SaveTimer(ctx, activeTimer)

	expiredTimer := &GroupTimer{
		GroupKey:  "expired-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now.Add(-1 * time.Minute),
		ExpiresAt: now.Add(-30 * time.Second), // Past
		State:     TimerStateActive,
	}
	storage.SaveTimer(ctx, expiredTimer)

	// Create manager and restore (TN-125: use storage)
	groupManager := &DefaultGroupManager{
		storage:          NewMemoryGroupStorage(&MemoryGroupStorageConfig{}),
		fingerprintIndex: make(map[string]GroupKey),
		logger:           slog.Default(),
	}

	// Add test groups to storage
	groupManager.storage.Store(ctx, &AlertGroup{
		Key:    "active-group",
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	})
	groupManager.storage.Store(ctx, &AlertGroup{
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
	defer manager.Shutdown(context.Background())

	// Restore timers
	restored, missed, err := manager.RestoreTimers(ctx)
	require.NoError(t, err)

	// Verify counts
	assert.Equal(t, 1, restored)  // active-group
	assert.Equal(t, 1, missed)    // expired-group
}

// TestDefaultTimerManager_GetStats tests statistics
func TestDefaultTimerManager_GetStats(t *testing.T) {
	manager, _, _ := setupTestTimerManager(t)
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	// Start timers
	manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	manager.StartTimer(ctx, "group-2", GroupIntervalTimer, 5*time.Minute)

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
	manager.StartTimer(ctx, "group-1", GroupWaitTimer, 30*time.Second)
	manager.StartTimer(ctx, "group-2", GroupWaitTimer, 30*time.Second)

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
	defer manager.Shutdown(context.Background())

	ctx := context.Background()
	var wg sync.WaitGroup

	// Concurrent starts
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			groupKey := GroupKey(string(rune('a' + id)))
			manager.StartTimer(ctx, groupKey, GroupWaitTimer, 30*time.Second)
		}(i)
	}

	// Concurrent cancels
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			time.Sleep(10 * time.Millisecond)
			groupKey := GroupKey(string(rune('a' + id)))
			manager.CancelTimer(ctx, groupKey)
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
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groupKey := GroupKey(string(rune('a' + (i % 26))))
		manager.StartTimer(ctx, groupKey, GroupWaitTimer, 30*time.Second)
	}
}

// BenchmarkDefaultTimerManager_CancelTimer benchmarks cancelling timers
func BenchmarkDefaultTimerManager_CancelTimer(b *testing.B) {
	manager, _, _ := setupTestTimerManager(&testing.T{})
	defer manager.Shutdown(context.Background())

	ctx := context.Background()

	// Pre-populate timers
	for i := 0; i < b.N; i++ {
		groupKey := GroupKey(string(rune('a' + (i % 26))))
		manager.StartTimer(ctx, groupKey, GroupWaitTimer, 30*time.Second)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groupKey := GroupKey(string(rune('a' + (i % 26))))
		manager.CancelTimer(ctx, groupKey)
	}
}
