package publishing

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"
)

// Benchmarks comparing LRU vs InMemory cache

// BenchmarkLRUCache_Set benchmarks LRU cache Set operation
func BenchmarkLRUCache_Set(b *testing.B) {
	cache := NewLRUCache(1000, 5*time.Minute)
	value := map[string]any{"test": "value"}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		cache.Set(key, value, 0)
	}
}

// BenchmarkInMemoryCache_Set benchmarks InMemory cache Set operation
func BenchmarkInMemoryCache_Set(b *testing.B) {
	cache := NewInMemoryCache(1000)
	value := map[string]any{"test": "value"}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		cache.Set(key, value, 5*time.Minute)
	}
}

// BenchmarkLRUCache_Get benchmarks LRU cache Get operation (hot path)
func BenchmarkLRUCache_Get(b *testing.B) {
	cache := NewLRUCache(1000, 5*time.Minute)

	// Pre-populate cache
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 0)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		cache.Get(key)
	}
}

// BenchmarkInMemoryCache_Get benchmarks InMemory cache Get operation
func BenchmarkInMemoryCache_Get(b *testing.B) {
	cache := NewInMemoryCache(1000)

	// Pre-populate cache
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 5*time.Minute)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		cache.Get(key)
	}
}

// BenchmarkLRUCache_GetHitRate benchmarks realistic workload (90% hit rate)
func BenchmarkLRUCache_GetHitRate(b *testing.B) {
	cache := NewLRUCache(100, 5*time.Minute)

	// Pre-populate with 100 entries
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 0)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// 90% hits (keys 0-89), 10% misses (keys 100-109)
		key := fmt.Sprintf("key-%d", i%110)
		cache.Get(key)
	}
}

// BenchmarkLRUCache_ConcurrentGet benchmarks parallel Get operations
func BenchmarkLRUCache_ConcurrentGet(b *testing.B) {
	cache := NewLRUCache(1000, 5*time.Minute)

	// Pre-populate cache
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 0)
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%d", i%1000)
			cache.Get(key)
			i++
		}
	})
}

// BenchmarkLRUCache_EvictionHeavy benchmarks LRU eviction performance
func BenchmarkLRUCache_EvictionHeavy(b *testing.B) {
	cache := NewLRUCache(100, 0) // Small capacity to trigger frequent evictions

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i) // Always new keys (triggers eviction)
		cache.Set(key, map[string]any{"id": i}, 0)
	}
}

// Benchmarks comparing FNV-1a vs SHA-256 hashing

// BenchmarkHashKey_FNV1a benchmarks FNV-1a hashing (used in LRU cache)
func BenchmarkHashKey_FNV1a(b *testing.B) {
	data := []byte("fingerprint-abc123-status-firing-severity-critical")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = HashKey(data)
	}
}

// BenchmarkHashKey_SHA256 benchmarks SHA-256 hashing (used in InMemory cache)
func BenchmarkHashKey_SHA256(b *testing.B) {
	data := []byte("fingerprint-abc123-status-firing-severity-critical")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hash := sha256.Sum256(data)
		_ = hash[:]
	}
}

// BenchmarkHashKeyString_FNV1a benchmarks FNV-1a string hashing
func BenchmarkHashKeyString_FNV1a(b *testing.B) {
	str := "fingerprint-abc123-status-firing-severity-critical"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = HashKeyString(str)
	}
}

// BenchmarkLRUCache_CleanupExpired benchmarks expired entry cleanup
func BenchmarkLRUCache_CleanupExpired(b *testing.B) {
	cache := NewLRUCache(1000, 0).(*LRUCache)

	// Pre-populate with expired entries
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 1*time.Nanosecond) // Already expired
	}

	// Add some non-expired entries
	for i := 500; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 5*time.Minute)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cache.CleanupExpired()

		// Re-populate expired entries for next iteration
		if i < b.N-1 {
			for j := 0; j < 500; j++ {
				key := fmt.Sprintf("key-%d", j)
				cache.Set(key, map[string]any{"id": j}, 1*time.Nanosecond)
			}
		}
	}
}

// BenchmarkLRUCache_HighCapacity benchmarks large cache performance
func BenchmarkLRUCache_HighCapacity(b *testing.B) {
	cache := NewLRUCache(10000, 5*time.Minute)

	// Pre-populate with 5000 entries
	for i := 0; i < 5000; i++ {
		key := fmt.Sprintf("key-%d", i)
		cache.Set(key, map[string]any{"id": i}, 0)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Mix of hits and new entries
		key := fmt.Sprintf("key-%d", i%7000)
		if i%2 == 0 {
			cache.Get(key)
		} else {
			cache.Set(key, map[string]any{"id": i}, 0)
		}
	}
}
