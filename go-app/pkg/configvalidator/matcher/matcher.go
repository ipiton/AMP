package matcher

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ================================================================================
// Label Matcher Parser and Validator
// ================================================================================
// Parses and validates Alertmanager label matchers (TN-151).
//
// Matcher formats:
// - label=value          (exact match)
// - label!=value         (not equal)
// - label=~regex         (regex match)
// - label!~regex         (negative regex match)
//
// Performance Target: < 1ms per matcher
// Quality Target: 150% (Grade A+ EXCEPTIONAL)
// Author: AI Assistant
// Date: 2025-11-22

// MatcherType represents the type of label matcher.
type MatcherType string

const (
	// MatchEqual is exact match (label=value)
	MatchEqual MatcherType = "="

	// MatchNotEqual is not equal (label!=value)
	MatchNotEqual MatcherType = "!="

	// MatchRegexp is regex match (label=~regex)
	MatchRegexp MatcherType = "=~"

	// MatchNotRegexp is negative regex match (label!~regex)
	MatchNotRegexp MatcherType = "!~"
)

// Matcher represents a parsed label matcher.
type Matcher struct {
	// Label is the label name
	Label string

	// Type is the matcher type (=, !=, =~, !~)
	Type MatcherType

	// Value is the match value (or regex pattern)
	Value string

	// CompiledRegex is the compiled regex (for =~ and !~ matchers)
	CompiledRegex *regexp.Regexp
}

// String returns string representation of matcher.
func (m *Matcher) String() string {
	return fmt.Sprintf("%s%s%s", m.Label, m.Type, m.Value)
}

// IsRegex returns true if matcher uses regex.
func (m *Matcher) IsRegex() bool {
	return m.Type == MatchRegexp || m.Type == MatchNotRegexp
}

// ErrorKind classifies why a matcher failed to parse, so callers can map
// errors to Alertmanager config error codes (E104 vs E105) without
// resorting to substring matching on Message.
type ErrorKind int

const (
	// ErrKindSyntax covers structural problems: empty matcher, missing
	// operator, empty label/value, or an invalid label name.
	ErrKindSyntax ErrorKind = iota

	// ErrKindRegex means the label/operator/value were well-formed but the
	// regex pattern itself failed to compile (only reachable for =~/!~).
	ErrKindRegex
)

// ParseError represents a matcher parse error.
type ParseError struct {
	Matcher    string
	Message    string
	Suggestion string
	Kind       ErrorKind
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("invalid matcher '%s': %s", e.Matcher, e.Message)
}

// matcherPattern anchors label+operator+value the same way
// business/routing.matcherExprPattern does (fix-round finding I-4): the
// label only matches identifier characters immediately after the string
// start, so an operator-looking substring embedded inside a quoted value
// (e.g. `label="a!=b"`) can never be mistaken for the real operator.
//
// This REPLACES the old strings.Index-based operator search below, which
// scanned the WHOLE matcher string and found the FIRST occurrence of !~/
// !=/=~/= anywhere in it — including inside a quoted value. A real config
// with `summary="a!=b"` used to hard-fail startup validation with a
// nonsensical "invalid label name 'summary=\"a'" (E104), while the actual
// route tree (built via business/routing.parseMatcherExpr's anchored
// regex) parsed the exact same YAML entry fine.
//
// Alternation order matters: != and =~ must be tried before the bare =,
// otherwise the single-character = alternative would win before the engine
// ever considers the longer alternative — Go's regexp/RE2 tries
// alternatives in declared order and takes the first one that leads to an
// overall match, and the trailing (.*)$ trivially matches any remainder,
// so ordering (not length) decides here. Kept in sync with
// business/routing.matcherExprPattern's ordering deliberately.
var matcherPattern = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(!=|=~|!~|=)\s*(.*)$`)

var matcherOperatorTypes = map[string]MatcherType{
	"=":  MatchEqual,
	"!=": MatchNotEqual,
	"=~": MatchRegexp,
	"!~": MatchNotRegexp,
}

// unquoteMatcherValue applies prometheus/alertmanager's pkg/labels matcher
// value grammar verbatim — ported from ParseMatcher in
// github.com/prometheus/alertmanager@v0.34.0/pkg/labels/parse.go, the
// authority for this parser and business/routing.parseMatcherExpr alike
// (alertmanager-parity wave-5 item 5, FU-PARSEARGUMENT-QUOTE-HANDLING).
//
// Before this task, Parse never stripped quotes at all: Parse(`severity="critical"`)
// returned Value == `"critical"` (quotes included literally) instead of
// `critical` — a real bug: for a regex matcher, that raw quoted value was
// fed straight into regexp.Compile below, so `severity=~"crit.*"` compiled
// a pattern that required the label value to literally contain quote
// characters — never the intent, and silently different from what the
// route tree actually matches against for the exact same YAML.
//
// fix-round finding I-3: the FIRST fix (a simplified "strip a matched
// outer quote pair, unescape only \" and \\") diverged from upstream on
// four verified points — `\n` was never unescaped; escaping was skipped
// for an unquoted value; an unescaped inner `"` was silently accepted; an
// unterminated/unmatched quote failed open. This ports upstream's actual
// ~25-line loop instead of approximating it — see
// business/routing.unquoteMatcherValue's doc comment for the full rule
// list; this is intentionally the same algorithm, kept as a separate,
// duplicated implementation (not a shared import) because pkg/ is meant to
// stay leaf-level with no internal/ dependency.
func unquoteMatcherValue(rawValue string) (string, error) {
	var expectTrailingQuote bool
	if after, hasQuote := strings.CutPrefix(rawValue, `"`); hasQuote {
		rawValue = after
		expectTrailingQuote = true
	}

	if !utf8.ValidString(rawValue) {
		return "", fmt.Errorf("value is not valid UTF-8")
	}

	var value strings.Builder
	var escaped bool
	for i := 0; i < len(rawValue); i++ {
		c := rawValue[i]

		if escaped {
			escaped = false
			switch c {
			case 'n':
				value.WriteByte('\n')
			case '"', '\\':
				value.WriteByte(c)
			default:
				// Spurious escape: keep the backslash literal.
				value.WriteByte('\\')
				value.WriteByte(c)
			}
			continue
		}

		switch c {
		case '\\':
			if i < len(rawValue)-1 {
				escaped = true
				continue
			}
			// Trailing lone backslash: literal.
			value.WriteByte('\\')
		case '"':
			if !expectTrailingQuote || i < len(rawValue)-1 {
				return "", fmt.Errorf("value contains an unescaped double quote")
			}
			expectTrailingQuote = false
		default:
			value.WriteByte(c)
		}
	}

	if expectTrailingQuote {
		return "", fmt.Errorf("value contains an unescaped double quote (unterminated quoted value)")
	}

	return value.String(), nil
}

// Parse parses a label matcher string.
//
// Supported formats:
//   - label=value          (exact match)
//   - label!=value         (not equal)
//   - label=~regex         (regex match)
//   - label!~regex         (negative regex match)
//
// A value may optionally be wrapped in double quotes, with escapes
// recognized inside AND outside them per upstream's grammar (task fu5-cfg
// item 5, FU-PARSEARGUMENT-QUOTE-HANDLING; fix-round finding I-3) — see
// unquoteMatcherValue. The operator is located via the same anchored
// `^label(op)value$` shape business/routing.parseMatcherExpr uses (fix-round
// finding I-4), so an operator-looking substring inside a quoted value
// (`label="a!=b"`) is never mistaken for the real operator.
//
// Parameters:
//   - matcher: Matcher string
//
// Returns:
//   - *Matcher: Parsed matcher
//   - error: Parse error if invalid
//
// Performance: < 1ms per matcher
//
// Examples:
//
//	Parse("severity=critical")      → {Label: "severity", Type: "=", Value: "critical"}
//	Parse(`severity="critical"`)    → {Label: "severity", Type: "=", Value: "critical"}
//	Parse("alertname!=test")        → {Label: "alertname", Type: "!=", Value: "test"}
//	Parse("instance=~.*prod.*")     → {Label: "instance", Type: "=~", Value: ".*prod.*"}
//	Parse(`summary="a!=b"`)         → {Label: "summary", Type: "=", Value: "a!=b"}
func Parse(matcher string) (*Matcher, error) {
	if matcher == "" {
		return nil, &ParseError{
			Matcher:    matcher,
			Message:    "matcher is empty",
			Suggestion: "Provide a valid matcher (e.g., label=value)",
		}
	}

	groups := matcherPattern.FindStringSubmatch(matcher)
	if groups == nil {
		return nil, &ParseError{
			Matcher:    matcher,
			Message:    "no operator found (expected =, !=, =~, or !~)",
			Suggestion: "Use format: label=value, label!=value, label=~regex, or label!~regex",
		}
	}

	label := strings.TrimSpace(groups[1])
	matchType := matcherOperatorTypes[groups[2]]
	rawValue := strings.TrimSpace(groups[3])

	// Validate label name. matcherPattern's [a-zA-Z_][a-zA-Z0-9_]* charset
	// already guarantees a non-empty, valid label whenever the regex
	// matches at all, so these two checks are now defense-in-depth rather
	// than reachable in practice — kept for a stable public error
	// contract if that charset ever changes.
	if label == "" {
		return nil, &ParseError{
			Matcher:    matcher,
			Message:    "label name is empty",
			Suggestion: "Provide a valid label name before operator",
		}
	}

	if !isValidLabelName(label) {
		return nil, &ParseError{
			Matcher:    matcher,
			Message:    fmt.Sprintf("invalid label name '%s' (must match [a-zA-Z_][a-zA-Z0-9_]*)", label),
			Suggestion: "Label names must start with letter or underscore, followed by letters, digits, or underscores",
		}
	}

	// Validate value. Checked on rawValue (before quote-stripping) so an
	// explicit empty quoted value (label="") — legitimate upstream syntax
	// for "label absent or empty" — is not rejected just because it
	// unquotes to "": only truly nothing-after-the-operator (label=) is a
	// syntax error (task fu5-cfg item 5, FU-PARSEARGUMENT-QUOTE-HANDLING).
	if rawValue == "" {
		return nil, &ParseError{
			Matcher:    matcher,
			Message:    "value is empty",
			Suggestion: "Provide a value after operator",
		}
	}
	value, err := unquoteMatcherValue(rawValue)
	if err != nil {
		return nil, &ParseError{
			Matcher:    matcher,
			Message:    err.Error(),
			Suggestion: `Check quoting: a value may be unquoted or wrapped in "double quotes"; an inner quote must be escaped as \"`,
		}
	}

	// Create matcher
	m := &Matcher{
		Label: label,
		Type:  matchType,
		Value: value,
	}

	// If regex matcher, compile and validate regex
	if m.IsRegex() {
		re, err := regexp.Compile(value)
		if err != nil {
			return nil, &ParseError{
				Matcher:    matcher,
				Message:    fmt.Sprintf("invalid regex pattern '%s': %v", value, err),
				Suggestion: "Check regex syntax. Common issues: unmatched parentheses, invalid character classes, unescaped special chars",
				Kind:       ErrKindRegex,
			}
		}
		m.CompiledRegex = re
	}

	return m, nil
}

// ParseMatchers parses multiple matcher strings.
//
// Parameters:
//   - matchers: List of matcher strings
//
// Returns:
//   - []*Matcher: List of parsed matchers
//   - []error: List of parse errors (one per invalid matcher)
//
// Note: Returns partial results - some matchers may be valid even if others fail
func ParseMatchers(matchers []string) ([]*Matcher, []error) {
	if len(matchers) == 0 {
		return nil, nil
	}

	parsed := make([]*Matcher, 0, len(matchers))
	errors := make([]error, 0)

	for _, matcherStr := range matchers {
		m, err := Parse(matcherStr)
		if err != nil {
			errors = append(errors, err)
		} else {
			parsed = append(parsed, m)
		}
	}

	return parsed, errors
}

// isValidLabelName checks if label name is valid per Prometheus conventions.
//
// Valid label names must match: [a-zA-Z_][a-zA-Z0-9_]*
//
// Rules:
// - Must start with letter (a-z, A-Z) or underscore (_)
// - Can contain letters, digits, and underscores
// - Cannot be empty
func isValidLabelName(name string) bool {
	if len(name) == 0 {
		return false
	}

	// First character must be letter or underscore
	first := name[0]
	if (first < 'a' || first > 'z') &&
		(first < 'A' || first > 'Z') &&
		first != '_' {
		return false
	}

	// Remaining characters must be letter, digit, or underscore
	for i := 1; i < len(name); i++ {
		c := name[i]
		if (c < 'a' || c > 'z') &&
			(c < 'A' || c > 'Z') &&
			(c < '0' || c > '9') &&
			c != '_' {
			return false
		}
	}

	return true
}

// ValidateLabelName validates a label name.
//
// Returns error if label name is invalid.
func ValidateLabelName(name string) error {
	if !isValidLabelName(name) {
		if len(name) == 0 {
			return fmt.Errorf("label name is empty")
		}
		return fmt.Errorf("invalid label name '%s': must match [a-zA-Z_][a-zA-Z0-9_]*", name)
	}
	return nil
}

// ValidateRegex validates a regex pattern.
//
// Returns compiled regex or error.
func ValidateRegex(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, fmt.Errorf("regex pattern is empty")
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex '%s': %v", pattern, err)
	}

	return re, nil
}
