package routing

import "time"

// MultiReceiverResult represents the result of multi-receiver publishing.
//
// Includes aggregate statistics and per-receiver details.
//
// Thread Safety:
//
//	Immutable after creation.
//	Safe to read from multiple goroutines.
type MultiReceiverResult struct {
	// TotalReceivers is the number of receivers (primary + alternatives)
	TotalReceivers int

	// SuccessCount is the number of successful publishes
	SuccessCount int

	// FailureCount is the number of failed publishes
	FailureCount int

	// Results are per-receiver results
	//
	// Ordered as: [Primary, Alternative1, Alternative2, ...]
	// Never nil (empty slice if no receivers)
	Results []*ReceiverResult

	// TotalDuration is the max receiver duration (parallel)
	//
	// Total duration = max(receiver durations), not sum.
	// This is the wall-clock time from start to all complete.
	TotalDuration time.Duration
}

// ReceiverResult represents a single receiver's result.
type ReceiverResult struct {
	// Receiver is the receiver name (e.g., "pagerduty")
	Receiver string

	// Success indicates if publish succeeded
	Success bool

	// Duration is the time taken to publish
	Duration time.Duration

	// Error is the error (if failed)
	// nil if Success=true
	Error error
}

// IsFullSuccess returns true if all receivers succeeded.
//
// Example:
//
//	if result.IsFullSuccess() {
//	    log.Info("all receivers succeeded")
//	}
func (r *MultiReceiverResult) IsFullSuccess() bool {
	return r.FailureCount == 0 && r.TotalReceivers > 0
}

// IsPartialSuccess returns true if at least one receiver succeeded
// and at least one failed.
//
// Example:
//
//	if result.IsPartialSuccess() {
//	    log.Warn("partial success",
//	        "failed", result.FailedReceivers())
//	}
func (r *MultiReceiverResult) IsPartialSuccess() bool {
	return r.SuccessCount > 0 && r.FailureCount > 0
}

// FailedReceivers returns names of failed receivers.
//
// Returns empty slice if all succeeded.
//
// Example:
//
//	if result.FailureCount > 0 {
//	    failed := result.FailedReceivers()
//	    log.Warn("some receivers failed", "failed", failed)
//	}
func (r *MultiReceiverResult) FailedReceivers() []string {
	if r.FailureCount == 0 {
		return []string{}
	}

	failed := make([]string, 0, r.FailureCount)
	for _, result := range r.Results {
		if !result.Success {
			failed = append(failed, result.Receiver)
		}
	}
	return failed
}

// SuccessfulReceivers returns names of successful receivers.
//
// Returns empty slice if all failed.
//
// Example:
//
//	if result.SuccessCount > 0 {
//	    successful := result.SuccessfulReceivers()
//	    log.Info("successful receivers", "successful", successful)
//	}
func (r *MultiReceiverResult) SuccessfulReceivers() []string {
	if r.SuccessCount == 0 {
		return []string{}
	}

	successful := make([]string, 0, r.SuccessCount)
	for _, result := range r.Results {
		if result.Success {
			successful = append(successful, result.Receiver)
		}
	}
	return successful
}
