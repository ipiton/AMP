package publishing

import "errors"

// Error types for parallel publishing operations.
//
// These errors are returned by ParallelPublisher methods to indicate
// various failure conditions.

var (
	// ErrInvalidInput indicates that the input parameters are invalid.
	//
	// This error is returned when:
	//   - alert is nil
	//   - targets is nil or empty
	//   - options are invalid (timeout <= 0, max_concurrent <= 0)
	//
	// Example:
	//
	//	result, err := parallelPublisher.PublishToMultiple(ctx, nil, targets)
	//	if errors.Is(err, ErrInvalidInput) {
	//	    log.Error("Invalid input", "error", err)
	//	}
	ErrInvalidInput = errors.New("invalid input: alert or targets nil/empty")

	// ErrAllTargetsFailed indicates that all targets failed to publish.
	//
	// This error is returned when:
	//   - SuccessCount == 0
	//   - FailureCount > 0 OR SkippedCount > 0
	//
	// Note: This error is NOT returned if at least one target succeeded.
	//
	// Example:
	//
	//	result, err := parallelPublisher.PublishToMultiple(ctx, alert, targets)
	//	if errors.Is(err, ErrAllTargetsFailed) {
	//	    log.Error("All targets failed", "error", err)
	//	    // Check per-target errors
	//	    for _, targetResult := range result.Results {
	//	        if !targetResult.Success {
	//	            log.Error("Target failed", "target", targetResult.TargetName, "error", targetResult.Error)
	//	        }
	//	    }
	//	}
	ErrAllTargetsFailed = errors.New("all targets failed")

	// ErrContextTimeout indicates that the context timeout was exceeded.
	//
	// This error is returned when:
	//   - Context timeout is exceeded before all publishes complete
	//   - Some goroutines may still be running (they will be cancelled)
	//
	// Example:
	//
	//	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	//	defer cancel()
	//
	//	result, err := parallelPublisher.PublishToMultiple(ctx, alert, targets)
	//	if errors.Is(err, ErrContextTimeout) {
	//	    log.Warn("Context timeout", "error", err)
	//	    // Check partial results
	//	    log.Info("Partial results", "success_count", result.SuccessCount)
	//	}
	ErrContextTimeout = errors.New("context timeout exceeded")

	// ErrContextCancelled indicates that the context was cancelled.
	//
	// This error is returned when:
	//   - Context is cancelled before all publishes complete
	//   - Some goroutines may still be running (they will be stopped)
	//
	// Example:
	//
	//	ctx, cancel := context.WithCancel(ctx)
	//	defer cancel()
	//
	//	// Cancel context after 5 seconds
	//	go func() {
	//	    time.Sleep(5 * time.Second)
	//	    cancel()
	//	}()
	//
	//	result, err := parallelPublisher.PublishToMultiple(ctx, alert, targets)
	//	if errors.Is(err, ErrContextCancelled) {
	//	    log.Warn("Context cancelled", "error", err)
	//	}
	ErrContextCancelled = errors.New("context cancelled")

	// ErrNoHealthyTargets indicates that no healthy targets are available.
	//
	// This error is returned when:
	//   - CheckHealth is true
	//   - All targets are unhealthy (or degraded, depending on strategy)
	//   - No targets to publish to
	//
	// Example:
	//
	//	result, err := parallelPublisher.PublishToHealthy(ctx, alert)
	//	if errors.Is(err, ErrNoHealthyTargets) {
	//	    log.Warn("No healthy targets", "error", err)
	//	    // Fallback: publish to all targets (ignore health)
	//	    result, err = parallelPublisher.PublishToAll(ctx, alert)
	//	}
	ErrNoHealthyTargets = errors.New("no healthy targets available")

	// ErrNoEnabledTargets indicates that no enabled targets are available.
	//
	// This error is returned when:
	//   - All targets are disabled (Enabled = false)
	//   - No targets to publish to
	//
	// Example:
	//
	//	result, err := parallelPublisher.PublishToAll(ctx, alert)
	//	if errors.Is(err, ErrNoEnabledTargets) {
	//	    log.Warn("No enabled targets", "error", err)
	//	    // Check discovery configuration
	//	}
	ErrNoEnabledTargets = errors.New("no enabled targets available")
)
