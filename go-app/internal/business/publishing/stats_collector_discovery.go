package publishing

import (
	"context"
	"fmt"
	"time"
)

// DiscoveryMetricsCollector collects metrics from TN-047 Target Discovery.
//
// This collector uses the TargetDiscoveryManager's GetStats() method to efficiently
// retrieve current discovery statistics without scraping Prometheus metrics.
//
// Metrics collected (from DiscoveryStats struct):
//   - targets_total (total discovered secrets)
//   - targets_valid (valid targets in cache)
//   - targets_invalid (skipped/failed targets)
//   - last_discovery_timestamp (Unix timestamp)
//   - discovery_errors_total (cumulative error count)
//
// Additional metrics (from ListTargets):
//   - targets_by_type{type} (rootly, pagerduty, slack, webhook)
//   - targets_enabled (count of enabled targets)
//   - targets_disabled (count of disabled targets)
//
// Performance: <10µs (direct struct access via GetStats + ListTargets)
//
// Thread-Safe: Yes (TargetDiscoveryManager.GetStats is thread-safe)
//
// Example:
//
//	collector := NewDiscoveryMetricsCollector(discoveryManager)
//	metrics, err := collector.Collect(ctx)
//	if err == nil {
//	    fmt.Printf("Total targets: %v\n", metrics["targets_total"])
//	}
type DiscoveryMetricsCollector struct {
	manager TargetDiscoveryManager // TargetDiscoveryManager interface (from TN-047)
}

// NewDiscoveryMetricsCollector creates DiscoveryMetricsCollector.
//
// Parameters:
//   - manager: TargetDiscoveryManager instance (from TN-047)
//
// Returns:
//   - *DiscoveryMetricsCollector: Collector instance
//
// Example:
//
//	collector := NewDiscoveryMetricsCollector(discoveryManager)
func NewDiscoveryMetricsCollector(manager TargetDiscoveryManager) *DiscoveryMetricsCollector {
	return &DiscoveryMetricsCollector{manager: manager}
}

// Collect reads discovery metrics from TargetDiscoveryManager.
//
// This method uses two data sources:
//  1. TargetDiscoveryManager.GetStats() - discovery statistics
//  2. TargetDiscoveryManager.ListTargets() - target breakdown by type
//
// Algorithm:
//  1. Check if manager is available (nil check)
//  2. Call manager.GetStats() (returns DiscoveryStats)
//  3. Call manager.ListTargets() (for type breakdown)
//  4. Convert to metric map:
//     - "targets_total" = TotalTargets
//     - "targets_valid" = ValidTargets
//     - "targets_invalid" = InvalidTargets
//     - "discovery_errors_total" = DiscoveryErrors
//     - "last_discovery_timestamp" = LastDiscovery.Unix()
//     - "targets_by_type{type=\"X\"}" = count per type
//     - "targets_enabled" = count of enabled targets
//     - "targets_disabled" = count of disabled targets
//
// Performance: <10µs (GetStats + ListTargets are cached O(1) operations)
//
// Example:
//
//	metrics, err := collector.Collect(ctx)
//	// metrics = {
//	//   "targets_total": 10.0,
//	//   "targets_valid": 8.0,
//	//   "targets_invalid": 2.0,
//	//   "targets_by_type{type=\"rootly\"}": 3.0,
//	//   "targets_by_type{type=\"pagerduty\"}": 2.0,
//	//   "targets_by_type{type=\"slack\"}": 2.0,
//	//   "targets_by_type{type=\"webhook\"}": 1.0,
//	//   "targets_enabled": 7.0,
//	//   "targets_disabled": 1.0,
//	//   "discovery_errors_total": 0.0,
//	//   "last_discovery_timestamp": 1699876543.0,
//	// }
func (c *DiscoveryMetricsCollector) Collect(ctx context.Context) (map[string]float64, error) {
	if c.manager == nil {
		return nil, fmt.Errorf("discovery manager not initialized")
	}

	// Get discovery statistics (direct cache access)
	stats := c.manager.GetStats()

	// Get all targets for type breakdown
	targets := c.manager.ListTargets()

	// Initialize metric map (pre-allocate for ~10 metrics)
	metrics := make(map[string]float64, 10)

	// Discovery stats metrics
	metrics["targets_total"] = float64(stats.TotalTargets)
	metrics["targets_valid"] = float64(stats.ValidTargets)
	metrics["targets_invalid"] = float64(stats.InvalidTargets)
	metrics["discovery_errors_total"] = float64(stats.DiscoveryErrors)

	// Last discovery timestamp (Unix timestamp)
	if !stats.LastDiscovery.IsZero() {
		metrics["last_discovery_timestamp"] = float64(stats.LastDiscovery.Unix())
	} else {
		metrics["last_discovery_timestamp"] = 0.0 // Never discovered
	}

	// Discovery age (seconds since last discovery)
	if !stats.LastDiscovery.IsZero() {
		age := time.Since(stats.LastDiscovery).Seconds()
		metrics["last_discovery_age_seconds"] = age
	} else {
		metrics["last_discovery_age_seconds"] = -1.0 // Never discovered (special value)
	}

	// Target breakdown by type
	typeCounts := make(map[string]int)
	enabledCount := 0
	disabledCount := 0

	for _, target := range targets {
		// Count by type
		typeCounts[target.Type]++

		// Count by enabled/disabled
		if target.Enabled {
			enabledCount++
		} else {
			disabledCount++
		}
	}

	// Export type counts as separate metrics
	for targetType, count := range typeCounts {
		metricName := fmt.Sprintf("targets_by_type{type=%q}", targetType)
		metrics[metricName] = float64(count)
	}

	// Enabled/disabled counts
	metrics["targets_enabled"] = float64(enabledCount)
	metrics["targets_disabled"] = float64(disabledCount)

	return metrics, nil
}

// Name returns "discovery" (for debugging).
func (c *DiscoveryMetricsCollector) Name() string {
	return "discovery"
}

// IsAvailable returns true if manager initialized.
func (c *DiscoveryMetricsCollector) IsAvailable() bool {
	return c.manager != nil
}
