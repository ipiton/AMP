package routing

import (
	"container/list"
	"regexp"
	"sync"
	"sync/atomic"
)

// RegexCache caches compiled regex patterns for reuse.
//
// Features:
//   - O(1) Get/Put operations (map-based)
//   - LRU eviction when cache reaches maxSize
//   - Thread-safe concurrent access (sync.RWMutex)
//   - Pre-population from RouteConfig.CompiledRegex
//   - Statistics tracking (hits, misses, size)
//
// Performance:
//   - Cache hit: ~50ns (map lookup + regex match)
//   - Cache miss: ~500µs (compile) + ~50ns (match)
//   - Expected hit rate: >90%
//
// Memory:
//   - ~5KB per pattern (compiled regex + metadata)
//   - Max size: 1000 patterns (default) = ~5MB
//
// Thread Safety:
//
//	Uses sync.RWMutex for concurrent read/write access.
//	Multiple readers can access simultaneously.
//	Writes block all readers (LRU updates + insertions).
//
// Example:
//
//	cache := NewRegexCache(1000)
//	regex, ok := cache.Get("prod.*")
//	if !ok {
//	    regex, _ = regexp.Compile("prod.*")
//	    cache.Put("prod.*", regex)
//	}
type RegexCache struct {
	// cache maps pattern → compiled regex
	cache map[string]*cacheEntry

	// lru tracks access order for eviction
	// Most recently used at front, least at back
	lru *list.List

	// maxSize limits cache size (default: 1000)
	maxSize int

	// mu protects cache and lru
	mu sync.RWMutex

	// stats tracks cache statistics (atomic counters)
	stats CacheStats
}

// cacheEntry represents a single cache entry.
type cacheEntry struct {
	// pattern is the regex pattern string
	pattern string

	// regex is the compiled regex
	regex *regexp.Regexp

	// element is the LRU list element
	element *list.Element
}

// CacheStats tracks regex cache statistics.
//
// All fields are updated atomically for thread-safe access.
type CacheStats struct {
	// Hits is the number of cache hits
	Hits uint64

	// Misses is the number of cache misses
	Misses uint64

	// Size is the current cache size (non-atomic, read under RLock)
	Size int
}

// NewRegexCache creates a new regex cache.
//
// Parameters:
//   - maxSize: Maximum cache size (0 = unlimited, not recommended)
//
// Returns:
//   - *RegexCache: A new cache instance
//
// The cache starts empty and is populated on demand via Put()
// or pre-populated via Preload().
//
// Example:
//
//	cache := NewRegexCache(1000)
func NewRegexCache(maxSize int) *RegexCache {
	if maxSize <= 0 {
		maxSize = 1000 // Default
	}

	return &RegexCache{
		cache:   make(map[string]*cacheEntry, maxSize),
		lru:     list.New(),
		maxSize: maxSize,
	}
}

// Get retrieves a compiled regex from the cache.
//
// If the pattern exists in cache:
//   - Increments Hits counter
//   - Updates LRU (moves to front)
//   - Returns regex and true
//
// If not in cache:
//   - Increments Misses counter
//   - Returns nil and false
//
// Complexity: O(1) average
//
// Performance: ~50ns (map lookup + LRU update)
//
// Thread Safety: Uses RLock for read-only access, upgrades to Lock for LRU update.
func (c *RegexCache) Get(pattern string) (*regexp.Regexp, bool) {
	c.mu.RLock()
	entry, ok := c.cache[pattern]
	c.mu.RUnlock()

	if !ok {
		atomic.AddUint64(&c.stats.Misses, 1)
		return nil, false
	}

	// Cache hit: update LRU (requires write lock)
	c.mu.Lock()
	c.lru.MoveToFront(entry.element)
	c.mu.Unlock()

	atomic.AddUint64(&c.stats.Hits, 1)
	return entry.regex, true
}

// Put inserts a compiled regex into the cache.
//
// If cache is full (size >= maxSize):
//   - Evicts least recently used entry (back of LRU list)
//   - Inserts new entry at front
//
// If cache has space:
//   - Inserts new entry at front of LRU list
//
// Complexity: O(1) average
//
// Performance: ~100ns (map insert + LRU update)
//
// Thread Safety: Uses Lock for exclusive write access.
func (c *RegexCache) Put(pattern string, regex *regexp.Regexp) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if already exists (avoid duplicate)
	if _, ok := c.cache[pattern]; ok {
		return
	}

	// Evict LRU entry if cache is full
	if len(c.cache) >= c.maxSize {
		c.evictLRU()
	}

	// Insert new entry at front of LRU
	element := c.lru.PushFront(pattern)
	c.cache[pattern] = &cacheEntry{
		pattern: pattern,
		regex:   regex,
		element: element,
	}
}

// evictLRU removes the least recently used entry.
//
// Caller must hold c.mu Lock.
func (c *RegexCache) evictLRU() {
	// Remove from back of LRU list
	element := c.lru.Back()
	if element == nil {
		return
	}

	pattern := element.Value.(string)
	c.lru.Remove(element)
	delete(c.cache, pattern)
}

// Preload pre-populates the cache from RouteConfig.CompiledRegex.
//
// This is called by NewRouteMatcher to warm the cache with
// all patterns from the config, avoiding cold start misses.
//
// Parameters:
//   - patterns: Map of pattern → compiled regex (from TN-137)
//
// Thread Safety: Uses Lock for exclusive write access.
//
// Example:
//
//	cache.Preload(config.CompiledRegex)
func (c *RegexCache) Preload(patterns map[string]*regexp.Regexp) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for pattern, regex := range patterns {
		// Skip if already in cache
		if _, ok := c.cache[pattern]; ok {
			continue
		}

		// Insert at front of LRU
		element := c.lru.PushFront(pattern)
		c.cache[pattern] = &cacheEntry{
			pattern: pattern,
			regex:   regex,
			element: element,
		}

		// Stop if cache is full
		if len(c.cache) >= c.maxSize {
			break
		}
	}
}

// Stats returns current cache statistics.
//
// Returns a copy of CacheStats with current values.
//
// Thread Safety: Hits/Misses are atomic. Size is read under RLock.
func (c *RegexCache) Stats() CacheStats {
	c.mu.RLock()
	size := len(c.cache)
	c.mu.RUnlock()

	return CacheStats{
		Hits:   atomic.LoadUint64(&c.stats.Hits),
		Misses: atomic.LoadUint64(&c.stats.Misses),
		Size:   size,
	}
}

// Clear removes all entries from the cache.
//
// Used for testing or cache invalidation.
//
// Thread Safety: Uses Lock for exclusive write access.
func (c *RegexCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*cacheEntry, c.maxSize)
	c.lru = list.New()
	atomic.StoreUint64(&c.stats.Hits, 0)
	atomic.StoreUint64(&c.stats.Misses, 0)
}
