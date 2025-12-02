package silencing

import (
	"context"
)

// SilenceMatcher provides functionality to check if alerts match silence rules.
//
// A silence rule consists of multiple matchers (label matching criteria).
// An alert matches a silence if and only if ALL matchers in the silence match
// the alert's labels (AND logic).
//
// Thread-safety: Implementations MUST be thread-safe and support concurrent access.
// Context: All methods MUST respect context cancellation for graceful shutdown.
//
// Example usage:
//
//	matcher := NewSilenceMatcher()
//	alert := Alert{
//	    Labels: map[string]string{
//	        "alertname": "HighCPU",
//	        "job":       "api-server",
//	        "severity":  "critical",
//	    },
//	}
//	silence := &Silence{
//	    ID: "abc123",
//	    Matchers: []Matcher{
//	        {Name: "alertname", Value: "HighCPU", Type: MatcherTypeEqual},
//	        {Name: "job", Value: "api-server", Type: MatcherTypeEqual},
//	    },
//	}
//	matched, err := matcher.Matches(ctx, alert, silence)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if matched {
//	    log.Info("Alert is silenced")
//	}
type SilenceMatcher interface {
	// Matches checks if an alert matches a silence rule.
	//
	// Matching Logic (AND):
	//   - Iterates through all matchers in the silence
	//   - Each matcher must match the alert's labels
	//   - If ANY matcher fails → returns false
	//   - If ALL matchers pass → returns true
	//
	// Operator Semantics:
	//   - = (Equal): Label exists AND value equals matcher value
	//   - != (NotEqual): Label missing OR value not equals matcher value
	//   - =~ (Regex): Label exists AND matches regex pattern
	//   - !~ (NotRegex): Label missing OR not matches regex pattern
	//
	// Performance:
	//   - Target: <500µs for silence with 10 matchers
	//   - Optimization: Early exit on first non-matching matcher
	//   - Regex caching: Compiled patterns cached for performance
	//
	// Errors:
	//   - ErrInvalidAlert: if alert.Labels is nil
	//   - ErrInvalidSilence: if silence is nil or has no matchers
	//   - ErrRegexCompilationFailed: if regex pattern is invalid
	//   - ErrContextCancelled: if context is cancelled
	//
	// Example:
	//
	//	alert := Alert{Labels: map[string]string{"job": "api", "severity": "critical"}}
	//	silence := &Silence{
	//	    Matchers: []Matcher{
	//	        {Name: "job", Value: "api", Type: "="},
	//	        {Name: "severity", Value: "(critical|warning)", Type: "=~"},
	//	    },
	//	}
	//	matched, err := matcher.Matches(ctx, alert, silence)
	//	// matched = true (both matchers pass)
	Matches(ctx context.Context, alert Alert, silence *Silence) (bool, error)

	// MatchesAny checks if an alert matches ANY of the provided silences.
	//
	// Returns a list of matched silence IDs. An empty list means no matches.
	// Does NOT stop on first match - checks all silences to return ALL matches.
	//
	// Performance:
	//   - Target: <1ms for 100 silences (10 matchers each)
	//   - Target: <10ms for 1000 silences
	//   - Complexity: O(N*M) where N = len(silences), M = avg matchers per silence
	//
	// Context Cancellation:
	//   - Checks ctx.Done() on each silence iteration
	//   - Returns partial results if cancelled (matched IDs up to cancellation point)
	//   - Returns ErrContextCancelled error
	//
	// Errors:
	//   - ErrInvalidAlert: if alert.Labels is nil
	//   - ErrContextCancelled: if context is cancelled during iteration
	//   - ErrRegexCompilationFailed: if any regex pattern is invalid
	//
	// Example:
	//
	//	alert := Alert{Labels: map[string]string{"job": "api"}}
	//	silences := []*Silence{
	//	    {ID: "s1", Matchers: []Matcher{{Name: "job", Value: "api", Type: "="}}},
	//	    {ID: "s2", Matchers: []Matcher{{Name: "job", Value: "db", Type: "="}}},
	//	}
	//	matchedIDs, err := matcher.MatchesAny(ctx, alert, silences)
	//	// matchedIDs = ["s1"] (only first silence matches)
	MatchesAny(ctx context.Context, alert Alert, silences []*Silence) ([]string, error)
}

// Alert represents an alert for matching purposes.
//
// This is a simplified representation of an alert containing only the fields
// needed for silence matching. The full Alert model may contain additional
// fields (firing time, resolved time, etc.) that are not used for matching.
//
// Fields:
//   - Labels: Alert labels used for matching (REQUIRED)
//   - Annotations: Alert annotations (OPTIONAL, not used for matching)
//
// Example:
//
//	alert := Alert{
//	    Labels: map[string]string{
//	        "alertname": "HighCPU",
//	        "job":       "api-server",
//	        "instance":  "server-01",
//	        "severity":  "critical",
//	    },
//	    Annotations: map[string]string{
//	        "summary":     "CPU usage above 90%",
//	        "description": "Server server-01 has high CPU usage",
//	    },
//	}
type Alert struct {
	// Labels contains the alert labels used for matching.
	// This field is REQUIRED and MUST NOT be nil for matching operations.
	//
	// Label names follow Prometheus naming conventions:
	//   - Valid: [a-zA-Z_][a-zA-Z0-9_]*
	//   - Common labels: alertname, job, instance, severity, namespace
	//
	// Example:
	//   Labels: map[string]string{
	//       "alertname": "HighCPU",
	//       "job":       "api-server",
	//       "severity":  "critical",
	//   }
	Labels map[string]string

	// Annotations contains alert annotations (optional).
	// Annotations are NOT used for silence matching - only Labels are matched.
	//
	// Annotations are typically used for:
	//   - Human-readable descriptions
	//   - Runbook links
	//   - Dashboard URLs
	//
	// Example:
	//   Annotations: map[string]string{
	//       "summary":     "High CPU usage detected",
	//       "description": "CPU usage is above 90% for 5 minutes",
	//       "runbook_url": "https://wiki.example.com/runbooks/high-cpu",
	//   }
	Annotations map[string]string
}
