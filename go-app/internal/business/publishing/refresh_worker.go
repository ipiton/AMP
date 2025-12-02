package publishing

import (
	"context"
	"time"
)

// runBackgroundWorker is the main goroutine for periodic refresh.
//
// This goroutine:
//   1. Waits for warmup period (30s delay before first refresh)
//   2. Executes first refresh
//   3. Creates ticker for periodic refresh (5m interval)
//   4. Executes refresh on each tick
//   5. Exits gracefully on context cancellation
//   6. Signals completion via WaitGroup
//
// Lifecycle:
//   - Started by Start() (via go m.runBackgroundWorker())
//   - Stopped by Stop() (via context cancellation)
//   - Tracked by WaitGroup (m.wg)
//
// Performance:
//   - Warmup period: 30s (configurable)
//   - Refresh interval: 5m (configurable)
//   - Zero goroutine leaks (proper cleanup)
//
// Thread-Safe: Yes (no shared state modifications without locks)
func (m *DefaultRefreshManager) runBackgroundWorker() {
	defer m.wg.Done()

	m.logger.Info("Background refresh worker started",
		"interval", m.config.Interval,
		"warmup_period", m.config.WarmupPeriod)

	// Warmup period (avoid refresh immediately on startup)
	if m.config.WarmupPeriod > 0 {
		m.logger.Debug("Waiting for warmup period", "duration", m.config.WarmupPeriod)
		select {
		case <-time.After(m.config.WarmupPeriod):
			m.logger.Debug("Warmup period completed")
		case <-m.ctx.Done():
			m.logger.Info("Worker cancelled during warmup")
			return
		}
	}

	// Execute first refresh immediately after warmup
	m.logger.Info("Executing first refresh after warmup")
	m.executeRefresh(false) // isManual=false

	// Create ticker for periodic refresh
	ticker := time.NewTicker(m.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Periodic refresh triggered
			m.logger.Debug("Periodic refresh triggered by ticker")
			m.executeRefresh(false) // isManual=false

		case <-m.ctx.Done():
			// Graceful shutdown
			m.logger.Info("Background worker stopping (context cancelled)")
			return
		}
	}
}

// executeRefresh performs actual refresh with retry logic.
//
// This method:
//   1. Checks if refresh already in progress (skip if yes)
//   2. Sets state to in_progress
//   3. Updates metrics (in_progress=1)
//   4. Calls refreshWithRetry() (retry logic with exponential backoff)
//   5. Updates state based on result (success/failed)
//   6. Records metrics (duration, errors, last_success)
//   7. Logs result (success/failure with details)
//
// Parameters:
//   - isManual: true if triggered via API, false if periodic
//
// Thread-Safe: Yes (single-flight pattern via mutex)
func (m *DefaultRefreshManager) executeRefresh(isManual bool) {
	// Single-flight pattern: skip if already in progress
	m.mu.Lock()
	if m.inProgress {
		m.logger.Debug("Refresh already in progress, skipping",
			"is_manual", isManual)
		m.mu.Unlock()
		return
	}
	m.inProgress = true
	m.state = RefreshStateInProgress
	m.mu.Unlock()

	// Ensure inProgress is reset on exit
	defer func() {
		m.mu.Lock()
		m.inProgress = false
		m.mu.Unlock()
	}()

	// Record start time
	startTime := time.Now()

	// Update metrics (in_progress=1)
	m.metrics.InProgress.Set(1)
	defer m.metrics.InProgress.Set(0)

	// Log refresh start
	refreshType := "periodic"
	if isManual {
		refreshType = "manual"
	}
	m.logger.Info("Refresh started",
		"type", refreshType,
		"timeout", m.config.RefreshTimeout)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(m.ctx, m.config.RefreshTimeout)
	defer cancel()

	// Execute refresh with retry logic
	err := m.refreshWithRetry(ctx)

	// Calculate duration
	duration := time.Since(startTime)

	if err != nil {
		// Refresh failed
		m.logger.Error("Refresh failed",
			"type", refreshType,
			"error", err,
			"duration", duration,
			"consecutive_failures", m.consecutiveFailures+1)

		// Update state
		m.updateState(
			RefreshStateFailed,
			time.Time{}, // lastRefresh unchanged
			err,
			targetStats{}, // targetStats unchanged
			duration,
		)

		// Record metrics
		m.metrics.Total.WithLabelValues("failed").Inc()
		m.metrics.Duration.WithLabelValues("failed").Observe(duration.Seconds())

		// Classify error for metrics
		errorType, _ := classifyError(err)
		m.metrics.ErrorsTotal.WithLabelValues(errorType).Inc()

	} else {
		// Refresh succeeded
		m.logger.Info("Refresh completed successfully",
			"type", refreshType,
			"duration", duration)

		// Get stats from discovery manager
		discoveryStats := m.discovery.GetStats()
		stats := targetStats{
			Total:   discoveryStats.TotalTargets,
			Valid:   discoveryStats.ValidTargets,
			Invalid: discoveryStats.InvalidTargets,
		}

		m.logger.Info("Target discovery stats",
			"targets_total", stats.Total,
			"targets_valid", stats.Valid,
			"targets_invalid", stats.Invalid)

		// Update state
		m.updateState(
			RefreshStateSuccess,
			time.Now(),
			nil,
			stats,
			duration,
		)

		// Record metrics
		m.metrics.Total.WithLabelValues("success").Inc()
		m.metrics.Duration.WithLabelValues("success").Observe(duration.Seconds())
		m.metrics.LastSuccessTimestamp.Set(float64(time.Now().Unix()))
	}
}
