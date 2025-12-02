package publishing

import (
	"github.com/prometheus/client_golang/prometheus"
)

// RefreshMetrics holds Prometheus metrics for target refresh.
//
// This struct provides 5 metrics:
//   1. Total: Counter of refresh attempts (labels: status=success|failed)
//   2. Duration: Histogram of refresh duration (labels: status=success|failed)
//   3. ErrorsTotal: Counter of errors by type (labels: error_type)
//   4. LastSuccessTimestamp: Gauge of last successful refresh (Unix timestamp)
//   5. InProgress: Gauge indicating if refresh currently running (1=running, 0=idle)
//
// Thread Safety: Prometheus metrics are thread-safe
//
// Example Usage:
//
//	// Create metrics
//	metrics := NewRefreshMetrics(prometheus.DefaultRegisterer)
//
//	// Record refresh attempt
//	startTime := time.Now()
//	err := doRefresh()
//	duration := time.Since(startTime)
//
//	if err != nil {
//	    metrics.Total.WithLabelValues("failed").Inc()
//	    metrics.Duration.WithLabelValues("failed").Observe(duration.Seconds())
//	    metrics.ErrorsTotal.WithLabelValues("network").Inc()
//	} else {
//	    metrics.Total.WithLabelValues("success").Inc()
//	    metrics.Duration.WithLabelValues("success").Observe(duration.Seconds())
//	    metrics.LastSuccessTimestamp.Set(float64(time.Now().Unix()))
//	}
type RefreshMetrics struct {
	// Total tracks total refresh attempts by status.
	//
	// Labels:
	//   - status: "success" or "failed"
	//
	// Type: Counter (monotonically increasing)
	//
	// PromQL Examples:
	//   - Rate of refresh attempts: rate(alert_history_publishing_refresh_total[5m])
	//   - Success rate: rate(alert_history_publishing_refresh_total{status="success"}[5m])
	//   - Failure rate: rate(alert_history_publishing_refresh_total{status="failed"}[5m])
	Total *prometheus.CounterVec

	// Duration tracks refresh duration by status.
	//
	// Labels:
	//   - status: "success" or "failed"
	//
	// Type: Histogram (distribution)
	//
	// Buckets: 0.1s, 0.5s, 1s, 2s, 5s, 10s, 30s, 60s
	//
	// PromQL Examples:
	//   - p95 success duration: histogram_quantile(0.95, alert_history_publishing_refresh_duration_seconds{status="success"})
	//   - p99 duration: histogram_quantile(0.99, alert_history_publishing_refresh_duration_seconds)
	//   - Average duration: rate(alert_history_publishing_refresh_duration_seconds_sum[5m]) / rate(alert_history_publishing_refresh_duration_seconds_count[5m])
	Duration *prometheus.HistogramVec

	// ErrorsTotal tracks errors by type.
	//
	// Labels:
	//   - error_type: "network", "timeout", "auth", "parse", "k8s_api", "k8s_auth", "dns", "cancelled", "unknown"
	//
	// Type: Counter (monotonically increasing)
	//
	// PromQL Examples:
	//   - Error rate by type: rate(alert_history_publishing_refresh_errors_total[5m])
	//   - Network errors: alert_history_publishing_refresh_errors_total{error_type="network"}
	//   - Timeout errors: alert_history_publishing_refresh_errors_total{error_type="timeout"}
	ErrorsTotal *prometheus.CounterVec

	// LastSuccessTimestamp tracks last successful refresh.
	//
	// Type: Gauge (set to Unix timestamp)
	//
	// PromQL Examples:
	//   - Time since last success: time() - alert_history_publishing_refresh_last_success_timestamp
	//   - Alert if stale (>15m): time() - alert_history_publishing_refresh_last_success_timestamp > 900
	LastSuccessTimestamp prometheus.Gauge

	// InProgress indicates if refresh currently running.
	//
	// Values:
	//   - 1: Refresh in progress
	//   - 0: Refresh idle
	//
	// Type: Gauge (set to 0 or 1)
	//
	// PromQL Examples:
	//   - Check if refresh running: alert_history_publishing_refresh_in_progress == 1
	//   - Alert if stuck (>60s): alert_history_publishing_refresh_in_progress == 1 and changes(alert_history_publishing_refresh_in_progress[60s]) == 0
	InProgress prometheus.Gauge
}

// NewRefreshMetrics creates refresh metrics.
//
// Parameters:
//   - reg: Prometheus registerer (e.g., prometheus.DefaultRegisterer)
//
// Returns:
//   - RefreshMetrics instance with all 5 metrics registered
//
// Panics:
//   - If metrics already registered (duplicate registration)
//
// Example:
//
//	metrics := NewRefreshMetrics(prometheus.DefaultRegisterer)
//	metrics.Total.WithLabelValues("success").Inc()
func NewRefreshMetrics(reg prometheus.Registerer) *RefreshMetrics {
	m := &RefreshMetrics{
		Total: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "refresh_total",
				Help:      "Total number of target refresh attempts",
			},
			[]string{"status"},
		),

		Duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "refresh_duration_seconds",
				Help:      "Target refresh duration in seconds",
				Buckets:   []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
			},
			[]string{"status"},
		),

		ErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "refresh_errors_total",
				Help:      "Total number of refresh errors by type",
			},
			[]string{"error_type"},
		),

		LastSuccessTimestamp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "refresh_last_success_timestamp",
				Help:      "Unix timestamp of last successful target refresh",
			},
		),

		InProgress: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "refresh_in_progress",
				Help:      "1 if refresh currently running, 0 otherwise",
			},
		),
	}

	// Register all metrics
	reg.MustRegister(
		m.Total,
		m.Duration,
		m.ErrorsTotal,
		m.LastSuccessTimestamp,
		m.InProgress,
	)

	return m
}

// MetricNames returns list of all metric names (for documentation).
func (m *RefreshMetrics) MetricNames() []string {
	return []string{
		"alert_history_publishing_refresh_total",
		"alert_history_publishing_refresh_duration_seconds",
		"alert_history_publishing_refresh_errors_total",
		"alert_history_publishing_refresh_last_success_timestamp",
		"alert_history_publishing_refresh_in_progress",
	}
}
