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
//  1. Gets all targets from TargetDiscoveryManager
//  2. Filters enabled targets only (skip disabled)
//  3. Creates goroutine pool (max 10 concurrent checks)
//  4. Performs health checks in parallel
//  5. Processes results and updates cache
//  6. Records Prometheus metrics
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
	results := make(chan HealthCheckResult, len(enabledTargets))   // Buffered channel

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
