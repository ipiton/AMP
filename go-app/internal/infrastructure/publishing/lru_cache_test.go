package publishing

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLRUCache_BasicOperations tests Get/Set/Delete
func TestLRUCache_BasicOperations(t *testing.T) {
	cache := NewLRUCache(10, 5*time.Minute)

	// Test Set + Get
	value := map[string]any{"test": "value"}
	cache.Set("key1", value, 0)

	retrieved, found := cache.Get("key1")
	assert.True(t, found, "Should find cached value")
	assert.Equal(t, "value", retrieved["test"])

	// Test Get non-existent key
	_, found = cache.Get("nonexistent")
	assert.False(t, found, "Should not find non-existent key")

	// Test Delete
	cache.Delete("key1")
	_, found = cache.Get("key1")
	assert.False(t, found, "Should not find deleted key")
}

// TestLRUCache_TrueLRUEviction tests proper LRU eviction order
func TestLRUCache_TrueLRUEviction(t *testing.T) {
	cache := NewLRUCache(3, 0) // Capacity: 3, no TTL

	// Fill cache
	cache.Set("key1", map[string]any{"v": 1}, 0)
	cache.Set("key2", map[string]any{"v": 2}, 0)
	cache.Set("key3", map[string]any{"v": 3}, 0)

	// Access key1 (makes it most recently used)
	cache.Get("key1")

	// Add key4 (should evict key2, the least recently used)
	cache.Set("key4", map[string]any{"v": 4}, 0)

	// Verify key2 was evicted (least recently used)
	_, found := cache.Get("key2")
	assert.False(t, found, "key2 should be evicted (LRU)")

	// Verify key1 still exists (accessed recently)
	_, found = cache.Get("key1")
	assert.True(t, found, "key1 should still exist (accessed recently)")

	// Verify key3 and key4 exist
	_, found = cache.Get("key3")
	assert.True(t, found, "key3 should exist")
	_, found = cache.Get("key4")
	assert.True(t, found, "key4 should exist")
}

// TestLRUCache_TTLExpiration tests TTL-based expiration
func TestLRUCache_TTLExpiration(t *testing.T) {
	cache := NewLRUCache(10, 0) // No default TTL

	// Set with short TTL
	cache.Set("key1", map[string]any{"test": "value"}, 50*time.Millisecond)

	// Should be available immediately
	_, found := cache.Get("key1")
	assert.True(t, found, "Should find value immediately")

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, found = cache.Get("key1")
	assert.False(t, found, "Should not find expired value")

	// Verify eviction reason
	reasons := cache.(*LRUCache).GetEvictionReasons()
	assert.Equal(t, int64(1), reasons["ttl"], "Should have 1 TTL eviction")
}

// TestLRUCache_DefaultTTL tests default TTL usage
func TestLRUCache_DefaultTTL(t *testing.T) {
	cache := NewLRUCache(10, 100*time.Millisecond) // Default TTL: 100ms

	// Set without specifying TTL (should use default)
	cache.Set("key1", map[string]any{"test": "value"}, 0)

	// Should be available immediately
	_, found := cache.Get("key1")
	assert.True(t, found)

	// Wait for default TTL expiration
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	_, found = cache.Get("key1")
	assert.False(t, found, "Should expire after default TTL")
}

// TestLRUCache_UpdateExisting tests updating existing keys
func TestLRUCache_UpdateExisting(t *testing.T) {
	cache := NewLRUCache(10, 0)

	// Set initial value
	cache.Set("key1", map[string]any{"version": 1}, 0)

	// Update value
	cache.Set("key1", map[string]any{"version": 2}, 0)

	// Should have updated value
	retrieved, found := cache.Get("key1")
	require.True(t, found)
	assert.Equal(t, 2, retrieved["version"], "Should have updated value")

	// Verify no new entry created (size should be 1)
	stats := cache.Stats()
	assert.Equal(t, 1, stats.Size, "Should not create duplicate entry")
}

// TestLRUCache_CleanupExpired tests manual expired entry cleanup
func TestLRUCache_CleanupExpired(t *testing.T) {
	cache := NewLRUCache(10, 0).(*LRUCache)

	// Add entries with different TTLs
	cache.Set("short1", map[string]any{}, 50*time.Millisecond)
	cache.Set("short2", map[string]any{}, 50*time.Millisecond)
	cache.Set("long", map[string]any{}, 5*time.Minute)

	// Wait for short TTLs to expire
	time.Sleep(100 * time.Millisecond)

	// Cleanup expired
	removed := cache.CleanupExpired()

	assert.Equal(t, 2, removed, "Should remove 2 expired entries")

	// Verify long TTL entry still exists
	_, found := cache.Get("long")
	assert.True(t, found, "Long TTL entry should still exist")

	// Verify size is correct
	stats := cache.Stats()
	assert.Equal(t, 1, stats.Size, "Size should be 1 after cleanup")
}

// TestLRUCache_Clear tests clearing cache
func TestLRUCache_Clear(t *testing.T) {
	cache := NewLRUCache(10, 0)

	// Add entries
	cache.Set("key1", map[string]any{}, 0)
	cache.Set("key2", map[string]any{}, 0)
	cache.Get("key1") // Generate hit

	// Clear cache
	cache.Clear()

	// Verify cleared
	stats := cache.Stats()
	assert.Equal(t, 0, stats.Size, "Cache should be empty")
	assert.Equal(t, int64(0), stats.Hits, "Hits should be reset")
	assert.Equal(t, int64(0), stats.Misses, "Misses should be reset")

	// Verify keys are gone
	_, found := cache.Get("key1")
	assert.False(t, found, "Keys should be deleted")
}

// TestLRUCache_Stats tests cache statistics
func TestLRUCache_Stats(t *testing.T) {
	cache := NewLRUCache(10, 0)

	// Initial stats
	stats := cache.Stats()
	assert.Equal(t, int64(0), stats.Hits)
	assert.Equal(t, int64(0), stats.Misses)
	assert.Equal(t, 0.0, stats.HitRate)
	assert.Equal(t, 10, stats.Capacity)

	// Add entries
	cache.Set("key1", map[string]any{}, 0)
	cache.Set("key2", map[string]any{}, 0)

	// Generate hits and misses
	cache.Get("key1")          // hit
	cache.Get("key1")          // hit
	cache.Get("nonexistent")   // miss
	cache.Get("key2")          // hit

	// Check stats
	stats = cache.Stats()
	assert.Equal(t, int64(3), stats.Hits, "Should have 3 hits")
	assert.Equal(t, int64(1), stats.Misses, "Should have 1 miss")
	assert.Equal(t, 0.75, stats.HitRate, "Hit rate should be 75%")
	assert.Equal(t, 2, stats.Size, "Cache size should be 2")
}

// TestLRUCache_EvictionReasons tests detailed eviction tracking
func TestLRUCache_EvictionReasons(t *testing.T) {
	cache := NewLRUCache(2, 0).(*LRUCache) // Capacity: 2

	// LRU eviction
	cache.Set("key1", map[string]any{}, 0)
	cache.Set("key2", map[string]any{}, 0)
	cache.Set("key3", map[string]any{}, 0) // Evicts key1 (LRU)

	// Manual deletion
	cache.Delete("key2")

	// TTL expiration
	cache.Set("key4", map[string]any{}, 10*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	cache.Get("key4") // Triggers TTL check and eviction

	// Check eviction reasons
	reasons := cache.GetEvictionReasons()
	assert.Equal(t, int64(1), reasons["lru"], "Should have 1 LRU eviction")
	assert.Equal(t, int64(1), reasons["manual"], "Should have 1 manual deletion")
	assert.Equal(t, int64(1), reasons["ttl"], "Should have 1 TTL eviction")
}

// TestLRUCache_ConcurrentAccess tests thread safety
func TestLRUCache_ConcurrentAccess(t *testing.T) {
	cache := NewLRUCache(100, 0)
	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3)

	// Concurrent Set operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("key-%d-%d", id, j)
				cache.Set(key, map[string]any{"id": id, "j": j}, 0)
			}
		}(i)
	}

	// Concurrent Get operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("key-%d-%d", id, j%10)
				cache.Get(key)
			}
		}(i)
	}

	// Concurrent Delete operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := fmt.Sprintf("key-%d-%d", id, j%5)
				cache.Delete(key)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify cache is still functional
	cache.Set("test", map[string]any{"value": "test"}, 0)
	_, found := cache.Get("test")
	assert.True(t, found, "Cache should still be functional after concurrent access")
}

// TestLRUCache_HighCapacity tests cache with large capacity
func TestLRUCache_HighCapacity(t *testing.T) {
	cache := NewLRUCache(10000, 0)

	// Add 5000 entries
	for i := 0; i < 5000; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 0)
	}

	// Verify size
	stats := cache.Stats()
	assert.Equal(t, 5000, stats.Size)

	// Access some entries (test LRU ordering)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, found := cache.Get(key)
		assert.True(t, found)
	}

	// Add 6000 more entries (will trigger evictions)
	for i := 5000; i < 11000; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 0)
	}

	// Verify capacity respected
	stats = cache.Stats()
	assert.Equal(t, 10000, stats.Size, "Cache should respect capacity")
	assert.Greater(t, stats.Evictions, int64(0), "Should have evictions")

	// Verify recently accessed keys still exist (first 1000)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, found := cache.Get(key)
		assert.True(t, found, "Recently accessed keys should still exist")
	}
}

// TestHashKey tests FNV-1a hashing
func TestHashKey(t *testing.T) {
	// Test deterministic hashing
	data := []byte("test-data")
	hash1 := HashKey(data)
	hash2 := HashKey(data)

	assert.Equal(t, hash1, hash2, "Hash should be deterministic")
	assert.NotEqual(t, uint64(0), hash1, "Hash should not be zero")

	// Test different data produces different hash
	hash3 := HashKey([]byte("different-data"))
	assert.NotEqual(t, hash1, hash3, "Different data should produce different hash")
}

// TestHashKeyString tests string convenience wrapper
func TestHashKeyString(t *testing.T) {
	hash1 := HashKeyString("test")
	hash2 := HashKeyString("test")
	hash3 := HashKeyString("different")

	assert.Equal(t, hash1, hash2, "Same string should produce same hash")
	assert.NotEqual(t, hash1, hash3, "Different strings should produce different hash")
}

// TestLRUCache_MoveToFront tests that Get moves entry to front
func TestLRUCache_MoveToFront(t *testing.T) {
	cache := NewLRUCache(3, 0)

	// Fill cache
	cache.Set("key1", map[string]any{}, 0)
	cache.Set("key2", map[string]any{}, 0)
	cache.Set("key3", map[string]any{}, 0)

	// Access key1 (moves to front)
	cache.Get("key1")

	// Access key2 (moves to front)
	cache.Get("key2")

	// Add key4 (should evict key3, the least recently used)
	cache.Set("key4", map[string]any{}, 0)

	// Verify key3 was evicted
	_, found := cache.Get("key3")
	assert.False(t, found, "key3 should be evicted")

	// Verify key1 and key2 still exist
	_, found = cache.Get("key1")
	assert.True(t, found, "key1 should exist")
	_, found = cache.Get("key2")
	assert.True(t, found, "key2 should exist")
}
