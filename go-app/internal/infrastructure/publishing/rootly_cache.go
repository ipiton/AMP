package publishing

import (
	"sync"
	"time"
)

// IncidentIDCache defines interface for incident ID tracking
type IncidentIDCache interface {
	Set(fingerprint, incidentID string)
	Get(fingerprint string) (incidentID string, exists bool)
	Delete(fingerprint string)
	Size() int
}

// cacheEntry holds cached incident ID with expiration
type cacheEntry struct {
	incidentID string
	expiresAt  time.Time
}

// inMemoryIncidentCache implements IncidentIDCache using sync.Map
type inMemoryIncidentCache struct {
	data     sync.Map
	ttl      time.Duration
	ticker   *time.Ticker
	stopChan chan struct{}
}

// NewIncidentIDCache creates a new incident ID cache with TTL
func NewIncidentIDCache(ttl time.Duration) IncidentIDCache {
	cache := &inMemoryIncidentCache{
		data:     sync.Map{},
		ttl:      ttl,
		ticker:   time.NewTicker(1 * time.Hour), // Cleanup every hour
		stopChan: make(chan struct{}),
	}

	// Start cleanup goroutine
	go cache.cleanup()

	return cache
}

// Set stores incident ID for fingerprint
func (c *inMemoryIncidentCache) Set(fingerprint, incidentID string) {
	c.data.Store(fingerprint, cacheEntry{
		incidentID: incidentID,
		expiresAt:  time.Now().Add(c.ttl),
	})
}

// Get retrieves incident ID for fingerprint
func (c *inMemoryIncidentCache) Get(fingerprint string) (string, bool) {
	value, exists := c.data.Load(fingerprint)
	if !exists {
		return "", false
	}

	entry := value.(cacheEntry)

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		c.data.Delete(fingerprint)
		return "", false
	}

	return entry.incidentID, true
}

// Delete removes incident ID for fingerprint
func (c *inMemoryIncidentCache) Delete(fingerprint string) {
	c.data.Delete(fingerprint)
}

// Size returns number of entries in cache
func (c *inMemoryIncidentCache) Size() int {
	count := 0
	c.data.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}

// cleanup removes expired entries periodically
func (c *inMemoryIncidentCache) cleanup() {
	for {
		select {
		case <-c.ticker.C:
			// Remove expired entries
			now := time.Now()
			c.data.Range(func(key, value interface{}) bool {
				entry := value.(cacheEntry)
				if now.After(entry.expiresAt) {
					c.data.Delete(key)
				}
				return true
			})
		case <-c.stopChan:
			c.ticker.Stop()
			return
		}
	}
}

// Stop stops the cleanup goroutine
func (c *inMemoryIncidentCache) Stop() {
	close(c.stopChan)
}
