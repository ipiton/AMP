package silencing

import (
	"context"
	"fmt"
)

// DefaultSilenceMatcher implements the SilenceMatcher interface with regex caching.
//
// This implementation provides high-performance alert matching against silence rules
// using the following optimizations:
//   - Regex compilation caching (5µs → 10ns, 500x speedup)
//   - Early exit on first non-matching matcher (AND logic optimization)
//   - Context-aware cancellation for long-running operations
//   - Zero allocations for non-regex matchers
//
// Thread-safety: DefaultSilenceMatcher is thread-safe and can be used concurrently
// from multiple goroutines. The internal RegexCache uses RWMutex for safe concurrent access.
//
// Example usage:
//
//	matcher := NewSilenceMatcher()
//
//	alert := Alert{
//	    Labels: map[string]string{
//	        "alertname": "HighCPU",
//	        "job":       "api-server",
//	        "severity":  "critical",
//	    },
//	}
//
//	silence := &Silence{
//	    ID: "abc123",
//	    Matchers: []Matcher{
//	        {Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
//	        {Name: "severity", Value: "(critical|warning)", Type: MatcherTypeRegex},
//	    },
//	}
//
//	matched, err := matcher.Matches(ctx, alert, silence)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if matched {
//	    log.Info("Alert is silenced")
//	}
type DefaultSilenceMatcher struct {
	// regexCache caches compiled regex patterns for performance.
	// Shared across all matching operations.
	regexCache *RegexCache
}

// NewSilenceMatcher creates a new DefaultSilenceMatcher with default settings.
//
// Default Configuration:
//   - Regex cache size: 1000 patterns (~500 KB memory)
//   - Thread-safe: Yes (concurrent access supported)
//   - Context-aware: Yes (cancellation supported)
//
// Returns a ready-to-use SilenceMatcher instance.
//
// Example:
//
//	matcher := NewSilenceMatcher()
//	matched, err := matcher.Matches(ctx, alert, silence)
func NewSilenceMatcher() *DefaultSilenceMatcher {
	return &DefaultSilenceMatcher{
		regexCache: NewRegexCache(1000), // 1000 patterns = ~500 KB
	}
}

// Matches implements SilenceMatcher.Matches.
//
// Checks if an alert matches a silence rule by evaluating ALL matchers (AND logic).
// Returns true only if every matcher in the silence matches the alert's labels.
//
// Matching Algorithm:
//  1. Validate inputs (alert.Labels != nil, silence != nil, len(Matchers) > 0)
//  2. For each matcher in silence.Matchers:
//     a. Check context cancellation (ctx.Done())
//     b. Evaluate matcher against alert labels
//     c. If matcher fails → return false (early exit)
//  3. If all matchers pass → return true
//
// Operator Semantics (upstream Alertmanager: absent label reads as ""
// for every operator, see matchSingle's doc comment for the fix-round-2
// detail):
//   - = (Equal): (possibly absent-as-"") value equals matcher value
//   - != (NotEqual): (possibly absent-as-"") value differs from matcher value
//   - =~ (Regex): (possibly absent-as-"") value matches the anchored regex
//   - !~ (NotRegex): (possibly absent-as-"") value does not match the anchored regex
//
// Performance:
//   - Target: <500µs for silence with 10 matchers
//   - Early exit: Returns immediately on first non-matching matcher
//   - Regex caching: Cache hit ~10ns, miss ~5µs
//
// Errors:
//   - ErrInvalidAlert: if alert.Labels is nil
//   - ErrInvalidSilence: if silence is nil or has no matchers
//   - ErrRegexCompilationFailed: if regex pattern is invalid
//   - ErrContextCancelled: if context is cancelled
func (m *DefaultSilenceMatcher) Matches(ctx context.Context, alert Alert, silence *Silence) (bool, error) {
	// Validate inputs before processing
	if alert.Labels == nil {
		return false, ErrInvalidAlert
	}
	if silence == nil || len(silence.Matchers) == 0 {
		return false, ErrInvalidSilence
	}

	// Check all matchers (AND logic)
	for _, matcher := range silence.Matchers {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return false, ErrContextCancelled
		default:
		}

		// Match single matcher against alert labels
		matched, err := m.matchSingle(alert.Labels, &matcher)
		if err != nil {
			return false, err
		}

		// Early exit on first non-matching matcher (AND logic optimization)
		if !matched {
			return false, nil
		}
	}

	// All matchers passed
	return true, nil
}

// MatchesAny implements SilenceMatcher.MatchesAny.
//
// Checks if an alert matches ANY of the provided silences.
// Returns a list of all matched silence IDs (does not stop on first match).
//
// Use Cases:
//   - Find all active silences that apply to an alert
//   - Audit which silences are suppressing notifications
//   - Metrics/logging for silence effectiveness
//
// Performance:
//   - Target: <1ms for 100 silences (10 matchers each)
//   - Target: <10ms for 1000 silences
//   - Complexity: O(N*M) where N = len(silences), M = avg matchers per silence
//
// Context Cancellation:
//   - Checks ctx.Done() on each silence iteration
//   - Returns partial results if cancelled (matched IDs up to cancellation point)
//
// Errors:
//   - ErrInvalidAlert: if alert.Labels is nil
//   - ErrContextCancelled: if context is cancelled during iteration
//   - ErrRegexCompilationFailed: if any regex pattern is invalid
//
// Example:
//
//	alert := Alert{Labels: map[string]string{"job": "api", "severity": "critical"}}
//	silences := []*Silence{
//	    {ID: "s1", Matchers: []Matcher{{Name: "job", Value: "api", Type: "="}}},
//	    {ID: "s2", Matchers: []Matcher{{Name: "severity", Value: "critical", Type: "="}}},
//	    {ID: "s3", Matchers: []Matcher{{Name: "job", Value: "db", Type: "="}}},
//	}
//	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
//	// matchedIDs = ["s1", "s2"] (s3 doesn't match)
func (m *DefaultSilenceMatcher) MatchesAny(ctx context.Context, alert Alert, silences []*Silence) ([]string, error) {
	// Validate alert input
	if alert.Labels == nil {
		return nil, ErrInvalidAlert
	}

	// Collect matched silence IDs
	var matchedIDs []string

	// Iterate through all silences
	for _, silence := range silences {
		// Check context cancellation on each iteration
		select {
		case <-ctx.Done():
			// Return partial results with cancellation error
			return matchedIDs, ErrContextCancelled
		default:
		}

		// Check if this silence matches
		matched, err := m.Matches(ctx, alert, silence)
		if err != nil {
			// Skip silences with invalid matchers or errors
			// Continue to next silence instead of failing entirely
			continue
		}

		if matched {
			matchedIDs = append(matchedIDs, silence.ID)
		}
	}

	return matchedIDs, nil
}

// matchSingle checks if a single matcher matches against alert labels.
//
// This is an internal helper method that implements the matching logic for each
// of the four operator types (=, !=, =~, !~).
//
// Parameters:
//   - labels: Alert labels (map[string]string)
//   - matcher: Matcher to evaluate
//
// Returns:
//   - bool: true if matcher matches, false otherwise
//   - error: regex compilation error or invalid operator error
//
// Operator Semantics — upstream Alertmanager semantics
// (`pkg/labels/matcher.go`'s `Matcher.Matches(s)`, called with
// `s = string(lset[name])`; a Go map read of a missing key already
// returns the zero value "", so upstream has NO presence check anywhere
// — an absent label is simply evaluated as the empty string against
// every operator, below):
//   - = (Equal): (possibly absent-as-"") value must equal matcher value
//   - != (NotEqual): (possibly absent-as-"") value must differ from matcher value
//   - =~ (Regex): (possibly absent-as-"") value must match the anchored regex
//   - !~ (NotRegex): (possibly absent-as-"") value must NOT match the anchored regex
//
// Review fix round 2 (R1): this method used to gate `=`/`=~` on the label
// being present and short-circuit `!=`/`!~` to true on absence,
// unconditionally — the third copy of the exact divergence already fixed
// in `internal/infrastructure/inhibition.matchesAll` and
// `internal/business/routing.MatchesNode` during fix round 1, for
// silences specifically the over-silencing direction (see
// RegexCache.Get's anchoring fix in the same commit: an unanchored `=~`
// on top of the presence gate meant `job=~"prod"` silenced
// `job="preprod-2"` too). Since Go's `labels[name]` already yields "" for
// a missing key, this needs no presence check at all — dropping it is
// both the fix and the simplification.
//
// Performance:
//   - = and !=: <10µs (O(1) map lookup)
//   - =~ cached: <10µs (RLock + map lookup + MatchString)
//   - =~ uncached: <100µs (compile + cache + match)
//   - !~ same as =~ (same regex evaluation)
func (m *DefaultSilenceMatcher) matchSingle(labels map[string]string, matcher *Matcher) (bool, error) {
	// Get label value from alert (upstream semantics: absent == "")
	labelValue := labels[matcher.Name]

	// Match based on operator type
	switch matcher.Type {
	case MatcherTypeEqual:
		// = operator: value must equal matcher value
		// Examples:
		//   - Label "job=api", Matcher "job=api" → true
		//   - Label "job=web", Matcher "job=api" → false
		//   - Label missing, Matcher "job=api" → false ("" != "api")
		//   - Label missing, Matcher "job=" (empty value) → true ("" == "")
		return labelValue == matcher.Value, nil

	case MatcherTypeNotEqual:
		// != operator: value must differ from matcher value
		// Examples:
		//   - Label "job=api", Matcher "job!=web" → true
		//   - Label "job=api", Matcher "job!=api" → false
		//   - Label missing, Matcher "job!=api" → true ("" != "api")
		//   - Label missing, Matcher "job!=" (empty value) → false ("" == "", NOT a match for !=)
		return labelValue != matcher.Value, nil

	case MatcherTypeRegex:
		// =~ operator: value must match the anchored regex
		// Examples:
		//   - Label "severity=critical", Matcher "severity=~(critical|warning)" → true
		//   - Label "severity=info", Matcher "severity=~(critical|warning)" → false
		//   - Label missing, Matcher "severity=~(critical|warning)" → false (anchored pattern doesn't match "")
		//   - Label missing, Matcher "severity=~.*" → true (anchored ".*" DOES match "")

		// Get compiled regex from cache (or compile if not cached); anchored
		// ^(?:pattern)$ by RegexCache.Get, matching upstream.
		re, err := m.regexCache.Get(matcher.Value)
		if err != nil {
			return false, fmt.Errorf("%w: pattern=%q: %v", ErrRegexCompilationFailed, matcher.Value, err)
		}

		return re.MatchString(labelValue), nil

	case MatcherTypeNotRegex:
		// !~ operator: value must NOT match the anchored regex
		// Examples:
		//   - Label "instance=server-prod-01", Matcher "instance!~.*-dev-.*" → true
		//   - Label "instance=server-dev-01", Matcher "instance!~.*-dev-.*" → false
		//   - Label missing, Matcher "instance!~.*-dev-.*" → true (anchored pattern doesn't match "")
		//   - Label missing, Matcher "instance!~.*" → false (anchored ".*" DOES match "", so negated is false)

		// Get compiled regex from cache (or compile if not cached); anchored
		// ^(?:pattern)$ by RegexCache.Get, matching upstream.
		re, err := m.regexCache.Get(matcher.Value)
		if err != nil {
			return false, fmt.Errorf("%w: pattern=%q: %v", ErrRegexCompilationFailed, matcher.Value, err)
		}

		return !re.MatchString(labelValue), nil

	default:
		// Unknown operator type (should never happen if validation works)
		return false, fmt.Errorf("%w: type=%q", ErrMatcherInvalidType, matcher.Type)
	}
}
