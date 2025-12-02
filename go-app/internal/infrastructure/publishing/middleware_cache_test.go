package publishing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ipiton/AMP/internal/core"
)

// TestCachingMiddleware_CacheHit tests cache hit scenario
func TestCachingMiddleware_CacheHit(t *testing.T) {
	cache := NewInMemoryCache(100)
	cachingMiddleware := NewCachingMiddleware(cache)

	callCount := 0
	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		callCount++
		return map[string]any{"formatted": true, "count": callCount}, nil
	}

	chain := NewMiddlewareChain(baseFormatter, cachingMiddleware)
	alert := createTestEnrichedAlert()

	// First call - cache miss
	result1, err := chain.Format(alert)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "Should call base formatter on cache miss")
	assert.Equal(t, 1, result1["count"], "Should return result from base formatter")

	// Second call - cache hit
	result2, err := chain.Format(alert)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "Should NOT call base formatter on cache hit")
	assert.Equal(t, 1, result2["count"], "Should return cached result")

	// Verify cache stats
	stats := cache.Stats()
	assert.Equal(t, int64(1), stats.Hits, "Should have 1 cache hit")
	assert.Equal(t, int64(1), stats.Misses, "Should have 1 cache miss")
	assert.Equal(t, 0.5, stats.HitRate, "Hit rate should be 50%")
}

// TestCachingMiddleware_DifferentAlerts tests caching of different alerts
func TestCachingMiddleware_DifferentAlerts(t *testing.T) {
	cache := NewInMemoryCache(100)
	cachingMiddleware := NewCachingMiddleware(cache)

	callCount := 0
	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		callCount++
		return map[string]any{"fingerprint": alert.Alert.Fingerprint}, nil
	}

	chain := NewMiddlewareChain(baseFormatter, cachingMiddleware)

	// Create 2 different alerts
	alert1 := createTestEnrichedAlert()
	alert1.Alert.Fingerprint = "fingerprint-1"

	alert2 := createTestEnrichedAlert()
	alert2.Alert.Fingerprint = "fingerprint-2"

	// Format alert1 (cache miss)
	result1, err := chain.Format(alert1)
	require.NoError(t, err)
	assert.Equal(t, "fingerprint-1", result1["fingerprint"])

	// Format alert2 (cache miss - different fingerprint)
	result2, err := chain.Format(alert2)
	require.NoError(t, err)
	assert.Equal(t, "fingerprint-2", result2["fingerprint"])

	// Format alert1 again (cache hit)
	result3, err := chain.Format(alert1)
	require.NoError(t, err)
	assert.Equal(t, "fingerprint-1", result3["fingerprint"])

	// Verify call count
	assert.Equal(t, 2, callCount, "Should call base formatter twice (2 different alerts)")

	// Verify cache stats
	stats := cache.Stats()
	assert.Equal(t, int64(1), stats.Hits, "Should have 1 cache hit")
	assert.Equal(t, int64(2), stats.Misses, "Should have 2 cache misses")
}

// TestCachingMiddleware_ErrorsNotCached tests that errors are not cached
func TestCachingMiddleware_ErrorsNotCached(t *testing.T) {
	cache := NewInMemoryCache(100)
	cachingMiddleware := NewCachingMiddleware(cache)

	callCount := 0
	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		callCount++
		if callCount == 1 {
			return nil, assert.AnError // First call fails
		}
		return map[string]any{"formatted": true}, nil
	}

	chain := NewMiddlewareChain(baseFormatter, cachingMiddleware)
	alert := createTestEnrichedAlert()

	// First call - error (should not be cached)
	_, err := chain.Format(alert)
	require.Error(t, err)
	assert.Equal(t, 1, callCount)

	// Second call - should call base formatter again (error not cached)
	result, err := chain.Format(alert)
	require.NoError(t, err)
	assert.Equal(t, 2, callCount, "Should call base formatter again (error not cached)")
	assert.Equal(t, true, result["formatted"])
}

// TestGenerateCacheKey_Deterministic tests cache key generation
func TestGenerateCacheKey_Deterministic(t *testing.T) {
	alert1 := createTestEnrichedAlert()
	alert1.Alert.Fingerprint = "test-fingerprint"

	alert2 := createTestEnrichedAlert()
	alert2.Alert.Fingerprint = "test-fingerprint"

	// Same alerts should generate same key
	key1 := generateCacheKey(alert1)
	key2 := generateCacheKey(alert2)

	assert.Equal(t, key1, key2, "Same alerts should generate same cache key")
	assert.Equal(t, 64, len(key1), "Cache key should be 64 characters (SHA-256 hex)")
}

// TestGenerateCacheKey_Different tests different cache keys
func TestGenerateCacheKey_Different(t *testing.T) {
	alert1 := createTestEnrichedAlert()
	alert1.Alert.Fingerprint = "fingerprint-1"

	alert2 := createTestEnrichedAlert()
	alert2.Alert.Fingerprint = "fingerprint-2"

	key1 := generateCacheKey(alert1)
	key2 := generateCacheKey(alert2)

	assert.NotEqual(t, key1, key2, "Different fingerprints should generate different keys")
}

// TestGenerateCacheKey_ClassificationImpact tests classification in cache key
func TestGenerateCacheKey_ClassificationImpact(t *testing.T) {
	alert1 := createTestEnrichedAlert()
	alert1.Classification.Severity = core.SeverityCritical

	alert2 := createTestEnrichedAlert()
	alert2.Classification.Severity = core.SeverityWarning

	key1 := generateCacheKey(alert1)
	key2 := generateCacheKey(alert2)

	assert.NotEqual(t, key1, key2, "Different severity should generate different keys")
}

// TestInMemoryCache_BasicOperations tests basic cache operations
func TestInMemoryCache_BasicOperations(t *testing.T) {
	cache := NewInMemoryCache(10)

	// Test Set + Get
	value := map[string]any{"test": "value"}
	cache.Set("key1", value, 1*time.Minute)

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

// TestInMemoryCache_Expiration tests cache expiration
func TestInMemoryCache_Expiration(t *testing.T) {
	cache := NewInMemoryCache(10)

	// Set with very short TTL
	value := map[string]any{"test": "value"}
	cache.Set("key1", value, 50*time.Millisecond)

	// Should be available immediately
	_, found := cache.Get("key1")
	assert.True(t, found, "Should find cached value immediately")

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, found = cache.Get("key1")
	assert.False(t, found, "Should not find expired value")
}

// TestInMemoryCache_CapacityEviction tests LRU eviction
func TestInMemoryCache_CapacityEviction(t *testing.T) {
	cache := NewInMemoryCache(3) // Small capacity

	// Fill cache to capacity
	cache.Set("key1", map[string]any{"v": 1}, 1*time.Minute)
	cache.Set("key2", map[string]any{"v": 2}, 1*time.Minute)
	cache.Set("key3", map[string]any{"v": 3}, 1*time.Minute)

	stats := cache.Stats()
	assert.Equal(t, 3, stats.Size, "Cache should be at capacity")

	// Add 4th entry (should evict oldest)
	cache.Set("key4", map[string]any{"v": 4}, 1*time.Minute)

	stats = cache.Stats()
	assert.Equal(t, 3, stats.Size, "Cache should stay at capacity")

	// One of the old keys should be evicted (implementation-dependent)
	// We can't predict which one, but cache should work
	_, found := cache.Get("key4")
	assert.True(t, found, "Newly added key should exist")
}

// TestInMemoryCache_Stats tests cache statistics
func TestInMemoryCache_Stats(t *testing.T) {
	cache := NewInMemoryCache(10)

	// Initial stats
	stats := cache.Stats()
	assert.Equal(t, int64(0), stats.Hits)
	assert.Equal(t, int64(0), stats.Misses)
	assert.Equal(t, 0.0, stats.HitRate)
	assert.Equal(t, 10, stats.Capacity)

	// Add entries
	cache.Set("key1", map[string]any{}, 1*time.Minute)
	cache.Set("key2", map[string]any{}, 1*time.Minute)

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

// TestInMemoryCache_Clear tests cache clear
func TestInMemoryCache_Clear(t *testing.T) {
	cache := NewInMemoryCache(10)

	// Add entries
	cache.Set("key1", map[string]any{}, 1*time.Minute)
	cache.Set("key2", map[string]any{}, 1*time.Minute)
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

// TestCachingMiddleware_HighHitRate tests achieving 30%+ hit rate
func TestCachingMiddleware_HighHitRate(t *testing.T) {
	cache := NewInMemoryCache(100)
	cachingMiddleware := NewCachingMiddleware(cache)

	baseFormatter := func(alert *core.EnrichedAlert) (map[string]any, error) {
		return map[string]any{"formatted": true}, nil
	}

	chain := NewMiddlewareChain(baseFormatter, cachingMiddleware)

	// Simulate workload: 10 unique alerts, 100 total requests
	// Expected hit rate: ~90% (10 misses, 90 hits)
	alerts := make([]*core.EnrichedAlert, 10)
	for i := 0; i < 10; i++ {
		alert := createTestEnrichedAlert()
		alert.Alert.Fingerprint = "fingerprint-" + string(rune('a'+i))
		alerts[i] = alert
	}

	// Process 100 requests (random distribution)
	for i := 0; i < 100; i++ {
		alertIdx := i % 10 // Round-robin through alerts
		_, err := chain.Format(alerts[alertIdx])
		require.NoError(t, err)
	}

	// Verify high hit rate
	stats := cache.Stats()
	assert.GreaterOrEqual(t, stats.HitRate, 0.30, "Hit rate should be >= 30%")
	assert.Equal(t, int64(90), stats.Hits, "Should have 90 hits (10 unique alerts × 9 repeats)")
	assert.Equal(t, int64(10), stats.Misses, "Should have 10 misses (10 unique alerts)")
	assert.Equal(t, 0.9, stats.HitRate, "Hit rate should be 90%")
}
