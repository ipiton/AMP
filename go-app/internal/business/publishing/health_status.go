package publishing

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// processHealthCheckResult updates health status based on check result.
//
// This function:
//  1. Retrieves current status from cache (or initializes new)
//  2. Updates statistics (TotalChecks, TotalSuccesses, TotalFailures)
//  3. Updates timestamps (LastCheck, LastSuccess, LastFailure)
//  4. Applies failure threshold logic (3 consecutive failures → unhealthy)
//  5. Applies degraded detection (latency >= 5s → degraded)
//  6. Applies recovery detection (1 success → healthy)
//  7. Calculates success rate
//  8. Updates status cache
//  9. Records Prometheus metrics
//  10. Logs status transitions (INFO/WARN)
//
// Parameters:
//   - cache: Health status cache
//   - metrics: Prometheus metrics recorder
//   - logger: Structured logger
//   - config: Health configuration (thresholds)
//   - result: Health check result
//
// Example:
//
//	result := performHealthCheck(target)
//	processHealthCheckResult(cache, metrics, logger, config, result)
func processHealthCheckResult(
	cache *healthStatusCache,
	metrics *HealthMetrics,
	logger *slog.Logger,
	config HealthConfig,
	result HealthCheckResult,
) {
	// Atomic update to prevent race conditions
	// Multiple concurrent calls for same target are now safe
	updatedStatus := cache.Update(result.TargetName, func(status *TargetHealthStatus) {
		// Update statistics
		status.TotalChecks++
		status.LastCheck = result.CheckedAt

		if result.Success {
			// Success: Update success counters
			status.TotalSuccesses++
			status.LastSuccess = &result.CheckedAt
			status.LatencyMs = result.LatencyMs
			status.ErrorMessage = nil

			// Check if degraded (latency >= 5s)
			if result.LatencyMs != nil && time.Duration(*result.LatencyMs)*time.Millisecond >= config.DegradedThreshold {
				// Degraded: Slow response
				transitionStatus(status, HealthStatusDegraded, "latency >= 5s", logger)
			} else {
				// Healthy: Fast response
				transitionStatus(status, HealthStatusHealthy, "check succeeded", logger)
			}

			// Reset consecutive failures
			status.ConsecutiveFailures = 0

		} else {
			// Failure: Update failure counters
			status.TotalFailures++
			status.LastFailure = &result.CheckedAt
			status.LatencyMs = nil
			status.ErrorMessage = result.ErrorMessage

			// Increment consecutive failures
			status.ConsecutiveFailures++

			// Check failure threshold FIRST
			if status.ConsecutiveFailures >= config.FailureThreshold {
				// Unhealthy: Too many consecutive failures
				transitionStatus(
					status,
					HealthStatusUnhealthy,
					fmt.Sprintf("%d consecutive failures", status.ConsecutiveFailures),
					logger,
				)
			} else if status.Status == HealthStatusUnknown {
				// First failure (below threshold): transition from unknown to degraded
				transitionStatus(
					status,
					HealthStatusDegraded,
					fmt.Sprintf("failure %d/%d", status.ConsecutiveFailures, config.FailureThreshold),
					logger,
				)
			} else {
				// Still healthy/degraded (below threshold)
				logger.Debug("Target failure (below threshold)",
					"target_name", result.TargetName,
					"consecutive_failures", status.ConsecutiveFailures,
					"threshold", config.FailureThreshold,
					"error", result.ErrorMessage)
			}
		}

		// Calculate success rate
		if status.TotalChecks > 0 {
			status.SuccessRate = (float64(status.TotalSuccesses) / float64(status.TotalChecks)) * 100
		}
	})

	// Update Prometheus metrics (outside lock for better performance)
	metrics.SetTargetHealthStatus(result.TargetName, updatedStatus.TargetType, updatedStatus.Status)
	metrics.SetConsecutiveFailures(result.TargetName, updatedStatus.ConsecutiveFailures)
	metrics.SetSuccessRate(result.TargetName, updatedStatus.SuccessRate)
}

// transitionStatus transitions health status with logging.
//
// This function:
//  1. Checks if status actually changed (skip if same)
//  2. Updates status field
//  3. Determines appropriate log level:
//     - WARN: healthy → unhealthy (alert on degradation)
//     - INFO: unhealthy → healthy (recovery celebration)
//     - INFO: other transitions
//  4. Logs transition with full context
//
// Parameters:
//   - status: Target health status (modified in-place)
//   - newStatus: New health status
//   - reason: Human-readable reason for transition
//   - logger: Structured logger
//
// Example:
//
//	transitionStatus(status, HealthStatusUnhealthy, "3 consecutive failures", logger)
func transitionStatus(
	status *TargetHealthStatus,
	newStatus HealthStatus,
	reason string,
	logger *slog.Logger,
) {
	oldStatus := status.Status

	// Skip if no change
	if oldStatus == newStatus {
		return
	}

	// Update status
	status.Status = newStatus

	// Determine log level based on transition
	logLevel := slog.LevelInfo
	if newStatus == HealthStatusUnhealthy {
		// Alert on unhealthy transition
		logLevel = slog.LevelWarn
	} else if newStatus == HealthStatusHealthy && oldStatus == HealthStatusUnhealthy {
		// Celebrate recovery
		logLevel = slog.LevelInfo
	} else if newStatus == HealthStatusDegraded {
		// Warn on degraded (slow target)
		logLevel = slog.LevelWarn
	}

	// Log transition with full context
	logger.Log(context.Background(), logLevel,
		"Target health status changed",
		"target_name", status.TargetName,
		"old_status", oldStatus,
		"new_status", newStatus,
		"reason", reason,
		"consecutive_failures", status.ConsecutiveFailures,
		"success_rate", fmt.Sprintf("%.1f%%", status.SuccessRate),
		"latency_ms", status.LatencyMs)
}

// initializeHealthStatus creates initial health status for new target.
//
// This function creates default TargetHealthStatus with:
//   - Status: HealthStatusUnknown (no checks yet)
//   - All counters: 0
//   - All timestamps: nil
//   - SuccessRate: 0.0%
//
// Parameters:
//   - targetName: Name of target
//   - targetType: Type of target (rootly/pagerduty/slack/webhook)
//   - enabled: Is target enabled?
//
// Returns:
//   - *TargetHealthStatus: Initialized status
//
// Example:
//
//	status := initializeHealthStatus("rootly-prod", "rootly", true)
//	cache.Set(status)
func initializeHealthStatus(targetName, targetType string, enabled bool) *TargetHealthStatus {
	return &TargetHealthStatus{
		TargetName:          targetName,
		TargetType:          targetType,
		Enabled:             enabled,
		Status:              HealthStatusUnknown,
		LatencyMs:           nil,
		ErrorMessage:        nil,
		LastCheck:           time.Time{}, // Zero time
		LastSuccess:         nil,
		LastFailure:         nil,
		ConsecutiveFailures: 0,
		TotalChecks:         0,
		TotalSuccesses:      0,
		TotalFailures:       0,
		SuccessRate:         0.0,
	}
}

// calculateAggregateStats calculates aggregate health statistics.
//
// This function:
//  1. Iterates over all health statuses
//  2. Counts targets by status (healthy/unhealthy/degraded/unknown)
//  3. Calculates overall success rate (weighted average)
//  4. Finds most recent check time
//  5. Returns HealthStats
//
// Parameters:
//   - statuses: Array of all target health statuses
//
// Returns:
//   - *HealthStats: Aggregate statistics
//
// Performance: O(n) for n targets
//
// Example:
//
//	allStatuses := cache.GetAll()
//	stats := calculateAggregateStats(allStatuses)
//	log.Info("Health stats",
//	    "total", stats.TotalTargets,
//	    "healthy", stats.HealthyCount,
//	    "overall_success_rate", stats.OverallSuccessRate)
func calculateAggregateStats(statuses []TargetHealthStatus) *HealthStats {
	stats := &HealthStats{
		TotalTargets:       len(statuses),
		HealthyCount:       0,
		UnhealthyCount:     0,
		DegradedCount:      0,
		UnknownCount:       0,
		LastCheckTime:      nil,
		OverallSuccessRate: 0.0,
	}

	if len(statuses) == 0 {
		return stats
	}

	var totalSuccesses int64
	var totalChecks int64
	var mostRecentCheck time.Time

	for _, status := range statuses {
		// Count by status
		switch status.Status {
		case HealthStatusHealthy:
			stats.HealthyCount++
		case HealthStatusUnhealthy:
			stats.UnhealthyCount++
		case HealthStatusDegraded:
			stats.DegradedCount++
		case HealthStatusUnknown:
			stats.UnknownCount++
		}

		// Aggregate for overall success rate
		totalSuccesses += status.TotalSuccesses
		totalChecks += status.TotalChecks

		// Track most recent check
		if status.LastCheck.After(mostRecentCheck) {
			mostRecentCheck = status.LastCheck
		}
	}

	// Calculate overall success rate
	if totalChecks > 0 {
		stats.OverallSuccessRate = (float64(totalSuccesses) / float64(totalChecks)) * 100
	}

	// Set most recent check time (if any checks performed)
	if !mostRecentCheck.IsZero() {
		stats.LastCheckTime = &mostRecentCheck
	}

	return stats
}

// shouldSkipHealthCheck determines if target should skip health check.
//
// Targets should skip health check if:
//   - Target is disabled (Enabled: false)
//   - Target URL is empty (invalid configuration)
//
// Parameters:
//   - targetName: Name of target
//   - targetURL: Target URL
//   - enabled: Is target enabled?
//   - logger: Structured logger
//
// Returns:
//   - bool: true if should skip, false if should check
//
// Example:
//
//	if shouldSkipHealthCheck(target.Name, target.URL, target.Enabled, logger) {
//	    continue // Skip this target
//	}
//	performHealthCheck(target)
func shouldSkipHealthCheck(targetName, targetURL string, enabled bool, logger *slog.Logger) bool {
	// Skip disabled targets
	if !enabled {
		logger.Debug("Skipping disabled target",
			"target_name", targetName)
		return true
	}

	// Skip targets with empty URL
	if targetURL == "" {
		logger.Warn("Skipping target with empty URL",
			"target_name", targetName)
		return true
	}

	return false
}

// ptr is helper function to create pointer to value.
//
// This is useful for optional fields in TargetHealthStatus.
//
// Example:
//
//	latency := ptr(int64(123))
//	status.LatencyMs = latency
func ptr[T any](v T) *T {
	return &v
}
