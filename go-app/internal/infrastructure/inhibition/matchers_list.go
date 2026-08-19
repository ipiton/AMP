package inhibition

import (
	"fmt"
	"regexp"

	"github.com/ipiton/AMP/pkg/configvalidator/matcher"
)

// compiledMatcher is one parsed+compiled entry from a source_matchers/
// target_matchers list (upstream Alertmanager's modern inhibit_rule
// syntax, recommended since v0.22).
//
// Parsing reuses pkg/configvalidator/matcher.Parse — the upstream-verbatim
// grammar port from alertmanager-parity wave 5, the same authority
// internal/business/routing.parseMatcherExpr uses for route matchers
// (FU7-A brief: "DO NOT write a third parser"). Only the regex compilation
// is redone here: matcher.Parse compiles the pattern UNANCHORED (it only
// exists there to confirm the pattern compiles at all, for
// configvalidator's syntax check), while inhibition evaluation needs the
// same anchored `^(?:pattern)$` semantics internal/business/routing uses
// (routing.anchorRegex) so `=~ "warning"` doesn't also match "warning2" —
// see anchorMatcherRegex.
type compiledMatcher struct {
	Label string
	Type  matcher.MatcherType
	Value string
	Regex *regexp.Regexp // set only for =~ / !~ (matcher.MatchRegexp / matcher.MatchNotRegexp)
}

// anchorMatcherRegex mirrors internal/business/routing.anchorRegex: a
// label value must match the WHOLE pattern, not merely contain a substring
// that matches it. Kept as a literal duplicate (not a shared helper)
// because routing's copy lives in an unrelated package and this one is
// meant to stay easy to audit against it, the same posture
// pkg/configvalidator/matcher already takes on its own unquote helper.
func anchorMatcherRegex(pattern string) string {
	return "^(?:" + pattern + ")$"
}

// compileMatcherList parses a source_matchers/target_matchers list into
// evaluable form, anchoring any regex matcher the same way
// internal/business/routing does. Returns an error identifying the
// offending entry if any fails to parse or its regex fails to compile —
// this should already have been caught by
// pkg/configvalidator/validators.InhibitionValidator (E153/E154) for a
// config that went through `amp check-config`, but a ConfigFile-sourced
// inhibition rule set (internal/infrastructure/inhibition.ParseFile)
// bypasses configvalidator entirely, so this engine validates for itself
// rather than trusting an upstream check ran.
//
// An empty/nil input returns (nil, nil): a rule with no matchers list on a
// given side relies solely on the legacy match/match_re maps for that
// side, which is a normal, fully-supported configuration.
func compileMatcherList(exprs []string) ([]compiledMatcher, error) {
	if len(exprs) == 0 {
		return nil, nil
	}

	out := make([]compiledMatcher, 0, len(exprs))
	for _, expr := range exprs {
		parsed, err := matcher.Parse(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid matcher %q: %w", expr, err)
		}

		cm := compiledMatcher{Label: parsed.Label, Type: parsed.Type, Value: parsed.Value}
		if parsed.Type == matcher.MatchRegexp || parsed.Type == matcher.MatchNotRegexp {
			re, err := regexp.Compile(anchorMatcherRegex(parsed.Value))
			if err != nil {
				return nil, fmt.Errorf("invalid regex in matcher %q: %w", expr, err)
			}
			cm.Regex = re
		}

		out = append(out, cm)
	}

	return out, nil
}

// matchesAll evaluates a compiled matcher list against a label set,
// combining all entries with AND — the same decision table
// internal/business/routing.RouteMatcher.MatchesNode uses for route
// matchers (upstream Alertmanager semantics):
//
//	=   matched only if the label exists and equals Value
//	!=  matched if the label is missing OR differs from Value
//	=~  matched only if the label exists and matches the anchored regex
//	!~  matched if the label is missing OR does not match the anchored regex
//
// An empty/nil matcher list matches vacuously (true): a rule relying only
// on the legacy match/match_re maps for a side has no matchers list to
// evaluate, and must not be rejected here.
func matchesAll(matchers []compiledMatcher, labels map[string]string) bool {
	for _, m := range matchers {
		value, exists := labels[m.Label]

		var matched bool
		switch m.Type {
		case matcher.MatchEqual:
			matched = exists && value == m.Value
		case matcher.MatchNotEqual:
			matched = !exists || value != m.Value
		case matcher.MatchRegexp:
			matched = exists && m.Regex.MatchString(value)
		case matcher.MatchNotRegexp:
			matched = !exists || !m.Regex.MatchString(value)
		}

		if !matched {
			return false
		}
	}
	return true
}

// CompileMatchers parses and compiles the rule's SourceMatchers/
// TargetMatchers list-syntax fields into the evaluable form matchRuleFast
// reads (compiledSourceMatchers/compiledTargetMatchers).
//
// Both InhibitionRule construction paths must call this before the rule
// is used for matching:
//   - DefaultInhibitionParser (parser.go's compileRegexPatterns) calls it
//     for ConfigFile-sourced rules.
//   - internal/config.InhibitionConfig.ToInhibitionRules calls it for
//     inline `inhibition.inhibit_rules` entries, which build
//     InhibitionRule literals directly rather than going through the
//     parser — the compiled fields are unexported, so a caller outside
//     this package cannot populate them any other way.
//
// A rule with neither field set is a no-op call: nil in, nil compiled
// fields out, nil error — identical to never having called it.
func (r *InhibitionRule) CompileMatchers() error {
	sourceCompiled, err := compileMatcherList(r.SourceMatchers)
	if err != nil {
		return fmt.Errorf("source_matchers: %w", err)
	}

	targetCompiled, err := compileMatcherList(r.TargetMatchers)
	if err != nil {
		return fmt.Errorf("target_matchers: %w", err)
	}

	r.compiledSourceMatchers = sourceCompiled
	r.compiledTargetMatchers = targetCompiled
	return nil
}
