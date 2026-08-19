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
	cfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{SourceMatchers: []string{`alertname="ClusterDown"`}, TargetMatchers: []string{`severity="warning"`}},
		},
	}
	rules, err := cfg.ToInhibitionRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
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
