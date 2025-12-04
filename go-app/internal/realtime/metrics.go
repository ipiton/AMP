// Package realtime provides real-time event broadcasting system for dashboard updates.
package realtime

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RealtimeMetrics tracks real-time system metrics.
type RealtimeMetrics struct {
	// ConnectionsActive is the current number of active connections (SSE + WebSocket)
	ConnectionsActive prometheus.Gauge

	// EventsTotal is the total number of events published (by type and source)
	EventsTotal *prometheus.CounterVec

	// EventLatencySeconds is the latency from event creation to delivery (histogram)
	EventLatencySeconds prometheus.Histogram

	// ErrorsTotal is the total number of errors (by error type)
	ErrorsTotal *prometheus.CounterVec

	// ReconnectTotal is the total number of reconnections
	ReconnectTotal prometheus.Counter

	// BroadcastDuration is the duration of broadcast operations (histogram)
	BroadcastDuration prometheus.Histogram
}

// NewRealtimeMetrics creates a new RealtimeMetrics instance.
func NewRealtimeMetrics(namespace string) *RealtimeMetrics {
	return &RealtimeMetrics{
		ConnectionsActive: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "realtime",
			Name:      "connections_active_total",
			Help:      "Current number of active real-time connections (SSE + WebSocket)",
		}),

		EventsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "realtime",
			Name:      "events_total",
			Help:      "Total number of events published (by type and source)",
		}, []string{"type", "source"}),

		EventLatencySeconds: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "realtime",
			Name:      "event_latency_seconds",
			Help:      "Latency from event creation to delivery (seconds)",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
		}),

		ErrorsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "realtime",
			Name:      "errors_total",
			Help:      "Total number of errors (by error type)",
		}, []string{"error_type"}),

		ReconnectTotal: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "realtime",
			Name:      "reconnect_total",
			Help:      "Total number of reconnections",
		}),

		BroadcastDuration: promauto.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "realtime",
			Name:      "broadcast_duration_seconds",
			Help:      "Duration of broadcast operations (seconds)",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
		}),
	}
}
