package grouping

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInMemoryTimerStorage_SaveTimer tests saving timers to memory
func TestInMemoryTimerStorage_SaveTimer(t *testing.T) {
	storage := NewInMemoryTimerStorage(slog.Default())
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

	err := storage.SaveTimer(ctx, timer)
	assert.NoError(t, err)

	// Verify timer was saved
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, timer.GroupKey, loaded.GroupKey)
	assert.Equal(t, timer.TimerType, loaded.TimerType)
}

// TestInMemoryTimerStorage_SaveTimer_NilTimer tests nil timer handling
func TestInMemoryTimerStorage_SaveTimer_NilTimer(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	err := storage.SaveTimer(ctx, nil)
	assert.Error(t, err)
}

// TestInMemoryTimerStorage_SaveTimer_InvalidTimer tests validation
func TestInMemoryTimerStorage_SaveTimer_InvalidTimer(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	timer := &GroupTimer{
		GroupKey:  "", // Invalid
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
	}

	err := storage.SaveTimer(ctx, timer)
	assert.Error(t, err)
}

// TestInMemoryTimerStorage_LoadTimer tests loading timers
func TestInMemoryTimerStorage_LoadTimer(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
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

	err := storage.SaveTimer(ctx, timer)
	require.NoError(t, err)

	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.NotNil(t, loaded)
	assert.Equal(t, timer.GroupKey, loaded.GroupKey)
}

// TestInMemoryTimerStorage_LoadTimer_NotFound tests error for non-existent timer
func TestInMemoryTimerStorage_LoadTimer_NotFound(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	_, err := storage.LoadTimer(ctx, "non-existent")
	assert.ErrorIs(t, err, ErrTimerNotFound)
}

// TestInMemoryTimerStorage_DeleteTimer tests deleting timers
func TestInMemoryTimerStorage_DeleteTimer(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
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

	err := storage.SaveTimer(ctx, timer)
	require.NoError(t, err)

	err = storage.DeleteTimer(ctx, "test-group")
	assert.NoError(t, err)

	_, err = storage.LoadTimer(ctx, "test-group")
	assert.ErrorIs(t, err, ErrTimerNotFound)
}

// TestInMemoryTimerStorage_DeleteTimer_NotFound tests deletion of non-existent timer
func TestInMemoryTimerStorage_DeleteTimer_NotFound(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	err := storage.DeleteTimer(ctx, "non-existent")
	assert.NoError(t, err) // Should not error
}

// TestInMemoryTimerStorage_ListTimers tests listing all timers
func TestInMemoryTimerStorage_ListTimers(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()
	now := time.Now()

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
	}

	for _, timer := range timers {
		err := storage.SaveTimer(ctx, timer)
		require.NoError(t, err)
	}

	loaded, err := storage.ListTimers(ctx)
	require.NoError(t, err)
	assert.Len(t, loaded, 2)
}

// TestInMemoryTimerStorage_ListTimers_Empty tests listing when no timers
func TestInMemoryTimerStorage_ListTimers_Empty(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	timers, err := storage.ListTimers(ctx)
	require.NoError(t, err)
	assert.Empty(t, timers)
}

// TestInMemoryTimerStorage_AcquireLock tests lock acquisition
func TestInMemoryTimerStorage_AcquireLock(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	lockID, release, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	require.NoError(t, err)
	assert.NotEmpty(t, lockID)
	assert.NotNil(t, release)

	err = release()
	assert.NoError(t, err)
}

// TestInMemoryTimerStorage_AcquireLock_Conflict tests lock conflict
func TestInMemoryTimerStorage_AcquireLock_Conflict(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	lockID1, release1, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	require.NoError(t, err)
	defer release1()

	_, _, err = storage.AcquireLock(ctx, "test-group", 30*time.Second)
	assert.ErrorIs(t, err, ErrLockAlreadyAcquired)
	assert.NotEmpty(t, lockID1)
}

// TestInMemoryTimerStorage_AcquireLock_Release tests lock release
func TestInMemoryTimerStorage_AcquireLock_Release(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	_, release, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	require.NoError(t, err)

	err = release()
	assert.NoError(t, err)

	// Should be able to acquire again after release
	_, release2, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	assert.NoError(t, err)
	defer release2()
}

// TestInMemoryTimerStorage_AcquireLock_Expired tests expired lock replacement
func TestInMemoryTimerStorage_AcquireLock_Expired(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	// Acquire lock with very short TTL
	_, _, err := storage.AcquireLock(ctx, "test-group", 1*time.Millisecond)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Should be able to acquire after expiration
	_, release, err := storage.AcquireLock(ctx, "test-group", 30*time.Second)
	assert.NoError(t, err)
	defer release()
}

// TestInMemoryTimerStorage_CleanupExpiredLocks tests lock cleanup
func TestInMemoryTimerStorage_CleanupExpiredLocks(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	// Acquire 3 locks with short TTL
	_, _, _ = storage.AcquireLock(ctx, "group-1", 1*time.Millisecond)
	_, _, _ = storage.AcquireLock(ctx, "group-2", 1*time.Millisecond)
	_, release3, _ := storage.AcquireLock(ctx, "group-3", 30*time.Second)
	defer release3()

	// Wait for expiration of first 2
	time.Sleep(10 * time.Millisecond)

	// Cleanup
	cleaned := storage.CleanupExpiredLocks()
	assert.Equal(t, 2, cleaned)

	// Verify stats
	stats := storage.Stats()
	assert.Equal(t, 1, stats["locks"]) // Only group-3 remains
}

// TestInMemoryTimerStorage_Stats tests statistics
func TestInMemoryTimerStorage_Stats(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()
	now := time.Now()

	// Add timers
	for i := 0; i < 5; i++ {
		timer := &GroupTimer{
			GroupKey:  GroupKey(string(rune('a' + i))),
			TimerType: GroupWaitTimer,
			Duration:  30 * time.Second,
			StartedAt: now,
			ExpiresAt: now.Add(30 * time.Second),
			State:     TimerStateActive,
		}
		storage.SaveTimer(ctx, timer)
	}

	// Add locks
	storage.AcquireLock(ctx, "lock-1", 30*time.Second)
	storage.AcquireLock(ctx, "lock-2", 30*time.Second)

	stats := storage.Stats()
	assert.Equal(t, 5, stats["timers"])
	assert.Equal(t, 2, stats["locks"])
}

// TestInMemoryTimerStorage_ThreadSafety tests concurrent access
func TestInMemoryTimerStorage_ThreadSafety(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()
	now := time.Now()

	// Concurrent saves
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			timer := &GroupTimer{
				GroupKey:  GroupKey(string(rune('a' + id))),
				TimerType: GroupWaitTimer,
				Duration:  30 * time.Second,
				StartedAt: now,
				ExpiresAt: now.Add(30 * time.Second),
				State:     TimerStateActive,
			}
			storage.SaveTimer(ctx, timer)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all timers saved
	timers, err := storage.ListTimers(ctx)
	require.NoError(t, err)
	assert.Len(t, timers, 10)
}

// TestInMemoryTimerStorage_Clone_Independence tests clone independence
func TestInMemoryTimerStorage_Clone_Independence(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
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
			Version: 1,
		},
	}

	err := storage.SaveTimer(ctx, timer)
	require.NoError(t, err)

	// Load timer
	loaded, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)

	// Mutate loaded timer
	loaded.Metadata.Version = 999

	// Load again - should not be affected
	loaded2, err := storage.LoadTimer(ctx, "test-group")
	require.NoError(t, err)
	assert.Equal(t, int64(1), loaded2.Metadata.Version)
}

// BenchmarkInMemoryTimerStorage_SaveTimer benchmarks saving
func BenchmarkInMemoryTimerStorage_SaveTimer(b *testing.B) {
	storage := NewInMemoryTimerStorage(nil)
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
		timer.GroupKey = GroupKey(string(rune('a' + (i % 26))))
		_ = storage.SaveTimer(ctx, timer)
	}
}

// BenchmarkInMemoryTimerStorage_LoadTimer benchmarks loading
func BenchmarkInMemoryTimerStorage_LoadTimer(b *testing.B) {
	storage := NewInMemoryTimerStorage(nil)
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
	_ = storage.SaveTimer(ctx, timer)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = storage.LoadTimer(ctx, "bench-group")
	}
}

// BenchmarkInMemoryTimerStorage_AcquireLock benchmarks lock acquisition
func BenchmarkInMemoryTimerStorage_AcquireLock(b *testing.B) {
	storage := NewInMemoryTimerStorage(nil)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groupKey := GroupKey(string(rune('a' + (i % 26))))
		lockID, release, err := storage.AcquireLock(ctx, groupKey, 30*time.Second)
		if err == nil {
			release()
		}
		_ = lockID
	}
}
