package publishing

import (
	"context"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// checkAllTargets checks health of all enabled targets (parallel execution).
//
// This function:
//   1. Gets all targets from TargetDiscoveryManager
//   2. Filters enabled targets only (skip disabled)
//   3. Creates goroutine pool (max 10 concurrent checks)
//   4. Performs health checks in parallel
//   5. Processes results and updates cache
//   6. Records Prometheus metrics
//
// Parallelism Strategy:
//   - Semaphore pattern: Max 10 concurrent goroutines
//   - WaitGroup: Wait for all checks to complete
//   - Buffered channel: Collect results without blocking
//
// Performance:
//   - Single target: ~100-300ms (HTTP request)
//   - 20 targets (parallel): ~500ms-2s (limited by slowest target)
//   - 100 targets (parallel): ~2-5s (10 concurrent workers)
//
// Error Handling:
//   - Target list failure → returns error, keeps old cache
//   - Individual check failures → logged, doesn't stop others
//   - Context cancellation → stops gracefully (WaitGroup)
//
// Parameters:
//   - m: DefaultHealthMonitor instance
//   - ctx: Context (for cancellation)
//   - checkType: Check type (periodic/manual)
//
// Returns:
//   - error: If failed to list targets from discovery manager
//
// Example:
//
//	if err := m.checkAllTargets(ctx, CheckTypePeriodic); err != nil {
//	    m.logger.Error("Health check failed", "error", err)
//	}
func (m *DefaultHealthMonitor) checkAllTargets(ctx context.Context, checkType CheckType) error {
	// Get all targets from discovery manager
	targets := m.discoveryMgr.ListTargets()

	// Filter enabled targets only
	enabledTargets := make([]*core.PublishingTarget, 0, len(targets))
	for _, target := range targets {
		if target.Enabled {
			enabledTargets = append(enabledTargets, target)
		}
	}

	if len(enabledTargets) == 0 {
		m.logger.Debug("No enabled targets to check",
			"total_targets", len(targets))
		return nil
	}

	m.logger.Debug("Starting health checks",
		"total_targets", len(targets),
		"enabled_targets", len(enabledTargets),
		"check_type", checkType,
		"max_concurrent", m.config.MaxConcurrentChecks)

	// Record start time for overall duration
	startTime := time.Now()

	// Create goroutine pool for parallel checks
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, m.config.MaxConcurrentChecks) // Limit concurrency
	results := make(chan HealthCheckResult, len(enabledTargets))    // Buffered channel

	// Launch health check goroutines
	for _, target := range enabledTargets {
		wg.Add(1)
		go func(t *core.PublishingTarget) {
			defer wg.Done()

			// Acquire semaphore (blocks if pool full)
			semaphore <- struct{}{}
			defer func() { <-semaphore }() // Release semaphore

			// Check for context cancellation before starting
			select {
			case <-ctx.Done():
				m.logger.Debug("Health check cancelled",
					"target_name", t.Name)
				return
			default:
				// Continue with check
			}

			// Perform health check (with retry)
			result := checkTargetWithRetry(ctx, t, checkType, m.httpClient, m.config)

			// Send result to channel
			results <- result
		}(target)
	}

	// Wait for all checks to complete
	wg.Wait()
	close(results)

	// Process all results
	resultsProcessed := 0
	successCount := 0
	failureCount := 0

	for result := range results {
		// Process result (update cache, metrics, logs)
		processHealthCheckResult(m.statusCache, m.metrics, m.logger, m.config, result)

		// Count successes/failures
		resultsProcessed++
		if result.Success {
			successCount++
		} else {
			failureCount++
		}

		// Log detailed result (DEBUG level)
		if result.Success {
			m.logger.Debug("Health check succeeded",
				"target_name", result.TargetName,
				"latency_ms", result.LatencyMs,
				"status_code", result.StatusCode,
				"check_type", result.CheckType)
		} else {
			m.logger.Debug("Health check failed",
				"target_name", result.TargetName,
				"error", result.ErrorMessage,
				"error_type", result.ErrorType,
				"check_type", result.CheckType)
		}
	}

	// Calculate overall duration
	overallDuration := time.Since(startTime)

	// Log summary
	m.logger.Info("Health checks completed",
		"total_checked", resultsProcessed,
		"successes", successCount,
		"failures", failureCount,
		"duration", overallDuration,
		"check_type", checkType)

	return nil
}

// checkTargetAsync performs async health check for single target.
//
// This is a convenience wrapper for CheckNow that doesn't block.
// Useful for manual health checks triggered via API.
//
// Parameters:
//   - m: DefaultHealthMonitor instance
//   - ctx: Context
//   - targetName: Name of target to check
//
// Example:
//
//	go m.checkTargetAsync(ctx, "rootly-prod")
func (m *DefaultHealthMonitor) checkTargetAsync(ctx context.Context, targetName string) {
	// Get target
	target, err := m.discoveryMgr.GetTarget(targetName)
	if err != nil {
		m.logger.Error("Failed to get target for async check",
			"target_name", targetName,
			"error", err)
		return
	}

	// Perform health check
	result := checkTargetWithRetry(ctx, target, CheckTypeManual, m.httpClient, m.config)

	// Process result
	processHealthCheckResult(m.statusCache, m.metrics, m.logger, m.config, result)

	m.logger.Info("Async health check completed",
		"target_name", targetName,
		"success", result.Success)
}

// shouldCheckTarget determines if target should be checked.
//
// Targets are skipped if:
//   - Disabled (Enabled: false)
//   - Empty URL (invalid configuration)
//   - Name is empty (invalid target)
//
// Parameters:
//   - target: Publishing target
//
// Returns:
//   - bool: true if should check, false if should skip
//   - string: Reason for skipping (empty if should check)
//
// Example:
//
//	if shouldCheck, reason := shouldCheckTarget(target); !shouldCheck {
//	    m.logger.Debug("Skipping target", "name", target.Name, "reason", reason)
//	    continue
//	}
func shouldCheckTarget(target *core.PublishingTarget) (bool, string) {
	// Skip disabled targets
	if !target.Enabled {
		return false, "target disabled"
	}

	// Skip targets with empty URL
	if target.URL == "" {
		return false, "empty URL"
	}

	// Skip targets with empty name
	if target.Name == "" {
		return false, "empty name"
	}

	return true, ""
}

// getTargetsToCheck filters targets for health checking.
//
// This function:
//   1. Gets all targets from discovery
//   2. Filters enabled targets
//   3. Validates target configuration
//   4. Returns checkable targets
//
// Parameters:
//   - m: DefaultHealthMonitor instance
//
// Returns:
//   - []*core.PublishingTarget: Targets that should be checked
//   - int: Total number of targets (for stats)
//
// Example:
//
//	targets, total := m.getTargetsToCheck()
//	m.logger.Info("Checking targets", "checkable", len(targets), "total", total)
func (m *DefaultHealthMonitor) getTargetsToCheck() ([]*core.PublishingTarget, int) {
	// Get all targets
	allTargets := m.discoveryMgr.ListTargets()
	totalCount := len(allTargets)

	// Filter checkable targets
	checkableTargets := make([]*core.PublishingTarget, 0, totalCount)

	for _, target := range allTargets {
		// Check if target should be checked
		if shouldCheck, reason := shouldCheckTarget(target); shouldCheck {
			checkableTargets = append(checkableTargets, target)
		} else {
			m.logger.Debug("Skipping target",
				"target_name", target.Name,
				"reason", reason)
		}
	}

	return checkableTargets, totalCount
}

// recheckUnhealthyTargets performs focused health checks on unhealthy targets.
//
// This function is called periodically (between regular checks) to:
//   - Detect recovery faster (don't wait full interval)
//   - Reduce alert fatigue (faster recovery notification)
//   - Minimize unnecessary checks (only unhealthy targets)
//
// Strategy:
//   - Only check targets with status = unhealthy
//   - Run checks every 30s (faster than regular 2m interval)
//   - If target recovers → immediate status update
//
// Parameters:
//   - m: DefaultHealthMonitor instance
//   - ctx: Context
//
// Example:
//
//	// Call from background worker (every 30s)
//	m.recheckUnhealthyTargets(ctx)
func (m *DefaultHealthMonitor) recheckUnhealthyTargets(ctx context.Context) {
	// Get all health statuses
	allStatuses := m.statusCache.GetAll()

	// Filter unhealthy targets
	unhealthyTargets := make([]*core.PublishingTarget, 0)
	for _, status := range allStatuses {
		if status.Status == HealthStatusUnhealthy {
			// Get target from discovery
			target, err := m.discoveryMgr.GetTarget(status.TargetName)
			if err != nil {
				continue // Target no longer exists
			}
			unhealthyTargets = append(unhealthyTargets, target)
		}
	}

	if len(unhealthyTargets) == 0 {
		// No unhealthy targets, skip recheck
		return
	}

	m.logger.Debug("Rechecking unhealthy targets",
		"count", len(unhealthyTargets))

	// Check each unhealthy target
	for _, target := range unhealthyTargets {
		// Check if context cancelled
		select {
		case <-ctx.Done():
			return
		default:
			// Continue
		}

		// Perform health check
		result := checkSingleTarget(ctx, target, CheckTypePeriodic, m.httpClient, m.config)

		// Process result (will update status if recovered)
		processHealthCheckResult(m.statusCache, m.metrics, m.logger, m.config, result)

		// Log if recovered
		if result.Success {
			m.logger.Info("Target recovered",
				"target_name", target.Name,
				"latency_ms", result.LatencyMs)
		}
	}
}
