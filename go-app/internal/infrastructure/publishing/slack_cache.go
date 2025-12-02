package publishing

import (
	"sync"
	"time"
)

// slack_cache.go - Message ID cache for tracking Slack message timestamps (threading support)
// Uses sync.Map for thread-safe concurrent access, 24h TTL, background cleanup worker

// MessageEntry represents a cached Slack message
type MessageEntry struct {
	MessageTS string    // Message timestamp (ts) returned by Slack
	ThreadTS  string    // Thread timestamp (thread_ts) for replies
	CreatedAt time.Time // Cache creation time (for TTL)
}

// MessageIDCache stores alert fingerprint → MessageEntry mappings
// Enables threading: resolved/still-firing alerts reply to original message
type MessageIDCache interface {
	// Store saves MessageEntry for alert fingerprint
	Store(fingerprint string, entry *MessageEntry)

	// Get retrieves MessageEntry for alert fingerprint
	// Returns (entry, true) if found, (nil, false) if not found
	Get(fingerprint string) (*MessageEntry, bool)

	// Delete removes MessageEntry for alert fingerprint
	Delete(fingerprint string)

	// Cleanup removes expired entries (TTL 24h)
	// Should be called periodically by background worker
	Cleanup(ttl time.Duration) int

	// Size returns current cache size
	Size() int
}

// DefaultMessageCache implements MessageIDCache using sync.Map
// Thread-safe concurrent access, O(1) lookups, 24h TTL with background cleanup
type DefaultMessageCache struct {
	cache sync.Map // fingerprint → MessageEntry
}

// NewMessageCache creates a new message cache
func NewMessageCache() MessageIDCache {
	return &DefaultMessageCache{}
}

// Store saves MessageEntry for alert fingerprint
func (c *DefaultMessageCache) Store(fingerprint string, entry *MessageEntry) {
	c.cache.Store(fingerprint, entry)
}

// Get retrieves MessageEntry for alert fingerprint
func (c *DefaultMessageCache) Get(fingerprint string) (*MessageEntry, bool) {
	value, ok := c.cache.Load(fingerprint)
	if !ok {
		return nil, false
	}

	entry, ok := value.(*MessageEntry)
	if !ok {
		return nil, false
	}

	return entry, true
}

// Delete removes MessageEntry for alert fingerprint
func (c *DefaultMessageCache) Delete(fingerprint string) {
	c.cache.Delete(fingerprint)
}

// Cleanup removes expired entries (TTL 24h)
// Returns number of deleted entries
func (c *DefaultMessageCache) Cleanup(ttl time.Duration) int {
	deleted := 0
	now := time.Now()

	c.cache.Range(func(key, value interface{}) bool {
		entry, ok := value.(*MessageEntry)
		if !ok {
			// Invalid entry, delete it
			c.cache.Delete(key)
			deleted++
			return true
		}

		// Check TTL
		if now.Sub(entry.CreatedAt) > ttl {
			c.cache.Delete(key)
			deleted++
		}

		return true
	})

	return deleted
}

// Size returns current cache size
func (c *DefaultMessageCache) Size() int {
	size := 0
	c.cache.Range(func(_, _ interface{}) bool {
		size++
		return true
	})
	return size
}

// StartCleanupWorker starts background cleanup worker
// Runs every interval, removes entries older than TTL (24h)
// Returns cancel function to stop worker
func StartCleanupWorker(cache MessageIDCache, interval, ttl time.Duration) func() {
	ticker := time.NewTicker(interval)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				deleted := cache.Cleanup(ttl)
				if deleted > 0 {
					// Log cleanup (metrics will be added in Phase 6)
					_ = deleted
				}
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()

	// Return cancel function
	return func() {
		close(done)
	}
}
