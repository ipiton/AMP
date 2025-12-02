package publishing

import (
	"time"
)

// ParallelPublishResult represents the aggregate result of parallel publishing to multiple targets.
//
// This structure contains:
//   - Aggregate counts (total, success, failure, skipped)
//   - Per-target detailed results
//   - Total execution duration (parallel, not sequential)
//   - Partial success flag (some succeeded, some failed)
//
// Example usage:
//
//	result, err := parallelPublisher.PublishToMultiple(ctx, alert, targets)
//	if err != nil {
//	    log.Error("Publishing failed", "error", err)
//	}
//
//	log.Info("Publishing completed",
//	    "total_targets", result.TotalTargets,
//	    "success_count", result.SuccessCount,
//	    "failure_count", result.FailureCount,
//	    "duration_ms", result.Duration.Milliseconds(),
//	    "is_partial_success", result.IsPartialSuccess,
//	)
//
//	// Check if at least one target succeeded
//	if result.Success() {
//	    log.Info("At least one target succeeded")
//	}
//
//	// Check if all targets succeeded
//	if result.AllSucceeded() {
//	    log.Info("All targets succeeded")
//	}
//
//	// Calculate success rate
//	successRate := result.SuccessRate()
//	log.Info("Success rate", "rate", successRate)
type ParallelPublishResult struct {
	// Aggregate Counts

	// TotalTargets is the total number of targets attempted (enabled + healthy).
	TotalTargets int `json:"total_targets"`

	// SuccessCount is the number of targets that succeeded.
	SuccessCount int `json:"success_count"`

	// FailureCount is the number of targets that failed.
	FailureCount int `json:"failure_count"`

	// SkippedCount is the number of targets that were skipped (unhealthy, circuit breaker open, disabled).
	SkippedCount int `json:"skipped_count"`

	// Per-Target Results

	// Results contains detailed results for each target.
	Results []TargetPublishResult `json:"results"`

	// Timing

	// Duration is the total execution time for parallel publishing (not sequential).
	// This is the wall-clock time from start to finish, not the sum of individual durations.
	Duration time.Duration `json:"duration"`

	// Status

	// IsPartialSuccess indicates that some targets succeeded and some failed.
	// This is true when SuccessCount > 0 AND FailureCount > 0.
	IsPartialSuccess bool `json:"is_partial_success"`
}

// Success returns true if at least one target succeeded.
//
// This method is useful for determining if the parallel publish operation
// was successful overall, even if some targets failed.
//
// Returns:
//   - true if SuccessCount > 0
//   - false otherwise
//
// Example:
//
//	if result.Success() {
//	    log.Info("Publishing succeeded (at least one target)")
//	} else {
//	    log.Error("Publishing failed (all targets failed)")
//	}
func (r *ParallelPublishResult) Success() bool {
	return r.SuccessCount > 0
}

// AllSucceeded returns true if all targets succeeded (no failures, no skipped).
//
// This method is useful for determining if the parallel publish operation
// was completely successful with no failures or skipped targets.
//
// Returns:
//   - true if SuccessCount == TotalTargets AND FailureCount == 0 AND SkippedCount == 0
//   - false otherwise
//
// Example:
//
//	if result.AllSucceeded() {
//	    log.Info("Publishing succeeded (all targets)")
//	} else {
//	    log.Warn("Publishing had some failures or skipped targets")
//	}
func (r *ParallelPublishResult) AllSucceeded() bool {
	return r.SuccessCount == r.TotalTargets && r.FailureCount == 0 && r.SkippedCount == 0
}

// AllFailed returns true if all targets failed (no successes).
//
// This method is useful for determining if the parallel publish operation
// was a complete failure with no successful targets.
//
// Returns:
//   - true if SuccessCount == 0 AND (FailureCount > 0 OR SkippedCount > 0)
//   - false otherwise
//
// Example:
//
//	if result.AllFailed() {
//	    log.Error("Publishing failed (all targets failed or skipped)")
//	    return ErrAllTargetsFailed
//	}
func (r *ParallelPublishResult) AllFailed() bool {
	return r.SuccessCount == 0 && (r.FailureCount > 0 || r.SkippedCount > 0)
}

// SuccessRate returns the success rate as a percentage (0.0-100.0).
//
// This method calculates the success rate as:
//
//	success_rate = (SuccessCount / TotalTargets) * 100
//
// Returns:
//   - float64: Success rate percentage (0.0-100.0)
//   - 0.0 if TotalTargets == 0
//
// Example:
//
//	successRate := result.SuccessRate()
//	log.Info("Success rate", "rate", successRate)
//
//	if successRate < 80.0 {
//	    log.Warn("Low success rate", "rate", successRate)
//	}
func (r *ParallelPublishResult) SuccessRate() float64 {
	if r.TotalTargets == 0 {
		return 0.0
	}
	return (float64(r.SuccessCount) / float64(r.TotalTargets)) * 100.0
}

// TargetPublishResult represents the result of publishing to a single target.
//
// This structure contains:
//   - Target information (name, type)
//   - Publish result (success, error, duration)
//   - HTTP details (status code, if applicable)
//   - Skip details (skipped, skip reason)
//
// Example usage:
//
//	for _, targetResult := range result.Results {
//	    if targetResult.Success {
//	        log.Info("Target succeeded",
//	            "target_name", targetResult.TargetName,
//	            "duration_ms", targetResult.Duration.Milliseconds(),
//	        )
//	    } else if targetResult.Skipped {
//	        log.Warn("Target skipped",
//	            "target_name", targetResult.TargetName,
//	            "skip_reason", *targetResult.SkipReason,
//	        )
//	    } else {
//	        log.Error("Target failed",
//	            "target_name", targetResult.TargetName,
//	            "error", targetResult.Error,
//	        )
//	    }
//	}
type TargetPublishResult struct {
	// Target Info

	// TargetName is the name of the target (e.g., "rootly-prod", "pagerduty-oncall").
	TargetName string `json:"target_name"`

	// TargetType is the type of the target (rootly, pagerduty, slack, webhook, alertmanager).
	TargetType string `json:"target_type"`

	// Result

	// Success indicates whether the publish succeeded.
	Success bool `json:"success"`

	// Error contains the error details if the publish failed (nil if success or skipped).
	Error error `json:"error,omitempty"`

	// Duration is the time taken to publish to this target.
	Duration time.Duration `json:"duration"`

	// HTTP Details (optional)

	// StatusCode is the HTTP status code returned by the target (nil if not HTTP or error).
	// This is useful for debugging HTTP-based publishers (Rootly, PagerDuty, Slack, Webhook).
	StatusCode *int `json:"status_code,omitempty"`

	// Skip Details (optional)

	// Skipped indicates whether the target was skipped (not attempted).
	// Targets are skipped if:
	//   - Health status is unhealthy (3+ consecutive failures)
	//   - Circuit breaker is open (5+ consecutive failures)
	//   - Target is disabled (Enabled = false)
	Skipped bool `json:"skipped"`

	// SkipReason contains the reason for skipping the target (nil if not skipped).
	// Possible values:
	//   - "unhealthy" - Health status is unhealthy
	//   - "circuit_open" - Circuit breaker is open
	//   - "disabled" - Target is disabled
	//   - "degraded" - Health status is degraded (only if SkipUnhealthyAndDegraded strategy)
	SkipReason *string `json:"skip_reason,omitempty"`
}
