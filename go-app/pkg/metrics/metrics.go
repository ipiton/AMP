// Package metrics provides Prometheus metrics for the application.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// DefaultRegistry is the default Prometheus registry
var DefaultRegistry = prometheus.DefaultRegisterer

// MetricsRegistry is a stub for the metrics registry
type MetricsRegistry struct {
	// TODO: Implement full metrics registry
}

// NewMetricsRegistry creates a new metrics registry - stub
func NewMetricsRegistry() *MetricsRegistry {
	return &MetricsRegistry{}
}

// ================================================================================
// Business Metrics
// ================================================================================

// BusinessMetrics holds business-level metrics
type BusinessMetrics struct {
// Silence metrics
	SilenceOperationsTotal  *prometheus.CounterVec
	SilenceValidationErrors *prometheus.CounterVec
	SilenceCacheHitsTotal   *prometheus.CounterVec
	SilenceCacheMissesTotal *prometheus.CounterVec

	// Inhibition state metrics
	InhibitionStateActive    prometheus.Gauge
	InhibitionStateOperations *prometheus.CounterVec
	InhibitionStateRecords   *prometheus.CounterVec
	InhibitionStateRemovals  *prometheus.CounterVec
	InhibitionStateRedisErrors *prometheus.CounterVec
}

// NewBusinessMetrics creates a new BusinessMetrics instance
func NewBusinessMetrics() *BusinessMetrics {
	return &BusinessMetrics{
		SilenceOperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "silence_operations_total",
				Help: "Total number of silence operations.",
			},
			[]string{"operation", "status"},
		),
		SilenceValidationErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "silence_validation_errors_total",
				Help: "Total number of silence validation errors.",
			},
			[]string{"error_type"},
		),
		SilenceCacheHitsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "silence_cache_hits_total",
				Help: "Total number of silence cache hits.",
			},
			[]string{"path"},
		),
		SilenceCacheMissesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "silence_cache_misses_total",
				Help: "Total number of silence cache misses.",
			},
			[]string{"path"},
		),
		InhibitionStateActive: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "inhibition_state_active",
				Help: "Number of active inhibition states.",
			},
		),
		InhibitionStateOperations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "inhibition_state_operations_total",
				Help: "Total number of inhibition state operations.",
			},
			[]string{"operation", "status"},
		),
		InhibitionStateRecords: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "inhibition_state_records_total",
				Help: "Total number of inhibition state records.",
			},
			[]string{"status"},
		),
		InhibitionStateRemovals: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "inhibition_state_removals_total",
				Help: "Total number of inhibition state removals.",
			},
			[]string{"status"},
		),
		InhibitionStateRedisErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "inhibition_state_redis_errors_total",
				Help: "Total number of Redis errors in inhibition state.",
			},
			[]string{"operation"},
		),
	}
}

// RecordInhibitionStateOperation records an inhibition state operation
func (m *BusinessMetrics) RecordInhibitionStateOperation(operation, status string) {
	m.InhibitionStateOperations.WithLabelValues(operation, status).Inc()
}

// RecordInhibitionStateRecord records an inhibition state record
func (m *BusinessMetrics) RecordInhibitionStateRecord(status string) {
	m.InhibitionStateRecords.WithLabelValues(status).Inc()
}

// RecordInhibitionStateRemoval records an inhibition state removal
func (m *BusinessMetrics) RecordInhibitionStateRemoval(status string) {
	m.InhibitionStateRemovals.WithLabelValues(status).Inc()
}

// RecordInhibitionStateRedisError records a Redis error
func (m *BusinessMetrics) RecordInhibitionStateRedisError(operation string) {
	m.InhibitionStateRedisErrors.WithLabelValues(operation).Inc()
}

// SetInhibitionStateActive sets the number of active inhibition states
func (m *BusinessMetrics) SetInhibitionStateActive(count float64) {
	m.InhibitionStateActive.Set(count)
}

// RecordInhibitionStateExpired records an expired inhibition state
func (m *BusinessMetrics) RecordInhibitionStateExpired(status string) {
	m.InhibitionStateRemovals.WithLabelValues(status).Inc()
}

// IncActiveGroups increments the active groups counter
func (m *BusinessMetrics) IncActiveGroups() {
	// Stub - would need a gauge for this
}

// DecActiveGroups decrements the active groups counter
func (m *BusinessMetrics) DecActiveGroups() {
	// Stub - would need a gauge for this
}

// RecordGroupOperation records a group operation
func (m *BusinessMetrics) RecordGroupOperation(operation, status string) {
	// Stub - would need a counter for this
}

// RecordGroupOperationDuration records a group operation duration
func (m *BusinessMetrics) RecordGroupOperationDuration(operation string, duration float64) {
	// Stub - would need a histogram for this
}

// RecordGroupsCleanedUp records cleaned up groups
func (m *BusinessMetrics) RecordGroupsCleanedUp(count int) {
	// Stub - would need a counter for this
}

// RecordGroupsRestored records restored groups
func (m *BusinessMetrics) RecordGroupsRestored(count int) {
	// Stub - would need a counter for this
}

// SetStorageHealth sets storage health status
func (m *BusinessMetrics) SetStorageHealth(healthy bool) {
	// Stub - would need a gauge for this
}

// RecordStorageDuration records storage operation duration
func (m *BusinessMetrics) RecordStorageDuration(operation string, duration float64) {
	// Stub - would need a histogram for this
}

// RecordStorageOperation records a storage operation
func (m *BusinessMetrics) RecordStorageOperation(operation, status string) {
	// Stub - would need a counter for this
}

// IncStorageFallback increments storage fallback counter
func (m *BusinessMetrics) IncStorageFallback(reason string) {
	// Stub
}

// IncStorageRecovery increments storage recovery counter
func (m *BusinessMetrics) IncStorageRecovery() {
	// Stub
}

// RecordTimerStarted records a timer start
func (m *BusinessMetrics) RecordTimerStarted(timerType string) {
	// Stub
}

// IncActiveTimers increments active timers counter
func (m *BusinessMetrics) IncActiveTimers() {
	// Stub
}

// RecordTimerDuration records timer duration
func (m *BusinessMetrics) RecordTimerDuration(timerType string, duration float64) {
	// Stub
}

// RecordTimerReset records a timer reset
func (m *BusinessMetrics) RecordTimerReset(timerType string) {
	// Stub
}

// RecordTimerOperationDuration records timer operation duration
func (m *BusinessMetrics) RecordTimerOperationDuration(operation string, duration float64) {
	// Stub
}

// RecordTimerExpired records an expired timer
func (m *BusinessMetrics) RecordTimerExpired(timerType string) {
	// Stub
}

// DecActiveTimers decrements active timers counter
func (m *BusinessMetrics) DecActiveTimers() {
	// Stub
}

// RecordTimersRestored records restored timers
func (m *BusinessMetrics) RecordTimersRestored(count int) {
	// Stub
}

// RecordTimerCancelled records a cancelled timer
func (m *BusinessMetrics) RecordTimerCancelled(timerType string) {
	// Stub
}

// RecordTimersMissed records missed timers
func (m *BusinessMetrics) RecordTimersMissed(count int) {
	// Stub
}

// RecordInhibitionCheck records inhibition check
func (m *BusinessMetrics) RecordInhibitionCheck(result string) {
	// Stub - record inhibition check metric
}

// RecordInhibitionMatch records inhibition match
func (m *BusinessMetrics) RecordInhibitionMatch(ruleName string) {
	// Stub - record inhibition match metric
}

// RecordInhibitionDuration records inhibition duration
func (m *BusinessMetrics) RecordInhibitionDuration(operation string, seconds float64) {
	// Stub - record inhibition duration metric
}

// RecordClassificationDuration records classification duration
func (m *BusinessMetrics) RecordClassificationDuration(classifier string, duration float64) {
	// Stub - record classification duration
}

// LLMClassificationsTotal records LLM classification
func (m *BusinessMetrics) LLMClassificationsTotal(status string) {
	// Stub - record LLM classification
}

// RecordClassificationL1CacheHit records L1 cache hit
func (m *BusinessMetrics) RecordClassificationL1CacheHit() {
	// Stub
}

// RecordClassificationL2CacheHit records L2 cache hit
func (m *BusinessMetrics) RecordClassificationL2CacheHit() {
	// Stub
}

// DeduplicationDurationSeconds records deduplication duration
func (m *BusinessMetrics) DeduplicationDurationSeconds(operation string, duration float64) {
	// Stub
}

// DeduplicationCreatedTotal records new deduplication entries
func (m *BusinessMetrics) DeduplicationCreatedTotal() {
	// Stub
}

// DeduplicationUpdatedTotal records updated deduplication entries
func (m *BusinessMetrics) DeduplicationUpdatedTotal() {
	// Stub
}

// DeduplicationIgnoredTotal records ignored deduplication entries
func (m *BusinessMetrics) DeduplicationIgnoredTotal() {
	// Stub
}

// FilterMetrics - stub type
type FilterMetrics struct{}

// NewFilterMetrics creates new filter metrics
func NewFilterMetrics() *FilterMetrics {
	return &FilterMetrics{}
}

// RecordBlockedAlert records blocked alert
func (m *FilterMetrics) RecordBlockedAlert(reason string) {
	// Stub
}

// RecordAlertFiltered records filtered alert
func (m *FilterMetrics) RecordAlertFiltered(result string) {
	// Stub
}

// RecordFilterDuration records filter duration
func (m *FilterMetrics) RecordFilterDuration(duration float64) {
	// Stub
}

// MetricsManager - stub type
type MetricsManager struct{}

// EnrichmentModeManager - stub type for services
type EnrichmentModeManager struct{}

// RecordSilenceRequest records silence request
func (m *BusinessMetrics) RecordSilenceRequest(method, endpoint, status string, duration float64) {
	// Stub
}

// SilenceRateLimitExceeded records rate limit exceeded
func (m *BusinessMetrics) SilenceRateLimitExceeded() {
	// Stub
}

// ================================================================================
// Database Metrics
// ================================================================================

// DatabaseMetrics holds database-related metrics
type DatabaseMetrics struct {
	QueryDuration             *prometheus.HistogramVec
	QueryErrors               *prometheus.CounterVec
	ConnectionsActive         prometheus.Gauge
	ConnectionsTotal          *prometheus.CounterVec
	ConnectionsIdle           prometheus.Gauge
	QueryDurationSeconds      *prometheus.HistogramVec
	ErrorsTotal               *prometheus.CounterVec
	ConnectionWaitDurationSeconds *prometheus.HistogramVec
	QueriesTotal              *prometheus.CounterVec
}

// NewDatabaseMetrics creates a new DatabaseMetrics instance
func NewDatabaseMetrics() *DatabaseMetrics {
	return &DatabaseMetrics{
		QueryDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "database_query_duration_seconds",
				Help:    "Database query duration in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"query_type"},
		),
		QueryErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "database_query_errors_total",
				Help: "Total number of database query errors.",
			},
			[]string{"query_type", "error_type"},
		),
		ConnectionsActive: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "database_connections_active",
				Help: "Number of active database connections.",
			},
		),
		ConnectionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "database_connections_total",
				Help: "Total number of database connections.",
			},
			[]string{"status"},
		),
		ConnectionsIdle: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "database_connections_idle",
				Help: "Number of idle database connections.",
			},
		),
		QueryDurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "database_query_duration_seconds_hist",
				Help:    "Database query duration histogram.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"query_type"},
		),
		ErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "database_errors_total",
				Help: "Total database errors.",
			},
			[]string{"error_type"},
		),
		ConnectionWaitDurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "database_connection_wait_duration_seconds",
				Help:    "Connection wait duration.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"pool"},
		),
		QueriesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "database_queries_total",
				Help: "Total database queries.",
			},
			[]string{"query_type", "status"},
		),
	}
}

// ================================================================================
// Webhook Metrics
// ================================================================================

// WebhookMetrics holds webhook-related metrics
type WebhookMetrics struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	ResponseStatus   *prometheus.CounterVec
	PayloadSize      *prometheus.HistogramVec
	ErrorsTotal      *prometheus.CounterVec
}

// NewWebhookMetrics creates a new WebhookMetrics instance
func NewWebhookMetrics() *WebhookMetrics {
	return &WebhookMetrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_requests_total",
				Help: "Total number of webhook requests.",
			},
			[]string{"endpoint", "method"},
		),
		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "webhook_request_duration_seconds",
				Help:    "Webhook request duration in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"endpoint", "method"},
		),
		ResponseStatus: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_response_status_total",
				Help: "Total number of webhook responses by status code.",
			},
			[]string{"endpoint", "status_code"},
		),
		PayloadSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "webhook_payload_size_bytes",
				Help:    "Webhook payload size in bytes.",
				Buckets: []float64{100, 1000, 10000, 100000, 1000000},
			},
			[]string{"endpoint"},
		),
		ErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_errors_total",
				Help: "Total number of webhook errors.",
			},
			[]string{"endpoint", "error_type"},
		),
	}
}

// RecordProcessingStage records a webhook processing stage
func (m *WebhookMetrics) RecordProcessingStage(endpoint, stage string, duration float64) {
	// Stub - would need a counter for this
}

// RecordError records a webhook error
func (m *WebhookMetrics) RecordError(endpoint, errorType string) {
	m.ErrorsTotal.WithLabelValues(endpoint, errorType).Inc()
}

// RecordPayloadSize records webhook payload size
func (m *WebhookMetrics) RecordPayloadSize(endpoint string, size int) {
	// Stub
}

// RecordRequest records a webhook request
func (m *WebhookMetrics) RecordRequest(endpoint, method string, duration float64) {
	m.RequestsTotal.WithLabelValues(endpoint, method).Inc()
	m.RequestDuration.WithLabelValues(endpoint, method).Observe(duration)
}
