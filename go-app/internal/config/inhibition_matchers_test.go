package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

// Wave 7 (FU-INHIBIT-MATCHERS): source_matchers/target_matchers (upstream's
// modern `matchers:` list syntax) used to be captured on InhibitionRuleConfig
// only so ToInhibitionRules could log a loud per-rule Error naming them as
// unimplemented (final review finding 10) — the inhibition engine had no
// runtime support for them, so a rule using only this syntax inhibited
// nothing. These tests pin the fixed contract: the fields are compiled and
// actually inhibit, and a genuinely malformed matcher fails config loading
// instead of degrading to a no-op rule.

// staticAlertCache is a minimal inhibition.ActiveAlertCache for end-to-end
// assertions in this package (mirrors the same-named helper in
// internal/infrastructure/inhibition's own tests, duplicated here rather
// than exported since it's test-only plumbing).
type staticAlertCache struct {
	alerts []*core.Alert
}

func (c *staticAlertCache) GetFiringAlerts(_ context.Context) ([]*core.Alert, error) {
	return c.alerts, nil
}
func (c *staticAlertCache) AddFiringAlert(_ context.Context, _ *core.Alert) error { return nil }
func (c *staticAlertCache) RemoveAlert(_ context.Context, _ string) error        { return nil }

func TestToInhibitionRules_MatchersFormWiredAtRuntime(t *testing.T) {
	cfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{
				Name:           "cluster-down-mutes-nodes",
				SourceMatchers: []string{`alertname="ClusterDown"`},
				TargetMatchers: []string{`severity=~"warning|info"`},
				Equal:          []string{"cluster"},
			},
		},
	}

	rules, err := cfg.ToInhibitionRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)

	source := &core.Alert{Fingerprint: "src", Labels: map[string]string{"alertname": "ClusterDown", "cluster": "a"}}
	target := &core.Alert{Fingerprint: "tgt", Labels: map[string]string{"severity": "warning", "cluster": "a"}}

	matcher := inhibition.NewMatcher(&staticAlertCache{alerts: []*core.Alert{source}}, rules, nil)
	result, err := matcher.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, result.Matched, "a rule expressed only via source_matchers/target_matchers must now actually inhibit")
}

func TestToInhibitionRules_MatchersFormAloneIsSufficient(t *testing.T) {
	// Previously this shape (no source_match/target_match at all) was
	// accepted structurally but was a runtime no-op. It must now be a
	// fully valid, effective rule on its own.
	//
	// Review fix round 1 (I4): de-vacuated — the original version of this
	// test only asserted len(rules) == 1, which would still pass even if
	// CompileMatchers were deleted (an uncompiled rule with a nil
	// compiledSourceMatchers/compiledTargetMatchers still "exists", it
	// just silently never matches). This now proves the compiled rule
	// actually evaluates and inhibits end-to-end.
	cfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{SourceMatchers: []string{`alertname="ClusterDown"`}, TargetMatchers: []string{`severity="warning"`}},
		},
	}
	rules, err := cfg.ToInhibitionRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)

	source := &core.Alert{Fingerprint: "src", Labels: map[string]string{"alertname": "ClusterDown"}}
	target := &core.Alert{Fingerprint: "tgt", Labels: map[string]string{"severity": "warning"}}
	matcher := inhibition.NewMatcher(&staticAlertCache{alerts: []*core.Alert{source}}, rules, nil)
	result, err := matcher.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, result.Matched, "the compiled matchers-form-only rule must actually inhibit, not just structurally exist")
}

func TestToInhibitionRules_InvalidMatcherSyntaxFailsFast(t *testing.T) {
	cfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{Name: "broken", SourceMatchers: []string{"this is not a matcher"}, TargetMatch: map[string]string{"a": "b"}},
		},
	}
	rules, err := cfg.ToInhibitionRules()
	require.Error(t, err)
	assert.Nil(t, rules)
	assert.Contains(t, err.Error(), "broken")
}

func TestToInhibitionRules_UnnamedRuleWithInvalidMatcherIdentifiedByIndex(t *testing.T) {
	cfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{SourceMatch: map[string]string{"a": "b"}, TargetMatch: map[string]string{"c": "d"}},
			{SourceMatchers: []string{"not-a-matcher"}, TargetMatch: map[string]string{"c": "d"}},
		},
	}
	_, err := cfg.ToInhibitionRules()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inhibit_rules[1]")
}

// TestLoadConfig_CapturesInhibitMatchers proves the fields round-trip from
// YAML and are actually wired end-to-end through LoadConfig.
func TestLoadConfig_CapturesInhibitMatchers(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	cfg, err := LoadConfig(writeTempYAML(t, `
app:
  name: test-app
  environment: development
server:
  port: 8080
inhibition:
  inhibit_rules:
    - name: upstream-syntax
      source_matchers: ['alertname="ClusterDown"']
      target_matchers: ['severity="warning"']
      equal: [cluster]
`))
	require.NoError(t, err)
	require.Len(t, cfg.Inhibition.Rules, 1)
	assert.Equal(t, []string{`alertname="ClusterDown"`}, cfg.Inhibition.Rules[0].SourceMatchers)
	assert.Equal(t, []string{`severity="warning"`}, cfg.Inhibition.Rules[0].TargetMatchers)

	rules, err := cfg.Inhibition.ToInhibitionRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
}

// TestLoadConfig_MatchersFormOnly_WithRouteSection_LoadsAndInhibits is the
// fix-round-1 regression test for review Critical C1: `toAlertmanagerInhibitRules`
// (internal/config/alertmanager_validation.go) never copied
// SourceMatchers/TargetMatchers onto the amcfg.InhibitRule bridged into
// pkg/configvalidator, so a matchers-form-only rule arrived at the validator
// looking like it had NO source/target condition at all — E150 + E151 fired,
// and validateAlertmanagerSubset failed LoadConfig (and /-/reload) for any
// config with a `route:` section, which is every real Alertmanager-parity
// deployment. The wave-7 fixture (TestLoadConfig_CapturesInhibitMatchers,
// above) missed this because its YAML has no `route:` key, so
// validateAlertmanagerSubset's `viper.IsSet("route")` gate never ran at all.
// This test includes `route:` + `receivers:` so the validator path is
// actually exercised, and also proves the resulting rule works end-to-end.
func TestLoadConfig_MatchersFormOnly_WithRouteSection_LoadsAndInhibits(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	cfg, err := LoadConfig(writeTempYAML(t, `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

inhibition:
  inhibit_rules:
    - name: upstream-syntax-only
      source_matchers: ['alertname="ClusterDown"']
      target_matchers: ['severity=~"warning|info"']
      equal: [cluster]
`))
	require.NoError(t, err, "a matchers-form-only inhibit rule must not fail LoadConfig when route: is present")
	require.NotNil(t, cfg)
	require.Len(t, cfg.Inhibition.Rules, 1)

	rules, err := cfg.Inhibition.ToInhibitionRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)

	source := &core.Alert{Fingerprint: "src", Labels: map[string]string{"alertname": "ClusterDown", "cluster": "a"}}
	target := &core.Alert{Fingerprint: "tgt", Labels: map[string]string{"severity": "warning", "cluster": "a"}}
	matcher := inhibition.NewMatcher(&staticAlertCache{alerts: []*core.Alert{source}}, rules, nil)
	result, err := matcher.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, result.Matched, "the previously-refused matchers-form-only rule must load AND actually inhibit")
}

// TestLoadConfig_MatchersFormOnly_WithRouteSection_LegacyControl is the
// legacy-form control on the identical route:/receivers: shape, isolating
// that C1 was specifically about the matchers-form bridge, not the
// route:-gated validator path in general.
func TestLoadConfig_MatchersFormOnly_WithRouteSection_LegacyControl(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	_, err := LoadConfig(writeTempYAML(t, `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook

inhibition:
  inhibit_rules:
    - name: legacy
      source_match:
        alertname: ClusterDown
      target_match:
        severity: warning
      equal: [cluster]
`))
	require.NoError(t, err)
}

// TestToInhibitionRules_ReloadThroughConfig_MatchersForm closes the I4 gap
// "reload coverage stops at DefaultInhibitionMatcher.UpdateRules; nothing
// exercises config -> ToInhibitionRules -> UpdateRules for a matchers-form
// rule" — the exact path service_registry.go's reload handler drives, and
// where C1 also bit on `/-/reload` specifically (not just startup).
func TestToInhibitionRules_ReloadThroughConfig_MatchersForm(t *testing.T) {
	target := &core.Alert{Fingerprint: "tgt", Labels: map[string]string{"severity": "warning", "cluster": "a"}}
	source := &core.Alert{Fingerprint: "src", Labels: map[string]string{"alertname": "ClusterDown", "cluster": "a"}}
	cache := &staticAlertCache{}

	// Start with no inhibition config: not inhibited.
	m := inhibition.NewMatcher(cache, nil, nil)
	res, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.False(t, res.Matched)

	// Simulate a hot-reload that introduces a matchers-form-only rule,
	// exactly as service_registry.go's reload path does: new config ->
	// ToInhibitionRules() -> UpdateRules(newRules).
	newCfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{
				Name:           "reloaded-matchers-form",
				SourceMatchers: []string{`alertname="ClusterDown"`},
				TargetMatchers: []string{`severity="warning"`},
				Equal:          []string{"cluster"},
			},
		},
	}
	newRules, err := newCfg.ToInhibitionRules()
	require.NoError(t, err)

	cache.alerts = []*core.Alert{source}
	m.UpdateRules(newRules)

	res, err = m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	assert.True(t, res.Matched, "a matchers-form rule introduced by reload must apply, same as a legacy one")
}
