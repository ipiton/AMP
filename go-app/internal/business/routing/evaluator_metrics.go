package routing

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// EvaluatorMetrics tracks Prometheus metrics for route evaluation.
//
// Metrics:
//   - evaluations_total: Count of evaluations by receiver
//   - evaluation_duration_seconds: Histogram of evaluation latency
//   - no_match_total: Count of no-match fallbacks to root
//   - multi_receiver_total: Count of multi-receiver evaluations
//   - errors_total: Count of evaluation errors by type
//
// All metrics are prefixed with "alert_history_routing_" namespace.
type EvaluatorMetrics struct {
	// EvaluationsTotal counts evaluations by receiver
	EvaluationsTotal *prometheus.CounterVec

	// EvaluationDuration tracks evaluation latency
	EvaluationDuration prometheus.Histogram

	// NoMatchTotal counts no-match fallbacks to root
	NoMatchTotal prometheus.Counter

	// MultiReceiverTotal counts multi-receiver evaluations
	MultiReceiverTotal prometheus.Counter

	// ErrorsTotal counts evaluation errors by type
	ErrorsTotal *prometheus.CounterVec
}

// NewEvaluatorMetrics creates Prometheus metrics for RouteEvaluator.
//
// All metrics are auto-registered with the default Prometheus registry.
//
// Returns:
//   - *EvaluatorMetrics: A new metrics instance
//
// Example:
//
//	metrics := NewEvaluatorMetrics()
//	metrics.RecordEvaluation("pagerduty", 50*time.Microsecond)
func NewEvaluatorMetrics() *EvaluatorMetrics {
	return &EvaluatorMetrics{
		EvaluationsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "evaluations_total",
				Help:      "Total routing evaluations by receiver",
			},
			[]string{"receiver"},
		),

		EvaluationDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "evaluation_duration_seconds",
				Help:      "Time to make routing decision",
				// Buckets: 10µs to 10ms (exponential)
				Buckets: prometheus.ExponentialBuckets(0.00001, 2, 10),
			},
		),

		NoMatchTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "no_match_total",
				Help:      "Total no-match fallbacks to root receiver",
			},
		),

		MultiReceiverTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "multi_receiver_total",
				Help:      "Total multi-receiver evaluations (continue=true)",
			},
		),

		ErrorsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "routing",
				Name:      "errors_total",
				Help:      "Total evaluation errors by type",
			},
			[]string{"error_type"},
		),
	}
}

// RecordEvaluation records a successful routing evaluation.
//
// Parameters:
//   - receiver: The receiver name (e.g., "pagerduty")
//   - duration: The evaluation duration
//
// Updates:
//   - EvaluationsTotal counter (by receiver label)
//   - EvaluationDuration histogram
func (m *EvaluatorMetrics) RecordEvaluation(receiver string, duration time.Duration) {
	m.EvaluationsTotal.WithLabelValues(receiver).Inc()
	m.EvaluationDuration.Observe(duration.Seconds())
}

// RecordError records an evaluation error.
//
// Parameters:
//   - errorType: The error type (e.g., "empty_tree", "no_match", "no_receiver")
//
// Updates:
//   - ErrorsTotal counter (by error_type label)
func (m *EvaluatorMetrics) RecordError(errorType string) {
	m.ErrorsTotal.WithLabelValues(errorType).Inc()
}
