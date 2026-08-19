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
// combining all entries with AND, using upstream Alertmanager's exact
// absent-label semantics.
//
// Review fix round 1 (I1): the first version of this function gated `=`/
// `=~` on the label being present and short-circuited `!=`/`!~` to true
// on absence — a table copied from internal/business/routing's wave-5
// MatchesNode (see the fix to that function alongside this one; both
// diverged the same way). Upstream has NO presence check at all:
// `labels.Matchers.Matches` (pkg/labels/matcher.go:184-191) does
// `m.Matches(string(lset[name]))`, and a Go map read of a missing key
// already returns the zero value "" — so an absent label is simply
// treated as the empty string, for every operator:
//
//	=   matched if the (possibly absent-as-"") value equals Value
//	!=  matched if the (possibly absent-as-"") value differs from Value
//	=~  matched if the (possibly absent-as-"") value matches the anchored regex
//	!~  matched if the (possibly absent-as-"") value does not match the anchored regex
//
// Consequences this fixes: `job!=""` no longer matches an alert missing
// `job` (upstream: `"" != ""` is false); `foo=~".*"` now DOES match an
// alert missing `foo` (upstream: the anchored regex matches the empty
// string). Since Go's `labels[key]` already yields "" for a missing key,
// this needs no presence check at all — dropping it is both the fix and
// the simplification.
//
// An empty/nil matcher list matches vacuously (true): a rule relying only
// on the legacy match/match_re maps for a side has no matchers list to
// evaluate, and must not be rejected here.
func matchesAll(matchers []compiledMatcher, labels map[string]string) bool {
	for _, m := range matchers {
		value := labels[m.Label] // upstream semantics: absent == ""

		var matched bool
		switch m.Type {
		case matcher.MatchEqual:
			matched = value == m.Value
		case matcher.MatchNotEqual:
			matched = value != m.Value
		case matcher.MatchRegexp:
			matched = m.Regex.MatchString(value)
		case matcher.MatchNotRegexp:
			matched = !m.Regex.MatchString(value)
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

// regexCompileError names which legacy `*_match_re` map and label key
// failed to compile, so callers (parser.go) can build the same
// `rules[N].source_match_re.key`-shaped ParseError.Field they built before
// this compilation moved into a shared method.
type regexCompileError struct {
	Field   string // e.g. "source_match_re.service" or "target_match_re.severity"
	Pattern string
	Err     error
}

func (e *regexCompileError) Error() string {
	return fmt.Sprintf("%s: invalid regex %q: %v", e.Field, e.Pattern, e.Err)
}

func (e *regexCompileError) Unwrap() error { return e.Err }

// CompileLegacyRegex compiles SourceMatchRE/TargetMatchRE into anchored,
// evaluable regexes (compiledSourceRE/compiledTargetRE) — the same
// `^(?:pattern)$` anchoring upstream's `labels.NewMatcher` applies
// (matcher.go:69) and compileMatcherList already applies to the
// matchers-form list.
//
// Review fix round 1 (I3 + S1): two related bugs, fixed together because
// both live in "how are legacy *_match_re maps turned into
// compiledSourceRE/compiledTargetRE":
//
//   - I3: the pre-fix-round compilation (parser.go's compileRegexPatterns)
//     compiled the pattern UNANCHORED (`regexp.Compile(pattern)`), so
//     `target_match_re: {severity: "warning"}` also inhibited an alert
//     whose severity was "warning2" — upstream anchors this the same as
//     `=~`, and since wave 7 lets a matchers-form `=~` (anchored) and a
//     legacy `*_match_re` (previously unanchored) coexist on one rule,
//     the inconsistency was no longer just an old bug, it was two
//     different regex semantics side by side in the same rule.
//   - S1 (the implementer's own finding, confirmed by review):
//     `internal/config.ToInhibitionRules` builds InhibitionRule literals
//     for inline `inhibition.inhibit_rules` entries and used to call only
//     CompileMatchers() — compiledSourceRE/compiledTargetRE stayed nil,
//     and matchRuleFast treats a missing compiled regex as a hard
//     non-match, so every inline source_match_re/target_match_re rule was
//     a permanent, silent no-op (only ConfigFile-sourced rules, via
//     DefaultInhibitionParser, ever got compiled at all).
//
// Both are fixed by making this the ONE place *_match_re is compiled, and
// having both InhibitionRule construction paths call it (via Compile,
// below) instead of duplicating (and, in the inline path's case, omitting)
// the loop.
//
// Review fix round 2 (R5): unlike the old parser.go loop (which always
// allocated compiledSourceRE/compiledTargetRE, even to an empty map),
// this leaves them nil when the corresponding *_match_re map is empty —
// harmless today, since matchRuleFast's iteration is driven by
// SourceMatchRE/TargetMatchRE's own length (ranging over a nil map is a
// valid no-op), but worth flagging as a behavioral difference in this
// exported-ish helper for anyone reaching for "is this rule's regex
// compiled" via a nil check rather than the source map's length.
func (r *InhibitionRule) CompileLegacyRegex() error {
	if len(r.SourceMatchRE) > 0 {
		compiled := make(map[string]*regexp.Regexp, len(r.SourceMatchRE))
		for key, pattern := range r.SourceMatchRE {
			re, err := regexp.Compile(anchorMatcherRegex(pattern))
			if err != nil {
				return &regexCompileError{Field: "source_match_re." + key, Pattern: pattern, Err: err}
			}
			compiled[key] = re
		}
		r.compiledSourceRE = compiled
	}

	if len(r.TargetMatchRE) > 0 {
		compiled := make(map[string]*regexp.Regexp, len(r.TargetMatchRE))
		for key, pattern := range r.TargetMatchRE {
			re, err := regexp.Compile(anchorMatcherRegex(pattern))
			if err != nil {
				return &regexCompileError{Field: "target_match_re." + key, Pattern: pattern, Err: err}
			}
			compiled[key] = re
		}
		r.compiledTargetRE = compiled
	}

	return nil
}

// Compile is the single entry point that fully prepares a rule for
// matchRuleFast: legacy `*_match_re` regex compilation (anchored, via
// CompileLegacyRegex) plus the matchers-form list compilation (via
// CompileMatchers). Review fix round 1 (S1): both InhibitionRule
// construction paths — DefaultInhibitionParser (config_file-sourced
// rules) and internal/config.ToInhibitionRules (inline rules) — now call
// this ONE method instead of each doing (or, for the inline path, failing
// to do) their own regex compilation, closing the gap where inline legacy
// regex rules silently never matched.
func (r *InhibitionRule) Compile() error {
	if err := r.CompileLegacyRegex(); err != nil {
		return err
	}
	return r.CompileMatchers()
}
