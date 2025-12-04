// Package handlers provides HTTP handlers for the Alert History Service.
package handlers

import (
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsOnce sync.Once
	metricsInstance *SilenceUIMetrics
)

// SilenceUIMetrics provides Prometheus metrics for Silence UI operations.
// Phase 14: Observability enhancement.
type SilenceUIMetrics struct {
	// Page render metrics
	PageRenderDuration *prometheus.HistogramVec
	PageRenderTotal    *prometheus.CounterVec

	// Template cache metrics
	TemplateCacheHits   prometheus.Counter
	TemplateCacheMisses prometheus.Counter
	TemplateCacheSize   prometheus.Gauge

	// WebSocket metrics
	WebSocketConnections prometheus.Gauge
	WebSocketMessages    *prometheus.CounterVec

	// User action metrics
	UserActionsTotal *prometheus.CounterVec

	// Error metrics
	UIErrorsTotal *prometheus.CounterVec

	logger *slog.Logger
}

// NewSilenceUIMetrics creates a new SilenceUIMetrics instance.
// Uses singleton pattern to prevent duplicate metric registration.
func NewSilenceUIMetrics(logger *slog.Logger) *SilenceUIMetrics {
	metricsOnce.Do(func() {
		metricsInstance = &SilenceUIMetrics{
		PageRenderDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "page_render_duration_seconds",
				Help:      "Duration of UI page rendering in seconds",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2, 5},
			},
			[]string{"page"},
		),
		PageRenderTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "page_render_total",
				Help:      "Total number of page renders",
			},
			[]string{"page", "status"},
		),
		TemplateCacheHits: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "template_cache_hits_total",
				Help:      "Total number of template cache hits",
			},
		),
		TemplateCacheMisses: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "template_cache_misses_total",
				Help:      "Total number of template cache misses",
			},
		),
		TemplateCacheSize: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "template_cache_size",
				Help:      "Current number of cached templates",
			},
		),
		WebSocketConnections: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "websocket_connections",
				Help:      "Current number of WebSocket connections",
			},
		),
		WebSocketMessages: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "websocket_messages_total",
				Help:      "Total number of WebSocket messages sent",
			},
			[]string{"event_type"},
		),
		UserActionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "user_actions_total",
				Help:      "Total number of user actions",
			},
			[]string{"action", "status"},
		),
		UIErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "ui",
				Name:      "errors_total",
				Help:      "Total number of UI errors",
			},
			[]string{"error_type", "page"},
		),
		logger: logger,
		}
	})
	return metricsInstance
}

// RecordPageRender records a page render metric.
func (m *SilenceUIMetrics) RecordPageRender(page string, duration time.Duration, status string) {
	m.PageRenderDuration.WithLabelValues(page).Observe(duration.Seconds())
	m.PageRenderTotal.WithLabelValues(page, status).Inc()
}

// RecordTemplateCacheHit records a template cache hit.
func (m *SilenceUIMetrics) RecordTemplateCacheHit() {
	m.TemplateCacheHits.Inc()
}

// RecordTemplateCacheMiss records a template cache miss.
func (m *SilenceUIMetrics) RecordTemplateCacheMiss() {
	m.TemplateCacheMisses.Inc()
}

// UpdateTemplateCacheSize updates the template cache size metric.
func (m *SilenceUIMetrics) UpdateTemplateCacheSize(size int) {
	m.TemplateCacheSize.Set(float64(size))
}

// UpdateWebSocketConnections updates the WebSocket connections metric.
func (m *SilenceUIMetrics) UpdateWebSocketConnections(count int) {
	m.WebSocketConnections.Set(float64(count))
}

// RecordWebSocketMessage records a WebSocket message sent.
func (m *SilenceUIMetrics) RecordWebSocketMessage(eventType string) {
	m.WebSocketMessages.WithLabelValues(eventType).Inc()
}

// RecordUserAction records a user action.
func (m *SilenceUIMetrics) RecordUserAction(action, status string) {
	m.UserActionsTotal.WithLabelValues(action, status).Inc()
}

// RecordError records a UI error.
func (m *SilenceUIMetrics) RecordError(errorType, page string) {
	m.UIErrorsTotal.WithLabelValues(errorType, page).Inc()
}
