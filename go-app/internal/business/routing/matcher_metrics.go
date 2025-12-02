package routing

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// MatcherMetrics tracks Prometheus metrics for route matching.
//
// Metrics:
//   - route_matches_total: Count of matches by route path
//   - route_match_duration_seconds: Histogram of matching latency
//   - regex_cache_hits_total: Count of regex cache hits
//   - regex_cache_misses_total: Count of regex cache misses
//   - regex_cache_size: Current regex cache size (gauge)
//
// All metrics are prefixed with "alert_history_routing_" namespace.
type MatcherMetrics struct {
	// MatchesTotal counts matches by route path
	MatchesTotal *prometheus.CounterVec

	// MatchDuration tracks matching latency
	MatchDuration prometheus.Histogram

	// RegexCacheHits counts cache hits
	RegexCacheHits prometheus.Counter

	// RegexCacheMisses counts cache misses
	RegexCacheMisses prometheus.Counter

	// RegexCacheSize tracks current cache size
	RegexCacheSize prometheus.Gauge
}

// NewMatcherMetrics creates Prometheus metrics for RouteMatcher.
//
// All metrics are auto-registered with the default Prometheus registry.
//
// Returns:
//   - *MatcherMetrics: A new metrics instance
//
// Example:
//
//	metrics := NewMatcherMetrics()
//	metrics.RecordMatch("/routes[0]", 50*time.Microsecond)
func NewMatcherMetrics() *MatcherMetrics {
	return &MatcherMetrics{
		MatchesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "matches_total",
				Help:      "Total number of route matches by route path",
			},
			[]string{"route_path"},
		),

		MatchDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "match_duration_seconds",
				Help:      "Time to find matching routes",
				// Buckets: 10µs to 10ms (exponential)
				Buckets: prometheus.ExponentialBuckets(0.00001, 2, 10),
			},
		),

		RegexCacheHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "regex_cache_hits_total",
				Help:      "Total number of regex cache hits",
			},
		),

		RegexCacheMisses: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "regex_cache_misses_total",
				Help:      "Total number of regex cache misses",
			},
		),

		RegexCacheSize: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "regex_cache_size",
				Help:      "Current size of regex cache",
			},
		),
	}
}

// RecordMatch records a successful route match.
//
// Parameters:
//   - routePath: The path of the matched route (e.g., "/routes[0]")
//   - duration: The matching duration
//
// Updates:
//   - MatchesTotal counter (by route_path label)
//   - MatchDuration histogram
func (m *MatcherMetrics) RecordMatch(routePath string, duration time.Duration) {
	m.MatchesTotal.WithLabelValues(routePath).Inc()
	m.MatchDuration.Observe(duration.Seconds())
}

// UpdateCacheStats updates regex cache statistics.
//
// Parameters:
//   - stats: Current cache statistics
//
// Updates:
//   - RegexCacheSize gauge
//
// Note: RegexCacheHits and RegexCacheMisses are updated
// directly in RouteMatcher.regexMatch()
func (m *MatcherMetrics) UpdateCacheStats(stats CacheStats) {
	m.RegexCacheSize.Set(float64(stats.Size))
}
