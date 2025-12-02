package publishing

import (
	"log/slog"
	"sync"
	"time"
)

// Event Key Cache for tracking fingerprint → dedup_key mappings
// Full implementation will be done in Phase 7

// EventKeyCache defines the interface for event key cache
type EventKeyCache interface {
	// Set stores a fingerprint → dedup_key mapping
	Set(fingerprint string, dedupKey string)

	// Get retrieves a dedup_key for a fingerprint
	Get(fingerprint string) (dedupKey string, found bool)

	// Delete removes a fingerprint → dedup_key mapping
	Delete(fingerprint string)

	// Cleanup removes expired entries (called by background worker)
	Cleanup()

	// Size returns the number of entries in cache
	Size() int
}

// pagerDutyCacheEntry represents a single cache entry
type pagerDutyCacheEntry struct {
	DedupKey  string
	CreatedAt time.Time
}

// eventKeyCacheImpl implements EventKeyCache using sync.Map
type eventKeyCacheImpl struct {
	data   sync.Map
	ttl    time.Duration
	logger *slog.Logger
}

// NewEventKeyCache creates a new event key cache with specified TTL
// This is a temporary stub for Phase 4
// Full implementation will be done in Phase 7
func NewEventKeyCache(ttl time.Duration) EventKeyCache {
	cache := &eventKeyCacheImpl{
		ttl:    ttl,
		logger: slog.Default(),
	}

	// Start background cleanup worker
	go cache.cleanupWorker()

	return cache
}

// Set stores a fingerprint → dedup_key mapping
func (c *eventKeyCacheImpl) Set(fingerprint string, dedupKey string) {
	entry := pagerDutyCacheEntry{
		DedupKey:  dedupKey,
		CreatedAt: time.Now(),
	}
	c.data.Store(fingerprint, entry)

	c.logger.Debug("Cache entry set",
		"fingerprint", fingerprint,
		"dedup_key", dedupKey,
	)
}

// Get retrieves a dedup_key for a fingerprint
func (c *eventKeyCacheImpl) Get(fingerprint string) (string, bool) {
	value, ok := c.data.Load(fingerprint)
	if !ok {
		return "", false
	}

	entry := value.(pagerDutyCacheEntry)

	// Check TTL
	if time.Since(entry.CreatedAt) > c.ttl {
		// Expired
		c.data.Delete(fingerprint)
		return "", false
	}

	return entry.DedupKey, true
}

// Delete removes a fingerprint → dedup_key mapping
func (c *eventKeyCacheImpl) Delete(fingerprint string) {
	c.data.Delete(fingerprint)

	c.logger.Debug("Cache entry deleted",
		"fingerprint", fingerprint,
	)
}

// Cleanup removes expired entries
func (c *eventKeyCacheImpl) Cleanup() {
	removed := 0
	c.data.Range(func(key, value interface{}) bool {
		entry := value.(pagerDutyCacheEntry)
		if time.Since(entry.CreatedAt) > c.ttl {
			c.data.Delete(key)
			removed++
		}
		return true
	})

	if removed > 0 {
		c.logger.Info("Cache cleanup completed",
			"removed", removed,
		)
	}
}

// Size returns the number of entries in cache
func (c *eventKeyCacheImpl) Size() int {
	count := 0
	c.data.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// cleanupWorker runs periodic cleanup
func (c *eventKeyCacheImpl) cleanupWorker() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		c.Cleanup()
	}
}
