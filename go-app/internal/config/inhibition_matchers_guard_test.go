package config

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 10: inhibit_rules' source_matchers/target_matchers
// (upstream's `matchers:` list syntax) validate clean but are DROPPED at
// runtime — the inhibition engine only implements the
// source_match/source_match_re map form. Worse, the fields did not exist on
// InhibitionRuleConfig at all, so a config using them loaded silently and
// inhibited nothing.

func TestInhibitionRuleConfig_UnwiredMatcherFields(t *testing.T) {
	assert.Nil(t, InhibitionRuleConfig{
		SourceMatch: map[string]string{"severity": "critical"},
		TargetMatch: map[string]string{"severity": "warning"},
	}.UnwiredMatcherFields(), "the supported map form must not be flagged")

	assert.Equal(t, []string{"source_matchers"}, InhibitionRuleConfig{
		SourceMatchers: []string{`severity="critical"`},
	}.UnwiredMatcherFields())

	assert.Equal(t, []string{"source_matchers", "target_matchers"}, InhibitionRuleConfig{
		SourceMatchers: []string{`severity="critical"`},
		TargetMatchers: []string{`severity="warning"`},
	}.UnwiredMatcherFields())
}

func TestToInhibitionRules_LogsErrorForUnwiredMatchers(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(original)

	cfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{
				Name:           "cluster-down-mutes-nodes",
				SourceMatchers: []string{`alertname="ClusterDown"`},
				TargetMatchers: []string{`severity="warning"`},
			},
			{
				// Supported form — must not produce a warning.
				Name:        "wired",
				SourceMatch: map[string]string{"severity": "critical"},
				TargetMatch: map[string]string{"severity": "warning"},
			},
		},
	}

	rules, err := cfg.ToInhibitionRules()
	require.NoError(t, err, "the guard must warn, not refuse to start")
	require.Len(t, rules, 2, "both rules are still loaded (the matchers form just contributes nothing)")

	logged := buf.String()
	assert.Contains(t, logged, "level=ERROR", "an ineffective inhibit rule must be logged at Error")
	assert.Contains(t, logged, "cluster-down-mutes-nodes", "the log must name the affected rule")
	assert.Contains(t, logged, "source_matchers")
	assert.Contains(t, logged, "target_matchers")
	assert.NotContains(t, logged, "rule=wired", "a rule using the supported form must not be flagged")
}

// TestToInhibitionRules_UnnamedRuleIsIdentifiedByIndex keeps the log actionable
// for configs whose rules have no `name:` (name is optional upstream).
func TestToInhibitionRules_UnnamedRuleIsIdentifiedByIndex(t *testing.T) {
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(original)

	cfg := &InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{SourceMatch: map[string]string{"a": "b"}},
			{SourceMatchers: []string{`severity="critical"`}},
		},
	}
	_, err := cfg.ToInhibitionRules()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "inhibit_rules[1]")
}

// TestLoadConfig_CapturesInhibitMatchers proves the fields actually round-trip
// from YAML — before the fix they were absent from the struct, so a config using
// them produced no error, no warning, and no inhibition.
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
}
