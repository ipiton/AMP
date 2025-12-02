package silencing

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// SilenceMetrics contains Prometheus metrics for silence repository operations.
// All metrics follow the naming convention: alert_history_{category}_{subsystem}_{name}_{unit}
//
// Categories:
//   - infra_silence_repo: Repository-level infrastructure metrics
//   - business_silence: Business-level silence metrics
type SilenceMetrics struct {
	// Operations counts the total number of repository operations by type and status.
	// Labels:
	//   - operation: create|get_by_id|list|update|delete|count|expire|expiring_soon|bulk_update_status
	//   - status: success|error
	Operations *prometheus.CounterVec

	// OperationDuration tracks the duration of repository operations in seconds.
	// Labels:
	//   - operation: create|get_by_id|list|update|delete|count|expire|expiring_soon|bulk_update_status
	//   - status: success|error
	// Buckets: 1ms, 3ms, 5ms, 10ms, 20ms, 50ms, 100ms, 200ms, 500ms, 1s
	OperationDuration *prometheus.HistogramVec

	// Errors counts repository errors by operation and error type.
	// Labels:
	//   - operation: create|get_by_id|list|update|delete|count|expire|expiring_soon|bulk_update_status
	//   - error_type: validation|not_found|conflict|marshal|unmarshal|insert|update|delete|query|scan|rows|begin_tx|commit_tx|execute|invalid_uuid
	Errors *prometheus.CounterVec

	// ActiveSilences tracks the current number of silences by status.
	// This is a gauge that is incremented/decremented on create/delete operations.
	// Labels:
	//   - status: pending|active|expired|deleted
	ActiveSilences *prometheus.GaugeVec

	// CleanupDeleted counts the total number of silences deleted by the TTL cleanup worker.
	// This counter is incremented by the ExpireSilences method when deleteExpired=true.
	CleanupDeleted prometheus.Counter

	// CleanupDuration tracks the duration of TTL cleanup operations in seconds.
	// Buckets: 100ms, 250ms, 500ms, 1s, 2s, 5s, 10s, 30s, 60s
	CleanupDuration prometheus.Histogram
}

// NewSilenceMetrics creates and registers Prometheus metrics for silence repository operations.
// Metrics are automatically registered with the default Prometheus registry.
//
// This function should be called once during repository initialization.
// Multiple calls will cause metric registration conflicts.
//
// Returns:
//   - *SilenceMetrics: Initialized metrics struct ready for use
func NewSilenceMetrics() *SilenceMetrics {
	return &SilenceMetrics{
		Operations: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "infra_silence_repo",
				Name:      "operations_total",
				Help:      "Total silence repository operations by type and status",
			},
			[]string{"operation", "status"},
		),

		OperationDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "infra_silence_repo",
				Name:      "operation_duration_seconds",
				Help:      "Duration of silence repository operations in seconds",
				Buckets:   []float64{.001, .003, .005, .01, .02, .05, .1, .2, .5, 1},
			},
			[]string{"operation", "status"},
		),

		Errors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "infra_silence_repo",
				Name:      "errors_total",
				Help:      "Total silence repository errors by operation and error type",
			},
			[]string{"operation", "error_type"},
		),

		ActiveSilences: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "business_silence",
				Name:      "active_total",
				Help:      "Number of active silences by status (pending|active|expired|deleted)",
			},
			[]string{"status"},
		),

		CleanupDeleted: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "infra_silence_repo",
				Name:      "cleanup_deleted_total",
				Help:      "Total silences deleted by TTL cleanup worker",
			},
		),

		CleanupDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "infra_silence_repo",
				Name:      "cleanup_duration_seconds",
				Help:      "Duration of TTL cleanup operations in seconds",
				Buckets:   []float64{.1, .25, .5, 1, 2, 5, 10, 30, 60},
			},
		),
	}
}
