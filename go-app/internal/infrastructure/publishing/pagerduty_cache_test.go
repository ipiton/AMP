package publishing

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEventKeyCache_SetGet(t *testing.T) {
	cache := NewEventKeyCache(24 * time.Hour)
	t.Cleanup(cache.Stop)

	// Set entry
	cache.Set("fingerprint1", "dedup-key1")

	// Get entry
	dedupKey, found := cache.Get("fingerprint1")

	assert.True(t, found)
	assert.Equal(t, "dedup-key1", dedupKey)
}

func TestEventKeyCache_GetNotFound(t *testing.T) {
	cache := NewEventKeyCache(24 * time.Hour)
	t.Cleanup(cache.Stop)

	dedupKey, found := cache.Get("non-existent")

	assert.False(t, found)
	assert.Empty(t, dedupKey)
}

func TestEventKeyCache_Delete(t *testing.T) {
	cache := NewEventKeyCache(24 * time.Hour)
	t.Cleanup(cache.Stop)

	// Set and delete
	cache.Set("fingerprint1", "dedup-key1")
	cache.Delete("fingerprint1")

	// Verify deleted
	_, found := cache.Get("fingerprint1")
	assert.False(t, found)
}

func TestEventKeyCache_TTL(t *testing.T) {
	cache := NewEventKeyCache(100 * time.Millisecond)
	t.Cleanup(cache.Stop)

	// Set entry
	cache.Set("fingerprint1", "dedup-key1")

	// Get immediately - should exist
	_, found := cache.Get("fingerprint1")
	assert.True(t, found)

	// Wait for TTL expiration
	time.Sleep(150 * time.Millisecond)

	// Get after expiration - should not exist
	_, found = cache.Get("fingerprint1")
	assert.False(t, found)
}

func TestEventKeyCache_Size(t *testing.T) {
	cache := NewEventKeyCache(24 * time.Hour)
	t.Cleanup(cache.Stop)

	assert.Equal(t, 0, cache.Size())

	cache.Set("fp1", "dedup1")
	cache.Set("fp2", "dedup2")
	cache.Set("fp3", "dedup3")

	assert.Equal(t, 3, cache.Size())

	cache.Delete("fp2")

	assert.Equal(t, 2, cache.Size())
}

func TestEventKeyCache_Cleanup(t *testing.T) {
	cache := NewEventKeyCache(100 * time.Millisecond)
	t.Cleanup(cache.Stop)

	// Add entries
	cache.Set("fp1", "dedup1")
	cache.Set("fp2", "dedup2")
	cache.Set("fp3", "dedup3")

	assert.Equal(t, 3, cache.Size())

	// Wait for some entries to expire
	time.Sleep(150 * time.Millisecond)

	// Add new entry (not expired)
	cache.Set("fp4", "dedup4")

	// Run cleanup
	cache.Cleanup()

	// Only fp4 should remain
	assert.Equal(t, 1, cache.Size())

	_, found := cache.Get("fp4")
	assert.True(t, found)
}

func TestEventKeyCache_ConcurrentAccess(t *testing.T) {
	cache := NewEventKeyCache(24 * time.Hour)
	t.Cleanup(cache.Stop)

	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cache.Set("fp"+string(rune(id))+string(rune(j)), "dedup")
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				cache.Get("fp" + string(rune(id)) + string(rune(j)))
			}
		}(i)
	}

	wg.Wait()

	// Verify no panics occurred (test passes if no panic)
}

// TestEventKeyCache_StopStopsCleanupWorker is the regression test for item 2
// (FU-WAVE3-RELIABILITY): NewEventKeyCache started its cleanupWorker
// goroutine with `for range ticker.C` and no way to stop it, so every cache
// created (one per PublisherFactory) leaked a goroutine for the life of the
// process — drowning full-package -race runs in stacks belonging to caches
// long since abandoned by their test. Stop() must actually terminate the
// goroutine, not just be present on the interface.
//
// The worker signals its own exit via workerDone, so the assertion is
// deterministic — the earlier runtime.NumGoroutine sampling raced with
// unrelated tests' goroutines and flaked in full-package runs.
func TestEventKeyCache_StopStopsCleanupWorker(t *testing.T) {
	cache := NewEventKeyCache(24 * time.Hour)

	impl, ok := cache.(*eventKeyCacheImpl)
	if !ok {
		t.Fatalf("NewEventKeyCache returned %T, want *eventKeyCacheImpl", cache)
	}

	// Before Stop the worker must be running: its done marker is open.
	select {
	case <-impl.workerDone:
		t.Fatal("cleanup worker exited before Stop was called")
	default:
	}

	cache.Stop()

	// The worker closes workerDone on exit — deterministic, no
	// process-wide goroutine counting (the old NumGoroutine sampling raced
	// with unrelated tests' goroutines and flaked ~50% in full-package runs).
	select {
	case <-impl.workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup worker did not exit within 5s of Stop")
	}

	// Calling Stop again must not panic (double-close guard).
	cache.Stop()
}
