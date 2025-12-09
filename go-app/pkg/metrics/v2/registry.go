// Package v2 provides a unified, consolidated metrics registry for the application.
//
// This package addresses several issues from the legacy metrics implementation:
//   - Inconsistent namespacing (some metrics had namespace, some didn't)
//   - Duplicate metric definitions across different files
//   - Inconsistent registration methods (promauto vs MustRegister)
//   - Scattered metric types making maintenance difficult
//
// Usage:
//
//	// Initialize the registry once at startup
//	registry := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.DefaultRegisterer))
//
//	// Use specific metric groups
//	registry.Publishing.RecordAPIRequest("slack", "POST", 200, 0.123)
//	registry.HTTP.RecordRequest("GET", "/api/alerts", 200, 0.05)
//
// Migration:
//
//	The old metrics in pkg/metrics/metrics.go are deprecated.
//	Use the v2 package for all new code.
//	See individual metric group files for mapping from old to new.
package v2

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// Namespace is the Prometheus namespace for all metrics.
	// All metrics will be prefixed with "alert_history_".
	Namespace = "alert_history"
)

// Registry is the central metrics registry for the application.
// It provides access to all metric groups in a type-safe manner.
//
// Thread Safety: All methods are thread-safe and can be called from multiple goroutines.
type Registry struct {
	// Publishing metrics for external system integrations (Slack, PagerDuty, Rootly, Webhook)
	Publishing *PublishingMetrics

	// HTTP metrics for incoming HTTP requests
	HTTP *HTTPMetrics

	// Database metrics for database operations
	Database *DatabaseMetrics

	// Cache metrics for caching operations (Redis, in-memory)
	Cache *CacheMetrics

	// registerer is the Prometheus registerer to use
	registerer prometheus.Registerer

	// mu protects initialization
	mu sync.Mutex
}

// Option configures the Registry.
type Option func(*Registry)

// WithPrometheusRegisterer sets a custom Prometheus registerer.
// If not set, prometheus.DefaultRegisterer is used.
func WithPrometheusRegisterer(r prometheus.Registerer) Option {
	return func(reg *Registry) {
		reg.registerer = r
	}
}

// NewRegistry creates a new metrics registry with all metric groups initialized.
//
// Options:
//   - WithPrometheusRegisterer: Use a custom registerer (default: prometheus.DefaultRegisterer)
//
// Example:
//
//	// Use default registerer
//	registry := NewRegistry()
//
//	// Use custom registerer (e.g., for testing)
//	customReg := prometheus.NewRegistry()
//	registry := NewRegistry(WithPrometheusRegisterer(customReg))
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{
		registerer: prometheus.DefaultRegisterer,
	}

	for _, opt := range opts {
		opt(r)
	}

	// Initialize all metric groups
	r.Publishing = NewPublishingMetrics(r.registerer)
	r.HTTP = NewHTTPMetrics(r.registerer)
	r.Database = NewDatabaseMetrics(r.registerer)
	r.Cache = NewCacheMetrics(r.registerer)

	return r
}

// Registerer returns the Prometheus registerer used by this registry.
// Useful for registering additional custom metrics.
func (r *Registry) Registerer() prometheus.Registerer {
	return r.registerer
}

// Global registry instance for convenience.
// Use NewRegistry() for better testability.
var (
	globalRegistry     *Registry
	globalRegistryOnce sync.Once
)

// Global returns the global metrics registry.
// Creates it lazily on first access.
//
// Note: For testing, prefer NewRegistry() with a custom registerer.
func Global() *Registry {
	globalRegistryOnce.Do(func() {
		globalRegistry = NewRegistry()
	})
	return globalRegistry
}

// MustRegister registers collectors with the registerer.
// Panics if registration fails (duplicate registration).
func (r *Registry) MustRegister(collectors ...prometheus.Collector) {
	r.registerer.MustRegister(collectors...)
}

// Helper functions for creating metrics with consistent namespace/subsystem.

// newCounter creates a new Counter with the standard namespace.
func newCounter(registerer prometheus.Registerer, subsystem, name, help string) prometheus.Counter {
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: Namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	})
	registerer.MustRegister(counter)
	return counter
}

// newCounterVec creates a new CounterVec with the standard namespace.
func newCounterVec(registerer prometheus.Registerer, subsystem, name, help string, labels []string) *prometheus.CounterVec {
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, labels)
	registerer.MustRegister(counter)
	return counter
}

// newGauge creates a new Gauge with the standard namespace.
func newGauge(registerer prometheus.Registerer, subsystem, name, help string) prometheus.Gauge {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: Namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	})
	registerer.MustRegister(gauge)
	return gauge
}

// newGaugeVec creates a new GaugeVec with the standard namespace.
func newGaugeVec(registerer prometheus.Registerer, subsystem, name, help string, labels []string) *prometheus.GaugeVec {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, labels)
	registerer.MustRegister(gauge)
	return gauge
}

// newHistogram creates a new Histogram with the standard namespace.
func newHistogram(registerer prometheus.Registerer, subsystem, name, help string, buckets []float64) prometheus.Histogram {
	histogram := prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: Namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	})
	registerer.MustRegister(histogram)
	return histogram
}

// newHistogramVec creates a new HistogramVec with the standard namespace.
func newHistogramVec(registerer prometheus.Registerer, subsystem, name, help string, buckets []float64, labels []string) *prometheus.HistogramVec {
	histogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	}, labels)
	registerer.MustRegister(histogram)
	return histogram
}

// Standard bucket configurations for consistent histograms across the application.
var (
	// DurationBuckets are suitable for HTTP request latencies (1ms to 10s).
	DurationBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}

	// APILatencyBuckets are optimized for external API calls (5ms to 30s).
	APILatencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0}

	// DatabaseBuckets are suitable for database query latencies (1ms to 5s).
	DatabaseBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0}

	// PayloadSizeBuckets are suitable for request/response payload sizes (1KB to 16MB).
	PayloadSizeBuckets = prometheus.ExponentialBuckets(1024, 2, 15) // 1KB to 16MB
)
