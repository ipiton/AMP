package publishing

import (
	"context"
	"time"

	"github.com/ipiton/AMP/pkg/retry"
)

// refreshWithRetry executes refresh with exponential backoff retry.
//
// Migrated to use pkg/retry for unified retry strategy (TN-057).
//
// This method:
//   1. Attempts refresh (m.discovery.DiscoverTargets)
//   2. On failure, classifies error (transient vs permanent)
//   3. If transient, retries with exponential backoff (via pkg/retry)
//   4. If permanent, fails immediately (no retry)
//   5. Returns after maxRetries or success
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
	startTime := time.Now()

	// Create retry strategy with refresh-specific settings
	strategy := retry.Strategy{
		MaxAttempts:     m.config.MaxRetries,
		BaseDelay:       m.config.BaseBackoff,
		MaxDelay:        m.config.MaxBackoff,
		Multiplier:      2.0, // Exponential backoff (30s → 1m → 2m → 4m → 5m)
		JitterRatio:     0.1, // 10% jitter to prevent thundering herd
		ErrorClassifier: &refreshErrorClassifier{}, // Classify transient vs permanent
		Logger:          m.logger,
		OperationName:   "refresh_targets",
	}

	// Execute with unified retry logic
	err := retry.DoSimple(ctx, strategy, func() error {
		return m.discovery.DiscoverTargets(ctx)
	})

	// Wrap error in RefreshError for backward compatibility
	if err != nil {
		duration := time.Since(startTime)
		errorType, transient := classifyError(err)

		m.logger.Error("Refresh failed",
			"error", err,
			"error_type", errorType,
			"transient", transient,
			"duration", duration)

		return &RefreshError{
			Op:        "discover_targets",
			Err:       err,
			Retries:   m.config.MaxRetries,
			Duration:  duration,
			Transient: transient,
		}
	}

	duration := time.Since(startTime)
	m.logger.Info("Refresh succeeded",
		"duration", duration)

	return nil
}

// refreshErrorClassifier implements retry.ErrorClassifier for refresh operations.
type refreshErrorClassifier struct{}

func (c *refreshErrorClassifier) IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Use existing classifyError to determine if error is transient
	_, transient := classifyError(err)
	return transient
}
