package silencing

import (
	"regexp"
	"sync"
)

// anchorSilenceRegex wraps a raw matcher pattern so it must match the
// WHOLE label value, not merely contain a substring matching it —
// upstream Alertmanager semantics (`pkg/labels/matcher.go`'s
// `NewMatcher`: `regexp.Compile("^(?:" + v + ")$")`), and the same
// anchoring `internal/business/routing.anchorRegex` and
// `internal/infrastructure/inhibition.anchorMatcherRegex` already apply
// for route matchers and inhibit rules respectively.
//
// Review fix round 2 (R1 / silencing's own copy of the anchoring
// divergence): before this fix, RegexCache.Get compiled the raw pattern
// unanchored, so a silence matcher `job=~"prod"` would ALSO silence an
// alert with `job="preprod-2"` (substring match) — the over-silencing
// direction, and the more dangerous one of the two directions this class
// of bug can go in (a route/inhibition anchoring bug misses a match; a
// silence anchoring bug SUPPRESSES real alerts nobody asked to suppress).
// Kept as a literal, independently-duplicated helper (not a shared
// import) for the same reason the other two sites are: each package's
// copy is meant to stay easy to audit on its own, and pkg/ - style
// leaf-level packages avoid depending on internal/ siblings.
func anchorSilenceRegex(pattern string) string {
	return "^(?:" + pattern + ")$"
}

// RegexCache caches compiled regex patterns for performance optimization.
//
// Regex compilation is expensive (~5µs per pattern). Without caching, matching
// 100 alerts against 100 silences with 3 regex matchers each would require
// 30,000 compilations (150ms total). With 80% cache hit rate, this drops to
// 3ms - a 50x improvement.
//
// Thread-safety: RegexCache is safe for concurrent access using RWMutex.
// Multiple goroutines can safely call Get() simultaneously.
//
// Eviction Strategy: Simple clear when cache reaches maxSize. A more sophisticated
// LRU eviction could be added later if needed, but simple clearing is sufficient
// for typical usage patterns (stable set of silences).
//
// Memory Usage: Each cached pattern uses ~500 bytes. Default maxSize of 1000
// patterns = ~500 KB total memory, which is acceptable overhead.
//
// Example usage:
//
//	cache := NewRegexCache(1000)
//
//	// First call: compile and cache (slow path)
//	re1, err := cache.Get(".*-prod-.*")  // ~5µs
//
//	// Subsequent calls: cache hit (fast path)
//	re2, err := cache.Get(".*-prod-.*")  // ~10ns (500x faster!)
//	// re1 == re2 (same *regexp.Regexp instance)
type RegexCache struct {
	// mu protects concurrent access to cache map.
	// Uses RWMutex for better read performance (multiple readers, single writer).
	mu sync.RWMutex

	// cache stores compiled regex patterns keyed by pattern string.
	// Key: regex pattern string (e.g., ".*-prod-.*")
	// Value: compiled *regexp.Regexp
	cache map[string]*regexp.Regexp

	// maxSize is the maximum number of patterns to cache.
	// When exceeded, entire cache is cleared (simple eviction strategy).
	// Default: 1000 patterns (~500 KB memory)
	maxSize int
}

// NewRegexCache creates a new RegexCache with the specified maximum size.
//
// Parameters:
//   - maxSize: Maximum number of regex patterns to cache. When exceeded,
//     the entire cache is cleared. Recommended values:
//   - 100: Small deployments (<100 silences)
//   - 1000: Medium deployments (100-1000 silences) [DEFAULT]
//   - 10000: Large deployments (>1000 silences)
//
// Memory usage: ~500 bytes per cached pattern
//
// Example:
//
//	cache := NewRegexCache(1000)  // 1000 patterns max (~500 KB)
func NewRegexCache(maxSize int) *RegexCache {
	return &RegexCache{
		cache:   make(map[string]*regexp.Regexp, maxSize),
		maxSize: maxSize,
	}
}

// Get returns a compiled regex for the given pattern.
//
// If the pattern is already cached, returns it immediately (fast path ~10ns).
// If not cached, compiles the pattern and caches it for future use (slow path ~5µs).
//
// Performance:
//   - Cache hit: ~10ns (RLock + map lookup)
//   - Cache miss: ~5µs (compile + Lock + insert)
//
// Eviction: When cache is full (len(cache) >= maxSize), the entire cache is
// cleared before inserting the new pattern. This ensures memory usage stays bounded.
//
// Thread-safety: Multiple goroutines can safely call Get() concurrently.
// Uses double-checked locking pattern for optimal performance.
//
// Errors:
//   - Returns error if pattern is invalid regex syntax
//
// Anchoring (review fix round 2, R1): the compiled regex is anchored
// `^(?:pattern)$` (see anchorSilenceRegex), matching upstream
// Alertmanager semantics — `re.MatchString("critical")` below requires
// the WHOLE label value to equal "critical", not merely contain it. The
// cache key remains the raw (unanchored) pattern string so callers pass
// the same value they configured; only the compiled *regexp.Regexp is
// anchored.
//
// Example:
//
//	cache := NewRegexCache(1000)
//
//	// First call: compile and cache
//	re, err := cache.Get("(critical|warning)")
//	if err != nil {
//	    return fmt.Errorf("invalid regex: %w", err)
//	}
//	matched := re.MatchString("critical")  // true
//	matched = re.MatchString("critical-ish")  // false (anchored)
//
//	// Second call: cache hit (same pattern)
//	re2, _ := cache.Get("(critical|warning)")
//	// re == re2 (same instance)
func (rc *RegexCache) Get(pattern string) (*regexp.Regexp, error) {
	// Fast path: Try read lock first (optimistic cache hit)
	// This allows multiple concurrent readers without blocking
	rc.mu.RLock()
	if re, ok := rc.cache[pattern]; ok {
		rc.mu.RUnlock()
		return re, nil
	}
	rc.mu.RUnlock()

	// Slow path: Not in cache, need to compile and insert
	// Acquire write lock for exclusive access
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// Double-check: Another goroutine may have inserted while we waited for lock
	if re, ok := rc.cache[pattern]; ok {
		return re, nil
	}

	// Compile regex pattern, anchored to the whole label value (upstream
	// semantics — see anchorSilenceRegex's doc comment above).
	re, err := regexp.Compile(anchorSilenceRegex(pattern))
	if err != nil {
		// Return compilation error without caching
		return nil, err
	}

	// Eviction strategy: Clear entire cache if full
	// This is simpler than LRU but sufficient for stable silence sets
	if len(rc.cache) >= rc.maxSize {
		rc.cache = make(map[string]*regexp.Regexp, rc.maxSize)
	}

	// Cache the compiled pattern
	rc.cache[pattern] = re
	return re, nil
}

// Size returns the current number of cached patterns.
//
// This method is primarily for testing and monitoring purposes.
// It acquires a read lock, so it's safe to call concurrently.
//
// Example:
//
//	cache := NewRegexCache(1000)
//	cache.Get("pattern1")
//	cache.Get("pattern2")
//	fmt.Println(cache.Size())  // Output: 2
func (rc *RegexCache) Size() int {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return len(rc.cache)
}

// Clear removes all cached patterns from the cache.
//
// This method is primarily for testing purposes. In production, the cache
// automatically clears itself when it reaches maxSize.
//
// Thread-safety: Safe to call concurrently with other methods.
//
// Example:
//
//	cache := NewRegexCache(1000)
//	cache.Get("pattern1")
//	cache.Get("pattern2")
//	fmt.Println(cache.Size())  // Output: 2
//	cache.Clear()
//	fmt.Println(cache.Size())  // Output: 0
func (rc *RegexCache) Clear() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.cache = make(map[string]*regexp.Regexp, rc.maxSize)
}
