package routing

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// MultiReceiverMetrics tracks Prometheus metrics for multi-receiver publishing.
//
// Metrics:
//   - multi_receiver_publishes_total: Count of multi-receiver publishes
//   - multi_receiver_duration_seconds: Histogram of publish duration
//   - receiver_publish_success_total: Count of successes by receiver
//   - receiver_publish_failure_total: Count of failures by receiver + error_type
//   - parallel_receivers_count: Histogram of parallel receiver count
//
// All metrics are prefixed with "alert_history_" namespace.
type MultiReceiverMetrics struct {
	// MultiReceiverPublishesTotal counts multi-receiver publishes
	MultiReceiverPublishesTotal prometheus.Counter

	// MultiReceiverDuration tracks total duration (parallel)
	MultiReceiverDuration prometheus.Histogram

	// ReceiverPublishSuccessTotal counts successes by receiver
	ReceiverPublishSuccessTotal *prometheus.CounterVec

	// ReceiverPublishFailureTotal counts failures by receiver + error_type
	ReceiverPublishFailureTotal *prometheus.CounterVec

	// ParallelReceiversCount tracks number of parallel receivers
	ParallelReceiversCount prometheus.Histogram
}

// NewMultiReceiverMetrics creates Prometheus metrics.
//
// All metrics are auto-registered with the default Prometheus registry.
//
// Returns:
//   - *MultiReceiverMetrics: A new metrics instance
//
// Example:
//
//	metrics := NewMultiReceiverMetrics()
//	metrics.RecordPublish(result)
func NewMultiReceiverMetrics() *MultiReceiverMetrics {
	return &MultiReceiverMetrics{
		MultiReceiverPublishesTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "multi_receiver",
				Name:      "publishes_total",
				Help:      "Total multi-receiver publishes",
			},
		),

		MultiReceiverDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "multi_receiver",
				Name:      "duration_seconds",
				Help:      "Multi-receiver publish duration (parallel)",
				// Buckets: 10ms to 10s (exponential)
				Buckets: prometheus.ExponentialBuckets(0.01, 2, 10),
			},
		),

		ReceiverPublishSuccessTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "receiver",
				Name:      "publish_success_total",
				Help:      "Successful publishes by receiver",
			},
			[]string{"receiver"},
		),

		ReceiverPublishFailureTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "receiver",
				Name:      "publish_failure_total",
				Help:      "Failed publishes by receiver + error type",
			},
			[]string{"receiver", "error_type"},
		),

		ParallelReceiversCount: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "multi_receiver",
				Name:      "parallel_receivers_count",
				Help:      "Number of parallel receivers per publish",
				// Buckets: 1 to 10 receivers (linear)
				Buckets: prometheus.LinearBuckets(1, 1, 10),
			},
		),
	}
}

// RecordPublish records a multi-receiver publish result.
//
// Updates all relevant metrics:
//   - MultiReceiverPublishesTotal: Incremented
//   - MultiReceiverDuration: Duration recorded
//   - ReceiverPublishSuccessTotal: Per-receiver success counts
//   - ReceiverPublishFailureTotal: Per-receiver failure counts
//   - ParallelReceiversCount: Number of receivers
//
// Parameters:
//   - result: The publish result
func (m *MultiReceiverMetrics) RecordPublish(result *MultiReceiverResult) {
	// Overall metrics
	m.MultiReceiverPublishesTotal.Inc()
	m.MultiReceiverDuration.Observe(result.TotalDuration.Seconds())
	m.ParallelReceiversCount.Observe(float64(result.TotalReceivers))

	// Per-receiver metrics
	for _, r := range result.Results {
		if r.Success {
			m.ReceiverPublishSuccessTotal.WithLabelValues(r.Receiver).Inc()
		} else {
			errorType := "unknown"
			if r.Error != nil {
				errorType = classifyError(r.Error)
			}
			m.ReceiverPublishFailureTotal.WithLabelValues(r.Receiver, errorType).Inc()
		}
	}
}

// RecordError records a multi-receiver error (evaluation failed, no receivers).
//
// Parameters:
//   - errorType: The error type (e.g., "evaluation_failed", "no_receivers")
func (m *MultiReceiverMetrics) RecordError(errorType string) {
	// Use a special "error" receiver for evaluation failures
	m.ReceiverPublishFailureTotal.WithLabelValues("_evaluation", errorType).Inc()
}

// classifyError classifies an error into a type for metrics.
func classifyError(err error) string {
	if err == nil {
		return "unknown"
	}

	errStr := err.Error()

	// Classify by error message
	switch {
	case contains(errStr, "timeout") || contains(errStr, "deadline"):
		return "timeout"
	case contains(errStr, "connection") || contains(errStr, "network"):
		return "network"
	case contains(errStr, "unauthorized") || contains(errStr, "forbidden"):
		return "auth"
	case contains(errStr, "panic"):
		return "panic"
	case contains(errStr, "no publisher"):
		return "no_publisher"
	default:
		return "other"
	}
}

// contains checks if s contains substr (case-insensitive).
func contains(s, substr string) bool {
	// Simple case-insensitive check
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			findSubstring(s, substr)))
}

// findSubstring checks if substr is in s.
func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
