package inhibition

import (
	"context"
	"fmt"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// InhibitionMatcher defines the interface for checking if alerts should be inhibited.
//
// An alert is inhibited when there exists a firing source alert that matches
// the inhibition rule conditions for the target alert.
//
// Thread-safety: Implementations must be safe for concurrent use.
// Performance target: <1ms per inhibition check (p99).
//
// Example usage:
//
//	matcher := inhibition.NewMatcher(cache, rules, metrics, logger)
//	result, err := matcher.ShouldInhibit(ctx, targetAlert)
//	if err != nil {
//	    log.Error("Inhibition check failed", "error", err)
//	} else if result.Matched {
//	    log.Info("Alert inhibited", "inhibitor", result.InhibitedBy.Fingerprint)
//	}
type InhibitionMatcher interface {
	// ShouldInhibit checks if the target alert should be inhibited.
	//
	// The check process:
	//  1. Get all firing alerts from cache
	//  2. For each inhibition rule:
	//     - For each firing alert (potential source):
	//       - Check if source alert matches source conditions
	//       - Check if target alert matches target conditions
	//       - Check if equal labels match between source and target
	//       - If all match → INHIBITED (return first match)
	//  3. If no match found → NOT INHIBITED
	//
	// Parameters:
	//   - ctx: context with timeout and cancellation
	//   - targetAlert: the alert to check for inhibition
	//
	// Returns:
	//   - *MatchResult: result with matched status and details
	//   - error: cache error, context error
	//
	// Performance: <1ms (p99)
	//
	// Example:
	//
	//	result, err := matcher.ShouldInhibit(ctx, targetAlert)
	//	if err != nil {
	//	    return err
	//	}
	//	if result.Matched {
	//	    log.Printf("Alert %s inhibited by %s (rule: %s)",
	//	        targetAlert.Fingerprint,
	//	        result.InhibitedBy.Fingerprint,
	//	        result.Rule.Name)
	//	}
	ShouldInhibit(ctx context.Context, targetAlert *core.Alert) (*MatchResult, error)

	// FindInhibitors returns ALL source alerts that inhibit the target alert.
	//
	// Unlike ShouldInhibit (which returns first match), this method returns
	// all matching inhibitors. Useful for debugging and analytics.
	//
	// Parameters:
	//   - ctx: context with timeout and cancellation
	//   - targetAlert: the alert to check for inhibition
	//
	// Returns:
	//   - []*MatchResult: all matching inhibitors (may be empty)
	//   - error: cache error, context error
	//
	// Example:
	//
	//	inhibitors, err := matcher.FindInhibitors(ctx, targetAlert)
	//	for _, result := range inhibitors {
	//	    log.Printf("Inhibitor: %s (rule: %s)",
	//	        result.InhibitedBy.Fingerprint, result.Rule.Name)
	//	}
	FindInhibitors(ctx context.Context, targetAlert *core.Alert) ([]*MatchResult, error)

	// MatchRule checks if a specific rule matches between source and target alerts.
	//
	// This is a pure function (no I/O) that evaluates:
	//  1. source_match: exact label matching for source alert
	//  2. source_match_re: regex label matching for source alert
	//  3. target_match: exact label matching for target alert
	//  4. target_match_re: regex label matching for target alert
	//  5. equal: labels that must have the same value in both alerts
	//
	// Parameters:
	//   - rule: the inhibition rule to evaluate
	//   - sourceAlert: the potential inhibitor (must be firing)
	//   - targetAlert: the alert to potentially inhibit
	//
	// Returns:
	//   - bool: true if rule matches (alert should be inhibited)
	//
	// Performance: <10µs per call
	//
	// Example:
	//
	//	if matcher.MatchRule(&rule, sourceAlert, targetAlert) {
	//	    log.Printf("Rule %s matches", rule.Name)
	//	}
	MatchRule(rule *InhibitionRule, sourceAlert, targetAlert *core.Alert) bool
}

// MatchResult represents the result of an inhibition check.
//
// Contains information about whether the alert was inhibited,
// which source alert caused the inhibition, and performance metrics.
type MatchResult struct {
	// Matched indicates whether the target alert is inhibited.
	// true = alert should be suppressed (inhibited)
	// false = alert should be processed normally
	Matched bool

	// InhibitedBy is the source alert that caused the inhibition.
	// Only populated if Matched == true.
	// nil if Matched == false.
	InhibitedBy *core.Alert

	// Rule is the inhibition rule that matched.
	// Only populated if Matched == true.
	// nil if Matched == false.
	Rule *InhibitionRule

	// MatchDuration is the time taken to perform the inhibition check.
	// Useful for performance monitoring and metrics.
	MatchDuration time.Duration
}

// String returns a human-readable representation of the match result.
// Useful for logging and debugging.
func (mr *MatchResult) String() string {
	if !mr.Matched {
		return "MatchResult{matched=false}"
	}

	inhibitorFP := "unknown"
	if mr.InhibitedBy != nil {
		inhibitorFP = mr.InhibitedBy.Fingerprint
	}

	ruleName := "unknown"
	if mr.Rule != nil {
		ruleName = mr.Rule.Name
	}

	return fmt.Sprintf("MatchResult{matched=true, inhibitor=%s, rule=%s, duration=%v}",
		inhibitorFP, ruleName, mr.MatchDuration)
}

// ActiveAlertCache defines the interface for caching firing alerts.
//
// This is a stub interface until TN-128 is implemented.
// The cache provides fast access to currently firing alerts for inhibition checks.
//
// Thread-safety: Implementations must be safe for concurrent use.
// Performance: <1ms for GetFiringAlerts (p99).
type ActiveAlertCache interface {
	// GetFiringAlerts returns all currently firing alerts.
	//
	// Returns:
	//   - []*core.Alert: list of firing alerts (may be empty)
	//   - error: cache error (Redis unavailable, etc.)
	GetFiringAlerts(ctx context.Context) ([]*core.Alert, error)

	// AddFiringAlert adds a firing alert to the cache.
	// Used to keep the cache up-to-date as alerts fire.
	AddFiringAlert(ctx context.Context, alert *core.Alert) error

	// RemoveAlert removes an alert from the cache.
	// Used when alerts resolve or expire.
	RemoveAlert(ctx context.Context, fingerprint string) error
}
