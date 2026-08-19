package inhibition

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
)

// DefaultInhibitionMatcher is the standard implementation of InhibitionMatcher.
//
// Thread-safety: Safe for concurrent use (all operations are read-only or use thread-safe cache).
// Performance: <500µs per inhibition check (p99), <5µs per rule matching.
//
// Optimizations:
//   - Alert pre-filtering by alertname (source_match)
//   - Early exit on first mismatch
//   - Zero allocations in hot path
//   - Inlined label checking
//
// Example:
//
//	matcher := inhibition.NewMatcher(cache, rules, logger)
//	result, err := matcher.ShouldInhibit(ctx, targetAlert)
type DefaultInhibitionMatcher struct {
	cache ActiveAlertCache
	// rules is guarded by mu: read via snapshot in matching methods,
	// replaced wholesale by UpdateRules on config hot-reload (PARITY-A2).
	rules  []InhibitionRule
	mu     sync.RWMutex
	logger *slog.Logger
}

// NewMatcher creates a new InhibitionMatcher with the given configuration.
//
// Parameters:
//   - cache: cache for accessing firing alerts
//   - rules: list of inhibition rules to evaluate
//   - logger: structured logger for debugging
//
// Returns:
//   - *DefaultInhibitionMatcher: initialized matcher ready to use
//
// Example:
//
//	rules := []InhibitionRule{...}
//	matcher := inhibition.NewMatcher(cache, rules, logger)
func NewMatcher(cache ActiveAlertCache, rules []InhibitionRule, logger *slog.Logger) *DefaultInhibitionMatcher {
	if logger == nil {
		logger = slog.Default()
	}

	return &DefaultInhibitionMatcher{
		cache:  cache,
		rules:  rules,
		logger: logger,
	}
}

// ShouldInhibit implements InhibitionMatcher.ShouldInhibit.
//
// Returns the FIRST matching inhibition (early return optimization).
// For all matches, use FindInhibitors.
//
// Performance optimizations:
//   - Early exit on context cancellation
//   - Pre-filter alerts by source_match.alertname if present
//   - Skip self-inhibition check early
//   - Minimal allocations (reuse slices where possible)
func (m *DefaultInhibitionMatcher) ShouldInhibit(
	ctx context.Context,
	targetAlert *core.Alert,
) (*MatchResult, error) {
	startTime := time.Now()

	// Early exit on cancelled context (performance optimization)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Get all firing alerts from cache
	firingAlerts, err := m.cache.GetFiringAlerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get firing alerts: %w", err)
	}

	// Fast path: no firing alerts = no inhibition
	if len(firingAlerts) == 0 {
		return &MatchResult{
			Matched:       false,
			MatchDuration: time.Since(startTime),
		}, nil
	}

	// Pre-compute target fingerprint for self-inhibition check (avoid repeated string comparison)
	targetFP := targetAlert.Fingerprint

	// Snapshot rules so a concurrent UpdateRules can't race the iteration
	m.mu.RLock()
	rules := m.rules
	m.mu.RUnlock()

	// Check each rule (early exit on first match)
	for i := range rules {
		rule := &rules[i]

		// Pre-filter optimization: if rule has source_match.alertname, only check alerts with that alertname
		var candidateAlerts []*core.Alert
		if alertname, hasAlertname := rule.SourceMatch["alertname"]; hasAlertname {
			// Filter alerts by alertname (significant performance boost for large alert sets)
			candidateAlerts = make([]*core.Alert, 0, len(firingAlerts)/10) // estimate 10% match rate
			for _, alert := range firingAlerts {
				if alert.Fingerprint != targetFP && alert.Labels["alertname"] == alertname {
					candidateAlerts = append(candidateAlerts, alert)
				}
			}
		} else {
			// No alertname filter, check all firing alerts (but skip self-inhibition)
			candidateAlerts = make([]*core.Alert, 0, len(firingAlerts))
			for _, alert := range firingAlerts {
				if alert.Fingerprint != targetFP {
					candidateAlerts = append(candidateAlerts, alert)
				}
			}
		}

		// Check each candidate alert as potential source
		for _, sourceAlert := range candidateAlerts {
			// Check if rule matches (inlined hot path)
			if m.matchRuleFast(rule, sourceAlert, targetAlert) {
				duration := time.Since(startTime)

				// Only log in debug mode to avoid I/O overhead in hot path
				if m.logger != nil {
					m.logger.Info("Alert inhibited",
						"target", targetFP,
						"source", sourceAlert.Fingerprint,
						"rule", rule.Name,
						"duration", duration)
				}

				return &MatchResult{
					Matched:       true,
					InhibitedBy:   sourceAlert,
					Rule:          rule,
					MatchDuration: duration,
				}, nil
			}
		}
	}

	// No match found
	return &MatchResult{
		Matched:       false,
		MatchDuration: time.Since(startTime),
	}, nil
}

// FindInhibitors implements InhibitionMatcher.FindInhibitors.
//
// Returns ALL matching inhibitions (no early return).
//
// Performance optimizations:
//   - Pre-filter alerts by source_match.alertname if present
//   - Skip self-inhibition check early
//   - Pre-allocate results slice with estimated capacity
func (m *DefaultInhibitionMatcher) FindInhibitors(
	ctx context.Context,
	targetAlert *core.Alert,
) ([]*MatchResult, error) {
	startTime := time.Now()

	// Early exit on cancelled context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Get all firing alerts from cache
	firingAlerts, err := m.cache.GetFiringAlerts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get firing alerts: %w", err)
	}

	// Fast path: no firing alerts = no inhibitions
	if len(firingAlerts) == 0 {
		return []*MatchResult{}, nil
	}

	// Snapshot rules so a concurrent UpdateRules can't race the iteration
	m.mu.RLock()
	rules := m.rules
	m.mu.RUnlock()

	// Pre-allocate results slice (estimate: 5% of rules might match)
	results := make([]*MatchResult, 0, len(rules)/20+1)
	targetFP := targetAlert.Fingerprint

	// Check each rule (collect ALL matches, no early return)
	for i := range rules {
		rule := &rules[i]

		// Pre-filter optimization: if rule has source_match.alertname, only check alerts with that alertname
		var candidateAlerts []*core.Alert
		if alertname, hasAlertname := rule.SourceMatch["alertname"]; hasAlertname {
			candidateAlerts = make([]*core.Alert, 0, len(firingAlerts)/10)
			for _, alert := range firingAlerts {
				if alert.Fingerprint != targetFP && alert.Labels["alertname"] == alertname {
					candidateAlerts = append(candidateAlerts, alert)
				}
			}
		} else {
			candidateAlerts = make([]*core.Alert, 0, len(firingAlerts))
			for _, alert := range firingAlerts {
				if alert.Fingerprint != targetFP {
					candidateAlerts = append(candidateAlerts, alert)
				}
			}
		}

		// Check each candidate alert as potential source
		for _, sourceAlert := range candidateAlerts {
			if m.matchRuleFast(rule, sourceAlert, targetAlert) {
				results = append(results, &MatchResult{
					Matched:       true,
					InhibitedBy:   sourceAlert,
					Rule:          rule,
					MatchDuration: time.Since(startTime),
				})
			}
		}
	}

	// Only log in debug mode
	if m.logger != nil {
		m.logger.Debug("Find inhibitors complete",
			"target", targetFP,
			"inhibitors_found", len(results),
			"duration", time.Since(startTime))
	}

	return results, nil
}

// UpdateRules atomically replaces the rule set (PARITY-A2 config hot-reload).
// In-flight ShouldInhibit/FindInhibitors calls finish on their snapshot of the
// old rules; subsequent calls see the new set. The input slice is copied so
// the caller cannot mutate matcher state afterwards.
func (m *DefaultInhibitionMatcher) UpdateRules(rules []InhibitionRule) {
	snapshot := make([]InhibitionRule, len(rules))
	copy(snapshot, rules)

	m.mu.Lock()
	m.rules = snapshot
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Info("Inhibition rules updated", "rules", len(snapshot))
	}
}

// MatchRule implements InhibitionMatcher.MatchRule.
//
// Core matching logic (pure function, no I/O):
//  1. Source side: source_match AND source_match_re AND source_matchers (ruleMatchesSourceSide)
//  2. Target side: target_match AND target_match_re AND target_matchers (ruleMatchesTargetSide)
//  3. excludeTwoSidedMatch: reject a candidate that would let two alerts
//     each matching both sides mutually inhibit each other (upstream
//     inhibit.go's hasEqual guard — review fix round 1, C2)
//  4. equal labels (must have the same value — including "both absent" — in both alerts)
//
// All conditions must match (AND logic).
//
// Performance: <5µs per call, no allocations.
//
// Note: This is a public API method. Internal hot path uses matchRuleFast() for better performance.
func (m *DefaultInhibitionMatcher) MatchRule(
	rule *InhibitionRule,
	sourceAlert, targetAlert *core.Alert,
) bool {
	return m.matchRuleFast(rule, sourceAlert, targetAlert)
}

// ruleMatchesSourceSide reports whether the given label set satisfies a
// rule's SOURCE-side conditions: source_match (exact) AND source_match_re
// (regex) AND source_matchers (upstream's matchers-form list) - the three
// blocks matchRuleFast ANDs when checking the actual source alert.
// Factored out (review fix round 1, C2) so the SAME predicate can also be
// evaluated against the TARGET alert's own labels for
// excludeTwoSidedMatch below - upstream's guard needs to ask "does this
// label set ALSO qualify as a source under this rule?" regardless of
// which alert it came from.
//
// Review fix round 2 (R2): source_match/source_match_re no longer gate on
// label presence - upstream converts both legacy maps into
// labels.Matcher{Type: MatchEqual}/{Type: MatchRegexp} (inhibit.go's
// NewInhibitRule) exactly like the matchers-form, and Matcher.Matches
// reads an absent label as "" with no presence check at all, for EVERY
// form. This was fix round 1's I1 fix applied to compiledSourceMatchers
// only (matchesAll); the legacy maps here still had the old presence gate
// until now - I1 is complete in 3 of 3 tables (matchers-form,
// business/routing.MatchesNode, and this function) only as of this round.
func ruleMatchesSourceSide(rule *InhibitionRule, labels map[string]string) bool {
	for key, requiredValue := range rule.SourceMatch {
		if labels[key] != requiredValue { // upstream semantics: absent == ""
			return false
		}
	}

	for key := range rule.SourceMatchRE {
		actualValue := labels[key] // upstream semantics: absent == ""
		// hasRE is false only for a rule that was never run through
		// InhibitionRule.Compile()/CompileLegacyRegex() — the sole legal
		// construction path for a matching-ready rule (review fix round 3,
		// R8). Both real construction sites (DefaultInhibitionParser and
		// internal/config.ToInhibitionRules) always call Compile(); this
		// silently fails closed (no match) rather than panicking on a nil
		// map read, which matters for hand-built InhibitionRule literals in
		// tests that skip Compile() on purpose (e.g. to exercise a
		// not-yet-compiled rule) - a hard error here would turn "forgot to
		// compile" into a crash instead of a loud "why doesn't this match"
		// during development. A future third construction path that also
		// skips Compile() would reproduce S1 (fix round 1) silently; there
		// is deliberately no defensive panic/error for that today.
		re, hasRE := rule.compiledSourceRE[key]
		if !hasRE || !re.MatchString(actualValue) {
			return false
		}
	}

	return matchesAll(rule.compiledSourceMatchers, labels)
}

// ruleMatchesTargetSide is ruleMatchesSourceSide's target-side twin:
// target_match AND target_match_re AND target_matchers. Same fix round 2
// (R2) absent-label fix applies here.
func ruleMatchesTargetSide(rule *InhibitionRule, labels map[string]string) bool {
	for key, requiredValue := range rule.TargetMatch {
		if labels[key] != requiredValue { // upstream semantics: absent == ""
			return false
		}
	}

	for key := range rule.TargetMatchRE {
		actualValue := labels[key] // upstream semantics: absent == ""
		re, hasRE := rule.compiledTargetRE[key] // hasRE convention: see ruleMatchesSourceSide's twin guard above (R8)
		if !hasRE || !re.MatchString(actualValue) {
			return false
		}
	}

	return matchesAll(rule.compiledTargetMatchers, labels)
}

// matchRuleFast is an optimized version of MatchRule for internal hot path usage.
//
// Performance: <2µs per call (hot path optimized) — a handful of small
// map lookups per side, no allocations.
//
//go:inline
func (m *DefaultInhibitionMatcher) matchRuleFast(
	rule *InhibitionRule,
	sourceAlert, targetAlert *core.Alert,
) bool {
	// 1-2b. source_match / source_match_re / source_matchers (all AND'd) —
	// candidate source alert must satisfy the rule's source side.
	if !ruleMatchesSourceSide(rule, sourceAlert.Labels) {
		return false
	}

	// 3-4b. target_match / target_match_re / target_matchers (all AND'd) —
	// the alert under evaluation must satisfy the rule's target side.
	if !ruleMatchesTargetSide(rule, targetAlert.Labels) {
		return false
	}

	// 5. excludeTwoSidedMatch (review fix round 1, C2) — ports upstream
	// inhibit.go's Mutes/hasEqual guard (inhibit.go:206-218, 411-418):
	//
	//	if inhibitedByFP, eq := r.hasEqual(lset, r.SourceMatchers.Matches(lset), now); eq {
	//	...
	//	if excludeTwoSidedMatch && r.TargetMatchers.Matches(equal.Labels) {
	//		return model.Fingerprint(0), false
	//	}
	//
	// Without it, two distinct alerts that each satisfy BOTH sides of a
	// rule (e.g. source_matchers `severity="critical"` + target_matchers
	// `severity=~"critical|warning"`, two alerts both severity=critical
	// sharing the equal label) mutually inhibit each other — every such
	// alert vanishes with only an INFO log line as a trace, which is
	// exactly the "suppression rule silently kills alerting" class the
	// parity epic's final review flagged. Upstream instead: if the TARGET
	// alert would also qualify as a SOURCE under this rule, any candidate
	// source that would also qualify as a TARGET is disregarded — so
	// neither A nor B inhibits the other, matching upstream exactly.
	if ruleMatchesSourceSide(rule, targetAlert.Labels) && ruleMatchesTargetSide(rule, sourceAlert.Labels) {
		return false
	}

	// 6. Check equal labels (must match between source and target).
	//
	// Review fix round 1 (I2): a label absent on BOTH alerts now counts
	// as equal, matching upstream's fingerprintEquals (inhibit.go:338-344,
	// `equalSet[n] = lset[n]` — a Go map read of a missing key is "",
	// hashed identically for both sides). The pre-fix version required
	// `sourceOk && targetOk`, so "neither alert carries this label" was
	// treated as UNEQUAL and blocked the rule — upstream treats it as a
	// match. Reading with the map's own zero-value default (dropping the
	// ", ok" form entirely) is both the fix and the simplification.
	for _, labelName := range rule.Equal {
		if sourceAlert.Labels[labelName] != targetAlert.Labels[labelName] {
			return false
		}
	}

	// All conditions matched
	return true
}
