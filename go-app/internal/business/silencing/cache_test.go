package silencing

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// Test helpers

func newTestSilence(id string, status silencing.SilenceStatus) *silencing.Silence {
	return &silencing.Silence{
		ID:        id,
		CreatedBy: "test@example.com",
		Comment:   "Test silence",
		StartsAt:  time.Now().Add(-1 * time.Hour),
		EndsAt:    time.Now().Add(1 * time.Hour),
		Status:    status,
		Matchers: []silencing.Matcher{
			{Name: "alertname", Value: "Test", Type: silencing.MatcherTypeEqual},
		},
	}
}

// TestCache_SetGet tests basic set and get operations.
func TestCache_SetGet(t *testing.T) {
	cache := newSilenceCache()

	// Test 1: Get from empty cache (should not be found)
	_, found := cache.Get("nonexistent")
	assert.False(t, found, "Should not find silence in empty cache")

	// Test 2: Set silence
	silence := newTestSilence("test-id-1", silencing.SilenceStatusActive)
	cache.Set(silence)

	// Test 3: Get existing silence
	retrieved, found := cache.Get("test-id-1")
	assert.True(t, found, "Should find silence after Set")
	assert.Equal(t, "test-id-1", retrieved.ID)
	assert.Equal(t, silencing.SilenceStatusActive, retrieved.Status)

	// Test 4: Update silence (Set with same ID)
	silence.Comment = "Updated comment"
	cache.Set(silence)

	retrieved, found = cache.Get("test-id-1")
	assert.True(t, found)
	assert.Equal(t, "Updated comment", retrieved.Comment)
}

// TestCache_Delete tests delete operation.
func TestCache_Delete(t *testing.T) {
	cache := newSilenceCache()

	// Set 3 silences
	silence1 := newTestSilence("id-1", silencing.SilenceStatusActive)
	silence2 := newTestSilence("id-2", silencing.SilenceStatusActive)
	silence3 := newTestSilence("id-3", silencing.SilenceStatusPending)
	cache.Set(silence1)
	cache.Set(silence2)
	cache.Set(silence3)

	// Verify all exist
	_, found1 := cache.Get("id-1")
	_, found2 := cache.Get("id-2")
	_, found3 := cache.Get("id-3")
	assert.True(t, found1)
	assert.True(t, found2)
	assert.True(t, found3)

	// Delete one silence
	cache.Delete("id-2")

	// Verify deleted
	_, found := cache.Get("id-2")
	assert.False(t, found, "Deleted silence should not be found")

	// Verify others still exist
	_, found1 = cache.Get("id-1")
	_, found3 = cache.Get("id-3")
	assert.True(t, found1)
	assert.True(t, found3)

	// Delete nonexistent (should be no-op)
	cache.Delete("nonexistent")
}

// TestCache_GetByStatus tests filtering by status.
func TestCache_GetByStatus(t *testing.T) {
	cache := newSilenceCache()

	// Set silences with different statuses
	active1 := newTestSilence("active-1", silencing.SilenceStatusActive)
	active2 := newTestSilence("active-2", silencing.SilenceStatusActive)
	pending1 := newTestSilence("pending-1", silencing.SilenceStatusPending)
	expired1 := newTestSilence("expired-1", silencing.SilenceStatusExpired)

	cache.Set(active1)
	cache.Set(active2)
	cache.Set(pending1)
	cache.Set(expired1)

	// Test filtering by active
	actives := cache.GetByStatus(silencing.SilenceStatusActive)
	assert.Len(t, actives, 2, "Should find 2 active silences")

	// Test filtering by pending
	pendings := cache.GetByStatus(silencing.SilenceStatusPending)
	assert.Len(t, pendings, 1, "Should find 1 pending silence")

	// Test filtering by expired
	expireds := cache.GetByStatus(silencing.SilenceStatusExpired)
	assert.Len(t, expireds, 1, "Should find 1 expired silence")

	// Test filtering by nonexistent status
	nonexistent := cache.GetByStatus("nonexistent")
	assert.Nil(t, nonexistent, "Should return nil for nonexistent status")
}

// TestCache_GetAll tests getting all silences.
func TestCache_GetAll(t *testing.T) {
	cache := newSilenceCache()

	// Test empty cache
	all := cache.GetAll()
	assert.Empty(t, all, "Empty cache should return empty slice")

	// Add 5 silences
	for i := 0; i < 5; i++ {
		silence := newTestSilence(string(rune('a'+i)), silencing.SilenceStatusActive)
		cache.Set(silence)
	}

	// Get all
	all = cache.GetAll()
	assert.Len(t, all, 5, "Should return all 5 silences")
}

// TestCache_Rebuild tests cache rebuild operation.
func TestCache_Rebuild(t *testing.T) {
	cache := newSilenceCache()

	// Add some initial silences
	cache.Set(newTestSilence("old-1", silencing.SilenceStatusActive))
	cache.Set(newTestSilence("old-2", silencing.SilenceStatusActive))

	// Verify initial state
	assert.Len(t, cache.GetAll(), 2)

	// Rebuild with new data
	newSilences := []*silencing.Silence{
		newTestSilence("new-1", silencing.SilenceStatusActive),
		newTestSilence("new-2", silencing.SilenceStatusActive),
		newTestSilence("new-3", silencing.SilenceStatusPending),
	}
	cache.Rebuild(newSilences)

	// Verify new state
	all := cache.GetAll()
	assert.Len(t, all, 3, "Should have 3 silences after rebuild")

	// Verify old silences are gone
	_, found1 := cache.Get("old-1")
	_, found2 := cache.Get("old-2")
	assert.False(t, found1, "Old silence should be gone")
	assert.False(t, found2, "Old silence should be gone")

	// Verify new silences exist
	_, found3 := cache.Get("new-1")
	_, found4 := cache.Get("new-2")
	_, found5 := cache.Get("new-3")
	assert.True(t, found3)
	assert.True(t, found4)
	assert.True(t, found5)

	// Verify status index updated
	actives := cache.GetByStatus(silencing.SilenceStatusActive)
	pendings := cache.GetByStatus(silencing.SilenceStatusPending)
	assert.Len(t, actives, 2)
	assert.Len(t, pendings, 1)
}

// TestCache_Stats tests cache statistics.
func TestCache_Stats(t *testing.T) {
	cache := newSilenceCache()

	// Stats of empty cache
	stats := cache.Stats()
	assert.Equal(t, 0, stats.Size)
	assert.Zero(t, stats.LastSync)

	// Add silences
	cache.Set(newTestSilence("a1", silencing.SilenceStatusActive))
	cache.Set(newTestSilence("a2", silencing.SilenceStatusActive))
	cache.Set(newTestSilence("p1", silencing.SilenceStatusPending))

	// Stats after set
	stats = cache.Stats()
	assert.Equal(t, 3, stats.Size)
	assert.Equal(t, 2, stats.ByStatus[silencing.SilenceStatusActive])
	assert.Equal(t, 1, stats.ByStatus[silencing.SilenceStatusPending])
	assert.Equal(t, 0, stats.ByStatus[silencing.SilenceStatusExpired])

	// Rebuild updates LastSync
	cache.Rebuild([]*silencing.Silence{})
	stats = cache.Stats()
	assert.NotZero(t, stats.LastSync, "Rebuild should update LastSync")
}

// TestCache_Concurrent tests thread safety with concurrent operations.
func TestCache_Concurrent(t *testing.T) {
	cache := newSilenceCache()

	var wg sync.WaitGroup
	numGoroutines := 10
	numOpsPerGoroutine := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				silence := newTestSilence(string(rune('a'+id)), silencing.SilenceStatusActive)
				cache.Set(silence)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				cache.Get(string(rune('a' + id)))
				cache.GetByStatus(silencing.SilenceStatusActive)
				cache.GetAll()
			}
		}(i)
	}

	// Wait for all goroutines
	wg.Wait()

	// Verify cache is still consistent
	all := cache.GetAll()
	assert.LessOrEqual(t, len(all), numGoroutines, "Should have at most numGoroutines silences")
}

// TestCache_LargeDataset tests performance with 1000 silences.
func TestCache_LargeDataset(t *testing.T) {
	cache := newSilenceCache()

	// Add 1000 silences
	silences := make([]*silencing.Silence, 1000)
	for i := 0; i < 1000; i++ {
		var status silencing.SilenceStatus
		switch i % 3 {
		case 0:
			status = silencing.SilenceStatusActive
		case 1:
			status = silencing.SilenceStatusPending
		case 2:
			status = silencing.SilenceStatusExpired
		}
		silences[i] = newTestSilence(string(rune('a'+i)), status)
	}

	// Rebuild with 1000 silences
	start := time.Now()
	cache.Rebuild(silences)
	rebuildDuration := time.Since(start)

	// Verify rebuild completed quickly (<10ms)
	assert.Less(t, rebuildDuration, 10*time.Millisecond, "Rebuild should be fast")

	// Verify all added
	all := cache.GetAll()
	assert.Len(t, all, 1000)

	// Verify status index
	actives := cache.GetByStatus(silencing.SilenceStatusActive)
	assert.InDelta(t, 333, len(actives), 1, "Should have ~333 active silences")

	// Test Get performance
	start = time.Now()
	for i := 0; i < 1000; i++ {
		cache.Get(string(rune('a' + i)))
	}
	getDuration := time.Since(start)

	// Should be <1ms for 1000 gets (O(1) lookups)
	assert.Less(t, getDuration, 1*time.Millisecond, "1000 Gets should be <1ms")

	// Test GetByStatus performance
	start = time.Now()
	for i := 0; i < 100; i++ {
		cache.GetByStatus(silencing.SilenceStatusActive)
	}
	getByStatusDuration := time.Since(start)

	// Should be <10ms for 100 GetByStatus
	assert.Less(t, getByStatusDuration, 10*time.Millisecond, "100 GetByStatus should be <10ms")
}

// TestCache_EmptyCache tests operations on empty cache.
func TestCache_EmptyCache(t *testing.T) {
	cache := newSilenceCache()

	// Get from empty cache
	_, found := cache.Get("nonexistent")
	assert.False(t, found)

	// GetByStatus from empty cache
	actives := cache.GetByStatus(silencing.SilenceStatusActive)
	assert.Nil(t, actives)

	// GetAll from empty cache
	all := cache.GetAll()
	assert.Empty(t, all)

	// Delete from empty cache (no-op)
	cache.Delete("nonexistent")

	// Stats of empty cache
	stats := cache.Stats()
	assert.Equal(t, 0, stats.Size)
	assert.Zero(t, stats.LastSync)
}

// TestCache_IndexConsistency tests that status index stays consistent.
func TestCache_IndexConsistency(t *testing.T) {
	cache := newSilenceCache()

	// Add silences
	cache.Set(newTestSilence("a1", silencing.SilenceStatusActive))
	cache.Set(newTestSilence("a2", silencing.SilenceStatusActive))
	cache.Set(newTestSilence("p1", silencing.SilenceStatusPending))

	// Verify index
	actives := cache.GetByStatus(silencing.SilenceStatusActive)
	require.Len(t, actives, 2)

	// Update silence status (via Set)
	silence := newTestSilence("a1", silencing.SilenceStatusExpired)
	cache.Set(silence)

	// Verify index updated
	actives = cache.GetByStatus(silencing.SilenceStatusActive)
	expireds := cache.GetByStatus(silencing.SilenceStatusExpired)
	assert.Len(t, actives, 1, "Should have 1 active after status change")
	assert.Len(t, expireds, 1, "Should have 1 expired after status change")

	// Delete silence
	cache.Delete("a1")

	// Verify index updated
	expireds = cache.GetByStatus(silencing.SilenceStatusExpired)
	assert.Len(t, expireds, 0, "Should have 0 expired after delete")
}
