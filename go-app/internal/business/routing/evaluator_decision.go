package routing

import (
	"time"
)

// RoutingDecision represents a complete routing decision for an alert.
//
// Includes all information needed for alert processing:
//   - Receiver (where to send)
//   - GroupBy (how to group alerts)
//   - Timers (when to send notifications)
//   - Debug info (matched route, duration, statistics)
//
// This struct is used by:
//   - Grouping System (TN-121-125) - uses GroupBy
//   - Publishing System (Phase 5) - uses Receiver
//   - Throttling Logic - uses RepeatInterval
//
// Thread Safety:
//
//	RoutingDecision is immutable after creation.
//	Safe to read from multiple goroutines.
type RoutingDecision struct {
	// Receiver is the target receiver name.
	//
	// This is the name of the receiver to publish to.
	// Example: "pagerduty", "slack", "webhook-prod"
	//
	// Must not be empty (validated by evaluator).
	Receiver string

	// GroupBy are the labels to group alerts by.
	//
	// Alerts with same GroupBy values are grouped together.
	// Empty slice means group all alerts together.
	//
	// Example: ["alertname", "cluster", "namespace"]
	//
	// Inherited from matched route (or root if no match).
	GroupBy []string

	// GroupWait is the initial delay before sending first notification.
	//
	// When a new alert group is created, wait this long before
	// sending the first notification (to collect more alerts).
	//
	// Default: 30s (from root config)
	//
	// Inherited from matched route (or root if no match).
	GroupWait time.Duration

	// GroupInterval is the delay between notifications for same group.
	//
	// After sending a notification, wait this long before sending
	// another notification for the same group (if new alerts arrive).
	//
	// Default: 5m (from root config)
	//
	// Inherited from matched route (or root if no match).
	GroupInterval time.Duration

	// RepeatInterval is the delay before re-sending notification.
	//
	// If an alert group hasn't changed, wait this long before
	// re-sending the notification (to remind about ongoing issue).
	//
	// Default: 4h (from root config)
	//
	// Inherited from matched route (or root if no match).
	RepeatInterval time.Duration

	// MatchedRoute is the path of matched route (for debugging).
	//
	// Example: "/routes[0]" or "/routes[0]/routes[1]"
	// If no match: "/ (root default)"
	//
	// Used for debugging slow routing decisions.
	MatchedRoute string

	// MatchDuration is the time taken to find matching route.
	//
	// Includes tree traversal + matcher evaluation.
	//
	// Typical: <100µs
	// Target: <50µs
	//
	// If >100µs, check tree depth and matcher performance.
	MatchDuration time.Duration

	// RoutesEvaluated is the number of routes checked.
	//
	// Used for debugging slow evaluations.
	//
	// Typical: 10-50 routes
	// If >100, consider optimizing tree structure.
	RoutesEvaluated int

	// CacheHitRate is the regex cache hit rate (0-1).
	//
	// Target: >0.90
	//
	// If <0.90, increase regex cache size or
	// check for pattern proliferation.
	CacheHitRate float64
}

// EvaluationResult represents the complete evaluation result.
//
// Includes primary decision, alternatives (if continue=true),
// and statistics for debugging.
//
// Used by EvaluateWithAlternatives() when you need all
// matching receivers or want detailed statistics.
type EvaluationResult struct {
	// Primary is the primary routing decision (first match).
	//
	// This is the main decision that should be used.
	// Never nil unless Error is set.
	Primary *RoutingDecision

	// Alternatives are additional decisions (if continue=true).
	//
	// When a matched route has continue=true, the matcher
	// continues to siblings and returns additional matches.
	//
	// Each alternative is a separate decision with its own
	// receiver and grouping parameters.
	//
	// Empty if continue=false (default) or no other matches.
	Alternatives []*RoutingDecision

	// TotalDuration is the total evaluation time.
	//
	// Includes matching + building all decisions.
	//
	// Typical: <50µs for single receiver
	// Typical: <200µs for 5 receivers
	TotalDuration time.Duration

	// RoutesEvaluated is the total routes checked.
	//
	// Same as Primary.RoutesEvaluated
	// (included here for convenience).
	RoutesEvaluated int

	// CacheHitRate is the overall cache hit rate (0-1).
	//
	// Same as Primary.CacheHitRate
	// (included here for convenience).
	CacheHitRate float64

	// Error is set if evaluation failed.
	//
	// Very rare (only on invalid config or nil tree).
	// If set, Primary may be nil.
	//
	// Possible errors:
	// - ErrEmptyTree: tree is nil or root is nil
	// - ErrNoReceiver: matched route has no receiver
	// - ErrNoMatch: no routes matched and FallbackToRoot=false
	Error error
}

// HasAlternatives returns true if there are alternative decisions.
//
// Indicates that continue=true was used and multiple routes matched.
func (r *EvaluationResult) HasAlternatives() bool {
	return len(r.Alternatives) > 0
}

// ReceiverCount returns the total number of receivers.
//
// Includes primary + alternatives.
//
// Returns:
//   - 0: If Error is set
//   - 1: Single receiver (normal case)
//   - 2+: Multi-receiver (continue=true)
func (r *EvaluationResult) ReceiverCount() int {
	if r.Error != nil || r.Primary == nil {
		return 0
	}
	return 1 + len(r.Alternatives)
}

// AllReceivers returns all receiver names (primary + alternatives).
//
// Useful for logging or publishing to all receivers.
//
// Returns empty slice if Error is set.
//
// Example:
//
//	receivers := result.AllReceivers()
//	// ["pagerduty", "slack", "webhook"]
func (r *EvaluationResult) AllReceivers() []string {
	if r.Error != nil || r.Primary == nil {
		return []string{}
	}

	receivers := make([]string, 0, 1+len(r.Alternatives))
	receivers = append(receivers, r.Primary.Receiver)

	for _, alt := range r.Alternatives {
		receivers = append(receivers, alt.Receiver)
	}

	return receivers
}
