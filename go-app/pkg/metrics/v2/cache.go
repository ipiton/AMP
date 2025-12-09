package v2

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const cacheSubsystem = "cache"

// Cache type constants for consistent labeling.
const (
	CacheTypeRedis    = "redis"
	CacheTypeMemory   = "memory"
	CacheTypeL1       = "l1"
	CacheTypeL2       = "l2"
	CacheTypeTemplate = "template"
)

// CacheMetrics provides metrics for caching operations.
//
// This struct consolidates cache metrics from various places:
//   - pkg/metrics/metrics.go (ClassificationMetrics L1/L2 cache)
//   - internal/infrastructure/publishing/slack_metrics.go (cache hits/misses)
//   - internal/ui/template_metrics.go (template cache)
//
// Migration:
//
//	Old: classificationMetrics.L1CacheHits.Inc()
//	New: registry.Cache.RecordHit(v2.CacheTypeL1, "classification")
type CacheMetrics struct {
	// ========================================================================
	// Core Cache Metrics
	// ========================================================================

	// hitsTotal counts cache hits by cache type and key type.
	// Labels: cache_type (redis/memory/l1/l2/template), key_type (classification/message_id/etc)
	hitsTotal *prometheus.CounterVec

	// missesTotal counts cache misses by cache type and key type.
	// Labels: cache_type, key_type
	missesTotal *prometheus.CounterVec

	// operationsTotal counts cache operations by cache type, operation, and status.
	// Labels: cache_type, operation (get/set/delete/expire), status (success/error)
	operationsTotal *prometheus.CounterVec

	// operationDurationSeconds measures operation duration.
	// Labels: cache_type, operation
	operationDurationSeconds *prometheus.HistogramVec

	// ========================================================================
	// Capacity Metrics
	// ========================================================================

	// sizeGauge tracks current cache size (number of entries) by cache type.
	// Labels: cache_type
	sizeGauge *prometheus.GaugeVec

	// evictionsTotal counts cache evictions by cache type and reason.
	// Labels: cache_type, reason (ttl/lru/memory_pressure)
	evictionsTotal *prometheus.CounterVec

	// ========================================================================
	// Redis-specific Metrics
	// ========================================================================

	// redisConnectionsActive tracks active Redis connections.
	redisConnectionsActive prometheus.Gauge

	// redisCommandsTotal counts Redis commands by command type and status.
	// Labels: command, status
	redisCommandsTotal *prometheus.CounterVec

	// redisLatencySeconds measures Redis command latency.
	// Labels: command
	redisLatencySeconds *prometheus.HistogramVec
}

// NewCacheMetrics creates and registers all cache metrics.
func NewCacheMetrics(registerer prometheus.Registerer) *CacheMetrics {
	m := &CacheMetrics{}

	// Core Cache Metrics
	m.hitsTotal = newCounterVec(registerer, cacheSubsystem,
		"hits_total",
		"Cache hits by cache type and key type",
		[]string{"cache_type", "key_type"})

	m.missesTotal = newCounterVec(registerer, cacheSubsystem,
		"misses_total",
		"Cache misses by cache type and key type",
		[]string{"cache_type", "key_type"})

	m.operationsTotal = newCounterVec(registerer, cacheSubsystem,
		"operations_total",
		"Cache operations by cache type, operation, and status",
		[]string{"cache_type", "operation", "status"})

	m.operationDurationSeconds = newHistogramVec(registerer, cacheSubsystem,
		"operation_duration_seconds",
		"Cache operation duration in seconds",
		[]float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
		[]string{"cache_type", "operation"})

	// Capacity Metrics
	m.sizeGauge = newGaugeVec(registerer, cacheSubsystem,
		"size",
		"Current cache size (number of entries) by cache type",
		[]string{"cache_type"})

	m.evictionsTotal = newCounterVec(registerer, cacheSubsystem,
		"evictions_total",
		"Cache evictions by cache type and reason",
		[]string{"cache_type", "reason"})

	// Redis-specific
	m.redisConnectionsActive = newGauge(registerer, cacheSubsystem,
		"redis_connections_active",
		"Number of active Redis connections")

	m.redisCommandsTotal = newCounterVec(registerer, cacheSubsystem,
		"redis_commands_total",
		"Redis commands by command type and status",
		[]string{"command", "status"})

	m.redisLatencySeconds = newHistogramVec(registerer, cacheSubsystem,
		"redis_latency_seconds",
		"Redis command latency in seconds",
		[]float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1},
		[]string{"command"})

	return m
}

// ============================================================================
// Core Cache Methods
// ============================================================================

// RecordHit records a cache hit.
func (m *CacheMetrics) RecordHit(cacheType, keyType string) {
	m.hitsTotal.WithLabelValues(cacheType, keyType).Inc()
}

// RecordMiss records a cache miss.
func (m *CacheMetrics) RecordMiss(cacheType, keyType string) {
	m.missesTotal.WithLabelValues(cacheType, keyType).Inc()
}

// RecordOperation records a cache operation.
func (m *CacheMetrics) RecordOperation(cacheType, operation string, success bool, duration time.Duration) {
	status := "error"
	if success {
		status = "success"
	}
	m.operationsTotal.WithLabelValues(cacheType, operation, status).Inc()
	m.operationDurationSeconds.WithLabelValues(cacheType, operation).Observe(duration.Seconds())
}

// RecordGet is a convenience method for recording a get operation with hit/miss.
func (m *CacheMetrics) RecordGet(cacheType, keyType string, hit bool, duration time.Duration) {
	if hit {
		m.hitsTotal.WithLabelValues(cacheType, keyType).Inc()
	} else {
		m.missesTotal.WithLabelValues(cacheType, keyType).Inc()
	}
	m.operationsTotal.WithLabelValues(cacheType, "get", "success").Inc()
	m.operationDurationSeconds.WithLabelValues(cacheType, "get").Observe(duration.Seconds())
}

// ============================================================================
// Capacity Methods
// ============================================================================

// SetSize sets the cache size.
func (m *CacheMetrics) SetSize(cacheType string, size int) {
	m.sizeGauge.WithLabelValues(cacheType).Set(float64(size))
}

// RecordEviction records a cache eviction.
func (m *CacheMetrics) RecordEviction(cacheType, reason string) {
	m.evictionsTotal.WithLabelValues(cacheType, reason).Inc()
}

// ============================================================================
// Redis Methods
// ============================================================================

// SetRedisConnectionsActive sets the number of active Redis connections.
func (m *CacheMetrics) SetRedisConnectionsActive(count int) {
	m.redisConnectionsActive.Set(float64(count))
}

// RecordRedisCommand records a Redis command.
func (m *CacheMetrics) RecordRedisCommand(command string, success bool, duration time.Duration) {
	status := "error"
	if success {
		status = "success"
	}
	m.redisCommandsTotal.WithLabelValues(command, status).Inc()
	m.redisLatencySeconds.WithLabelValues(command).Observe(duration.Seconds())
}

// ============================================================================
// Convenience Methods for L1/L2 Cache (Classification)
// ============================================================================

// RecordL1CacheHit records an L1 (in-memory) cache hit for classification.
func (m *CacheMetrics) RecordL1CacheHit() {
	m.hitsTotal.WithLabelValues(CacheTypeL1, "classification").Inc()
}

// RecordL2CacheHit records an L2 (Redis) cache hit for classification.
func (m *CacheMetrics) RecordL2CacheHit() {
	m.hitsTotal.WithLabelValues(CacheTypeL2, "classification").Inc()
}

// RecordCacheMiss records a cache miss for classification (both L1 and L2).
func (m *CacheMetrics) RecordCacheMiss() {
	m.missesTotal.WithLabelValues(CacheTypeL1, "classification").Inc()
	m.missesTotal.WithLabelValues(CacheTypeL2, "classification").Inc()
}
