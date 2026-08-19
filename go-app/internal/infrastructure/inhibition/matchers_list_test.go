package inhibition

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// --- compileMatcherList / matchesAll unit tests -----------------------------

func TestCompileMatcherList_Empty(t *testing.T) {
	compiled, err := compileMatcherList(nil)
	require.NoError(t, err)
	assert.Nil(t, compiled)

	compiled, err = compileMatcherList([]string{})
	require.NoError(t, err)
	assert.Nil(t, compiled)
}

func TestCompileMatcherList_InvalidSyntax(t *testing.T) {
	_, err := compileMatcherList([]string{"not-a-matcher"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-matcher")
}

func TestCompileMatcherList_InvalidRegex(t *testing.T) {
	_, err := compileMatcherList([]string{`severity=~"("`})
	require.Error(t, err)
}

func TestMatchesAll_AllFourOperators(t *testing.T) {
	compiled, err := compileMatcherList([]string{
		`severity="critical"`,
		`region!="eu"`,
		`service=~"api.*"`,
		`env!~"^dev.*$"`,
	})
	require.NoError(t, err)
	require.Len(t, compiled, 4)

	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{
			name: "all satisfied",
			labels: map[string]string{
				"severity": "critical",
				"region":   "us",
				"service":  "api-gateway",
				"env":      "prod",
			},
			want: true,
		},
		{
			name: "= fails on wrong value",
			labels: map[string]string{
				"severity": "warning",
				"region":   "us",
				"service":  "api-gateway",
				"env":      "prod",
			},
			want: false,
		},
		{
			name: "!= fails when equal",
			labels: map[string]string{
				"severity": "critical",
				"region":   "eu",
				"service":  "api-gateway",
				"env":      "prod",
			},
			want: false,
		},
		{
			name: "!= passes when label missing and Value is non-empty (upstream: \"\" != \"eu\")",
			labels: map[string]string{
				"severity": "critical",
				"service":  "api-gateway",
				"env":      "prod",
			},
			want: true,
		},
		{
			name: "=~ fails on missing label",
			labels: map[string]string{
				"severity": "critical",
				"region":   "us",
				"env":      "prod",
			},
			want: false,
		},
		{
			name: "=~ fails on substring-only match (anchored)",
			labels: map[string]string{
				"severity": "critical",
				"region":   "us",
				"service":  "my-api-gateway", // contains "api" but doesn't match ^api.*$
				"env":      "prod",
			},
			want: false,
		},
		{
			name: "!~ passes when label missing and pattern doesn't match \"\" (upstream: !re.MatchString(\"\"))",
			labels: map[string]string{
				"severity": "critical",
				"region":   "us",
				"service":  "api-gateway",
			},
			want: true,
		},
		{
			name: "!~ fails when regex matches",
			labels: map[string]string{
				"severity": "critical",
				"region":   "us",
				"service":  "api-gateway",
				"env":      "dev-1",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, matchesAll(compiled, tt.labels))
		})
	}
}

func TestMatchesAll_EmptyListIsVacuouslyTrue(t *testing.T) {
	assert.True(t, matchesAll(nil, map[string]string{"a": "b"}))
	assert.True(t, matchesAll([]compiledMatcher{}, nil))
}

// TestMatchesAll_AbsentLabelUpstreamSemantics is the review fix-round-1
// (I1) table test: upstream Alertmanager's Matchers.Matches has NO
// presence check at all (pkg/labels/matcher.go:184-191 reads
// `lset[name]`, which is "" for an absent Go map key), so an absent label
// is evaluated as the empty string against every operator — never
// short-circuited by existence the way the pre-fix-round matchesAll did.
// Each row pins one operator against a label that is entirely absent
// from the input map, both for an operand that is itself empty (the
// sharpest edge, where the pre-fix table diverged) and a typical
// non-empty operand (where both tables happen to agree, so a regression
// back to the exists-gated version would only be caught by the
// empty-operand rows).
func TestMatchesAll_AbsentLabelUpstreamSemantics(t *testing.T) {
	tests := []struct {
		name    string
		exprs   []string
		want    bool
		explain string
	}{
		{
			name:    `job!="" on absent label`,
			exprs:   []string{`job!=""`},
			want:    false,
			explain: `upstream: "" != "" is false -> NOT matched (the pre-fix version returned true)`,
		},
		{
			name:    `foo=~".*" on absent label`,
			exprs:   []string{`foo=~".*"`},
			want:    true,
			explain: `upstream: anchored ".*" matches "" -> matched (the pre-fix version returned false)`,
		},
		{
			name:    `foo="" on absent label`,
			exprs:   []string{`foo=""`},
			want:    true,
			explain: `upstream: "" == "" -> matched (the pre-fix version returned false)`,
		},
		{
			name:    `foo!~".*" on absent label`,
			exprs:   []string{`foo!~".*"`},
			want:    false,
			explain: `upstream: anchored ".*" matches "" so negated is false -> NOT matched (the pre-fix version returned true)`,
		},
		{
			name:    `foo="bar" on absent label (non-empty operand, tables agree)`,
			exprs:   []string{`foo="bar"`},
			want:    false,
		},
		{
			name:    `foo!="bar" on absent label (non-empty operand, tables agree)`,
			exprs:   []string{`foo!="bar"`},
			want:    true,
		},
		{
			name:    `foo=~"bar" on absent label (non-empty operand, tables agree)`,
			exprs:   []string{`foo=~"bar"`},
			want:    false,
		},
		{
			name:    `foo!~"bar" on absent label (non-empty operand, tables agree)`,
			exprs:   []string{`foo!~"bar"`},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := compileMatcherList(tt.exprs)
			require.NoError(t, err)
			got := matchesAll(compiled, map[string]string{"unrelated": "value"}) // "foo"/"job" absent
			assert.Equal(t, tt.want, got, tt.explain)
		})
	}
}

func TestMatchesAll_RegexAnchoring(t *testing.T) {
	// "warning" must not also match "warning2" or "not-warning" — anchored
	// full-string match, same as internal/business/routing.anchorRegex.
	compiled, err := compileMatcherList([]string{`severity=~"warning"`})
	require.NoError(t, err)

	assert.True(t, matchesAll(compiled, map[string]string{"severity": "warning"}))
	assert.False(t, matchesAll(compiled, map[string]string{"severity": "warning2"}))
	assert.False(t, matchesAll(compiled, map[string]string{"severity": "not-warning"}))
}

// --- Review fix round 1: CompileLegacyRegex / Compile (I3 + S1) -----------

// TestCompileLegacyRegex_Anchored is the I3 regression test: legacy
// *_match_re patterns must be anchored ^(?:pattern)$ the same as the
// matchers-form =~/!~ and upstream's own labels.NewMatcher, not evaluated
// as a raw substring search.
func TestCompileLegacyRegex_Anchored(t *testing.T) {
	rule := InhibitionRule{
		SourceMatch:   map[string]string{"alertname": "NodeDown"},
		TargetMatchRE: map[string]string{"severity": "warning"},
	}
	require.NoError(t, rule.CompileLegacyRegex())

	cache := &mockCache{firingAlerts: []*core.Alert{
		{Fingerprint: "src", Labels: map[string]string{"alertname": "NodeDown"}},
	}}
	m := NewMatcher(cache, []InhibitionRule{rule}, nil)

	// Exact "warning": matched.
	res, err := m.ShouldInhibit(context.Background(), &core.Alert{
		Fingerprint: "tgt-exact", Labels: map[string]string{"severity": "warning"},
	})
	require.NoError(t, err)
	assert.True(t, res.Matched, "anchored target_match_re must match the exact value")

	// "warning2": anchoring must reject the substring match a raw
	// regexp.Compile("warning") would have allowed before this fix.
	res, err = m.ShouldInhibit(context.Background(), &core.Alert{
		Fingerprint: "tgt-substring", Labels: map[string]string{"severity": "warning2"},
	})
	require.NoError(t, err)
	assert.False(t, res.Matched, "anchored target_match_re must NOT match \"warning2\"")
}

// TestCompileLegacyRegex_NoOpWhenUnset mirrors
// TestInhibitionRule_CompileMatchers_NoOpWhenUnset for the legacy side.
func TestCompileLegacyRegex_NoOpWhenUnset(t *testing.T) {
	rule := InhibitionRule{}
	require.NoError(t, rule.CompileLegacyRegex())
	assert.Nil(t, rule.compiledSourceRE)
	assert.Nil(t, rule.compiledTargetRE)
}

func TestCompileLegacyRegex_InvalidPatternNamesSideAndKey(t *testing.T) {
	rule := InhibitionRule{SourceMatchRE: map[string]string{"service": "("}}
	err := rule.CompileLegacyRegex()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source_match_re.service")

	rule = InhibitionRule{TargetMatchRE: map[string]string{"service": "("}}
	err = rule.CompileLegacyRegex()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target_match_re.service")
}

// TestInhibitionRule_Compile_InlineLegacyRegexRuleActuallyInhibits is the
// S1 regression test: before the fix round, InhibitionRule.CompileMatchers
// (called by internal/config.ToInhibitionRules for inline rules) compiled
// ONLY the matchers-form list — compiledSourceRE/compiledTargetRE stayed
// nil for an inline rule, and matchRuleFast treats a missing compiled
// regex as a hard non-match, so an inline source_match_re/target_match_re
// rule was a permanent, silent no-op. Compile() (which ToInhibitionRules
// now calls) must actually wire it up.
func TestInhibitionRule_Compile_InlineLegacyRegexRuleActuallyInhibits(t *testing.T) {
	rule := InhibitionRule{
		Name:          "inline-legacy-regex",
		SourceMatchRE: map[string]string{"alertname": "Node.*"},
		TargetMatch:   map[string]string{"alertname": "InstanceDown"},
		Equal:         []string{"cluster"},
	}
	require.NoError(t, rule.Compile())
	require.NotNil(t, rule.compiledSourceRE, "Compile must populate compiledSourceRE for an inline rule")

	cache := &mockCache{firingAlerts: []*core.Alert{
		{Fingerprint: "src", Labels: map[string]string{"alertname": "NodeDown", "cluster": "a"}},
	}}
	m := NewMatcher(cache, []InhibitionRule{rule}, nil)

	res, err := m.ShouldInhibit(context.Background(), &core.Alert{
		Fingerprint: "tgt", Labels: map[string]string{"alertname": "InstanceDown", "cluster": "a"},
	})
	require.NoError(t, err)
	assert.True(t, res.Matched, "an inline source_match_re rule must actually inhibit once compiled via Compile()")
}

// --- InhibitionRule.CompileMatchers ----------------------------------------

func TestInhibitionRule_CompileMatchers_Wiring(t *testing.T) {
	rule := InhibitionRule{
		SourceMatchers: []string{`alertname="NodeDown"`},
		TargetMatchers: []string{`severity=~"warning|info"`},
	}
	require.NoError(t, rule.CompileMatchers())
	assert.Len(t, rule.compiledSourceMatchers, 1)
	assert.Len(t, rule.compiledTargetMatchers, 1)
}

func TestInhibitionRule_CompileMatchers_ErrorNamesSide(t *testing.T) {
	rule := InhibitionRule{SourceMatchers: []string{"bad matcher"}}
	err := rule.CompileMatchers()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source_matchers")

	rule = InhibitionRule{TargetMatchers: []string{"bad matcher"}}
	err = rule.CompileMatchers()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target_matchers")
}

func TestInhibitionRule_CompileMatchers_NoOpWhenUnset(t *testing.T) {
	rule := InhibitionRule{}
	require.NoError(t, rule.CompileMatchers())
	assert.Nil(t, rule.compiledSourceMatchers)
	assert.Nil(t, rule.compiledTargetMatchers)
}

// --- End-to-end: matchers-form rule via DefaultInhibitionMatcher ----------

func matchersFormAlert(name, severity, cluster string) *core.Alert {
	return &core.Alert{
		AlertName:   name,
		Fingerprint: "fp-" + name + "-" + cluster,
		Labels: map[string]string{
			"alertname": name,
			"severity":  severity,
			"cluster":   cluster,
		},
		Status: "firing",
	}
}

// TestShouldInhibit_MatchersFormOnly proves a rule using ONLY
// source_matchers/target_matchers (upstream's modern syntax, previously
// refused/ignored by the runtime loader) inhibits exactly like the legacy
// map form once compiled.
func TestShouldInhibit_MatchersFormOnly(t *testing.T) {
	source := matchersFormAlert("NodeDown", "critical", "prod")
	target := matchersFormAlert("InstanceDown", "warning", "prod")

	rule := InhibitionRule{
		Name:           "matchers-form",
		SourceMatchers: []string{`severity="critical"`},
		TargetMatchers: []string{`severity=~"warning|info"`},
		Equal:          []string{"cluster"},
	}
	require.NoError(t, rule.CompileMatchers())

	cache := &mockCache{firingAlerts: []*core.Alert{source}}
	m := NewMatcher(cache, []InhibitionRule{rule}, nil)

	result, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, result.Matched)
	assert.Equal(t, source.Fingerprint, result.InhibitedBy.Fingerprint)
}

// TestShouldInhibit_MatchersFormRegexAnchoredAndNegation exercises !=/!~ in
// the matchers-form and confirms anchoring (the target's severity "infoX"
// must NOT match =~"info").
func TestShouldInhibit_MatchersFormRegexAnchoredAndNegation(t *testing.T) {
	rule := InhibitionRule{
		Name:           "negation-and-anchoring",
		SourceMatchers: []string{`alertname="NodeDown"`, `env!="dev"`},
		TargetMatchers: []string{`severity=~"info"`},
		Equal:          []string{"cluster"},
	}
	require.NoError(t, rule.CompileMatchers())

	cache := &mockCache{}
	m := NewMatcher(cache, []InhibitionRule{rule}, nil)

	source := &core.Alert{
		Fingerprint: "src",
		Labels:      map[string]string{"alertname": "NodeDown", "env": "prod", "cluster": "a"},
	}

	// Exact "info" target: matched.
	targetExact := &core.Alert{
		Fingerprint: "tgt-exact",
		Labels:      map[string]string{"severity": "info", "cluster": "a"},
	}
	cache.firingAlerts = []*core.Alert{source}
	res, err := m.ShouldInhibit(context.Background(), targetExact)
	require.NoError(t, err)
	assert.True(t, res.Matched, "anchored =~\"info\" must match the exact value")

	// "infoX" target: anchoring must reject substring match.
	targetSubstring := &core.Alert{
		Fingerprint: "tgt-substring",
		Labels:      map[string]string{"severity": "infoX", "cluster": "a"},
	}
	res, err = m.ShouldInhibit(context.Background(), targetSubstring)
	require.NoError(t, err)
	assert.False(t, res.Matched, "anchored =~\"info\" must not match \"infoX\"")

	// Source in "dev" env: env!="dev" must reject the rule.
	sourceDev := &core.Alert{
		Fingerprint: "src-dev",
		Labels:      map[string]string{"alertname": "NodeDown", "env": "dev", "cluster": "a"},
	}
	cache.firingAlerts = []*core.Alert{sourceDev}
	res, err = m.ShouldInhibit(context.Background(), targetExact)
	require.NoError(t, err)
	assert.False(t, res.Matched, "env!=\"dev\" must reject a dev-env source")
}

// TestShouldInhibit_MixedRuleMatchersANDLegacy proves a rule combining the
// matchers-form and the legacy match/match_re maps on the SAME side
// combines them with AND, matching upstream (both forms allowed on one
// rule).
func TestShouldInhibit_MixedRuleMatchersANDLegacy(t *testing.T) {
	rule := InhibitionRule{
		Name:           "mixed",
		SourceMatch:    map[string]string{"alertname": "NodeDown"},
		SourceMatchers: []string{`severity="critical"`}, // additional AND condition
		TargetMatch:    map[string]string{"alertname": "InstanceDown"},
		Equal:          []string{"cluster"},
	}
	require.NoError(t, rule.CompileMatchers())

	cache := &mockCache{}
	m := NewMatcher(cache, []InhibitionRule{rule}, nil)
	target := &core.Alert{
		Fingerprint: "tgt",
		Labels:      map[string]string{"alertname": "InstanceDown", "cluster": "a"},
	}

	// Satisfies source_match (alertname) but NOT source_matchers (severity) -> no inhibit.
	sourceWrongSeverity := &core.Alert{
		Fingerprint: "src-warn",
		Labels:      map[string]string{"alertname": "NodeDown", "severity": "warning", "cluster": "a"},
	}
	cache.firingAlerts = []*core.Alert{sourceWrongSeverity}
	res, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.False(t, res.Matched, "mixed rule must AND source_match with source_matchers")

	// Satisfies both -> inhibit.
	sourceBoth := &core.Alert{
		Fingerprint: "src-crit",
		Labels:      map[string]string{"alertname": "NodeDown", "severity": "critical", "cluster": "a"},
	}
	cache.firingAlerts = []*core.Alert{sourceBoth}
	res, err = m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, res.Matched, "mixed rule must inhibit once both forms are satisfied")
}

// TestShouldInhibit_LegacyOnlyRegression pins that a rule using only the
// legacy map form still works exactly as before wave 7 - no matchers-form
// fields present, compiledSourceMatchers/compiledTargetMatchers therefore
// nil (CompileMatchers on an empty list is a no-op).
func TestShouldInhibit_LegacyOnlyRegression(t *testing.T) {
	rule := createTestRule("legacy-only")
	require.NoError(t, rule.CompileMatchers())
	assert.Nil(t, rule.compiledSourceMatchers)
	assert.Nil(t, rule.compiledTargetMatchers)

	source := createTestAlert("NodeDown", "critical", "node1", "prod")
	target := createTestAlert("InstanceDown", "warning", "node1", "prod")
	cache := &mockCache{firingAlerts: []*core.Alert{source}}
	m := NewMatcher(cache, []InhibitionRule{rule}, nil)

	res, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, res.Matched)
}

// TestUpdateRules_HotReload_MatchersForm proves a matchers-form rule
// applies on hot-reload exactly like a legacy one (PARITY-A2 + wave 7).
func TestUpdateRules_HotReload_MatchersForm(t *testing.T) {
	source := &core.Alert{
		Fingerprint: "src",
		Labels:      map[string]string{"alertname": "NodeDown", "cluster": "a"},
	}
	target := &core.Alert{
		Fingerprint: "tgt",
		Labels:      map[string]string{"alertname": "HighLatency", "cluster": "a"},
	}
	cache := &staticAlertCache{alerts: []*core.Alert{source}}

	m := NewMatcher(cache, nil, nil)
	res, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.False(t, res.Matched)

	rule := InhibitionRule{
		Name:           "matchers-form-reload",
		SourceMatchers: []string{`alertname="NodeDown"`},
		TargetMatchers: []string{`alertname="HighLatency"`},
		Equal:          []string{"cluster"},
	}
	require.NoError(t, rule.CompileMatchers())

	m.UpdateRules([]InhibitionRule{rule})
	res, err = m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, res.Matched, "matchers-form rule must apply after UpdateRules, same as legacy")

	m.UpdateRules(nil)
	res, err = m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.False(t, res.Matched)
}

// --- Parser-level: source_matchers/target_matchers round-trip via YAML ---

func TestParser_ParseString_MatchersFormRule(t *testing.T) {
	yamlData := `
inhibit_rules:
  - name: upstream-syntax
    source_matchers:
      - alertname="ClusterDown"
    target_matchers:
      - severity=~"warning|info"
    equal:
      - cluster
`
	parser := NewParser()
	cfg, err := parser.ParseString(yamlData)
	require.NoError(t, err)
	require.Len(t, cfg.Rules, 1)

	rule := cfg.Rules[0]
	assert.Equal(t, []string{`alertname="ClusterDown"`}, rule.SourceMatchers)
	assert.Equal(t, []string{`severity=~"warning|info"`}, rule.TargetMatchers)
	require.Len(t, rule.compiledSourceMatchers, 1, "parser must compile the matchers list")
	require.Len(t, rule.compiledTargetMatchers, 1)

	// End-to-end: the parsed+compiled rule actually inhibits.
	source := &core.Alert{Fingerprint: "src", Labels: map[string]string{"alertname": "ClusterDown", "cluster": "a"}}
	target := &core.Alert{Fingerprint: "tgt", Labels: map[string]string{"severity": "warning", "cluster": "a"}}
	cache := &mockCache{firingAlerts: []*core.Alert{source}}
	m := NewMatcher(cache, cfg.Rules, nil)

	res, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, res.Matched)
}

func TestParser_ParseString_MatchersFormAlonePassesSemanticValidation(t *testing.T) {
	// Previously refused by validateSemantics ("at least one of
	// source_match or source_match_re required") even though the rule is
	// structurally complete via the matchers-form alone.
	yamlData := `
inhibit_rules:
  - source_matchers:
      - alertname="ClusterDown"
    target_matchers:
      - severity="warning"
`
	parser := NewParser()
	_, err := parser.ParseString(yamlData)
	require.NoError(t, err)
}

func TestParser_ParseString_InvalidMatcherSyntaxFails(t *testing.T) {
	yamlData := `
inhibit_rules:
  - source_matchers:
      - "not-a-valid-matcher"
    target_match:
      severity: warning
`
	parser := NewParser()
	_, err := parser.ParseString(yamlData)
	require.Error(t, err)
}

// TestInhibitionRule_Validate_MatchersFormSatisfiesRequiredOneOf pins
// InhibitionRule.Validate()'s wave-7 acceptance of the matchers-form alone.
func TestInhibitionRule_Validate_MatchersFormSatisfiesRequiredOneOf(t *testing.T) {
	rule := InhibitionRule{
		SourceMatchers: []string{`alertname="ClusterDown"`},
		TargetMatchers: []string{`severity="warning"`},
	}
	assert.NoError(t, rule.Validate())
}
