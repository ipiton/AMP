package publishing

import (
	"context"
	"time"
)

// refreshWithRetry executes refresh with exponential backoff retry.
//
// This method:
//   1. Attempts refresh (m.discovery.DiscoverTargets)
//   2. On failure, classifies error (transient vs permanent)
//   3. If transient, retries with exponential backoff
//   4. If permanent, fails immediately (no retry)
//   5. Returns after maxRetries or success
//
// Backoff Schedule (default config):
//   - Attempt 1: 0s (immediate)
//   - Attempt 2: 30s (baseBackoff)
//   - Attempt 3: 1m (2x)
//   - Attempt 4: 2m (2x)
//   - Attempt 5: 4m (2x)
//   - Attempt 6: 5m (maxBackoff, capped)
//
// Error Classification:
//   - Transient: Network timeout, connection refused, 503
//     Action: Retry with exponential backoff
//   - Permanent: 401, 403, parse error
//     Action: Fail immediately (no retry)
//
// Parameters:
//   - ctx: Context with timeout (e.g., 30s)
//
// Returns:
//   - nil on success (any attempt)
//   - RefreshError with retry context on failure
//
// Thread-Safe: Yes (no shared state modifications)
func (m *DefaultRefreshManager) refreshWithRetry(ctx context.Context) error {
	var lastErr error
	backoff := m.config.BaseBackoff
	totalStartTime := time.Now()

	for attempt := 0; attempt < m.config.MaxRetries; attempt++ {
		// Attempt refresh
		attemptStartTime := time.Now()
		err := m.discovery.DiscoverTargets(ctx)
		attemptDuration := time.Since(attemptStartTime)

		if err == nil {
			// Success!
			totalDuration := time.Since(totalStartTime)
			m.logger.Info("Refresh succeeded",
				"attempt", attempt+1,
				"attempt_duration", attemptDuration,
				"total_duration", totalDuration)
			return nil
		}

		// Failure - classify error
		errorType, transient := classifyError(err)
		lastErr = err

		m.logger.Warn("Refresh attempt failed",
			"attempt", attempt+1,
			"max_retries", m.config.MaxRetries,
			"error", err,
			"error_type", errorType,
			"transient", transient,
			"attempt_duration", attemptDuration)

		// Permanent error - no retry
		if !transient {
			totalDuration := time.Since(totalStartTime)
			m.logger.Error("Permanent error detected, no retry",
				"error", err,
				"error_type", errorType,
				"total_duration", totalDuration)

			return &RefreshError{
				Op:        "discover_targets",
				Err:       err,
				Retries:   attempt,
				Duration:  totalDuration,
				Transient: false,
			}
		}

		// Transient error - retry with backoff (if not last attempt)
		if attempt < m.config.MaxRetries-1 {
			// Calculate next backoff (exponential)
			nextBackoff := backoff * 2
			if nextBackoff > m.config.MaxBackoff {
				nextBackoff = m.config.MaxBackoff
			}

			m.logger.Info("Retrying refresh after backoff",
				"next_attempt", attempt+2,
				"backoff", backoff,
				"next_backoff", nextBackoff)

			// Wait for backoff (respecting context)
			select {
			case <-time.After(backoff):
				// Continue to next attempt
				m.logger.Debug("Backoff completed, retrying",
					"attempt", attempt+2)
			case <-ctx.Done():
				// Context cancelled/timeout during backoff
				totalDuration := time.Since(totalStartTime)
				m.logger.Warn("Context cancelled during backoff",
					"error", ctx.Err(),
					"total_duration", totalDuration)

				return &RefreshError{
					Op:        "discover_targets",
					Err:       ctx.Err(),
					Retries:   attempt + 1,
					Duration:  totalDuration,
					Transient: false, // Context cancellation is permanent
				}
			}

			// Update backoff for next iteration
			backoff = nextBackoff
		}
	}

	// Max retries exceeded
	totalDuration := time.Since(totalStartTime)
	m.logger.Error("Max retries exceeded",
		"max_retries", m.config.MaxRetries,
		"last_error", lastErr,
		"total_duration", totalDuration)

	return &RefreshError{
		Op:        "discover_targets",
		Err:       lastErr,
		Retries:   m.config.MaxRetries,
		Duration:  totalDuration,
		Transient: true, // Still transient, just exhausted retries
	}
}
