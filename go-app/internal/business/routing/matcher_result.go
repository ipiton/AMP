package routing

import (
	"time"
)

// MatchResult contains the result of route matching.
//
// Includes:
//   - Matched routes (in match order)
//   - Match duration
//   - Statistics (matchers evaluated, cache hits/misses)
//
// Used by RouteMatcher.FindMatchingRoutes() to return
// both the matches and debug information.
type MatchResult struct {
	// Matches are the matched route nodes (in order)
	//
	// If len(Matches) == 0: no matching route, use root default
	// If len(Matches) > 0: use first match (Matches[0])
	Matches []*RouteNode

	// Duration is the total matching time
	//
	// Includes tree traversal + matcher evaluation + cache lookups
	Duration time.Duration

	// MatchersEvaluated is the count of matchers checked
	//
	// Total across all visited nodes
	MatchersEvaluated int

	// CacheHits is the regex cache hit count during this match
	CacheHits int

	// CacheMisses is the regex cache miss count during this match
	CacheMisses int
}

// Empty returns true if no routes matched.
//
// When Empty() is true, the caller should use the root node's
// receiver as the default.
func (r *MatchResult) Empty() bool {
	return len(r.Matches) == 0
}

// First returns the first matched route.
//
// Returns nil if no matches.
//
// In Alertmanager routing, the first match is used
// (unless continue=true, then all matches are processed).
func (r *MatchResult) First() *RouteNode {
	if len(r.Matches) == 0 {
		return nil
	}
	return r.Matches[0]
}

// Count returns the number of matched routes.
func (r *MatchResult) Count() int {
	return len(r.Matches)
}

// CacheHitRate returns the regex cache hit rate (0-1).
//
// Returns 0 if no cache accesses occurred.
func (r *MatchResult) CacheHitRate() float64 {
	total := r.CacheHits + r.CacheMisses
	if total == 0 {
		return 0
	}
	return float64(r.CacheHits) / float64(total)
}
