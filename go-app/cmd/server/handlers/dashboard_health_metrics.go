// Package handlers provides HTTP handlers for the Alert History Service.
// TN-83: GET /api/dashboard/health (basic) - Prometheus Metrics
package handlers

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// DashboardHealthMetrics contains Prometheus metrics for dashboard health checks.
type DashboardHealthMetrics struct {
	// ChecksTotal counts total health checks performed by component and status.
	ChecksTotal *prometheus.CounterVec

	// CheckDuration tracks health check duration by component.
	CheckDuration *prometheus.HistogramVec

	// StatusGauge tracks current health status by component (1=healthy, 0.5=degraded, 0=unhealthy).
	StatusGauge *prometheus.GaugeVec

	// OverallStatusGauge tracks overall system health status.
	OverallStatusGauge *prometheus.GaugeVec
}

var (
	// dashboardHealthMetrics is the singleton instance of DashboardHealthMetrics.
	dashboardHealthMetrics *DashboardHealthMetrics
	dashboardHealthMetricsOnce sync.Once
)

// initDashboardHealthMetrics initializes dashboard health metrics (singleton pattern).
func initDashboardHealthMetrics() {
	dashboardHealthMetrics = &DashboardHealthMetrics{
		ChecksTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert_history_technical_dashboard_health_checks_total",
				Help: "Total number of dashboard health checks performed by component and status",
			},
			[]string{"component", "status"}, // component: database, redis, llm_service, publishing; status: healthy, unhealthy, degraded, not_configured, available, unavailable
		),
		CheckDuration: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "alert_history_technical_dashboard_health_check_duration_seconds",
				Help:    "Duration of dashboard health checks by component",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to 1s
			},
			[]string{"component"},
		),
		StatusGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "alert_history_technical_dashboard_health_status",
				Help: "Current health status by component (1=healthy/available, 0.5=degraded, 0=unhealthy/unavailable)",
			},
			[]string{"component"},
		),
		OverallStatusGauge: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "alert_history_technical_dashboard_health_overall_status",
				Help: "Overall system health status (1=healthy, 0.5=degraded, 0=unhealthy)",
			},
			[]string{"status"},
		),
	}
}

// GetDashboardHealthMetrics returns the singleton DashboardHealthMetrics instance.
func GetDashboardHealthMetrics() *DashboardHealthMetrics {
	dashboardHealthMetricsOnce.Do(initDashboardHealthMetrics)
	return dashboardHealthMetrics
}

// RecordCheck records a health check result.
func (m *DashboardHealthMetrics) RecordCheck(component, status string, duration time.Duration) {
	m.ChecksTotal.WithLabelValues(component, status).Inc()
	m.CheckDuration.WithLabelValues(component).Observe(duration.Seconds())

	// Record status gauge value
	statusValue := 0.0
	switch status {
	case "healthy", "available":
		statusValue = 1.0
	case "degraded":
		statusValue = 0.5
	case "unhealthy", "unavailable":
		statusValue = 0.0
	default:
		// Skip not_configured - don't record gauge
		return
	}

	m.StatusGauge.WithLabelValues(component).Set(statusValue)
}

// RecordOverallStatus records overall system health status.
func (m *DashboardHealthMetrics) RecordOverallStatus(status string) {
	statusValue := 0.0
	switch status {
	case "healthy":
		statusValue = 1.0
	case "degraded":
		statusValue = 0.5
	case "unhealthy":
		statusValue = 0.0
	default:
		return
	}

	m.OverallStatusGauge.WithLabelValues(status).Set(statusValue)
}
