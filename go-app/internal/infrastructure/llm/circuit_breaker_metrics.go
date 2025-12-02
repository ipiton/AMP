package llm

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CircuitBreakerMetrics holds Prometheus metrics for circuit breaker.
// Provides comprehensive observability for production monitoring.
type CircuitBreakerMetrics struct {
	// State represents current circuit breaker state (0=closed, 1=open, 2=half_open)
	State prometheus.Gauge

	// Failures tracks total number of failed LLM calls
	Failures prometheus.Counter

	// Successes tracks total number of successful LLM calls
	Successes prometheus.Counter

	// StateChanges tracks state transitions with from/to labels
	StateChanges *prometheus.CounterVec

	// RequestsBlocked tracks requests blocked when circuit is open
	RequestsBlocked prometheus.Counter

	// HalfOpenRequests tracks test requests in half-open state
	HalfOpenRequests prometheus.Counter

	// SlowCalls tracks calls exceeding slow call threshold
	SlowCalls prometheus.Counter

	// CallDuration tracks duration of LLM calls (150% enhancement: includes percentiles)
	CallDuration *prometheus.HistogramVec
}

var (
	// Global singleton metrics instance to prevent duplicate registration
	defaultMetrics     *CircuitBreakerMetrics
	defaultMetricsOnce sync.Once
)

// NewCircuitBreakerMetrics creates Prometheus metrics for circuit breaker.
// Uses standard alert_history namespace with llm_circuit_breaker subsystem.
// Returns singleton instance to prevent duplicate metric registration.
func NewCircuitBreakerMetrics() *CircuitBreakerMetrics {
	defaultMetricsOnce.Do(func() {
		defaultMetrics = NewCircuitBreakerMetricsWithNamespace("alert_history", "llm_circuit_breaker")
	})
	return defaultMetrics
}

// NewCircuitBreakerMetricsWithNamespace creates metrics with custom namespace.
// Allows flexibility for different deployment environments.
// WARNING: This should only be called once per namespace/subsystem combination to avoid duplicate registration.
func NewCircuitBreakerMetricsWithNamespace(namespace, subsystem string) *CircuitBreakerMetrics {
	return &CircuitBreakerMetrics{
		State: promauto.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "state",
			Help:      "Current state of LLM circuit breaker (0=closed, 1=open, 2=half_open)",
		}),

		Failures: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "failures_total",
			Help:      "Total number of failed LLM calls (includes slow calls)",
		}),

		Successes: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "successes_total",
			Help:      "Total number of successful LLM calls",
		}),

		StateChanges: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "state_changes_total",
				Help:      "Total number of circuit breaker state changes",
			},
			[]string{"from", "to"},
		),

		RequestsBlocked: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "requests_blocked_total",
			Help:      "Total number of requests blocked by circuit breaker (fail-fast)",
		}),

		HalfOpenRequests: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "half_open_requests_total",
			Help:      "Total number of test requests in half-open state",
		}),

		SlowCalls: promauto.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "slow_calls_total",
			Help:      "Total number of slow LLM calls (exceeding threshold)",
		}),

		// 150% Enhancement: Histogram for latency percentiles (p50, p95, p99)
		CallDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "call_duration_seconds",
				Help:      "Duration of LLM calls in seconds (enables p50/p95/p99 analysis)",
				// Buckets optimized for LLM API calls: 100ms to 30s
				Buckets: []float64{0.1, 0.25, 0.5, 1.0, 2.0, 3.0, 5.0, 10.0, 30.0},
			},
			[]string{"result"}, // result=success|failure
		),
	}
}

// RecordStateChange records a state transition in metrics.
// Helper method for consistent metric recording.
func (m *CircuitBreakerMetrics) RecordStateChange(from, to CircuitBreakerState) {
	if m.StateChanges != nil {
		m.StateChanges.WithLabelValues(from.String(), to.String()).Inc()
	}
	if m.State != nil {
		m.State.Set(float64(to))
	}
}

// RecordSuccess records a successful call with duration.
func (m *CircuitBreakerMetrics) RecordSuccess(duration float64) {
	if m.Successes != nil {
		m.Successes.Inc()
	}
	if m.CallDuration != nil {
		m.CallDuration.WithLabelValues("success").Observe(duration)
	}
}

// RecordFailure records a failed call with duration and slow indicator.
func (m *CircuitBreakerMetrics) RecordFailure(duration float64, slow bool) {
	if m.Failures != nil {
		m.Failures.Inc()
	}
	if slow && m.SlowCalls != nil {
		m.SlowCalls.Inc()
	}
	if m.CallDuration != nil {
		m.CallDuration.WithLabelValues("failure").Observe(duration)
	}
}
