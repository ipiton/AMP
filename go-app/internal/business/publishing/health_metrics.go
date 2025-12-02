package publishing

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HealthMetrics tracks health check metrics for Prometheus.
//
// This struct provides 6 metrics:
//   1. health_checks_total (Counter by target, status)
//   2. health_check_duration_seconds (Histogram by target)
//   3. target_health_status (Gauge by target, type: 0=unknown, 1=healthy, 2=degraded, 3=unhealthy)
//   4. target_consecutive_failures (Gauge by target)
//   5. target_success_rate (Gauge by target, percentage 0-100)
//   6. health_check_errors_total (Counter by target, error_type)
//
// Performance:
//   - RecordHealthCheck: <10µs (counter + histogram)
//   - SetTargetHealthStatus: <5µs (gauge set)
//   - SetConsecutiveFailures: <5µs (gauge set)
//   - SetSuccessRate: <5µs (gauge set)
//   - RecordHealthCheckError: <5µs (counter)
//
// Thread Safety:
//   - All Prometheus metrics are thread-safe (internal locking)
//   - Safe to call from multiple goroutines
//
// Example Usage:
//
//	metrics, err := NewHealthMetrics()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Record health check
//	metrics.RecordHealthCheck("rootly-prod", true, 123*time.Millisecond)
//
//	// Set health status
//	metrics.SetTargetHealthStatus("rootly-prod", "rootly", HealthStatusHealthy)
//
//	// Record error
//	metrics.RecordHealthCheckError("rootly-prod", ErrorTypeTimeout)
type HealthMetrics struct {
	// Counters
	checksTotal *prometheus.CounterVec // By target_name, status (success/failure)
	errorsTotal *prometheus.CounterVec // By target_name, error_type

	// Histograms
	checkDuration *prometheus.HistogramVec // By target_name

	// Gauges
	targetHealthStatus  *prometheus.GaugeVec // By target_name, target_type (0=unknown, 1=healthy, 2=degraded, 3=unhealthy)
	consecutiveFailures *prometheus.GaugeVec // By target_name
	successRate         *prometheus.GaugeVec // By target_name (percentage 0-100)
}

var (
	healthMetricsInstance *HealthMetrics
	healthMetricsOnce     sync.Once
)

// NewHealthMetrics creates HealthMetrics and registers with Prometheus.
//
// This function:
//   1. Creates all 6 Prometheus metrics
//   2. Registers metrics (panics on duplicate registration)
//   3. Returns HealthMetrics instance
//
// Returns:
//   - *HealthMetrics: Metrics instance (never nil)
//   - error: If failed to create metrics
//
// Example:
//
//	metrics, err := NewHealthMetrics()
//	if err != nil {
//	    return fmt.Errorf("failed to create health metrics: %w", err)
//	}
func NewHealthMetrics() (*HealthMetrics, error) {
	var initErr error
	healthMetricsOnce.Do(func() {
	m := &HealthMetrics{}

	// 1. Health checks total (Counter)
	m.checksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alert_history_publishing_health_checks_total",
			Help: "Total number of health checks performed by target and status",
		},
		[]string{"target_name", "status"}, // status: success/failure
	)

	// 2. Health check duration (Histogram)
	m.checkDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "alert_history_publishing_health_check_duration_seconds",
			Help:    "Health check duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to 16s
		},
		[]string{"target_name"},
	)

	// 3. Target health status (Gauge)
	m.targetHealthStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "alert_history_publishing_target_health_status",
			Help: "Target health status (0=unknown, 1=healthy, 2=degraded, 3=unhealthy)",
		},
		[]string{"target_name", "target_type"},
	)

	// 4. Consecutive failures (Gauge)
	m.consecutiveFailures = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "alert_history_publishing_target_consecutive_failures",
			Help: "Number of consecutive health check failures for target",
		},
		[]string{"target_name"},
	)

	// 5. Success rate (Gauge)
	m.successRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "alert_history_publishing_target_success_rate",
			Help: "Health check success rate percentage for target (0-100)",
		},
		[]string{"target_name"},
	)

	// 6. Errors total (Counter)
	m.errorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "alert_history_publishing_health_check_errors_total",
			Help: "Total health check errors by target and error type",
		},
		[]string{"target_name", "error_type"}, // error_type: timeout/dns/tls/refused/http_error/unknown
	)

	// Register all metrics
	prometheus.MustRegister(
		m.checksTotal,
		m.checkDuration,
		m.targetHealthStatus,
		m.consecutiveFailures,
		m.successRate,
		m.errorsTotal,
	)

		healthMetricsInstance = m
	})

	return healthMetricsInstance, initErr
}

// RecordHealthCheck records health check metrics.
//
// This method:
//   1. Increments checks_total counter (success or failure)
//   2. Observes check duration in histogram
//
// Parameters:
//   - targetName: Name of target (e.g., "rootly-prod")
//   - success: true if check succeeded, false if failed
//   - duration: Check duration (full execution time)
//
// Performance: <10µs (counter + histogram observation)
//
// Thread-Safe: Yes
//
// Example:
//
//	startTime := time.Now()
//	success := performHealthCheck(target)
//	duration := time.Since(startTime)
//	metrics.RecordHealthCheck("rootly-prod", success, duration)
func (m *HealthMetrics) RecordHealthCheck(targetName string, success bool, duration time.Duration) {
	status := "failure"
	if success {
		status = "success"
	}

	m.checksTotal.WithLabelValues(targetName, status).Inc()
	m.checkDuration.WithLabelValues(targetName).Observe(duration.Seconds())
}

// RecordHealthCheckError records health check error metric.
//
// This method increments errors_total counter by target and error type.
//
// Parameters:
//   - targetName: Name of target (e.g., "rootly-prod")
//   - errorType: Error classification (timeout/dns/tls/refused/http_error/unknown)
//
// Performance: <5µs (counter increment)
//
// Thread-Safe: Yes
//
// Example:
//
//	err := performHealthCheck(target)
//	if err != nil {
//	    errType := classifyError(err)
//	    metrics.RecordHealthCheckError("rootly-prod", errType)
//	}
func (m *HealthMetrics) RecordHealthCheckError(targetName string, errorType ErrorType) {
	m.errorsTotal.WithLabelValues(targetName, string(errorType)).Inc()
}

// SetTargetHealthStatus sets health status gauge.
//
// This method sets target_health_status gauge to numeric value:
//   - 0 = unknown
//   - 1 = healthy
//   - 2 = degraded
//   - 3 = unhealthy
//
// Parameters:
//   - targetName: Name of target (e.g., "rootly-prod")
//   - targetType: Type of target (rootly/pagerduty/slack/webhook)
//   - status: Health status
//
// Performance: <5µs (gauge set)
//
// Thread-Safe: Yes
//
// Example:
//
//	metrics.SetTargetHealthStatus("rootly-prod", "rootly", HealthStatusHealthy)
func (m *HealthMetrics) SetTargetHealthStatus(targetName, targetType string, status HealthStatus) {
	var value float64
	switch status {
	case HealthStatusUnknown:
		value = 0
	case HealthStatusHealthy:
		value = 1
	case HealthStatusDegraded:
		value = 2
	case HealthStatusUnhealthy:
		value = 3
	}

	m.targetHealthStatus.WithLabelValues(targetName, targetType).Set(value)
}

// SetConsecutiveFailures sets consecutive failures gauge.
//
// This method sets target_consecutive_failures gauge to current failure count.
//
// Parameters:
//   - targetName: Name of target (e.g., "rootly-prod")
//   - count: Number of consecutive failures (0 = no failures)
//
// Performance: <5µs (gauge set)
//
// Thread-Safe: Yes
//
// Example:
//
//	if consecutiveFailures >= 3 {
//	    metrics.SetConsecutiveFailures("rootly-prod", consecutiveFailures)
//	}
func (m *HealthMetrics) SetConsecutiveFailures(targetName string, count int) {
	m.consecutiveFailures.WithLabelValues(targetName).Set(float64(count))
}

// SetSuccessRate sets success rate gauge.
//
// This method sets target_success_rate gauge to success rate percentage (0-100).
//
// Parameters:
//   - targetName: Name of target (e.g., "rootly-prod")
//   - rate: Success rate percentage (0.0 = 0%, 100.0 = 100%)
//
// Performance: <5µs (gauge set)
//
// Thread-Safe: Yes
//
// Example:
//
//	successRate := (float64(totalSuccesses) / float64(totalChecks)) * 100
//	metrics.SetSuccessRate("rootly-prod", successRate)
func (m *HealthMetrics) SetSuccessRate(targetName string, rate float64) {
	m.successRate.WithLabelValues(targetName).Set(rate)
}

// UnregisterTarget removes all metrics for target.
//
// This method deletes all metrics series for target (cleanup).
// Call this when target is deleted from K8s Secrets.
//
// Parameters:
//   - targetName: Name of target to unregister
//
// Performance: <50µs (deletes 6 metric series)
//
// Thread-Safe: Yes
//
// Example:
//
//	// Target deleted from K8s
//	metrics.UnregisterTarget("rootly-prod")
func (m *HealthMetrics) UnregisterTarget(targetName string) {
	// Delete counter series
	m.checksTotal.DeleteLabelValues(targetName, "success")
	m.checksTotal.DeleteLabelValues(targetName, "failure")

	// Delete histogram series
	m.checkDuration.DeleteLabelValues(targetName)

	// Delete gauge series
	m.targetHealthStatus.DeletePartialMatch(prometheus.Labels{"target_name": targetName})
	m.consecutiveFailures.DeleteLabelValues(targetName)
	m.successRate.DeleteLabelValues(targetName)

	// Delete error counter series (all error types)
	for _, errType := range []ErrorType{
		ErrorTypeTimeout,
		ErrorTypeDNS,
		ErrorTypeTLS,
		ErrorTypeRefused,
		ErrorTypeHTTP,
		ErrorTypeUnknown,
	} {
		m.errorsTotal.DeleteLabelValues(targetName, string(errType))
	}
}
