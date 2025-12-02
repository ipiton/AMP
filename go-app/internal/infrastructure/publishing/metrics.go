package publishing

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/ipiton/AMP/internal/core"
)

// FormatterMetrics holds all Prometheus metrics for alert formatting
type FormatterMetrics struct {
	// FormatDuration tracks formatting duration by format type
	FormatDuration *prometheus.HistogramVec

	// FormatTotal counts total formatting requests by format and status (success/failure)
	FormatTotal *prometheus.CounterVec

	// FormatErrors counts formatting errors by format and error type
	FormatErrors *prometheus.CounterVec

	// CacheHits counts cache hits by format
	CacheHits *prometheus.CounterVec

	// CacheMisses counts cache misses by format
	CacheMisses *prometheus.CounterVec

	// ValidationFailures counts validation failures by rule
	ValidationFailures *prometheus.CounterVec

	// FormatBytes tracks formatted payload size by format
	FormatBytes *prometheus.HistogramVec
}

// NewFormatterMetrics creates and registers all formatter metrics
//
// Metrics:
//   1. format_duration_seconds - Histogram (format, status)
//   2. format_total - Counter (format, status)
//   3. format_errors_total - Counter (format, error_type)
//   4. cache_hits_total - Counter (format)
//   5. cache_misses_total - Counter (format)
//   6. validation_failures_total - Counter (rule)
//   7. format_bytes - Histogram (format)
//
// Returns:
//   *FormatterMetrics: Registered metrics
func NewFormatterMetrics(namespace, subsystem string) *FormatterMetrics {
	return &FormatterMetrics{
		FormatDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "format_duration_seconds",
				Help:      "Time spent formatting alerts (seconds)",
				Buckets:   []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0}, // 100µs to 1s
			},
			[]string{"format", "status"}, // status: success, failure
		),

		FormatTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "format_total",
				Help:      "Total number of format requests",
			},
			[]string{"format", "status"},
		),

		FormatErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "format_errors_total",
				Help:      "Total number of format errors by type",
			},
			[]string{"format", "error_type"}, // error_type: validation, timeout, rate_limit, format_error
		),

		CacheHits: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cache_hits_total",
				Help:      "Total number of cache hits",
			},
			[]string{"format"},
		),

		CacheMisses: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "cache_misses_total",
				Help:      "Total number of cache misses",
			},
			[]string{"format"},
		),

		ValidationFailures: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "validation_failures_total",
				Help:      "Total number of validation failures by rule",
			},
			[]string{"rule"}, // rule: alert_name_required, fingerprint_format, etc.
		),

		FormatBytes: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Subsystem: subsystem,
				Name:      "format_bytes",
				Help:      "Size of formatted payloads (bytes)",
				Buckets:   prometheus.ExponentialBuckets(100, 2, 10), // 100B to 51KB
			},
			[]string{"format"},
		),
	}
}

// RecordFormatDuration records formatting duration
func (m *FormatterMetrics) RecordFormatDuration(format, status string, duration time.Duration) {
	m.FormatDuration.WithLabelValues(format, status).Observe(duration.Seconds())
}

// RecordFormatRequest records format request
func (m *FormatterMetrics) RecordFormatRequest(format, status string) {
	m.FormatTotal.WithLabelValues(format, status).Inc()
}

// RecordFormatError records format error
func (m *FormatterMetrics) RecordFormatError(format, errorType string) {
	m.FormatErrors.WithLabelValues(format, errorType).Inc()
}

// RecordCacheHit records cache hit
func (m *FormatterMetrics) RecordCacheHit(format string) {
	m.CacheHits.WithLabelValues(format).Inc()
}

// RecordCacheMiss records cache miss
func (m *FormatterMetrics) RecordCacheMiss(format string) {
	m.CacheMisses.WithLabelValues(format).Inc()
}

// RecordValidationFailure records validation failure
func (m *FormatterMetrics) RecordValidationFailure(rule string) {
	m.ValidationFailures.WithLabelValues(rule).Inc()
}

// RecordFormatBytes records formatted payload size
func (m *FormatterMetrics) RecordFormatBytes(format string, bytes int) {
	m.FormatBytes.WithLabelValues(format).Observe(float64(bytes))
}

// MetricsMiddleware wraps an AlertFormatter to record Prometheus metrics
func MetricsMiddleware(next AlertFormatter, metrics *FormatterMetrics) AlertFormatter {
	return &metricsFormatterMiddleware{
		next:    next,
		metrics: metrics,
	}
}

type metricsFormatterMiddleware struct {
	next    AlertFormatter
	metrics *FormatterMetrics
}

func (m *metricsFormatterMiddleware) FormatAlert(ctx context.Context, enrichedAlert *core.EnrichedAlert, format core.PublishingFormat) (map[string]any, error) {
	start := time.Now()
	formatStr := string(format)

	result, err := m.next.FormatAlert(ctx, enrichedAlert, format)
	duration := time.Since(start)

	// Record duration and request
	if err != nil {
		m.metrics.RecordFormatDuration(formatStr, "failure", duration)
		m.metrics.RecordFormatRequest(formatStr, "failure")

		// Classify error type
		errorType := classifyError(err)
		m.metrics.RecordFormatError(formatStr, errorType)

		// Record validation failures by rule
		if validationErr, ok := err.(*ValidationError); ok {
			m.metrics.RecordValidationFailure(validationErr.Field)
		}
	} else {
		m.metrics.RecordFormatDuration(formatStr, "success", duration)
		m.metrics.RecordFormatRequest(formatStr, "success")

		// Record payload size (approximate JSON size)
		if result != nil {
			// Rough estimate: sum of key/value string lengths
			size := estimateJSONSize(result)
			m.metrics.RecordFormatBytes(formatStr, size)
		}
	}

	return result, err
}

// classifyError classifies error for metrics
func classifyError(err error) string {
	switch err.(type) {
	case *ValidationError:
		return "validation"
	case *RateLimitError:
		return "rate_limit"
	case *TimeoutError:
		return "timeout"
	default:
		return "format_error"
	}
}

// estimateJSONSize estimates JSON size (rough approximation)
func estimateJSONSize(data map[string]any) int {
	size := 2 // {}
	for key, value := range data {
		size += len(key) + 4 // "key":
		size += estimateValueSize(value)
		size += 1 // comma
	}
	return size
}

func estimateValueSize(value any) int {
	switch v := value.(type) {
	case string:
		return len(v) + 2 // quotes
	case int, int64, float64:
		return 10 // approximate number size
	case bool:
		return 5 // true/false
	case map[string]any:
		return estimateJSONSize(v)
	case []any:
		size := 2 // []
		for _, item := range v {
			size += estimateValueSize(item)
			size += 1 // comma
		}
		return size
	case nil:
		return 4 // null
	default:
		return 10 // fallback
	}
}
