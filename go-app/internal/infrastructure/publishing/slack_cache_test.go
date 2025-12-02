package publishing

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slack_cache_test.go - Comprehensive tests for MessageIDCache
// Coverage target: 90%+, 10+ tests

// TestCache_Store tests Store operation
func TestCache_Store(t *testing.T) {
	cache := NewMessageCache()

	entry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}

	// Store entry
	cache.Store("fp123", entry)

	// Verify stored
	retrieved, found := cache.Get("fp123")
	require.True(t, found)
	assert.Equal(t, entry.MessageTS, retrieved.MessageTS)
	assert.Equal(t, entry.ThreadTS, retrieved.ThreadTS)
}

// TestCache_Get tests Get operation
func TestCache_Get(t *testing.T) {
	cache := NewMessageCache()

	// Get non-existent key
	_, found := cache.Get("nonexistent")
	assert.False(t, found)

	// Store and get
	entry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}
	cache.Store("fp123", entry)

	retrieved, found := cache.Get("fp123")
	require.True(t, found)
	assert.Equal(t, entry.MessageTS, retrieved.MessageTS)
}

// TestCache_Delete tests Delete operation
func TestCache_Delete(t *testing.T) {
	cache := NewMessageCache()

	// Store entry
	entry := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}
	cache.Store("fp123", entry)

	// Verify stored
	_, found := cache.Get("fp123")
	require.True(t, found)

	// Delete
	cache.Delete("fp123")

	// Verify deleted
	_, found = cache.Get("fp123")
	assert.False(t, found)
}

// TestCache_Delete_Nonexistent tests Delete on non-existent key (no panic)
func TestCache_Delete_Nonexistent(t *testing.T) {
	cache := NewMessageCache()

	// Delete non-existent key (should not panic)
	cache.Delete("nonexistent")

	// Verify cache still works
	entry := &MessageEntry{MessageTS: "test"}
	cache.Store("fp123", entry)
	_, found := cache.Get("fp123")
	assert.True(t, found)
}

// TestCache_Size tests Size operation
func TestCache_Size(t *testing.T) {
	cache := NewMessageCache()

	// Empty cache
	assert.Equal(t, 0, cache.Size())

	// Add 3 entries
	for i := 0; i < 3; i++ {
		cache.Store(string(rune('a'+i)), &MessageEntry{
			MessageTS: "test",
			ThreadTS:  "test",
			CreatedAt: time.Now(),
		})
	}

	// Verify size
	assert.Equal(t, 3, cache.Size())

	// Delete 1 entry
	cache.Delete("a")

	// Verify size
	assert.Equal(t, 2, cache.Size())
}

// TestCache_Cleanup tests Cleanup with TTL
func TestCache_Cleanup(t *testing.T) {
	cache := NewMessageCache()

	now := time.Now()

	// Add entries with different ages
	cache.Store("old1", &MessageEntry{
		MessageTS: "old1",
		ThreadTS:  "old1",
		CreatedAt: now.Add(-25 * time.Hour), // Older than 24h
	})

	cache.Store("old2", &MessageEntry{
		MessageTS: "old2",
		ThreadTS:  "old2",
		CreatedAt: now.Add(-30 * time.Hour), // Older than 24h
	})

	cache.Store("recent", &MessageEntry{
		MessageTS: "recent",
		ThreadTS:  "recent",
		CreatedAt: now.Add(-1 * time.Hour), // Recent (< 24h)
	})

	// Verify all stored
	assert.Equal(t, 3, cache.Size())

	// Cleanup with 24h TTL
	deleted := cache.Cleanup(24 * time.Hour)

	// Verify 2 old entries deleted
	assert.Equal(t, 2, deleted)
	assert.Equal(t, 1, cache.Size())

	// Verify recent entry remains
	_, found := cache.Get("recent")
	assert.True(t, found)

	// Verify old entries gone
	_, found = cache.Get("old1")
	assert.False(t, found)
	_, found = cache.Get("old2")
	assert.False(t, found)
}

// TestCache_Cleanup_NoExpired tests Cleanup when no entries expired
func TestCache_Cleanup_NoExpired(t *testing.T) {
	cache := NewMessageCache()

	// Add recent entries
	cache.Store("recent1", &MessageEntry{
		MessageTS: "recent1",
		ThreadTS:  "recent1",
		CreatedAt: time.Now(),
	})

	cache.Store("recent2", &MessageEntry{
		MessageTS: "recent2",
		ThreadTS:  "recent2",
		CreatedAt: time.Now().Add(-1 * time.Hour),
	})

	// Cleanup with 24h TTL
	deleted := cache.Cleanup(24 * time.Hour)

	// Verify no entries deleted
	assert.Equal(t, 0, deleted)
	assert.Equal(t, 2, cache.Size())
}

// TestCache_ConcurrentAccess tests concurrent Store/Get/Delete operations
func TestCache_ConcurrentAccess(t *testing.T) {
	cache := NewMessageCache()

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Concurrent Store
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := string(rune('a'+id)) + string(rune('0'+j%10))
				cache.Store(key, &MessageEntry{
					MessageTS: key,
					ThreadTS:  key,
					CreatedAt: time.Now(),
				})
			}
		}(i)
	}

	wg.Wait()

	// Verify no race conditions (test passes = no panic)
	size := cache.Size()
	assert.True(t, size > 0)
	assert.True(t, size <= numGoroutines*numOperations)

	// Concurrent Get
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := string(rune('a'+id)) + string(rune('0'+j%10))
				_, _ = cache.Get(key)
			}
		}(i)
	}

	wg.Wait()

	// Concurrent Delete
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := string(rune('a'+id)) + string(rune('0'+j%10))
				cache.Delete(key)
			}
		}(i)
	}

	wg.Wait()

	// Verify cache still works after concurrent stress
	cache.Store("final", &MessageEntry{MessageTS: "final", ThreadTS: "final", CreatedAt: time.Now()})
	_, found := cache.Get("final")
	assert.True(t, found)
}

// TestCache_ConcurrentCleanup tests concurrent Cleanup operations
func TestCache_ConcurrentCleanup(t *testing.T) {
	cache := NewMessageCache()

	// Add entries with different ages
	for i := 0; i < 100; i++ {
		age := time.Duration(i%50) * time.Hour
		cache.Store(string(rune('a'+i%26))+string(rune('0'+i%10)), &MessageEntry{
			MessageTS: "test",
			ThreadTS:  "test",
			CreatedAt: time.Now().Add(-age),
		})
	}

	var wg sync.WaitGroup
	numGoroutines := 5

	// Concurrent Cleanup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			cache.Cleanup(24 * time.Hour)
		}()
	}

	wg.Wait()

	// Verify cache still works after concurrent cleanup
	cache.Store("final", &MessageEntry{MessageTS: "final", ThreadTS: "final", CreatedAt: time.Now()})
	_, found := cache.Get("final")
	assert.True(t, found)
}

// TestStartCleanupWorker tests background cleanup worker
func TestStartCleanupWorker(t *testing.T) {
	cache := NewMessageCache()

	// Add old entries
	cache.Store("old1", &MessageEntry{
		MessageTS: "old1",
		ThreadTS:  "old1",
		CreatedAt: time.Now().Add(-25 * time.Hour),
	})

	cache.Store("old2", &MessageEntry{
		MessageTS: "old2",
		ThreadTS:  "old2",
		CreatedAt: time.Now().Add(-30 * time.Hour),
	})

	// Add recent entry
	cache.Store("recent", &MessageEntry{
		MessageTS: "recent",
		ThreadTS:  "recent",
		CreatedAt: time.Now(),
	})

	// Verify all stored
	assert.Equal(t, 3, cache.Size())

	// Start cleanup worker (run every 100ms, TTL 24h)
	cancel := StartCleanupWorker(cache, 100*time.Millisecond, 24*time.Hour)
	defer cancel()

	// Wait for cleanup to run (2 ticks = ~200ms)
	time.Sleep(300 * time.Millisecond)

	// Verify old entries cleaned up
	assert.Equal(t, 1, cache.Size())

	// Verify recent entry remains
	_, found := cache.Get("recent")
	assert.True(t, found)

	// Verify old entries gone
	_, found = cache.Get("old1")
	assert.False(t, found)
	_, found = cache.Get("old2")
	assert.False(t, found)
}

// TestStartCleanupWorker_Stop tests worker cleanup on stop
func TestStartCleanupWorker_Stop(t *testing.T) {
	cache := NewMessageCache()

	// Start worker
	cancel := StartCleanupWorker(cache, 50*time.Millisecond, 24*time.Hour)

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)

	// Stop worker
	cancel()

	// Add old entry after stop
	cache.Store("old", &MessageEntry{
		MessageTS: "old",
		ThreadTS:  "old",
		CreatedAt: time.Now().Add(-25 * time.Hour),
	})

	// Wait longer than cleanup interval
	time.Sleep(200 * time.Millisecond)

	// Verify worker stopped (old entry not cleaned up)
	_, found := cache.Get("old")
	assert.True(t, found, "Worker should have stopped, old entry should remain")
}

// TestCache_UpdateEntry tests updating an existing entry
func TestCache_UpdateEntry(t *testing.T) {
	cache := NewMessageCache()

	// Store initial entry
	entry1 := &MessageEntry{
		MessageTS: "1234567890.123456",
		ThreadTS:  "1234567890.123456",
		CreatedAt: time.Now(),
	}
	cache.Store("fp123", entry1)

	// Update entry (overwrite)
	entry2 := &MessageEntry{
		MessageTS: "9999999999.999999",
		ThreadTS:  "9999999999.999999",
		CreatedAt: time.Now().Add(1 * time.Hour),
	}
	cache.Store("fp123", entry2)

	// Verify updated
	retrieved, found := cache.Get("fp123")
	require.True(t, found)
	assert.Equal(t, entry2.MessageTS, retrieved.MessageTS)
	assert.Equal(t, entry2.ThreadTS, retrieved.ThreadTS)
	assert.NotEqual(t, entry1.MessageTS, retrieved.MessageTS)
}
