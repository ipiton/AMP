package publishing

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewIncidentIDCache(t *testing.T) {
	ttl := 1 * time.Hour
	cache := NewIncidentIDCache(ttl)

	assert.NotNil(t, cache)
}

func TestIncidentIDCache_SetAndGet(t *testing.T) {
	cache := NewIncidentIDCache(1 * time.Hour)

	fingerprint := "test-fingerprint"
	incidentID := "incident-123"

	// Set
	cache.Set(fingerprint, incidentID)

	// Get
	retrievedID, found := cache.Get(fingerprint)
	assert.True(t, found)
	assert.Equal(t, incidentID, retrievedID)
}

func TestIncidentIDCache_GetNonExistent(t *testing.T) {
	cache := NewIncidentIDCache(1 * time.Hour)

	retrievedID, found := cache.Get("nonexistent-fingerprint")
	assert.False(t, found)
	assert.Empty(t, retrievedID)
}

func TestIncidentIDCache_Expiry(t *testing.T) {
	cache := NewIncidentIDCache(50 * time.Millisecond) // Very short TTL

	fingerprint := "test-fingerprint"
	incidentID := "incident-123"

	cache.Set(fingerprint, incidentID)

	// Get immediately - should be present
	retrievedID, found := cache.Get(fingerprint)
	assert.True(t, found)
	assert.Equal(t, incidentID, retrievedID)

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	// Get after expiry - should be gone
	retrievedID, found = cache.Get(fingerprint)
	assert.False(t, found)
	assert.Empty(t, retrievedID)
}

func TestIncidentIDCache_Delete(t *testing.T) {
	cache := NewIncidentIDCache(1 * time.Hour)

	fingerprint := "test-fingerprint"
	incidentID := "incident-123"

	cache.Set(fingerprint, incidentID)

	// Verify it's there
	_, found := cache.Get(fingerprint)
	assert.True(t, found)

	// Delete
	cache.Delete(fingerprint)

	// Verify it's gone
	_, found = cache.Get(fingerprint)
	assert.False(t, found)
}

func TestIncidentIDCache_Size(t *testing.T) {
	cache := NewIncidentIDCache(1 * time.Hour)

	// Initially empty
	assert.Equal(t, 0, cache.Size())

	// Add entries
	cache.Set("fp1", "id1")
	cache.Set("fp2", "id2")
	cache.Set("fp3", "id3")

	// Should have 3 entries
	assert.Equal(t, 3, cache.Size())

	// Delete one
	cache.Delete("fp1")

	// Should have 2 entries
	assert.Equal(t, 2, cache.Size())
}

func TestIncidentIDCache_ConcurrentAccess(t *testing.T) {
	cache := NewIncidentIDCache(1 * time.Hour)

	numGoroutines := 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			fingerprint := "fingerprint-" + string(rune(id))
			incidentID := "incident-" + string(rune(id))
			cache.Set(fingerprint, incidentID)
		}(i)
	}

	wg.Wait()

	// Concurrent reads
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			fingerprint := "fingerprint-" + string(rune(id))
			_, _ = cache.Get(fingerprint)
		}(i)
	}

	wg.Wait()

	// Should not panic or race
}

func TestIncidentIDCache_ConcurrentDeleteAndRead(t *testing.T) {
	cache := NewIncidentIDCache(1 * time.Hour)

	fingerprint := "test-fp"
	cache.Set(fingerprint, "test-id")

	var wg sync.WaitGroup
	wg.Add(2)

	// Concurrent delete
	go func() {
		defer wg.Done()
		cache.Delete(fingerprint)
	}()

	// Concurrent read
	go func() {
		defer wg.Done()
		_, _ = cache.Get(fingerprint)
	}()

	wg.Wait()
	// Should not panic or race
}

func BenchmarkIncidentIDCache_Set(b *testing.B) {
	cache := NewIncidentIDCache(1 * time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set("benchmark-fp", "benchmark-id")
	}
}

func BenchmarkIncidentIDCache_Get(b *testing.B) {
	cache := NewIncidentIDCache(1 * time.Hour)
	cache.Set("benchmark-fp", "benchmark-id")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.Get("benchmark-fp")
	}
}

func BenchmarkIncidentIDCache_GetMiss(b *testing.B) {
	cache := NewIncidentIDCache(1 * time.Hour)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.Get("nonexistent-fp")
	}
}

func BenchmarkIncidentIDCache_ConcurrentReads(b *testing.B) {
	cache := NewIncidentIDCache(1 * time.Hour)
	cache.Set("benchmark-fp", "benchmark-id")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = cache.Get("benchmark-fp")
		}
	})
}

func BenchmarkIncidentIDCache_ConcurrentWrites(b *testing.B) {
	cache := NewIncidentIDCache(1 * time.Hour)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Set("benchmark-fp", "benchmark-id")
		}
	})
}
