package publishing

import (
	"github.com/prometheus/client_golang/prometheus"
)

// WebhookMetrics holds Prometheus metrics for webhook publisher
type WebhookMetrics struct {
	// RequestsTotal counts total webhook requests by target, status, and method
	RequestsTotal *prometheus.CounterVec

	// RequestDuration measures webhook request duration by target and status
	RequestDuration *prometheus.HistogramVec

	// ErrorsTotal counts webhook errors by target and error type
	ErrorsTotal *prometheus.CounterVec

	// RetriesTotal counts retry attempts by target and attempt number
	RetriesTotal *prometheus.CounterVec

	// PayloadSize measures webhook payload size distribution by target
	PayloadSize *prometheus.HistogramVec

	// AuthFailures counts authentication failures by target and auth type
	AuthFailures *prometheus.CounterVec

	// ValidationErrors counts validation errors by target and validation type
	ValidationErrors *prometheus.CounterVec

	// TimeoutErrors counts timeout errors by target
	TimeoutErrors *prometheus.CounterVec
}

// NewWebhookMetrics creates and registers webhook Prometheus metrics
func NewWebhookMetrics(registry prometheus.Registerer) *WebhookMetrics {
	metrics := &WebhookMetrics{
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_requests_total",
				Help: "Total number of webhook requests",
			},
			[]string{"target", "status", "method"},
		),
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "webhook_request_duration_seconds",
				Help:    "Webhook request duration in seconds",
				Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"target", "status"},
		),
		ErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_errors_total",
				Help: "Total number of webhook errors",
			},
			[]string{"target", "error_type"},
		),
		RetriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_retries_total",
				Help: "Total number of webhook retry attempts",
			},
			[]string{"target", "attempt"},
		),
		PayloadSize: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "webhook_payload_size_bytes",
				Help:    "Webhook payload size in bytes",
				Buckets: prometheus.ExponentialBuckets(1024, 2, 12), // 1KB to 4MB
			},
			[]string{"target"},
		),
		AuthFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_auth_failures_total",
				Help: "Total number of webhook authentication failures",
			},
			[]string{"target", "auth_type"},
		),
		ValidationErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_validation_errors_total",
				Help: "Total number of webhook validation errors",
			},
			[]string{"target", "validation_type"},
		),
		TimeoutErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "webhook_timeout_errors_total",
				Help: "Total number of webhook timeout errors",
			},
			[]string{"target"},
		),
	}

	// Register all metrics
	if registry != nil {
		registry.MustRegister(
			metrics.RequestsTotal,
			metrics.RequestDuration,
			metrics.ErrorsTotal,
			metrics.RetriesTotal,
			metrics.PayloadSize,
			metrics.AuthFailures,
			metrics.ValidationErrors,
			metrics.TimeoutErrors,
		)
	}

	return metrics
}

// RecordRequest records a webhook request metric
func (m *WebhookMetrics) RecordRequest(target, status, method string) {
	if m.RequestsTotal != nil {
		m.RequestsTotal.WithLabelValues(target, status, method).Inc()
	}
}

// RecordDuration records webhook request duration
func (m *WebhookMetrics) RecordDuration(target, status string, durationSeconds float64) {
	if m.RequestDuration != nil {
		m.RequestDuration.WithLabelValues(target, status).Observe(durationSeconds)
	}
}

// RecordError records a webhook error
func (m *WebhookMetrics) RecordError(target, errorType string) {
	if m.ErrorsTotal != nil {
		m.ErrorsTotal.WithLabelValues(target, errorType).Inc()
	}
}

// RecordRetry records a retry attempt
func (m *WebhookMetrics) RecordRetry(target string, attempt int) {
	if m.RetriesTotal != nil {
		m.RetriesTotal.WithLabelValues(target, string(rune(attempt+'0'))).Inc()
	}
}

// RecordPayloadSize records webhook payload size
func (m *WebhookMetrics) RecordPayloadSize(target string, sizeBytes int) {
	if m.PayloadSize != nil {
		m.PayloadSize.WithLabelValues(target).Observe(float64(sizeBytes))
	}
}

// RecordAuthFailure records an authentication failure
func (m *WebhookMetrics) RecordAuthFailure(target, authType string) {
	if m.AuthFailures != nil {
		m.AuthFailures.WithLabelValues(target, authType).Inc()
	}
}

// RecordValidationError records a validation error
func (m *WebhookMetrics) RecordValidationError(target, validationType string) {
	if m.ValidationErrors != nil {
		m.ValidationErrors.WithLabelValues(target, validationType).Inc()
	}
}

// RecordTimeoutError records a timeout error
func (m *WebhookMetrics) RecordTimeoutError(target string) {
	if m.TimeoutErrors != nil {
		m.TimeoutErrors.WithLabelValues(target).Inc()
	}
}
