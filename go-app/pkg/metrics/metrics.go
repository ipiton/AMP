// Package metrics provides Prometheus metrics for the application.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// BusinessMetrics holds business-level metrics
type BusinessMetrics struct {
// Silence metrics
SilenceOperationsTotal       *prometheus.CounterVec
SilenceValidationErrors      *prometheus.CounterVec
SilenceCacheHitsTotal        *prometheus.CounterVec
SilenceCacheMissesTotal      *prometheus.CounterVec

// Add other metrics as needed
}

// NewBusinessMetrics creates a new BusinessMetrics instance
func NewBusinessMetrics() *BusinessMetrics {
	return &BusinessMetrics{}
}
