package publishing

import (
	"github.com/prometheus/client_golang/prometheus"
)

// PublishingMetrics exports 12+ Prometheus metrics for queue observability
//
// This struct provides comprehensive metrics for:
//   - Queue size and capacity utilization (3 priority levels)
//   - Job processing (submissions, successes, failures, durations)
//   - Retry attempts and success rates
//   - Circuit breaker states and transitions
//   - Worker pool utilization
//   - Dead Letter Queue operations
//
// All metrics follow naming convention:
//   alert_history_publishing_<metric_name>_<unit>
//
// Example:
//
//	metrics := NewPublishingMetrics(prometheus.DefaultRegisterer)
//	metrics.RecordQueueSubmission("high", true)
//	metrics.UpdateQueueSize("high", 10, 500)
type PublishingMetrics struct {
	// Queue size metrics
	queueSize         *prometheus.GaugeVec   // Current queue depth by priority
	queueCapacityUtil *prometheus.GaugeVec   // Queue utilization (0-1) by priority
	queueSubmissions  *prometheus.CounterVec // Submissions by priority, result

	// Job processing metrics
	jobsProcessed *prometheus.CounterVec   // Jobs by target, state
	jobDuration   *prometheus.HistogramVec // Processing duration by target, priority
	jobWaitTime   *prometheus.HistogramVec // Queue wait time by priority

	// Retry metrics
	retryAttempts    *prometheus.CounterVec   // Retries by target, error_type
	retrySuccessRate *prometheus.HistogramVec // Success rate by target, attempt

	// Circuit breaker metrics
	circuitBreakerState      *prometheus.GaugeVec   // CB state by target
	circuitBreakerTrips      *prometheus.CounterVec // CB trips by target
	circuitBreakerRecoveries *prometheus.CounterVec // CB recoveries by target

	// Worker pool metrics
	workersActive        prometheus.Gauge          // Active workers
	workersIdle          prometheus.Gauge          // Idle workers
	workerProcessingTime *prometheus.HistogramVec  // Processing time by worker_id

	// DLQ metrics
	dlqSize    *prometheus.GaugeVec   // DLQ size by target
	dlqWrites  *prometheus.CounterVec // DLQ writes by target, error_type
	dlqReplays *prometheus.CounterVec // DLQ replays by target, result
}

// NewPublishingMetrics creates and registers all metrics
//
// Parameters:
//   - registry: Prometheus registerer (usually prometheus.DefaultRegisterer)
//
// Returns:
//   - *PublishingMetrics: Fully initialized metrics struct
//
// Note: Panics if metrics already registered (duplicate)
func NewPublishingMetrics(registry prometheus.Registerer) *PublishingMetrics {
	m := &PublishingMetrics{
		queueSize: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "queue_size",
				Help:      "Current queue depth by priority (high/medium/low)",
			},
			[]string{"priority"},
		),

		queueCapacityUtil: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "queue_capacity_utilization",
				Help:      "Queue capacity utilization (0-1) by priority",
			},
			[]string{"priority"},
		),

		queueSubmissions: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "queue_submissions_total",
				Help:      "Total queue submissions by priority and result (success/rejected)",
			},
			[]string{"priority", "result"},
		),

		jobsProcessed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "jobs_processed_total",
				Help:      "Total jobs processed by target and state (succeeded/failed/dlq)",
			},
			[]string{"target", "state"},
		),

		jobDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "job_duration_seconds",
				Help:      "Job processing duration (queue to completion) by target and priority",
				Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
			},
			[]string{"target", "priority"},
		),

		jobWaitTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "job_wait_time_seconds",
				Help:      "Time spent in queue (submitted to started) by priority",
				Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
			},
			[]string{"priority"},
		),

		retryAttempts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "retry_attempts_total",
				Help:      "Total retry attempts by target and error_type (transient/permanent/unknown)",
			},
			[]string{"target", "error_type"},
		),

		retrySuccessRate: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "retry_success_rate",
				Help:      "Retry success rate by target and attempt number",
				Buckets:   prometheus.LinearBuckets(0, 0.1, 11), // 0-1 in 0.1 steps
			},
			[]string{"target", "attempt"},
		),

		circuitBreakerState: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "circuit_breaker_state",
				Help:      "Circuit breaker state by target (0=closed, 1=halfopen, 2=open)",
			},
			[]string{"target"},
		),

		circuitBreakerTrips: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "circuit_breaker_trips_total",
				Help:      "Total circuit breaker trips (closed to open) by target",
			},
			[]string{"target"},
		),

		circuitBreakerRecoveries: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "circuit_breaker_recoveries_total",
				Help:      "Total circuit breaker recoveries (halfopen to closed) by target",
			},
			[]string{"target"},
		),

		workersActive: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "workers_active",
				Help:      "Number of workers currently processing jobs",
			},
		),

		workersIdle: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "workers_idle",
				Help:      "Number of idle workers waiting for jobs",
			},
		),

		workerProcessingTime: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "worker_processing_duration_seconds",
				Help:      "Worker processing time per job by worker_id",
				Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
			},
			[]string{"worker_id"},
		),

		dlqSize: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "dlq_size",
				Help:      "Dead letter queue size by target",
			},
			[]string{"target"},
		),

		dlqWrites: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "dlq_writes_total",
				Help:      "Total DLQ writes by target and error_type",
			},
			[]string{"target", "error_type"},
		),

		dlqReplays: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "alert_history",
				Subsystem: "publishing",
				Name:      "dlq_replays_total",
				Help:      "Total DLQ replays by target and result (success/failure)",
			},
			[]string{"target", "result"},
		),
	}

	// Register all metrics
	registry.MustRegister(
		m.queueSize,
		m.queueCapacityUtil,
		m.queueSubmissions,
		m.jobsProcessed,
		m.jobDuration,
		m.jobWaitTime,
		m.retryAttempts,
		m.retrySuccessRate,
		m.circuitBreakerState,
		m.circuitBreakerTrips,
		m.circuitBreakerRecoveries,
		m.workersActive,
		m.workersIdle,
		m.workerProcessingTime,
		m.dlqSize,
		m.dlqWrites,
		m.dlqReplays,
	)

	return m
}

// RecordQueueSubmission records a queue submission attempt
//
// Parameters:
//   - priority: "high", "medium", or "low"
//   - success: true if submitted successfully, false if rejected (queue full)
func (m *PublishingMetrics) RecordQueueSubmission(priority string, success bool) {
	result := "rejected"
	if success {
		result = "success"
	}
	m.queueSubmissions.WithLabelValues(priority, result).Inc()
}

// UpdateQueueSize updates queue size and capacity utilization metrics
//
// Parameters:
//   - priority: "high", "medium", or "low"
//   - currentSize: Number of jobs currently in queue
//   - capacity: Maximum queue capacity
func (m *PublishingMetrics) UpdateQueueSize(priority string, currentSize, capacity int) {
	m.queueSize.WithLabelValues(priority).Set(float64(currentSize))

	utilization := 0.0
	if capacity > 0 {
		utilization = float64(currentSize) / float64(capacity)
	}
	m.queueCapacityUtil.WithLabelValues(priority).Set(utilization)
}

// RecordJobSuccess records a successfully completed job
//
// Parameters:
//   - targetName: Name of the publishing target (e.g., "rootly-prod")
//   - priority: "high", "medium", or "low"
//   - durationSeconds: Total processing time (queue → completion)
func (m *PublishingMetrics) RecordJobSuccess(targetName, priority string, durationSeconds float64) {
	m.jobsProcessed.WithLabelValues(targetName, "succeeded").Inc()
	m.jobDuration.WithLabelValues(targetName, priority).Observe(durationSeconds)
}

// RecordJobFailure records a failed job (after all retries exhausted)
//
// Parameters:
//   - targetName: Name of the publishing target
//   - priority: "high", "medium", or "low"
//   - errorType: "transient", "permanent", or "unknown"
func (m *PublishingMetrics) RecordJobFailure(targetName, priority, errorType string) {
	m.jobsProcessed.WithLabelValues(targetName, "failed").Inc()
}

// RecordJobDLQ records a job sent to DLQ
//
// Parameters:
//   - targetName: Name of the publishing target
func (m *PublishingMetrics) RecordJobDLQ(targetName string) {
	m.jobsProcessed.WithLabelValues(targetName, "dlq").Inc()
}

// RecordJobWaitTime records time spent waiting in queue
//
// Parameters:
//   - priority: "high", "medium", or "low"
//   - waitTimeSeconds: Time from submitted to started
func (m *PublishingMetrics) RecordJobWaitTime(priority string, waitTimeSeconds float64) {
	m.jobWaitTime.WithLabelValues(priority).Observe(waitTimeSeconds)
}

// RecordRetryAttempt records a retry attempt
//
// Parameters:
//   - targetName: Name of the publishing target
//   - errorType: "transient", "permanent", or "unknown"
//   - willRetry: true if retry will be attempted, false if permanent error
func (m *PublishingMetrics) RecordRetryAttempt(targetName, errorType string, willRetry bool) {
	if willRetry {
		m.retryAttempts.WithLabelValues(targetName, errorType).Inc()
	}
}

// RecordRetrySuccess records retry success rate
//
// Parameters:
//   - targetName: Name of the publishing target
//   - attempt: Retry attempt number (1, 2, 3, ...)
//   - success: 1.0 if succeeded, 0.0 if failed
func (m *PublishingMetrics) RecordRetrySuccess(targetName string, attempt int, success float64) {
	m.retrySuccessRate.WithLabelValues(targetName, string(rune(attempt+'0'))).Observe(success)
}

// RecordCircuitBreakerTrip records circuit breaker opening (closed → open)
//
// Parameters:
//   - targetName: Name of the publishing target
func (m *PublishingMetrics) RecordCircuitBreakerTrip(targetName string) {
	m.circuitBreakerTrips.WithLabelValues(targetName).Inc()
}

// RecordCircuitBreakerRecovery records circuit breaker recovery (halfopen → closed)
//
// Parameters:
//   - targetName: Name of the publishing target
func (m *PublishingMetrics) RecordCircuitBreakerRecovery(targetName string) {
	m.circuitBreakerRecoveries.WithLabelValues(targetName).Inc()
}

// UpdateCircuitBreakerState updates circuit breaker state gauge
//
// Parameters:
//   - targetName: Name of the publishing target
//   - state: CircuitBreakerState (0=closed, 1=halfopen, 2=open)
func (m *PublishingMetrics) UpdateCircuitBreakerState(targetName string, state CircuitBreakerState) {
	m.circuitBreakerState.WithLabelValues(targetName).Set(float64(state))
}

// RecordWorkerActive updates active/idle worker counts
//
// Parameters:
//   - workerID: Worker ID (0-9 typically)
//   - active: true if worker is processing a job, false if idle
//
// Note: Call this twice (once with active=true, once with active=false)
// to maintain accurate counts.
func (m *PublishingMetrics) RecordWorkerActive(workerID int, active bool) {
	// This is a simplified approach - in production, use a more sophisticated
	// tracking mechanism (e.g., map[int]bool to track each worker's state)
	// For now, we'll just increment/decrement counters
	if active {
		m.workersActive.Inc()
		m.workersIdle.Dec()
	} else {
		m.workersActive.Dec()
		m.workersIdle.Inc()
	}
}

// RecordWorkerProcessing records worker processing duration
//
// Parameters:
//   - workerID: Worker ID (0-9 typically)
//   - durationSeconds: Processing time for single job
func (m *PublishingMetrics) RecordWorkerProcessing(workerID int, durationSeconds float64) {
	m.workerProcessingTime.WithLabelValues(string(rune(workerID + '0'))).Observe(durationSeconds)
}

// UpdateDLQSize updates DLQ size gauge
//
// Parameters:
//   - targetName: Name of the publishing target
//   - size: Current DLQ size
func (m *PublishingMetrics) UpdateDLQSize(targetName string, size int) {
	m.dlqSize.WithLabelValues(targetName).Set(float64(size))
}

// RecordDLQWrite records a write to DLQ
//
// Parameters:
//   - targetName: Name of the publishing target
//   - errorType: "transient", "permanent", or "unknown"
func (m *PublishingMetrics) RecordDLQWrite(targetName, errorType string) {
	m.dlqWrites.WithLabelValues(targetName, errorType).Inc()
}

// RecordDLQReplay records a DLQ job replay
//
// Parameters:
//   - targetName: Name of the publishing target
//   - result: "success" or "failure"
func (m *PublishingMetrics) RecordDLQReplay(targetName, result string) {
	m.dlqReplays.WithLabelValues(targetName, result).Inc()
}

// InitializeWorkerMetrics initializes worker idle count
//
// This should be called once during queue initialization to set baseline.
//
// Parameters:
//   - workerCount: Total number of workers in pool
func (m *PublishingMetrics) InitializeWorkerMetrics(workerCount int) {
	m.workersActive.Set(0)
	m.workersIdle.Set(float64(workerCount))
}
