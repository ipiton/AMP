package v2

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const httpSubsystem = "http"

// HTTPMetrics provides metrics for incoming HTTP requests.
//
// This struct consolidates HTTP metrics that were scattered across:
//   - pkg/metrics/metrics.go (WebhookMetrics, ProxyMetrics HTTP fields)
//   - internal/ui/template_metrics.go (TemplateMetrics)
//   - Various handler files
//
// Migration:
//
//	Old: webhookMetrics.RequestsTotal.WithLabelValues("/webhook", "POST").Inc()
//	New: registry.HTTP.RecordRequest("POST", "/webhook", 200, duration)
type HTTPMetrics struct {
	// ========================================================================
	// Request Metrics
	// ========================================================================

	// requestsTotal counts total HTTP requests by method, path, and status code.
	// Labels: method, path, status_code
	requestsTotal *prometheus.CounterVec

	// requestDurationSeconds measures request duration by method and path.
	// Labels: method, path
	requestDurationSeconds *prometheus.HistogramVec

	// requestSizeBytes measures request body size.
	// Labels: method
	requestSizeBytes *prometheus.HistogramVec

	// responseSizeBytes measures response body size.
	// Labels: method
	responseSizeBytes *prometheus.HistogramVec

	// requestsInFlight tracks concurrent requests.
	requestsInFlight prometheus.Gauge

	// ========================================================================
	// Error Metrics
	// ========================================================================

	// errorsTotal counts HTTP errors by method, path, and error type.
	// Labels: method, path, error_type
	errorsTotal *prometheus.CounterVec

	// validationErrorsTotal counts validation errors by endpoint.
	// Labels: endpoint, validation_type
	validationErrorsTotal *prometheus.CounterVec

	// ========================================================================
	// Webhook-specific Metrics
	// ========================================================================

	// webhookProcessingStageSeconds measures duration of processing stages.
	// Labels: endpoint, stage
	webhookProcessingStageSeconds *prometheus.HistogramVec

	// ========================================================================
	// Template Metrics
	// ========================================================================

	// templateRenderTotal counts template renders by template and status.
	// Labels: template, status
	templateRenderTotal *prometheus.CounterVec

	// templateRenderDurationSeconds measures template render duration.
	templateRenderDurationSeconds prometheus.Histogram

	// templateCacheHitsTotal counts template cache hits.
	templateCacheHitsTotal prometheus.Counter
}

// NewHTTPMetrics creates and registers all HTTP metrics.
func NewHTTPMetrics(registerer prometheus.Registerer) *HTTPMetrics {
	m := &HTTPMetrics{}

	// Request Metrics
	m.requestsTotal = newCounterVec(registerer, httpSubsystem,
		"requests_total",
		"Total HTTP requests by method, path, and status code",
		[]string{"method", "path", "status_code"})

	m.requestDurationSeconds = newHistogramVec(registerer, httpSubsystem,
		"request_duration_seconds",
		"HTTP request duration in seconds",
		DurationBuckets,
		[]string{"method", "path"})

	m.requestSizeBytes = newHistogramVec(registerer, httpSubsystem,
		"request_size_bytes",
		"HTTP request body size in bytes",
		PayloadSizeBuckets,
		[]string{"method"})

	m.responseSizeBytes = newHistogramVec(registerer, httpSubsystem,
		"response_size_bytes",
		"HTTP response body size in bytes",
		PayloadSizeBuckets,
		[]string{"method"})

	m.requestsInFlight = newGauge(registerer, httpSubsystem,
		"requests_in_flight",
		"Number of HTTP requests currently being processed")

	// Error Metrics
	m.errorsTotal = newCounterVec(registerer, httpSubsystem,
		"errors_total",
		"HTTP errors by method, path, and error type",
		[]string{"method", "path", "error_type"})

	m.validationErrorsTotal = newCounterVec(registerer, httpSubsystem,
		"validation_errors_total",
		"Validation errors by endpoint and validation type",
		[]string{"endpoint", "validation_type"})

	// Webhook Processing
	m.webhookProcessingStageSeconds = newHistogramVec(registerer, httpSubsystem,
		"webhook_processing_stage_seconds",
		"Webhook processing stage duration",
		DurationBuckets,
		[]string{"endpoint", "stage"})

	// Template Metrics
	m.templateRenderTotal = newCounterVec(registerer, httpSubsystem,
		"template_render_total",
		"Template renders by template and status",
		[]string{"template", "status"})

	m.templateRenderDurationSeconds = newHistogram(registerer, httpSubsystem,
		"template_render_duration_seconds",
		"Template render duration in seconds",
		[]float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0})

	m.templateCacheHitsTotal = newCounter(registerer, httpSubsystem,
		"template_cache_hits_total",
		"Total template cache hits")

	return m
}

// ============================================================================
// Request Methods
// ============================================================================

// RecordRequest records a complete HTTP request.
func (m *HTTPMetrics) RecordRequest(method, path string, statusCode int, duration time.Duration) {
	m.requestsTotal.WithLabelValues(method, path, fmt.Sprintf("%d", statusCode)).Inc()
	m.requestDurationSeconds.WithLabelValues(method, path).Observe(duration.Seconds())
}

// RecordRequestSize records the request body size.
func (m *HTTPMetrics) RecordRequestSize(method string, bytes int) {
	m.requestSizeBytes.WithLabelValues(method).Observe(float64(bytes))
}

// RecordResponseSize records the response body size.
func (m *HTTPMetrics) RecordResponseSize(method string, bytes int) {
	m.responseSizeBytes.WithLabelValues(method).Observe(float64(bytes))
}

// IncRequestsInFlight increments the in-flight request counter.
func (m *HTTPMetrics) IncRequestsInFlight() {
	m.requestsInFlight.Inc()
}

// DecRequestsInFlight decrements the in-flight request counter.
func (m *HTTPMetrics) DecRequestsInFlight() {
	m.requestsInFlight.Dec()
}

// ============================================================================
// Error Methods
// ============================================================================

// RecordError records an HTTP error.
func (m *HTTPMetrics) RecordError(method, path, errorType string) {
	m.errorsTotal.WithLabelValues(method, path, errorType).Inc()
}

// RecordValidationError records a validation error.
func (m *HTTPMetrics) RecordValidationError(endpoint, validationType string) {
	m.validationErrorsTotal.WithLabelValues(endpoint, validationType).Inc()
}

// ============================================================================
// Webhook Methods
// ============================================================================

// RecordWebhookStage records a webhook processing stage duration.
func (m *HTTPMetrics) RecordWebhookStage(endpoint, stage string, duration time.Duration) {
	m.webhookProcessingStageSeconds.WithLabelValues(endpoint, stage).Observe(duration.Seconds())
}

// ============================================================================
// Template Methods
// ============================================================================

// RecordTemplateRender records a template render.
func (m *HTTPMetrics) RecordTemplateRender(templateName string, success bool, duration time.Duration) {
	status := "success"
	if !success {
		status = "error"
	}
	m.templateRenderTotal.WithLabelValues(templateName, status).Inc()
	m.templateRenderDurationSeconds.Observe(duration.Seconds())
}

// RecordTemplateCacheHit records a template cache hit.
func (m *HTTPMetrics) RecordTemplateCacheHit() {
	m.templateCacheHitsTotal.Inc()
}
