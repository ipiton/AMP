package publishing

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// PublishingModeMetrics holds all Prometheus metrics for publishing mode
type PublishingModeMetrics struct {
	// ModeCurrent tracks current publishing mode (0=normal, 1=metrics-only)
	ModeCurrent prometheus.Gauge

	// ModeTransitionsTotal counts total mode transitions
	ModeTransitionsTotal prometheus.Counter

	// ModeDurationSeconds tracks duration spent in each mode
	ModeDurationSeconds *prometheus.HistogramVec

	// ModeCheckDurationSeconds tracks time spent checking mode
	ModeCheckDurationSeconds prometheus.Histogram

	// SubmissionsRejectedTotal counts rejected submissions due to metrics-only mode
	SubmissionsRejectedTotal prometheus.Counter

	// JobsSkippedTotal counts skipped jobs due to metrics-only mode
	JobsSkippedTotal prometheus.Counter
}

// NewPublishingModeMetrics creates and registers all mode metrics
//
// Metrics:
//   1. publishing_mode_current - Gauge (0=normal, 1=metrics-only)
//   2. publishing_mode_transitions_total - Counter
//   3. publishing_mode_duration_seconds - Histogram (mode)
//   4. publishing_mode_check_duration_seconds - Histogram
//   5. publishing_submissions_rejected_total{reason="metrics_only"} - Counter
//   6. publishing_jobs_skipped_total{reason="metrics_only"} - Counter
//
// Returns:
//   *PublishingModeMetrics: Registered metrics
func NewPublishingModeMetrics(namespace, subsystem string) *PublishingModeMetrics {
	return &PublishingModeMetrics{
		ModeCurrent: promauto.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "mode_current",
				Help:      "Current publishing mode (0=normal, 1=metrics-only)",
			},
		),

		ModeTransitionsTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "mode_transitions_total",
				Help:      "Total number of mode transitions",
			},
		),

		ModeDurationSeconds: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "mode_duration_seconds",
				Help:      "Duration spent in each mode (seconds)",
				Buckets:   []float64{1, 5, 10, 30, 60, 300, 600, 1800, 3600}, // 1s to 1h
			},
			[]string{"mode"}, // mode: normal, metrics-only
		),

		ModeCheckDurationSeconds: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "mode_check_duration_seconds",
				Help:      "Time spent checking mode (seconds)",
				Buckets:   []float64{0.000001, 0.000005, 0.00001, 0.00005, 0.0001, 0.0005, 0.001}, // 1µs to 1ms
			},
		),

		SubmissionsRejectedTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "submissions_rejected_total",
				Help:      "Total number of rejected submissions",
			},
		),

		JobsSkippedTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "jobs_skipped_total",
				Help:      "Total number of skipped jobs",
			},
		),
	}
}

// RecordModeTransition records a mode transition
func (m *PublishingModeMetrics) RecordModeTransition(from, to Mode, durationSeconds float64) {
	m.ModeTransitionsTotal.Inc()
	if to == ModeMetricsOnly {
		m.ModeCurrent.Set(1)
	} else {
		m.ModeCurrent.Set(0)
	}
	m.ModeDurationSeconds.WithLabelValues(from.String()).Observe(durationSeconds)
}

// RecordSubmissionRejected records a rejected submission
func (m *PublishingModeMetrics) RecordSubmissionRejected() {
	m.SubmissionsRejectedTotal.Inc()
}

// RecordJobSkipped records a skipped job
func (m *PublishingModeMetrics) RecordJobSkipped() {
	m.JobsSkippedTotal.Inc()
}

// RecordModeCheckDuration records mode check duration
func (m *PublishingModeMetrics) RecordModeCheckDuration(durationSeconds float64) {
	m.ModeCheckDurationSeconds.Observe(durationSeconds)
}
