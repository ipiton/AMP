// Package metrics provides Prometheus metrics for the application.
//
// Deprecated: This package is deprecated. Use pkg/metrics/v2 instead.
// The v2 package provides a unified, consolidated metrics registry with:
//   - Consistent namespacing across all metrics
//   - Deduplicated metric definitions
//   - Type-safe metric access
//   - Better testability with custom registerers
//
// Migration example:
//
//	// Old (deprecated):
//	registry := metrics.NewMetricsRegistry()
//	registry.Database.QueryDuration.WithLabelValues("select").Observe(0.05)
//
//	// New (recommended):
//	registry := v2.NewRegistry()
//	registry.Database.RecordQuery("select", true, 50*time.Millisecond)
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	// Namespace is the Prometheus namespace for all alert_history metrics
	Namespace = "alert_history"
)

// DefaultRegistry is the default Prometheus registry
var DefaultRegistry = prometheus.DefaultRegisterer

// MetricsRegistry holds all application metrics
//
// Deprecated: Use v2.Registry from pkg/metrics/v2 instead.
type MetricsRegistry struct {
	Business         *BusinessMetrics
	Database         *DatabaseMetrics
	Webhook          *WebhookMetrics
	PrometheusAlerts *PrometheusAlertsMetrics
	Proxy            *ProxyMetrics
	APIConfig        *APIConfigMetrics
	Dashboard        *DashboardMetrics
	Filter           *FilterMetrics
	Group            *GroupMetrics
	Timer            *TimerMetrics
	Storage          *StorageMetrics
	Classification   *ClassificationMetrics
	Deduplication    *DeduplicationMetrics
}

// NewMetricsRegistry creates a new metrics registry with all metrics initialized
func NewMetricsRegistry() *MetricsRegistry {
	return &MetricsRegistry{
		Business:         NewBusinessMetrics(),
		Database:         NewDatabaseMetrics(),
		Webhook:          NewWebhookMetrics(),
		PrometheusAlerts: NewPrometheusAlertsMetrics(),
		Proxy:            NewProxyMetrics(),
		APIConfig:        NewAPIConfigMetrics(),
		Dashboard:        NewDashboardMetrics(),
		Filter:           NewFilterMetrics(),
		Group:            NewGroupMetrics(),
		Timer:            NewTimerMetrics(),
		Storage:          NewStorageMetrics(),
		Classification:   NewClassificationMetrics(),
		Deduplication:    NewDeduplicationMetrics(),
	}
}

// ================================================================================
// Prometheus Alerts Metrics (alert_history_prometheus_alerts_*)
// ================================================================================

// PrometheusAlertsMetrics holds metrics for incoming Prometheus alerts
type PrometheusAlertsMetrics struct {
	RequestsTotal       *prometheus.CounterVec
	ReceivedTotal       *prometheus.CounterVec
	ProcessedTotal      *prometheus.CounterVec
	ProcessingErrors    *prometheus.CounterVec
	ValidationErrors    *prometheus.CounterVec
	DurationSeconds     *prometheus.HistogramVec
	PayloadBytes        *prometheus.HistogramVec
	ConcurrentRequests  prometheus.Gauge
}

// NewPrometheusAlertsMetrics creates new Prometheus alerts metrics
func NewPrometheusAlertsMetrics() *PrometheusAlertsMetrics {
	return &PrometheusAlertsMetrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "requests_total",
				Help:      "Total number of Prometheus alert requests.",
			},
			[]string{"status"},
		),
		ReceivedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "received_total",
				Help:      "Total number of alerts received.",
			},
			[]string{"status"},
		),
		ProcessedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "processed_total",
				Help:      "Total number of alerts processed.",
			},
			[]string{"status"},
		),
		ProcessingErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "processing_errors_total",
				Help:      "Total number of alert processing errors.",
			},
			[]string{"error_type"},
		),
		ValidationErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "validation_errors_total",
				Help:      "Total number of alert validation errors.",
			},
			[]string{"error_type"},
		),
		DurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "duration_seconds",
				Help:      "Duration of alert processing in seconds.",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
			},
			[]string{"operation"},
		),
		PayloadBytes: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "payload_bytes",
				Help:      "Size of alert payloads in bytes.",
				Buckets:   prometheus.ExponentialBuckets(100, 2, 12), // 100B to 400KB
			},
			[]string{"type"},
		),
		ConcurrentRequests: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "prometheus_alerts",
				Name:      "concurrent_requests",
				Help:      "Number of concurrent alert requests being processed.",
			},
		),
	}
}

// RecordRequest records an incoming request
func (m *PrometheusAlertsMetrics) RecordRequest(status string) {
	m.RequestsTotal.WithLabelValues(status).Inc()
}

// RecordReceived records received alerts
func (m *PrometheusAlertsMetrics) RecordReceived(count int, status string) {
	m.ReceivedTotal.WithLabelValues(status).Add(float64(count))
}

// RecordProcessed records processed alerts
func (m *PrometheusAlertsMetrics) RecordProcessed(count int, status string) {
	m.ProcessedTotal.WithLabelValues(status).Add(float64(count))
}

// RecordProcessingError records a processing error
func (m *PrometheusAlertsMetrics) RecordProcessingError(errorType string) {
	m.ProcessingErrors.WithLabelValues(errorType).Inc()
}

// RecordValidationError records a validation error
func (m *PrometheusAlertsMetrics) RecordValidationError(errorType string) {
	m.ValidationErrors.WithLabelValues(errorType).Inc()
}

// RecordDuration records processing duration
func (m *PrometheusAlertsMetrics) RecordDuration(operation string, seconds float64) {
	m.DurationSeconds.WithLabelValues(operation).Observe(seconds)
}

// RecordPayloadSize records payload size
func (m *PrometheusAlertsMetrics) RecordPayloadSize(payloadType string, bytes int) {
	m.PayloadBytes.WithLabelValues(payloadType).Observe(float64(bytes))
}

// IncConcurrentRequests increments concurrent requests
func (m *PrometheusAlertsMetrics) IncConcurrentRequests() {
	m.ConcurrentRequests.Inc()
}

// DecConcurrentRequests decrements concurrent requests
func (m *PrometheusAlertsMetrics) DecConcurrentRequests() {
	m.ConcurrentRequests.Dec()
}

// ================================================================================
// Proxy Pipeline Metrics (alert_history_proxy_*)
// ================================================================================

// ProxyMetrics holds metrics for the alert proxy pipeline
type ProxyMetrics struct {
	AlertsReceived       *prometheus.CounterVec
	AlertsProcessed      *prometheus.CounterVec
	BatchSize            *prometheus.HistogramVec
	ConcurrentRequests   prometheus.Gauge
	PipelineDuration     *prometheus.HistogramVec
	ClassificationDuration *prometheus.HistogramVec
	ClassificationErrors *prometheus.CounterVec
	FilteringDuration    *prometheus.HistogramVec
	FilteringErrors      *prometheus.CounterVec
	PublishingDuration   *prometheus.HistogramVec
	PublishingErrors     *prometheus.CounterVec
	PublishingTargets    prometheus.Gauge
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
	HTTPRequestSize      *prometheus.HistogramVec
	HTTPResponseSize     *prometheus.HistogramVec
	HTTPRequestsInFlight prometheus.Gauge
	HTTPErrors           *prometheus.CounterVec
}

// NewProxyMetrics creates new proxy metrics
func NewProxyMetrics() *ProxyMetrics {
	return &ProxyMetrics{
		AlertsReceived: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "alerts_received_total",
				Help:      "Total number of alerts received by proxy.",
			},
			[]string{"source"},
		),
		AlertsProcessed: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "alerts_processed_total",
				Help:      "Total number of alerts processed by proxy.",
			},
			[]string{"status"},
		),
		BatchSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "batch_size",
				Help:      "Size of alert batches.",
				Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
			},
			[]string{"type"},
		),
		ConcurrentRequests: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "concurrent_requests",
				Help:      "Number of concurrent proxy requests.",
			},
		),
		PipelineDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "pipeline_duration_seconds",
				Help:      "Duration of the entire proxy pipeline.",
				Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
			},
			[]string{"status"},
		),
		ClassificationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "classification_duration_seconds",
				Help:      "Duration of alert classification.",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
			},
			[]string{"classifier"},
		),
		ClassificationErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "classification_errors_total",
				Help:      "Total number of classification errors.",
			},
			[]string{"classifier", "error_type"},
		),
		FilteringDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "filtering_duration_seconds",
				Help:      "Duration of alert filtering.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"filter"},
		),
		FilteringErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "filtering_errors_total",
				Help:      "Total number of filtering errors.",
			},
			[]string{"filter", "error_type"},
		),
		PublishingDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "publishing_duration_seconds",
				Help:      "Duration of alert publishing.",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
			},
			[]string{"target"},
		),
		PublishingErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "publishing_errors_total",
				Help:      "Total number of publishing errors.",
			},
			[]string{"target", "error_type"},
		),
		PublishingTargets: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "publishing_targets_total",
				Help:      "Number of publishing targets.",
			},
		),
		HTTPRequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "http_requests_total",
				Help:      "Total number of HTTP requests.",
			},
			[]string{"method", "status"},
		),
		HTTPRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "http_request_duration_seconds",
				Help:      "Duration of HTTP requests.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method", "endpoint"},
		),
		HTTPRequestSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "http_request_size_bytes",
				Help:      "Size of HTTP requests.",
				Buckets:   prometheus.ExponentialBuckets(100, 2, 12),
			},
			[]string{"method"},
		),
		HTTPResponseSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "http_response_size_bytes",
				Help:      "Size of HTTP responses.",
				Buckets:   prometheus.ExponentialBuckets(100, 2, 12),
			},
			[]string{"method"},
		),
		HTTPRequestsInFlight: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "http_requests_in_flight",
				Help:      "Number of HTTP requests in flight.",
			},
		),
		HTTPErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "proxy",
				Name:      "http_errors_total",
				Help:      "Total number of HTTP errors.",
			},
			[]string{"method", "error_type"},
		),
	}
}

// RecordAlertsReceived records received alerts
func (m *ProxyMetrics) RecordAlertsReceived(count int, source string) {
	m.AlertsReceived.WithLabelValues(source).Add(float64(count))
}

// RecordAlertsProcessed records processed alerts
func (m *ProxyMetrics) RecordAlertsProcessed(count int, status string) {
	m.AlertsProcessed.WithLabelValues(status).Add(float64(count))
}

// RecordBatchSize records batch size
func (m *ProxyMetrics) RecordBatchSize(size int, batchType string) {
	m.BatchSize.WithLabelValues(batchType).Observe(float64(size))
}

// IncConcurrentRequests increments concurrent requests
func (m *ProxyMetrics) IncConcurrentRequests() {
	m.ConcurrentRequests.Inc()
}

// DecConcurrentRequests decrements concurrent requests
func (m *ProxyMetrics) DecConcurrentRequests() {
	m.ConcurrentRequests.Dec()
}

// RecordPipelineDuration records pipeline duration
func (m *ProxyMetrics) RecordPipelineDuration(seconds float64, status string) {
	m.PipelineDuration.WithLabelValues(status).Observe(seconds)
}

// RecordClassificationDuration records classification duration
func (m *ProxyMetrics) RecordClassificationDuration(classifier string, seconds float64) {
	m.ClassificationDuration.WithLabelValues(classifier).Observe(seconds)
}

// RecordClassificationError records classification error
func (m *ProxyMetrics) RecordClassificationError(classifier, errorType string) {
	m.ClassificationErrors.WithLabelValues(classifier, errorType).Inc()
}

// RecordFilteringDuration records filtering duration
func (m *ProxyMetrics) RecordFilteringDuration(filter string, seconds float64) {
	m.FilteringDuration.WithLabelValues(filter).Observe(seconds)
}

// RecordFilteringError records filtering error
func (m *ProxyMetrics) RecordFilteringError(filter, errorType string) {
	m.FilteringErrors.WithLabelValues(filter, errorType).Inc()
}

// RecordPublishingDuration records publishing duration
func (m *ProxyMetrics) RecordPublishingDuration(target string, seconds float64) {
	m.PublishingDuration.WithLabelValues(target).Observe(seconds)
}

// RecordPublishingError records publishing error
func (m *ProxyMetrics) RecordPublishingError(target, errorType string) {
	m.PublishingErrors.WithLabelValues(target, errorType).Inc()
}

// SetPublishingTargets sets number of publishing targets
func (m *ProxyMetrics) SetPublishingTargets(count int) {
	m.PublishingTargets.Set(float64(count))
}

// RecordHTTPRequest records HTTP request
func (m *ProxyMetrics) RecordHTTPRequest(method, status string, durationSeconds float64, endpoint string) {
	m.HTTPRequestsTotal.WithLabelValues(method, status).Inc()
	m.HTTPRequestDuration.WithLabelValues(method, endpoint).Observe(durationSeconds)
}

// RecordHTTPRequestSize records HTTP request size
func (m *ProxyMetrics) RecordHTTPRequestSize(method string, bytes int) {
	m.HTTPRequestSize.WithLabelValues(method).Observe(float64(bytes))
}

// RecordHTTPResponseSize records HTTP response size
func (m *ProxyMetrics) RecordHTTPResponseSize(method string, bytes int) {
	m.HTTPResponseSize.WithLabelValues(method).Observe(float64(bytes))
}

// IncHTTPRequestsInFlight increments HTTP requests in flight
func (m *ProxyMetrics) IncHTTPRequestsInFlight() {
	m.HTTPRequestsInFlight.Inc()
}

// DecHTTPRequestsInFlight decrements HTTP requests in flight
func (m *ProxyMetrics) DecHTTPRequestsInFlight() {
	m.HTTPRequestsInFlight.Dec()
}

// RecordHTTPError records HTTP error
func (m *ProxyMetrics) RecordHTTPError(method, errorType string) {
	m.HTTPErrors.WithLabelValues(method, errorType).Inc()
}

// ================================================================================
// API Config Export Metrics (alert_history_api_config_export_*)
// ================================================================================

// APIConfigMetrics holds metrics for API config export
type APIConfigMetrics struct {
	RequestsTotal   *prometheus.CounterVec
	DurationSeconds *prometheus.HistogramVec
	ErrorsTotal     *prometheus.CounterVec
	SizeBytes       *prometheus.HistogramVec
}

// NewAPIConfigMetrics creates new API config metrics
func NewAPIConfigMetrics() *APIConfigMetrics {
	return &APIConfigMetrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "api_config_export",
				Name:      "requests_total",
				Help:      "Total number of config export requests.",
			},
			[]string{"format", "status"},
		),
		DurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "api_config_export",
				Name:      "duration_seconds",
				Help:      "Duration of config export requests.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"format"},
		),
		ErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "api_config_export",
				Name:      "errors_total",
				Help:      "Total number of config export errors.",
			},
			[]string{"format", "error_type"},
		),
		SizeBytes: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "api_config_export",
				Name:      "size_bytes",
				Help:      "Size of exported config.",
				Buckets:   prometheus.ExponentialBuckets(100, 2, 12),
			},
			[]string{"format"},
		),
	}
}

// RecordRequest records config export request
func (m *APIConfigMetrics) RecordRequest(format, status string) {
	m.RequestsTotal.WithLabelValues(format, status).Inc()
}

// RecordDuration records config export duration
func (m *APIConfigMetrics) RecordDuration(format string, seconds float64) {
	m.DurationSeconds.WithLabelValues(format).Observe(seconds)
}

// RecordError records config export error
func (m *APIConfigMetrics) RecordError(format, errorType string) {
	m.ErrorsTotal.WithLabelValues(format, errorType).Inc()
}

// RecordSize records config export size
func (m *APIConfigMetrics) RecordSize(format string, bytes int) {
	m.SizeBytes.WithLabelValues(format).Observe(float64(bytes))
}

// ================================================================================
// Technical Dashboard Metrics (alert_history_technical_dashboard_*)
// ================================================================================

// DashboardMetrics holds metrics for technical dashboard
type DashboardMetrics struct {
	HealthChecksTotal    *prometheus.CounterVec
	HealthCheckDuration  *prometheus.HistogramVec
	HealthStatus         *prometheus.GaugeVec
	OverallHealthStatus  prometheus.Gauge
}

// NewDashboardMetrics creates new dashboard metrics
func NewDashboardMetrics() *DashboardMetrics {
	return &DashboardMetrics{
		HealthChecksTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "technical_dashboard",
				Name:      "health_checks_total",
				Help:      "Total number of health checks.",
			},
			[]string{"component"},
		),
		HealthCheckDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "technical_dashboard",
				Name:      "health_check_duration_seconds",
				Help:      "Duration of health checks.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"component"},
		),
		HealthStatus: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "technical_dashboard",
				Name:      "health_status",
				Help:      "Health status of components (1=healthy, 0=unhealthy).",
			},
			[]string{"component"},
		),
		OverallHealthStatus: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "technical_dashboard",
				Name:      "health_overall_status",
				Help:      "Overall health status (1=healthy, 0=unhealthy).",
			},
		),
	}
}

// RecordHealthCheck records health check
func (m *DashboardMetrics) RecordHealthCheck(component string) {
	m.HealthChecksTotal.WithLabelValues(component).Inc()
}

// RecordHealthCheckDuration records health check duration
func (m *DashboardMetrics) RecordHealthCheckDuration(component string, seconds float64) {
	m.HealthCheckDuration.WithLabelValues(component).Observe(seconds)
}

// SetHealthStatus sets component health status
func (m *DashboardMetrics) SetHealthStatus(component string, healthy bool) {
	if healthy {
		m.HealthStatus.WithLabelValues(component).Set(1)
	} else {
		m.HealthStatus.WithLabelValues(component).Set(0)
	}
}

// SetOverallHealthStatus sets overall health status
func (m *DashboardMetrics) SetOverallHealthStatus(healthy bool) {
	if healthy {
		m.OverallHealthStatus.Set(1)
	} else {
		m.OverallHealthStatus.Set(0)
	}
}

// ================================================================================
// Group Metrics (alert_history_group_*)
// ================================================================================

// GroupMetrics holds metrics for alert grouping
type GroupMetrics struct {
	ActiveGroups       prometheus.Gauge
	OperationsTotal    *prometheus.CounterVec
	OperationDuration  *prometheus.HistogramVec
	GroupSize          *prometheus.HistogramVec
	CleanedUpTotal     prometheus.Counter
	RestoredTotal      prometheus.Counter
}

// NewGroupMetrics creates new group metrics
func NewGroupMetrics() *GroupMetrics {
	return &GroupMetrics{
		ActiveGroups: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "group",
				Name:      "active_total",
				Help:      "Number of active alert groups.",
			},
		),
		OperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "group",
				Name:      "operations_total",
				Help:      "Total number of group operations.",
			},
			[]string{"operation", "status"},
		),
		OperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "group",
				Name:      "operation_duration_seconds",
				Help:      "Duration of group operations.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"operation"},
		),
		GroupSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "group",
				Name:      "size",
				Help:      "Size of alert groups.",
				Buckets:   []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000},
			},
			[]string{"status"},
		),
		CleanedUpTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "group",
				Name:      "cleaned_up_total",
				Help:      "Total number of groups cleaned up.",
			},
		),
		RestoredTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "group",
				Name:      "restored_total",
				Help:      "Total number of groups restored.",
			},
		),
	}
}

// ================================================================================
// Timer Metrics (alert_history_timer_*)
// ================================================================================

// TimerMetrics holds metrics for timers
type TimerMetrics struct {
	ActiveTimers      prometheus.Gauge
	StartedTotal      *prometheus.CounterVec
	ExpiredTotal      *prometheus.CounterVec
	CancelledTotal    *prometheus.CounterVec
	ResetTotal        *prometheus.CounterVec
	Duration          *prometheus.HistogramVec
	OperationDuration *prometheus.HistogramVec
	RestoredTotal     prometheus.Counter
	MissedTotal       prometheus.Counter
}

// NewTimerMetrics creates new timer metrics
func NewTimerMetrics() *TimerMetrics {
	return &TimerMetrics{
		ActiveTimers: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "active_total",
				Help:      "Number of active timers.",
			},
		),
		StartedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "started_total",
				Help:      "Total number of timers started.",
			},
			[]string{"type"},
		),
		ExpiredTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "expired_total",
				Help:      "Total number of timers expired.",
			},
			[]string{"type"},
		),
		CancelledTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "cancelled_total",
				Help:      "Total number of timers cancelled.",
			},
			[]string{"type"},
		),
		ResetTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "reset_total",
				Help:      "Total number of timers reset.",
			},
			[]string{"type"},
		),
		Duration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "duration_seconds",
				Help:      "Duration of timers.",
				Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
			},
			[]string{"type"},
		),
		OperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "operation_duration_seconds",
				Help:      "Duration of timer operations.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"operation"},
		),
		RestoredTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "restored_total",
				Help:      "Total number of timers restored.",
			},
		),
		MissedTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "timer",
				Name:      "missed_total",
				Help:      "Total number of timers missed.",
			},
		),
	}
}

// ================================================================================
// Storage Metrics (alert_history_storage_*)
// ================================================================================

// StorageMetrics holds metrics for storage operations
type StorageMetrics struct {
	Health            prometheus.Gauge
	OperationsTotal   *prometheus.CounterVec
	OperationDuration *prometheus.HistogramVec
	FallbackTotal     *prometheus.CounterVec
	RecoveryTotal     prometheus.Counter
}

// NewStorageMetrics creates new storage metrics
func NewStorageMetrics() *StorageMetrics {
	return &StorageMetrics{
		Health: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "storage",
				Name:      "health",
				Help:      "Storage health status (1=healthy, 0=unhealthy).",
			},
		),
		OperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "storage",
				Name:      "operations_total",
				Help:      "Total number of storage operations.",
			},
			[]string{"operation", "status"},
		),
		OperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "storage",
				Name:      "operation_duration_seconds",
				Help:      "Duration of storage operations.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"operation"},
		),
		FallbackTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "storage",
				Name:      "fallback_total",
				Help:      "Total number of storage fallbacks.",
			},
			[]string{"reason"},
		),
		RecoveryTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "storage",
				Name:      "recovery_total",
				Help:      "Total number of storage recoveries.",
			},
		),
	}
}

// ================================================================================
// Classification Metrics (alert_history_classification_*)
// ================================================================================

// ClassificationMetrics holds metrics for classification
type ClassificationMetrics struct {
	Duration       *prometheus.HistogramVec
	Total          *prometheus.CounterVec
	L1CacheHits    prometheus.Counter
	L2CacheHits    prometheus.Counter
	CacheMisses    prometheus.Counter
}

// NewClassificationMetrics creates new classification metrics
func NewClassificationMetrics() *ClassificationMetrics {
	return &ClassificationMetrics{
		Duration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "classification",
				Name:      "duration_seconds",
				Help:      "Duration of classification.",
				Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
			},
			[]string{"classifier"},
		),
		Total: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "classification",
				Name:      "total",
				Help:      "Total number of classifications.",
			},
			[]string{"classifier", "status"},
		),
		L1CacheHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "classification",
				Name:      "l1_cache_hits_total",
				Help:      "Total number of L1 cache hits.",
			},
		),
		L2CacheHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "classification",
				Name:      "l2_cache_hits_total",
				Help:      "Total number of L2 cache hits.",
			},
		),
		CacheMisses: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "classification",
				Name:      "cache_misses_total",
				Help:      "Total number of cache misses.",
			},
		),
	}
}

// ================================================================================
// Deduplication Metrics (alert_history_deduplication_*)
// ================================================================================

// DeduplicationMetrics holds metrics for deduplication
type DeduplicationMetrics struct {
	Duration     *prometheus.HistogramVec
	CreatedTotal prometheus.Counter
	UpdatedTotal prometheus.Counter
	IgnoredTotal prometheus.Counter
}

// NewDeduplicationMetrics creates new deduplication metrics
func NewDeduplicationMetrics() *DeduplicationMetrics {
	return &DeduplicationMetrics{
		Duration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "deduplication",
				Name:      "duration_seconds",
				Help:      "Duration of deduplication operations.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"operation"},
		),
		CreatedTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "deduplication",
				Name:      "created_total",
				Help:      "Total number of new entries created.",
			},
		),
		UpdatedTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "deduplication",
				Name:      "updated_total",
				Help:      "Total number of entries updated.",
			},
		),
		IgnoredTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "deduplication",
				Name:      "ignored_total",
				Help:      "Total number of entries ignored.",
			},
		),
	}
}

// ================================================================================
// Business Metrics (existing + implementations)
// ================================================================================

// BusinessMetrics holds business-level metrics
type BusinessMetrics struct {
// Silence metrics
	SilenceOperationsTotal  *prometheus.CounterVec
	SilenceValidationErrors *prometheus.CounterVec
	SilenceCacheHitsTotal   *prometheus.CounterVec
	SilenceCacheMissesTotal *prometheus.CounterVec
	SilenceRequestDuration  *prometheus.HistogramVec
	SilenceRateLimitHits    prometheus.Counter

	// Inhibition state metrics
	InhibitionStateActive      prometheus.Gauge
	InhibitionStateOperations  *prometheus.CounterVec
	InhibitionStateRecords     *prometheus.CounterVec
	InhibitionStateRemovals    *prometheus.CounterVec
	InhibitionStateRedisErrors *prometheus.CounterVec
	InhibitionCheckTotal       *prometheus.CounterVec
	InhibitionMatchTotal       *prometheus.CounterVec
	InhibitionDuration         *prometheus.HistogramVec

	// Inhibition cache metrics
	InhibitionCacheHits       prometheus.Counter
	InhibitionCacheMisses     prometheus.Counter
	InhibitionCacheEvictions  prometheus.Counter
	InhibitionCacheSize       prometheus.Gauge
	InhibitionCacheOperations *prometheus.CounterVec
	InhibitionCacheDuration   *prometheus.HistogramVec

	// References to specialized metrics
	groups         *GroupMetrics
	timers         *TimerMetrics
	storage        *StorageMetrics
	classification *ClassificationMetrics
	deduplication  *DeduplicationMetrics
}

// NewBusinessMetrics creates a new BusinessMetrics instance
func NewBusinessMetrics() *BusinessMetrics {
	return &BusinessMetrics{
		SilenceOperationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "silence_operations_total",
				Help:      "Total number of silence operations.",
			},
			[]string{"operation", "status"},
		),
		SilenceValidationErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "silence_validation_errors_total",
				Help:      "Total number of silence validation errors.",
			},
			[]string{"error_type"},
		),
		SilenceCacheHitsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "silence_cache_hits_total",
				Help:      "Total number of silence cache hits.",
			},
			[]string{"path"},
		),
		SilenceCacheMissesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "silence_cache_misses_total",
				Help:      "Total number of silence cache misses.",
			},
			[]string{"path"},
		),
		SilenceRequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Name:      "silence_request_duration_seconds",
				Help:      "Duration of silence requests.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method", "endpoint", "status"},
		),
		SilenceRateLimitHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "silence_rate_limit_hits_total",
				Help:      "Total number of rate limit hits.",
			},
		),
		InhibitionStateActive: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Name:      "inhibition_state_active",
				Help:      "Number of active inhibition states.",
			},
		),
		InhibitionStateOperations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "inhibition_state_operations_total",
				Help:      "Total number of inhibition state operations.",
			},
			[]string{"operation", "status"},
		),
		InhibitionStateRecords: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "inhibition_state_records_total",
				Help:      "Total number of inhibition state records.",
			},
			[]string{"status"},
		),
		InhibitionStateRemovals: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "inhibition_state_removals_total",
				Help:      "Total number of inhibition state removals.",
			},
			[]string{"status"},
		),
		InhibitionStateRedisErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "inhibition_state_redis_errors_total",
				Help:      "Total number of Redis errors in inhibition state.",
			},
			[]string{"operation"},
		),
		InhibitionCheckTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "inhibition_check_total",
				Help:      "Total number of inhibition checks.",
			},
			[]string{"result"},
		),
		InhibitionMatchTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Name:      "inhibition_match_total",
				Help:      "Total number of inhibition matches.",
			},
			[]string{"rule"},
		),
		InhibitionDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Name:      "inhibition_duration_seconds",
				Help:      "Duration of inhibition operations.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"operation"},
		),
		InhibitionCacheHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "inhibition_cache",
				Name:      "hits_total",
				Help:      "Total number of inhibition cache hits.",
			},
		),
		InhibitionCacheMisses: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "inhibition_cache",
				Name:      "misses_total",
				Help:      "Total number of inhibition cache misses.",
			},
		),
		InhibitionCacheEvictions: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "inhibition_cache",
				Name:      "evictions_total",
				Help:      "Total number of inhibition cache evictions.",
			},
		),
		InhibitionCacheSize: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "inhibition_cache",
				Name:      "size",
				Help:      "Current size of inhibition cache.",
			},
		),
		InhibitionCacheOperations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "inhibition_cache",
				Name:      "operations_total",
				Help:      "Total number of inhibition cache operations.",
			},
			[]string{"operation", "status"},
		),
		InhibitionCacheDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "inhibition_cache",
				Name:      "operation_duration_seconds",
				Help:      "Duration of inhibition cache operations.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"operation"},
		),
		groups:         NewGroupMetrics(),
		timers:         NewTimerMetrics(),
		storage:        NewStorageMetrics(),
		classification: NewClassificationMetrics(),
		deduplication:  NewDeduplicationMetrics(),
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

// RecordInhibitionCheck records inhibition check
func (m *BusinessMetrics) RecordInhibitionCheck(result string) {
	m.InhibitionCheckTotal.WithLabelValues(result).Inc()
}

// RecordInhibitionMatch records inhibition match
func (m *BusinessMetrics) RecordInhibitionMatch(ruleName string) {
	m.InhibitionMatchTotal.WithLabelValues(ruleName).Inc()
}

// RecordInhibitionDuration records inhibition duration
func (m *BusinessMetrics) RecordInhibitionDuration(operation string, seconds float64) {
	m.InhibitionDuration.WithLabelValues(operation).Observe(seconds)
}

// IncActiveGroups increments the active groups counter
func (m *BusinessMetrics) IncActiveGroups() {
	m.groups.ActiveGroups.Inc()
}

// DecActiveGroups decrements the active groups counter
func (m *BusinessMetrics) DecActiveGroups() {
	m.groups.ActiveGroups.Dec()
}

// RecordGroupOperation records a group operation
func (m *BusinessMetrics) RecordGroupOperation(operation, status string) {
	m.groups.OperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordGroupOperationDuration records a group operation duration
func (m *BusinessMetrics) RecordGroupOperationDuration(operation string, duration float64) {
	m.groups.OperationDuration.WithLabelValues(operation).Observe(duration)
}

// RecordGroupsCleanedUp records cleaned up groups
func (m *BusinessMetrics) RecordGroupsCleanedUp(count int) {
	m.groups.CleanedUpTotal.Add(float64(count))
}

// RecordGroupsRestored records restored groups
func (m *BusinessMetrics) RecordGroupsRestored(count int) {
	m.groups.RestoredTotal.Add(float64(count))
}

// SetStorageHealth sets storage health status
func (m *BusinessMetrics) SetStorageHealth(healthy bool) {
	if healthy {
		m.storage.Health.Set(1)
	} else {
		m.storage.Health.Set(0)
	}
}

// RecordStorageDuration records storage operation duration
func (m *BusinessMetrics) RecordStorageDuration(operation string, duration float64) {
	m.storage.OperationDuration.WithLabelValues(operation).Observe(duration)
}

// RecordStorageOperation records a storage operation
func (m *BusinessMetrics) RecordStorageOperation(operation, status string) {
	m.storage.OperationsTotal.WithLabelValues(operation, status).Inc()
}

// IncStorageFallback increments storage fallback counter
func (m *BusinessMetrics) IncStorageFallback(reason string) {
	m.storage.FallbackTotal.WithLabelValues(reason).Inc()
}

// IncStorageRecovery increments storage recovery counter
func (m *BusinessMetrics) IncStorageRecovery() {
	m.storage.RecoveryTotal.Inc()
}

// RecordTimerStarted records a timer start
func (m *BusinessMetrics) RecordTimerStarted(timerType string) {
	m.timers.StartedTotal.WithLabelValues(timerType).Inc()
}

// IncActiveTimers increments active timers counter
func (m *BusinessMetrics) IncActiveTimers() {
	m.timers.ActiveTimers.Inc()
}

// DecActiveTimers decrements active timers counter
func (m *BusinessMetrics) DecActiveTimers() {
	m.timers.ActiveTimers.Dec()
}

// RecordTimerDuration records timer duration
func (m *BusinessMetrics) RecordTimerDuration(timerType string, duration float64) {
	m.timers.Duration.WithLabelValues(timerType).Observe(duration)
}

// RecordTimerReset records a timer reset
func (m *BusinessMetrics) RecordTimerReset(timerType string) {
	m.timers.ResetTotal.WithLabelValues(timerType).Inc()
}

// RecordTimerOperationDuration records timer operation duration
func (m *BusinessMetrics) RecordTimerOperationDuration(operation string, duration float64) {
	m.timers.OperationDuration.WithLabelValues(operation).Observe(duration)
}

// RecordTimerExpired records an expired timer
func (m *BusinessMetrics) RecordTimerExpired(timerType string) {
	m.timers.ExpiredTotal.WithLabelValues(timerType).Inc()
}

// RecordTimersRestored records restored timers
func (m *BusinessMetrics) RecordTimersRestored(count int) {
	m.timers.RestoredTotal.Add(float64(count))
}

// RecordTimerCancelled records a cancelled timer
func (m *BusinessMetrics) RecordTimerCancelled(timerType string) {
	m.timers.CancelledTotal.WithLabelValues(timerType).Inc()
}

// RecordTimersMissed records missed timers
func (m *BusinessMetrics) RecordTimersMissed(count int) {
	m.timers.MissedTotal.Add(float64(count))
}

// RecordClassificationDuration records classification duration
func (m *BusinessMetrics) RecordClassificationDuration(classifier string, duration float64) {
	m.classification.Duration.WithLabelValues(classifier).Observe(duration)
}

// LLMClassificationsTotal records LLM classification
func (m *BusinessMetrics) LLMClassificationsTotal(status string) {
	m.classification.Total.WithLabelValues("llm", status).Inc()
}

// RecordClassificationL1CacheHit records L1 cache hit
func (m *BusinessMetrics) RecordClassificationL1CacheHit() {
	m.classification.L1CacheHits.Inc()
}

// RecordClassificationL2CacheHit records L2 cache hit
func (m *BusinessMetrics) RecordClassificationL2CacheHit() {
	m.classification.L2CacheHits.Inc()
}

// DeduplicationDurationSeconds records deduplication duration
func (m *BusinessMetrics) DeduplicationDurationSeconds(operation string, duration float64) {
	m.deduplication.Duration.WithLabelValues(operation).Observe(duration)
}

// DeduplicationCreatedTotal records new deduplication entries
func (m *BusinessMetrics) DeduplicationCreatedTotal() {
	m.deduplication.CreatedTotal.Inc()
}

// DeduplicationUpdatedTotal records updated deduplication entries
func (m *BusinessMetrics) DeduplicationUpdatedTotal() {
	m.deduplication.UpdatedTotal.Inc()
}

// DeduplicationIgnoredTotal records ignored deduplication entries
func (m *BusinessMetrics) DeduplicationIgnoredTotal() {
	m.deduplication.IgnoredTotal.Inc()
}

// RecordSilenceRequest records silence request
func (m *BusinessMetrics) RecordSilenceRequest(method, endpoint, status string, duration float64) {
	m.SilenceRequestDuration.WithLabelValues(method, endpoint, status).Observe(duration)
}

// SilenceRateLimitExceeded records rate limit exceeded
func (m *BusinessMetrics) SilenceRateLimitExceeded() {
	m.SilenceRateLimitHits.Inc()
}

// ================================================================================
// Filter Metrics
// ================================================================================

// FilterMetrics holds metrics for alert filtering
type FilterMetrics struct {
	BlockedTotal    *prometheus.CounterVec
	FilteredTotal   *prometheus.CounterVec
	FilterDuration  *prometheus.HistogramVec
}

// NewFilterMetrics creates new filter metrics
func NewFilterMetrics() *FilterMetrics {
	return &FilterMetrics{
		BlockedTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "filter",
				Name:      "blocked_total",
				Help:      "Total number of blocked alerts.",
			},
			[]string{"reason"},
		),
		FilteredTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "filter",
				Name:      "filtered_total",
				Help:      "Total number of filtered alerts.",
			},
			[]string{"result"},
		),
		FilterDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "filter",
				Name:      "duration_seconds",
				Help:      "Duration of filter operations.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"filter"},
		),
	}
}

// RecordBlockedAlert records blocked alert
func (m *FilterMetrics) RecordBlockedAlert(reason string) {
	m.BlockedTotal.WithLabelValues(reason).Inc()
}

// RecordAlertFiltered records filtered alert
func (m *FilterMetrics) RecordAlertFiltered(result string) {
	m.FilteredTotal.WithLabelValues(result).Inc()
}

// RecordFilterDuration records filter duration
func (m *FilterMetrics) RecordFilterDuration(duration float64) {
	m.FilterDuration.WithLabelValues("default").Observe(duration)
}

// ================================================================================
// Database Metrics
// ================================================================================

// DatabaseMetrics holds database-related metrics
type DatabaseMetrics struct {
	QueryDuration                 *prometheus.HistogramVec
	QueryErrors                   *prometheus.CounterVec
	ConnectionsActive             prometheus.Gauge
	ConnectionsTotal              *prometheus.CounterVec
	ConnectionsIdle               prometheus.Gauge
	QueryDurationSeconds          *prometheus.HistogramVec
	ErrorsTotal                   *prometheus.CounterVec
	ConnectionWaitDurationSeconds *prometheus.HistogramVec
	QueriesTotal                  *prometheus.CounterVec
}

// NewDatabaseMetrics creates a new DatabaseMetrics instance
func NewDatabaseMetrics() *DatabaseMetrics {
	return &DatabaseMetrics{
		QueryDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "query_duration_seconds",
				Help:      "Database query duration in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"query_type"},
		),
		QueryErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "query_errors_total",
				Help:      "Total number of database query errors.",
			},
			[]string{"query_type", "error_type"},
		),
		ConnectionsActive: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "connections_active",
				Help:      "Number of active database connections.",
			},
		),
		ConnectionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "connections_total",
				Help:      "Total number of database connections.",
			},
			[]string{"status"},
		),
		ConnectionsIdle: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "connections_idle",
				Help:      "Number of idle database connections.",
			},
		),
		QueryDurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "query_duration_seconds_hist",
				Help:      "Database query duration histogram.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"query_type"},
		),
		ErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "errors_total",
				Help:      "Total database errors.",
			},
			[]string{"error_type"},
		),
		ConnectionWaitDurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "connection_wait_duration_seconds",
				Help:      "Connection wait duration.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"pool"},
		),
		QueriesTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "database",
				Name:      "queries_total",
				Help:      "Total database queries.",
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
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	ResponseStatus  *prometheus.CounterVec
	PayloadSize     *prometheus.HistogramVec
	ErrorsTotal     *prometheus.CounterVec
	ProcessingStage *prometheus.HistogramVec
}

// NewWebhookMetrics creates a new WebhookMetrics instance
func NewWebhookMetrics() *WebhookMetrics {
	return &WebhookMetrics{
		RequestsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "webhook",
				Name:      "requests_total",
				Help:      "Total number of webhook requests.",
			},
			[]string{"endpoint", "method"},
		),
		RequestDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "webhook",
				Name:      "request_duration_seconds",
				Help:      "Webhook request duration in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"endpoint", "method"},
		),
		ResponseStatus: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "webhook",
				Name:      "response_status_total",
				Help:      "Total number of webhook responses by status code.",
			},
			[]string{"endpoint", "status_code"},
		),
		PayloadSize: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "webhook",
				Name:      "payload_size_bytes",
				Help:      "Webhook payload size in bytes.",
				Buckets:   []float64{100, 1000, 10000, 100000, 1000000},
			},
			[]string{"endpoint"},
		),
		ErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: Namespace,
				Subsystem: "webhook",
				Name:      "errors_total",
				Help:      "Total number of webhook errors.",
			},
			[]string{"endpoint", "error_type"},
		),
		ProcessingStage: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: Namespace,
				Subsystem: "webhook",
				Name:      "processing_stage_duration_seconds",
				Help:      "Duration of webhook processing stages.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"endpoint", "stage"},
		),
	}
}

// RecordProcessingStage records a webhook processing stage
func (m *WebhookMetrics) RecordProcessingStage(endpoint, stage string, duration float64) {
	m.ProcessingStage.WithLabelValues(endpoint, stage).Observe(duration)
}

// RecordError records a webhook error
func (m *WebhookMetrics) RecordError(endpoint, errorType string) {
	m.ErrorsTotal.WithLabelValues(endpoint, errorType).Inc()
}

// RecordPayloadSize records webhook payload size
func (m *WebhookMetrics) RecordPayloadSize(endpoint string, size int) {
	m.PayloadSize.WithLabelValues(endpoint).Observe(float64(size))
}

// RecordRequest records a webhook request
func (m *WebhookMetrics) RecordRequest(endpoint, method string, duration float64) {
	m.RequestsTotal.WithLabelValues(endpoint, method).Inc()
	m.RequestDuration.WithLabelValues(endpoint, method).Observe(duration)
}

// ================================================================================
// Stub types for compatibility
// ================================================================================

// MetricsManager is an alias for MetricsRegistry for backwards compatibility
type MetricsManager = MetricsRegistry

// EnrichmentModeManager - stub type for services
type EnrichmentModeManager struct{}
