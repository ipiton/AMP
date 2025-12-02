package publishing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// CachingMiddleware caches formatted alert results.
//
// Cache key: Hash of (Fingerprint + Format + Classification)
// Cache value: Formatted alert payload
// TTL: Configurable (default 5 minutes)
//
// Benefits:
//   - Reduces formatting overhead for duplicate alerts
//   - Improves throughput under high load
//   - Reduces CPU usage
//
// Metrics:
//   - Cache hit rate (target: 30%+)
//   - Cache miss count
//   - Cache eviction count
type CachingMiddleware struct {
	cache FormatterCache
}

// FormatterCache defines caching interface for formatted alerts.
type FormatterCache interface {
	// Get retrieves cached formatted alert
	//
	// Parameters:
	//   key: Cache key (hash of alert + format)
	//
	// Returns:
	//   value: Cached payload
	//   found: true if cache hit, false if cache miss
	Get(key string) (value map[string]any, found bool)

	// Set stores formatted alert in cache
	//
	// Parameters:
	//   key: Cache key
	//   value: Formatted payload
	//   ttl: Time-to-live (0 = use default TTL)
	Set(key string, value map[string]any, ttl time.Duration)

	// Delete removes cached alert
	Delete(key string)

	// Clear removes all cached alerts
	Clear()

	// Stats returns cache statistics
	Stats() CacheStats
}

// CacheStats provides cache performance metrics
type CacheStats struct {
	Hits      int64   // Cache hits
	Misses    int64   // Cache misses
	Evictions int64   // Cache evictions (LRU)
	Size      int     // Current cache size
	Capacity  int     // Maximum cache capacity
	HitRate   float64 // Hit rate (hits / (hits + misses))
}

// NewCachingMiddleware creates caching middleware.
//
// Parameters:
//   cache: Cache implementation (LRU, Redis, etc.)
//
// Returns:
//   FormatterMiddleware: Caching middleware
func NewCachingMiddleware(cache FormatterCache) FormatterMiddleware {
	return func(next formatFunc) formatFunc {
		return func(alert *core.EnrichedAlert) (map[string]any, error) {
			// Generate cache key
			key := generateCacheKey(alert)

			// Check cache
			if cached, found := cache.Get(key); found {
				// Cache hit - return cached result
				return cached, nil
			}

			// Cache miss - call next formatter
			result, err := next(alert)
			if err != nil {
				// Don't cache errors
				return nil, err
			}

			// Store result in cache (5 minute TTL)
			cache.Set(key, result, 5*time.Minute)

			return result, nil
		}
	}
}

// generateCacheKey creates a deterministic cache key from alert.
//
// Key components:
//   - Alert fingerprint (unique identifier)
//   - Classification severity + confidence (affects formatting)
//   - Classification reasoning (affects Slack/Rootly descriptions)
//
// Algorithm: SHA-256 hash of JSON-serialized components
//
// Returns: Hex-encoded hash (64 characters)
func generateCacheKey(alert *core.EnrichedAlert) string {
	// Build key components
	components := map[string]any{
		"fingerprint": alert.Alert.Fingerprint,
		"status":      string(alert.Alert.Status),
	}

	// Include classification if present (affects formatting)
	if alert.Classification != nil {
		components["severity"] = string(alert.Classification.Severity)
		components["confidence"] = alert.Classification.Confidence
		// Include first 100 chars of reasoning (for uniqueness, not full text)
		if len(alert.Classification.Reasoning) > 100 {
			components["reasoning_prefix"] = alert.Classification.Reasoning[:100]
		} else {
			components["reasoning_prefix"] = alert.Classification.Reasoning
		}
	}

	// Serialize to JSON
	jsonBytes, _ := json.Marshal(components)

	// Hash with SHA-256
	hash := sha256.Sum256(jsonBytes)

	// Return hex-encoded hash
	return hex.EncodeToString(hash[:])
}

// InMemoryCache is a simple in-memory cache implementation.
//
// NOT thread-safe (wrap with sync.RWMutex if needed).
// Use for testing or single-threaded scenarios.
type InMemoryCache struct {
	entries  map[string]*formatterCacheEntry
	hits     int64
	misses   int64
	capacity int
}

type formatterCacheEntry struct {
	value     map[string]any
	expiresAt time.Time
}

// NewInMemoryCache creates an in-memory cache.
//
// Parameters:
//   capacity: Maximum number of entries (LRU eviction when full)
//
// Returns:
//   FormatterCache: In-memory cache
func NewInMemoryCache(capacity int) FormatterCache {
	return &InMemoryCache{
		entries:  make(map[string]*formatterCacheEntry, capacity),
		capacity: capacity,
	}
}

// Get implements FormatterCache.Get
func (c *InMemoryCache) Get(key string) (map[string]any, bool) {
	entry, exists := c.entries[key]
	if !exists {
		c.misses++
		return nil, false
	}

	// Check expiration
	if time.Now().After(entry.expiresAt) {
		// Expired - delete and return miss
		delete(c.entries, key)
		c.misses++
		return nil, false
	}

	// Cache hit
	c.hits++
	return entry.value, true
}

// Set implements FormatterCache.Set
func (c *InMemoryCache) Set(key string, value map[string]any, ttl time.Duration) {
	// Default TTL: 5 minutes
	if ttl == 0 {
		ttl = 5 * time.Minute
	}

	// Check capacity (simple eviction: delete oldest)
	if len(c.entries) >= c.capacity {
		// Find oldest entry
		var oldestKey string
		var oldestTime time.Time
		for k, e := range c.entries {
			if oldestKey == "" || e.expiresAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = e.expiresAt
			}
		}
		// Delete oldest
		if oldestKey != "" {
			delete(c.entries, oldestKey)
		}
	}

	// Store entry
	c.entries[key] = &formatterCacheEntry{
		value:     value,
		expiresAt: time.Now().Add(ttl),
	}
}

// Delete implements FormatterCache.Delete
func (c *InMemoryCache) Delete(key string) {
	delete(c.entries, key)
}

// Clear implements FormatterCache.Clear
func (c *InMemoryCache) Clear() {
	c.entries = make(map[string]*formatterCacheEntry, c.capacity)
	c.hits = 0
	c.misses = 0
}

// Stats implements FormatterCache.Stats
func (c *InMemoryCache) Stats() CacheStats {
	total := c.hits + c.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return CacheStats{
		Hits:     c.hits,
		Misses:   c.misses,
		Size:     len(c.entries),
		Capacity: c.capacity,
		HitRate:  hitRate,
	}
}
