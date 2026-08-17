// Package validators implements the concrete rule checks behind
// pkg/configvalidator. Each file owns a slice of the Alertmanager config
// grammar; this file holds helpers shared across all of them so error
// shape (codes, messages, suggestions) stays consistent.
package validators

import (
	"strings"

	"github.com/ipiton/AMP/pkg/configvalidator/matcher"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
)

// docsURL is the default documentation link attached to errors that don't
// have a more specific reference.
const docsURL = "https://prometheus.io/docs/alerting/latest/configuration/"

// newError builds a types.Error with the common fields filled in.
func newError(code, section, field, message, suggestion string) types.Error {
	return types.Error{
		Type:       types.ErrorTypeSemantic,
		Code:       code,
		Message:    message,
		Location:   types.Location{Section: section, Field: field},
		Suggestion: suggestion,
		DocsURL:    docsURL,
	}
}

// newWarning builds a types.Warning with the common fields filled in.
func newWarning(code, section, field, message, suggestion string) types.Warning {
	return types.Warning{
		Type:       types.WarningTypeBestPractice,
		Code:       code,
		Message:    message,
		Location:   types.Location{Section: section, Field: field},
		Suggestion: suggestion,
		DocsURL:    docsURL,
	}
}

// isValidLabelName delegates to the matcher package's Prometheus label name
// rule ([a-zA-Z_][a-zA-Z0-9_]*), the single grammar shared by route
// matchers, group_by, and inhibition equal/match label names.
func isValidLabelName(name string) bool {
	return matcher.ValidateLabelName(name) == nil
}

// isValidRegex reports whether pattern compiles as a Go regexp, the same
// engine the matcher package and route_re/match_re compilation use.
func isValidRegex(pattern string) bool {
	_, err := matcher.ValidateRegex(pattern)
	return err == nil
}

// matcherErrorCode classifies a matcher.ParseError into E104 (bad syntax)
// vs E105 (syntax fine, regex invalid) per ERROR_CODES.md. Parse only
// reaches regex compilation after the label/operator/value are already
// well-formed, so a message mentioning "regex" at that point means the
// operator and label were fine and only the pattern itself is bad.
func matcherErrorCode(err error) string {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "regex") {
		return "E105"
	}
	return "E104"
}

// isNonNegativeDuration reports whether d (nanoseconds) is >= 0. Zero is
// treated as "unset / inherit default" for route timing fields, so it is
// not an error on its own; only genuinely negative durations (parseable
// from YAML as e.g. "-5m") are rejected.
func isNonNegativeDuration(d int64) bool { return d >= 0 }
