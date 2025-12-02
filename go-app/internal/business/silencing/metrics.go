package silencing

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsInstance *SilenceMetrics
	metricsOnce     sync.Once
)

// SilenceMetrics provides Prometheus metrics for SilenceManager.
//
// Metrics exported:
//  1. silence_manager_operations_total - Counter of all operations (CRUD, filtering)
//  2. silence_manager_operation_duration_seconds - Histogram of operation durations
//  3. silence_manager_errors_total - Counter of errors by operation and type
//  4. silence_manager_active_silences - Gauge of active silences count
//  5. silence_manager_cache_operations_total - Counter of cache hits/misses
//  6. silence_manager_gc_runs_total - Counter of GC worker runs
//  7. silence_manager_gc_cleaned_total - Counter of silences cleaned by GC
//  8. silence_manager_sync_runs_total - Counter of sync worker runs
//
// Usage:
//
//	metrics := NewSilenceMetrics()
//	metrics.Operations.WithLabelValues("create", "success").Inc()
//	metrics.RecordOperation("create", time.Since(start), nil)
type SilenceMetrics struct {
	// 1. Operations counter (operation: create/get/update/delete/list/filter, status: success/error)
	Operations *prometheus.CounterVec

	// 2. Operation duration histogram (operation: create/get/update/delete/list/filter)
	OperationDuration *prometheus.HistogramVec

	// 3. Errors counter (operation: create/get/update/delete/list/filter/gc/sync, type: validation/repository/cache/timeout)
	Errors *prometheus.CounterVec

	// 4. Active silences gauge (status: active/pending/expired)
	ActiveSilences *prometheus.GaugeVec

	// 5. Cache operations (type: hit/miss, operation: get/list)
	CacheOperations *prometheus.CounterVec

	// 6. GC worker runs (phase: expire/delete)
	GCRuns *prometheus.CounterVec

	// 7. GC silences cleaned (phase: expire/delete)
	GCCleaned *prometheus.CounterVec

	// 8. Sync worker runs
	SyncRuns prometheus.Counter

	// Internal counters for GetStats()
	cacheHits     atomic.Uint64
	cacheMisses   atomic.Uint64
	gcTotalRuns   atomic.Uint64
	syncTotalRuns atomic.Uint64
	startTime     time.Time
}

// NewSilenceMetrics creates and registers Prometheus metrics for SilenceManager.
//
// This function uses singleton pattern to ensure metrics are registered only once.
// Safe to call multiple times (returns the same instance).
//
// Returns:
//   - *SilenceMetrics: Initialized metrics struct (singleton)
//
// Example:
//
//	metrics := NewSilenceMetrics()
//	metrics.RecordOperation("create", duration, nil)
func NewSilenceMetrics() *SilenceMetrics {
	metricsOnce.Do(func() {
		metricsInstance = &SilenceMetrics{
		// 1. Operations counter
		Operations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert_history_business_silence_manager_operations_total",
				Help: "Total number of silence manager operations by type and status",
			},
			[]string{"operation", "status"},
		),

		// 2. Operation duration histogram
		OperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "alert_history_business_silence_manager_operation_duration_seconds",
				Help:    "Duration of silence manager operations in seconds",
				Buckets: prometheus.DefBuckets, // [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]
			},
			[]string{"operation"},
		),

		// 3. Errors counter
		Errors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert_history_business_silence_manager_errors_total",
				Help: "Total number of errors in silence manager by operation and type",
			},
			[]string{"operation", "type"},
		),

		// 4. Active silences gauge
		ActiveSilences: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "alert_history_business_silence_manager_active_silences",
				Help: "Current number of silences by status",
			},
			[]string{"status"},
		),

		// 5. Cache operations
		CacheOperations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert_history_business_silence_manager_cache_operations_total",
				Help: "Total number of cache operations by type",
			},
			[]string{"type", "operation"},
		),

		// 6. GC runs
		GCRuns: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert_history_business_silence_manager_gc_runs_total",
				Help: "Total number of GC worker runs by phase",
			},
			[]string{"phase"},
		),

		// 7. GC cleaned
		GCCleaned: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert_history_business_silence_manager_gc_cleaned_total",
				Help: "Total number of silences cleaned by GC by phase",
			},
			[]string{"phase"},
		),

		// 8. Sync runs
		SyncRuns: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "alert_history_business_silence_manager_sync_runs_total",
				Help: "Total number of sync worker runs",
			},
		),

			startTime: time.Now(),
		}
	})

	return metricsInstance
}

// RecordOperation records an operation with duration and optional error.
//
// This is a convenience method that updates multiple metrics:
//   - Operations counter (success/error)
//   - OperationDuration histogram (if no error)
//   - Errors counter (if error)
//
// Parameters:
//   - operation: Operation name (create/get/update/delete/list/filter/gc/sync)
//   - duration: Operation duration
//   - err: Error if operation failed, nil otherwise
//
// Example:
//
//	start := time.Now()
//	silence, err := sm.CreateSilence(ctx, silence)
//	sm.metrics.RecordOperation("create", time.Since(start), err)
func (m *SilenceMetrics) RecordOperation(operation string, duration time.Duration, err error) {
	if err != nil {
		m.Operations.WithLabelValues(operation, "error").Inc()
		m.Errors.WithLabelValues(operation, "unknown").Inc()
	} else {
		m.Operations.WithLabelValues(operation, "success").Inc()
		m.OperationDuration.WithLabelValues(operation).Observe(duration.Seconds())
	}
}

// RecordCacheHit records a cache hit for GetStats().
func (m *SilenceMetrics) RecordCacheHit(operation string) {
	m.CacheOperations.WithLabelValues("hit", operation).Inc()
	m.cacheHits.Add(1)
}

// RecordCacheMiss records a cache miss for GetStats().
func (m *SilenceMetrics) RecordCacheMiss(operation string) {
	m.CacheOperations.WithLabelValues("miss", operation).Inc()
	m.cacheMisses.Add(1)
}

// RecordGCRun records a GC worker run.
func (m *SilenceMetrics) RecordGCRun(phase string, cleaned int64) {
	m.GCRuns.WithLabelValues(phase).Inc()
	m.GCCleaned.WithLabelValues(phase).Add(float64(cleaned))
	m.gcTotalRuns.Add(1)
}

// RecordSyncRun records a sync worker run.
func (m *SilenceMetrics) RecordSyncRun() {
	m.SyncRuns.Inc()
	m.syncTotalRuns.Add(1)
}

// UpdateActiveSilencesGauge updates the active silences gauge.
//
// This should be called after cache rebuild or significant changes.
//
// Parameters:
//   - active: Number of active silences
//   - pending: Number of pending silences
//   - expired: Number of expired silences
func (m *SilenceMetrics) UpdateActiveSilencesGauge(active, pending, expired int) {
	m.ActiveSilences.WithLabelValues("active").Set(float64(active))
	m.ActiveSilences.WithLabelValues("pending").Set(float64(pending))
	m.ActiveSilences.WithLabelValues("expired").Set(float64(expired))
}

// GetCacheHits returns total cache hits (for GetStats()).
func (m *SilenceMetrics) GetCacheHits() uint64 {
	return m.cacheHits.Load()
}

// GetCacheMisses returns total cache misses (for GetStats()).
func (m *SilenceMetrics) GetCacheMisses() uint64 {
	return m.cacheMisses.Load()
}

// GetGCTotalRuns returns total GC runs (for GetStats()).
func (m *SilenceMetrics) GetGCTotalRuns() uint64 {
	return m.gcTotalRuns.Load()
}

// GetSyncTotalRuns returns total sync runs (for GetStats()).
func (m *SilenceMetrics) GetSyncTotalRuns() uint64 {
	return m.syncTotalRuns.Load()
}

// GetUptime returns manager uptime in seconds.
func (m *SilenceMetrics) GetUptime() int {
	return int(time.Since(m.startTime).Seconds())
}
