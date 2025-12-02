package grouping

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// setupTestRedisStorage creates a test Redis storage with miniredis
func setupTestRedisStorage(t *testing.T) (*RedisTimerStorage, *miniredis.Miniredis, func()) {
	// Start miniredis server
	mr, err := miniredis.Run()
	require.NoError(t, err)

	// Create Redis cache
	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		Password:    "",
		DB:          0,
		PoolSize:    5,
		DialTimeout: 1 * time.Second,
		ReadTimeout: 1 * time.Second,
	}, slog.Default())
	require.NoError(t, err)

	// Create storage
	storage, err := NewRedisTimerStorage(redisCache, slog.Default())
	require.NoError(t, err)

	cleanup := func() {
		redisCache.Close()
		mr.Close()
	}

	return storage, mr, cleanup
}

// TestRedisTimerStorage_SaveTimer tests saving timers to Redis
func TestRedisTimerStorage_SaveTimer(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	timer := &GroupTimer{
		GroupKey:  "test-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		State:     TimerStateActive,
		Metadata: &TimerMetadata{
			Version:   1,
			CreatedBy: "test-instance",
		},
	}

	// Save timer
	err := storage.SaveTimer(ctx, timer)
	assert.NoError(t, err)

	// Verify timer was saved
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, timer.GroupKey, loaded.GroupKey)
	assert.Equal(t, timer.TimerType, loaded.TimerType)
	assert.Equal(t, timer.Duration, loaded.Duration)
}

// TestRedisTimerStorage_SaveTimer_NilTimer tests error handling for nil timer
func TestRedisTimerStorage_SaveTimer_NilTimer(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	err := storage.SaveTimer(ctx, nil)
	assert.Error(t, err)
}

// TestRedisTimerStorage_SaveTimer_InvalidTimer tests validation
func TestRedisTimerStorage_SaveTimer_InvalidTimer(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	timer := &GroupTimer{
		GroupKey:  "", // Invalid: empty group key
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
	}

	err := storage.SaveTimer(ctx, timer)
	assert.Error(t, err)
}

// TestRedisTimerStorage_LoadTimer tests loading timers from Redis
func TestRedisTimerStorage_LoadTimer(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	timer := &GroupTimer{
		GroupKey:  "test-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		State:     TimerStateActive,
	}

	// Save timer
	err := storage.SaveTimer(ctx, timer)
	require.NoError(t, err)

	// Load timer
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.NotNil(t, loaded)
	assert.Equal(t, timer.GroupKey, loaded.GroupKey)
}

// TestRedisTimerStorage_LoadTimer_NotFound tests error for non-existent timer
func TestRedisTimerStorage_LoadTimer_NotFound(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	_, err := storage.LoadTimer(ctx, "non-existent")
	assert.ErrorIs(t, err, ErrTimerNotFound)
}

// TestRedisTimerStorage_DeleteTimer tests deleting timers
func TestRedisTimerStorage_DeleteTimer(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	timer := &GroupTimer{
		GroupKey:  "test-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		State:     TimerStateActive,
	}

	// Save timer
	err := storage.SaveTimer(ctx, timer)
	require.NoError(t, err)

	// Delete timer
	err = storage.DeleteTimer(ctx, "test-group")
	assert.NoError(t, err)

	// Verify timer was deleted
	_, err = storage.LoadTimer(ctx, "test-group")
	assert.ErrorIs(t, err, ErrTimerNotFound)
}

// TestRedisTimerStorage_DeleteTimer_NotFound tests deletion of non-existent timer
func TestRedisTimerStorage_DeleteTimer_NotFound(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Delete non-existent timer (should not error)
	err := storage.DeleteTimer(ctx, "non-existent")
	assert.NoError(t, err)
}

// TestRedisTimerStorage_ListTimers tests listing all timers
func TestRedisTimerStorage_ListTimers(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Save multiple timers
	timers := []*GroupTimer{
		{
			GroupKey:  "group-1",
			TimerType: GroupWaitTimer,
			Duration:  30 * time.Second,
			StartedAt: now,
			ExpiresAt: now.Add(30 * time.Second),
			State:     TimerStateActive,
		},
		{
			GroupKey:  "group-2",
			TimerType: GroupIntervalTimer,
			Duration:  5 * time.Minute,
			StartedAt: now,
			ExpiresAt: now.Add(5 * time.Minute),
			State:     TimerStateActive,
		},
		{
			GroupKey:  "group-3",
			TimerType: RepeatIntervalTimer,
			Duration:  4 * time.Hour,
			StartedAt: now,
			ExpiresAt: now.Add(4 * time.Hour),
			State:     TimerStateActive,
		},
	}

	for _, timer := range timers {
		err := storage.SaveTimer(ctx, timer)
		require.NoError(t, err)
	}

	// List timers
	loaded, err := storage.ListTimers(ctx)
	require.NoError(t, err)
	assert.Len(t, loaded, 3)
}

// TestRedisTimerStorage_ListTimers_Empty tests listing when no timers exist
func TestRedisTimerStorage_ListTimers_Empty(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	timers, err := storage.ListTimers(ctx)
	require.NoError(t, err)
	assert.Empty(t, timers)
}

// TestRedisTimerStorage_AcquireLock tests distributed lock acquisition
func TestRedisTimerStorage_AcquireLock(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Acquire lock
	lockID, release, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	require.NoError(t, err)
	assert.NotEmpty(t, lockID)
	assert.NotNil(t, release)

	// Release lock
	err = release()
	assert.NoError(t, err)
}

// TestRedisTimerStorage_AcquireLock_Conflict tests lock conflict
func TestRedisTimerStorage_AcquireLock_Conflict(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	// First lock acquisition
	lockID1, release1, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	require.NoError(t, err)
	defer release1()

	// Second lock acquisition (should fail)
	_, _, err = storage.AcquireLock(ctx, "test-group", 30*time.Second)
	assert.ErrorIs(t, err, ErrLockAlreadyAcquired)
	assert.NotEmpty(t, lockID1)
}

// TestRedisTimerStorage_AcquireLock_Release tests lock release
func TestRedisTimerStorage_AcquireLock_Release(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Acquire lock
	_, release, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	require.NoError(t, err)

	// Release lock
	err = release()
	assert.NoError(t, err)

	// Should be able to acquire again after release
	_, release2, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	assert.NoError(t, err)
	defer release2()
}

// TestRedisTimerStorage_AcquireLock_AutoExpire tests lock auto-expiration
func TestRedisTimerStorage_AcquireLock_AutoExpire(t *testing.T) {
	storage, mr, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Acquire lock with short TTL
	_, _, err := storage.AcquireLock(ctx, "test-group", 1*time.Second)
	require.NoError(t, err)

	// Fast-forward time in miniredis
	mr.FastForward(2 * time.Second)

	// Should be able to acquire lock after expiration
	_, release, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	assert.NoError(t, err)
	defer release()
}

// TestRedisTimerStorage_SaveLoad_PreservesMetadata tests metadata preservation
func TestRedisTimerStorage_SaveLoad_PreservesMetadata(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()
	lastResetAt := now.Add(-10 * time.Minute)

	timer := &GroupTimer{
		GroupKey:  "test-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		State:     TimerStateActive,
		Metadata: &TimerMetadata{
			Version:     5,
			CreatedBy:   "instance-123",
			ResetCount:  3,
			LastResetAt: &lastResetAt,
			LockID:      "lock-456",
		},
	}

	// Save timer
	err := storage.SaveTimer(ctx, timer)
	require.NoError(t, err)

	// Load timer
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)

	// Verify metadata
	require.NotNil(t, loaded.Metadata)
	assert.Equal(t, int64(5), loaded.Metadata.Version)
	assert.Equal(t, "instance-123", loaded.Metadata.CreatedBy)
	assert.Equal(t, 3, loaded.Metadata.ResetCount)
	require.NotNil(t, loaded.Metadata.LastResetAt)
	assert.Equal(t, "lock-456", loaded.Metadata.LockID)
}

// TestRedisTimerStorage_MultipleTimers tests handling multiple timers
func TestRedisTimerStorage_MultipleTimers(t *testing.T) {
	storage, _, cleanup := setupTestRedisStorage(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Save 100 timers
	for i := 0; i < 100; i++ {
		timer := &GroupTimer{
			GroupKey:  GroupKey(fmt.Sprintf("group-%d", i)),
			TimerType: GroupWaitTimer,
			Duration:  30 * time.Second,
			StartedAt: now,
			ExpiresAt: now.Add(30 * time.Second),
			State:     TimerStateActive,
		}
		err := storage.SaveTimer(ctx, timer)
		require.NoError(t, err)
	}

	// List all timers
	timers, err := storage.ListTimers(ctx)
	require.NoError(t, err)
	assert.Len(t, timers, 100)
}

// BenchmarkRedisTimerStorage_SaveTimer benchmarks timer saving
func BenchmarkRedisTimerStorage_SaveTimer(b *testing.B) {
	storage, _, cleanup := setupTestRedisStorage(&testing.T{})
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	timer := &GroupTimer{
		GroupKey:  "bench-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		State:     TimerStateActive,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		timer.GroupKey = GroupKey(fmt.Sprintf("group-%d", i))
		_ = storage.SaveTimer(ctx, timer)
	}
}

// BenchmarkRedisTimerStorage_LoadTimer benchmarks timer loading
func BenchmarkRedisTimerStorage_LoadTimer(b *testing.B) {
	storage, _, cleanup := setupTestRedisStorage(&testing.T{})
	defer cleanup()

	ctx := context.Background()
	now := time.Now()

	// Pre-populate timer
	timer := &GroupTimer{
		GroupKey:  "bench-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		State:     TimerStateActive,
	}
	_ = storage.SaveTimer(ctx, timer)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = storage.LoadTimer(ctx, "bench-group")
	}
}
