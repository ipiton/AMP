package publishing

import (
	"context"
	"fmt"
)

// RefreshMetricsCollector collects metrics from TN-048 Target Refresh.
//
// This collector uses the RefreshManager's GetStatus() method to efficiently
// retrieve current refresh statistics without scraping Prometheus metrics.
//
// Metrics collected (conceptual - actual values from RefreshManager):
//   - refresh_total (by status: success/failed)
//   - refresh_last_success_timestamp (Unix timestamp)
//   - refresh_in_progress (1 if running, 0 if idle)
//
// Performance: <10µs (direct struct access via GetStatus)
//
// Thread-Safe: Yes (RefreshManager.GetStatus is thread-safe)
//
// Example:
//
//	collector := NewRefreshMetricsCollector(refreshManager)
//	metrics, err := collector.Collect(ctx)
//	if err == nil {
//	    fmt.Printf("Last refresh: %v\n", metrics["refresh_last_success_timestamp"])
//	}
type RefreshMetricsCollector struct {
	manager RefreshManager // RefreshManager interface (from TN-048)
}

// NewRefreshMetricsCollector creates RefreshMetricsCollector.
//
// Parameters:
//   - manager: RefreshManager instance (from TN-048)
//
// Returns:
//   - *RefreshMetricsCollector: Collector instance
//
// Example:
//
//	collector := NewRefreshMetricsCollector(refreshManager)
func NewRefreshMetricsCollector(manager RefreshManager) *RefreshMetricsCollector {
	return &RefreshMetricsCollector{manager: manager}
}

// Collect reads refresh metrics from RefreshManager.
//
// This method uses RefreshManager.GetStatus() to get current refresh state,
// then converts to map[string]float64 format for aggregation.
//
// Algorithm:
//  1. Check if manager is available (nil check)
//  2. Call manager.GetStatus() (returns RefreshStatus)
//  3. Convert to metric map:
//     - "refresh_last_success_timestamp" = Unix timestamp
//     - "refresh_in_progress" = 1.0 (running) or 0.0 (idle)
//     - "refresh_interval_seconds" = configured interval
//
// Performance: <10µs (GetStatus returns cached state)
//
// Example:
//
//	metrics, err := collector.Collect(ctx)
//	// metrics = {
//	//   "refresh_last_success_timestamp": 1699876543.0,  // Unix timestamp
//	//   "refresh_in_progress": 0.0,                     // idle
//	//   "refresh_interval_seconds": 300.0,              // 5 minutes
//	// }
func (c *RefreshMetricsCollector) Collect(ctx context.Context) (map[string]float64, error) {
	if c.manager == nil {
		return nil, fmt.Errorf("refresh manager not initialized")
	}

	// Get refresh status
	status := c.manager.GetStatus()

	// Convert to metric map
	metrics := make(map[string]float64, 5)

	// Last success timestamp (Unix timestamp)
	if !status.LastRefresh.IsZero() {
		metrics["refresh_last_success_timestamp"] = float64(status.LastRefresh.Unix())
	} else {
		metrics["refresh_last_success_timestamp"] = 0.0 // Never refreshed
	}

	// Next refresh timestamp
	if !status.NextRefresh.IsZero() {
		metrics["refresh_next_refresh_timestamp"] = float64(status.NextRefresh.Unix())
	} else {
		metrics["refresh_next_refresh_timestamp"] = 0.0
	}

	// In progress flag (1.0 = running, 0.0 = idle)
	if status.State == RefreshStateInProgress {
		metrics["refresh_in_progress"] = 1.0
	} else {
		metrics["refresh_in_progress"] = 0.0
	}

	// Last refresh duration (in seconds)
	metrics["refresh_duration_seconds"] = status.RefreshDuration.Seconds()

	// Targets discovered
	metrics["refresh_targets_discovered"] = float64(status.TargetsDiscovered)

	// Note: RefreshManager also tracks errors, but those are in RefreshMetrics (Prometheus)
	// For simplicity, we only expose the essential state here
	// Full metrics can be scraped via Prometheus endpoint

	return metrics, nil
}

// Name returns "refresh" (for debugging).
func (c *RefreshMetricsCollector) Name() string {
	return "refresh"
}

// IsAvailable returns true if manager initialized.
func (c *RefreshMetricsCollector) IsAvailable() bool {
	return c.manager != nil
}
