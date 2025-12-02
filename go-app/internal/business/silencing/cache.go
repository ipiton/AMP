package silencing

import (
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core/silencing"
)

// silenceCache is an in-memory cache for active silences.
//
// Design:
//   - Primary store: map[string]*silencing.Silence (ID → Silence)
//   - Secondary index: map[SilenceStatus][]string (Status → IDs)
//   - Thread-safe: sync.RWMutex for concurrent access
//   - Memory efficient: Only stores active silences (~100KB for 1000 silences)
//   - Self-healing: Periodic sync worker rebuilds cache from database
//
// Performance:
//   - Get by ID: O(1)
//   - Get by status: O(N) where N = count of silences with that status
//   - Set/Delete: O(N) due to index rebuild (N = total cache size)
//
// Thread-safety:
//   - All public methods use RWMutex
//   - Read operations (Get, GetByStatus, GetAll): RLock (concurrent)
//   - Write operations (Set, Delete, Rebuild): Lock (exclusive)
//
// Example usage:
//
//	cache := newSilenceCache()
//	cache.Set(silence1)
//	cache.Set(silence2)
//
//	if silence, found := cache.Get("some-id"); found {
//	    fmt.Printf("Found: %s\n", silence.ID)
//	}
//
//	active := cache.GetByStatus(silencing.SilenceStatusActive)
//	fmt.Printf("Active silences: %d\n", len(active))
type silenceCache struct {
	mu sync.RWMutex

	// Primary store: ID → Silence
	// Key: silence.ID (UUID)
	// Value: pointer to Silence object
	silences map[string]*silencing.Silence

	// Secondary index: Status → IDs
	// Allows fast filtering by status without iterating all silences
	// Rebuilt on every Set/Delete/Rebuild operation
	byStatus map[silencing.SilenceStatus][]string

	// Metadata
	lastSync time.Time // Last time cache was rebuilt from database
	size     int       // Number of silences in cache (optimization)
}

// newSilenceCache creates a new empty cache.
//
// The cache is initialized with empty maps and zero metadata.
// Call Rebuild() to populate from database.
//
// Example:
//
//	cache := newSilenceCache()
//	cache.Rebuild(silences) // Load from database
func newSilenceCache() *silenceCache {
	return &silenceCache{
		silences: make(map[string]*silencing.Silence),
		byStatus: make(map[silencing.SilenceStatus][]string),
	}
}

// Get retrieves a silence by ID (thread-safe read).
//
// This method uses RLock, allowing multiple concurrent reads.
// Performance: O(1) map lookup.
//
// Parameters:
//   - id: Silence UUID
//
// Returns:
//   - *silencing.Silence: Silence object (or nil if not found)
//   - bool: true if found, false otherwise
//
// Example:
//
//	if silence, found := cache.Get("550e8400-e29b-41d4-a716-446655440000"); found {
//	    fmt.Printf("Silence: %+v\n", silence)
//	} else {
//	    fmt.Println("Not in cache")
//	}
func (c *silenceCache) Get(id string) (*silencing.Silence, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	silence, found := c.silences[id]
	return silence, found
}

// Set adds or updates a silence (thread-safe write).
//
// This method uses Lock, blocking all readers and writers.
// After updating the primary store, it rebuilds the status index.
//
// Performance: O(N) due to index rebuild, where N = cache size.
//
// Parameters:
//   - silence: Silence object to add/update (must not be nil)
//
// Note: If silence.ID already exists, it will be replaced.
//
// Example:
//
//	silence := &silencing.Silence{
//	    ID:       "550e8400-e29b-41d4-a716-446655440000",
//	    Status:   silencing.SilenceStatusActive,
//	    // ... other fields
//	}
//	cache.Set(silence)
func (c *silenceCache) Set(silence *silencing.Silence) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Add to primary store
	c.silences[silence.ID] = silence

	// Rebuild status index
	c.rebuildStatusIndex()
	c.size = len(c.silences)
}

// Delete removes a silence (thread-safe write).
//
// This method uses Lock, blocking all readers and writers.
// After removing from primary store, it rebuilds the status index.
//
// Performance: O(N) due to index rebuild, where N = cache size.
//
// Parameters:
//   - id: Silence UUID to remove
//
// Note: If ID doesn't exist, this is a no-op (no error).
//
// Example:
//
//	cache.Delete("550e8400-e29b-41d4-a716-446655440000")
func (c *silenceCache) Delete(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove from primary store
	delete(c.silences, id)

	// Rebuild status index
	c.rebuildStatusIndex()
	c.size = len(c.silences)
}

// GetByStatus returns all silences with given status (thread-safe read).
//
// This method uses RLock, allowing multiple concurrent reads.
// Uses the secondary index for O(N) performance where N = count of silences with that status.
//
// Parameters:
//   - status: Silence status to filter by
//
// Returns:
//   - []*silencing.Silence: List of silences (empty if none found)
//
// Note: Returns a new slice, safe to modify without affecting cache.
//
// Example:
//
//	active := cache.GetByStatus(silencing.SilenceStatusActive)
//	fmt.Printf("Active silences: %d\n", len(active))
//
//	expired := cache.GetByStatus(silencing.SilenceStatusExpired)
//	fmt.Printf("Expired silences: %d\n", len(expired))
func (c *silenceCache) GetByStatus(status silencing.SilenceStatus) []*silencing.Silence {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids, found := c.byStatus[status]
	if !found {
		return nil
	}

	// Build result slice
	result := make([]*silencing.Silence, 0, len(ids))
	for _, id := range ids {
		if silence, ok := c.silences[id]; ok {
			result = append(result, silence)
		}
	}
	return result
}

// GetAll returns all cached silences (thread-safe read).
//
// This method uses RLock, allowing multiple concurrent reads.
// Returns a new slice, safe to modify without affecting cache.
//
// Performance: O(N) where N = cache size.
//
// Returns:
//   - []*silencing.Silence: List of all silences (empty if cache is empty)
//
// Example:
//
//	all := cache.GetAll()
//	fmt.Printf("Total cached: %d\n", len(all))
func (c *silenceCache) GetAll() []*silencing.Silence {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]*silencing.Silence, 0, len(c.silences))
	for _, silence := range c.silences {
		result = append(result, silence)
	}
	return result
}

// Rebuild replaces the entire cache with new data (thread-safe write).
//
// This method is used by the sync worker to refresh the cache from database.
// It completely replaces the cache contents, rebuilds indexes, and updates metadata.
//
// Performance: O(N) where N = len(silences).
//
// Parameters:
//   - silences: New list of silences to cache
//
// Note: This is a destructive operation - all previous cache contents are lost.
//
// Example:
//
//	// Sync worker fetches active silences from database
//	silences, _ := repo.ListSilences(ctx, filter)
//	cache.Rebuild(silences)
func (c *silenceCache) Rebuild(silences []*silencing.Silence) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Create new map with appropriate capacity
	c.silences = make(map[string]*silencing.Silence, len(silences))
	for _, silence := range silences {
		c.silences[silence.ID] = silence
	}

	// Rebuild status index
	c.rebuildStatusIndex()

	// Update metadata
	c.lastSync = time.Now()
	c.size = len(c.silences)
}

// rebuildStatusIndex rebuilds the status index from scratch.
//
// This is an internal helper method that MUST be called with c.mu.Lock() held.
// It iterates through all silences and builds the status → IDs mapping.
//
// Performance: O(N) where N = cache size.
//
// Note: This method is NOT thread-safe on its own - caller must hold lock.
func (c *silenceCache) rebuildStatusIndex() {
	// Create new index
	c.byStatus = make(map[silencing.SilenceStatus][]string)

	// Populate index
	for id, silence := range c.silences {
		status := silence.Status
		c.byStatus[status] = append(c.byStatus[status], id)
	}
}

// Stats returns cache statistics (thread-safe read).
//
// This method provides insights into cache size, last sync time, and distribution by status.
// Useful for monitoring, dashboards, and debugging.
//
// Returns:
//   - CacheStats: Statistics object
//
// Example:
//
//	stats := cache.Stats()
//	fmt.Printf("Cache size: %d\n", stats.Size)
//	fmt.Printf("Last sync: %s\n", stats.LastSync.Format(time.RFC3339))
//	fmt.Printf("Active: %d, Pending: %d, Expired: %d\n",
//	    stats.ByStatus[silencing.SilenceStatusActive],
//	    stats.ByStatus[silencing.SilenceStatusPending],
//	    stats.ByStatus[silencing.SilenceStatusExpired],
//	)
func (c *silenceCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CacheStats{
		Size:     c.size,
		LastSync: c.lastSync,
		ByStatus: map[silencing.SilenceStatus]int{
			silencing.SilenceStatusPending: len(c.byStatus[silencing.SilenceStatusPending]),
			silencing.SilenceStatusActive:  len(c.byStatus[silencing.SilenceStatusActive]),
			silencing.SilenceStatusExpired: len(c.byStatus[silencing.SilenceStatusExpired]),
		},
	}
}

// CacheStats holds cache statistics.
//
// This struct is returned by silenceCache.Stats() and provides
// insights into cache health and size.
//
// Example usage:
//
//	stats := cache.Stats()
//	if stats.Size > 10000 {
//	    log.Warn("High cache size detected", "size", stats.Size)
//	}
//	if time.Since(stats.LastSync) > 5*time.Minute {
//	    log.Warn("Cache sync is stale", "last_sync", stats.LastSync)
//	}
type CacheStats struct {
	Size     int                                 // Number of silences in cache
	LastSync time.Time                           // Last cache rebuild time
	ByStatus map[silencing.SilenceStatus]int     // Count by status
}
