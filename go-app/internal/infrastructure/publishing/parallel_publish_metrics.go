package publishing

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ParallelPublishMetrics exports 10+ Prometheus metrics for parallel publishing observability.
//
// This struct provides comprehensive metrics for:
//   - Parallel publish duration (histogram)
//   - Publish results (total, success, partial success, failure)
//   - Per-target metrics (success, failure, skipped)
//   - Goroutine tracking
//
// All metrics follow naming convention:
//
//	alert_history_publishing_parallel_<metric_name>_<unit>
//
// Example:
//
//	metrics := NewParallelPublishMetrics(prometheus.DefaultRegisterer)
//	metrics.RecordPublish(result)
type ParallelPublishMetrics struct {
	// Duration histogram
	duration *prometheus.HistogramVec // Parallel publish duration by result

	// Counters
	total          *prometheus.CounterVec // Total publishes by result
	success        prometheus.Counter     // Successful publishes (≥1 target succeeded)
	partialSuccess prometheus.Counter     // Partial success (some succeeded, some failed)
	failure        prometheus.Counter     // Failed publishes (all targets failed)

	// Per-target counters
	targetsTotal   *prometheus.CounterVec // Total targets by type
	targetsSuccess *prometheus.CounterVec // Successful targets by name
	targetsFailure *prometheus.CounterVec // Failed targets by name, error_type
	targetsSkipped *prometheus.CounterVec // Skipped targets by name, skip_reason

	// Goroutine gauge
	goroutines prometheus.Gauge // Active goroutines for parallel publishing
}

// NewParallelPublishMetrics creates and registers all metrics.
//
// Parameters:
//   - registry: Prometheus registerer (usually prometheus.DefaultRegisterer)
//
// Returns:
//   - *ParallelPublishMetrics: Fully initialized metrics struct
//
// Note: Panics if metrics already registered (duplicate)
func NewParallelPublishMetrics(registry prometheus.Registerer) *ParallelPublishMetrics {
	m := &ParallelPublishMetrics{
		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_duration_seconds",
				Help:      "Parallel publish duration in seconds by result (success/partial_success/failure)",
				Buckets:   []float64{0.05, 0.1, 0.2, 0.5, 1.0, 2.0, 5.0, 10.0}, // 50ms to 10s
			},
			[]string{"result"},
		),

		total: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_total",
				Help:      "Total parallel publishes by result (success/partial_success/failure)",
			},
			[]string{"result"},
		),

		success: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_success_total",
				Help:      "Total successful parallel publishes (≥1 target succeeded)",
			},
		),

		partialSuccess: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_partial_success_total",
				Help:      "Total partial success parallel publishes (some succeeded, some failed)",
			},
		),

		failure: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_failure_total",
				Help:      "Total failed parallel publishes (all targets failed)",
			},
		),

		targetsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_targets_total",
				Help:      "Total targets attempted by type (rootly/pagerduty/slack/webhook)",
			},
			[]string{"target_type"},
		),

		targetsSuccess: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_targets_success_total",
				Help:      "Total successful target publishes by name",
			},
			[]string{"target_name"},
		),

		targetsFailure: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_targets_failure_total",
				Help:      "Total failed target publishes by name and error_type",
			},
			[]string{"target_name", "error_type"},
		),

		targetsSkipped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_targets_skipped_total",
				Help:      "Total skipped targets by name and skip_reason (unhealthy/circuit_open/disabled)",
			},
			[]string{"target_name", "skip_reason"},
		),

		goroutines: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "parallel_goroutines",
				Help:      "Active goroutines for parallel publishing",
			},
		),
	}

	// Register all metrics
	registry.MustRegister(
		m.duration,
		m.total,
		m.success,
		m.partialSuccess,
		m.failure,
		m.targetsTotal,
		m.targetsSuccess,
		m.targetsFailure,
		m.targetsSkipped,
		m.goroutines,
	)

	return m
}

// RecordPublish records a parallel publish result.
//
// This method updates:
//   - Duration histogram
//   - Total counter (by result)
//   - Success/partialSuccess/failure counters
//   - Per-target metrics (success/failure/skipped)
//
// Parameters:
//   - result: Parallel publish result
//
// Example:
//
//	result, err := parallelPublisher.PublishToMultiple(ctx, alert, targets)
//	metrics.RecordPublish(result)
func (m *ParallelPublishMetrics) RecordPublish(result *ParallelPublishResult) {
	// Determine result type
	var resultType string
	if result.AllSucceeded() {
		resultType = "success"
		m.success.Inc()
	} else if result.IsPartialSuccess {
		resultType = "partial_success"
		m.partialSuccess.Inc()
	} else {
		resultType = "failure"
		m.failure.Inc()
	}

	// Update duration histogram
	m.duration.WithLabelValues(resultType).Observe(result.Duration.Seconds())

	// Update total counter
	m.total.WithLabelValues(resultType).Inc()

	// Update per-target metrics
	for _, targetResult := range result.Results {
		// Update targets total
		m.targetsTotal.WithLabelValues(targetResult.TargetType).Inc()

		// Update per-target counters
		if targetResult.Skipped {
			// Skipped target
			skipReason := "unknown"
			if targetResult.SkipReason != nil {
				skipReason = *targetResult.SkipReason
			}
			m.targetsSkipped.WithLabelValues(targetResult.TargetName, skipReason).Inc()
		} else if targetResult.Success {
			// Successful target
			m.targetsSuccess.WithLabelValues(targetResult.TargetName).Inc()
		} else {
			// Failed target
			errorType := "unknown"
			if targetResult.Error != nil {
				// Classify error type (simplified)
				errorType = "publish_error"
			}
			m.targetsFailure.WithLabelValues(targetResult.TargetName, errorType).Inc()
		}
	}
}

// UpdateGoroutines updates the active goroutines gauge.
//
// Parameters:
//   - count: Number of active goroutines
//
// Example:
//
//	metrics.UpdateGoroutines(10) // 10 goroutines spawned
//	// ... parallel publishing ...
//	metrics.UpdateGoroutines(0)  // All goroutines completed
func (m *ParallelPublishMetrics) UpdateGoroutines(count int) {
	m.goroutines.Set(float64(count))
}
