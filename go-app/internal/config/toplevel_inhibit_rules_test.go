package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
)

// FU-TOPLEVEL-INHIBIT-RULES: upstream Alertmanager puts `inhibit_rules:` at the
// top level of alertmanager.yml. AMP used to parse that key and never apply it,
// so a verbatim upstream config loaded cleanly and inhibited nothing. These
// tests pin the fixed contract through LoadConfig + Config.InhibitionRules —
// the same path startup, hot reload and the LoadConfig fail-fast use.

const topLevelRouteYAML = `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

// inhibits reports whether rules mute target while source is firing.
func inhibits(t *testing.T, rules []inhibition.InhibitionRule, source, target *core.Alert) bool {
	t.Helper()
	m := inhibition.NewMatcher(&staticAlertCache{alerts: []*core.Alert{source}}, rules, nil)
	res, err := m.ShouldInhibit(context.Background(), target)
	require.NoError(t, err)
	return res.Matched
}

func loadTopLevelConfig(t *testing.T, content string) (*Config, error) {
	t.Helper()
	resetViper()
	unsetEnvKeys("SERVER_PORT")
	return LoadConfig(writeTempYAML(t, content))
}

// T1: both upstream forms, with and without `route:` (the validator path only
// runs with `route:`).
func TestTopLevelInhibitRules_BothFormsInhibit(t *testing.T) {
	rulesYAML := `
inhibit_rules:
  - source_match:
      alertname: NodeDown
    target_match_re:
      severity: warning|info
    equal: [node]
  - source_matchers: ['alertname="ClusterDown"']
    target_matchers: ['severity="warning"']
    equal: [cluster]
`
	for name, content := range map[string]string{
		"with route":    topLevelRouteYAML + rulesYAML,
		"without route": rulesYAML,
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadTopLevelConfig(t, content)
			require.NoError(t, err)
			require.Len(t, cfg.InhibitRules, 2)

			rules, err := cfg.InhibitionRules()
			require.NoError(t, err)
			require.Len(t, rules, 2)

			legacySrc := &core.Alert{Fingerprint: "s1", Labels: map[string]string{"alertname": "NodeDown", "node": "n1"}}
			legacyTgt := &core.Alert{Fingerprint: "t1", Labels: map[string]string{"severity": "info", "node": "n1"}}
			assert.True(t, inhibits(t, rules, legacySrc, legacyTgt), "legacy source_match/target_match_re rule must inhibit")

			modernSrc := &core.Alert{Fingerprint: "s2", Labels: map[string]string{"alertname": "ClusterDown", "cluster": "a"}}
			modernTgt := &core.Alert{Fingerprint: "t2", Labels: map[string]string{"severity": "warning", "cluster": "a"}}
			assert.True(t, inhibits(t, rules, modernSrc, modernTgt), "source_matchers/target_matchers rule must inhibit")

			otherCluster := &core.Alert{Fingerprint: "t3", Labels: map[string]string{"severity": "warning", "cluster": "b"}}
			assert.False(t, inhibits(t, rules, modernSrc, otherCluster), "equal must still scope the rule")
		})
	}
}

// T2: top-level, inline and config_file rules are merged in that order; a
// config with only the AMP wrapper behaves exactly as before.
func TestTopLevelInhibitRules_MergeOrder(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "inhibit.yaml")
	require.NoError(t, os.WriteFile(filePath, []byte(`
inhibit_rules:
  - name: from-file
    source_match:
      alertname: FileSource
    target_match:
      severity: warning
`), 0o600))

	cfg, err := loadTopLevelConfig(t, `
inhibit_rules:
  - name: from-top-level
    source_matchers: ['alertname="TopSource"']
    target_matchers: ['severity="warning"']

inhibition:
  config_file: `+filePath+`
  inhibit_rules:
    - name: from-inline
      source_match:
        alertname: InlineSource
      target_match:
        severity: warning
`)
	require.NoError(t, err)

	rules, err := cfg.InhibitionRules()
	require.NoError(t, err)
	names := make([]string, 0, len(rules))
	for _, r := range rules {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"from-top-level", "from-inline", "from-file"}, names)

	t.Run("wrapper only matches ToInhibitionRules", func(t *testing.T) {
		cfg, err := loadTopLevelConfig(t, `
inhibition:
  inhibit_rules:
    - name: only-inline
      source_match:
        alertname: InlineSource
      target_match:
        severity: warning
`)
		require.NoError(t, err)
		require.Empty(t, cfg.InhibitRules)

		merged, err := cfg.InhibitionRules()
		require.NoError(t, err)
		wrapped, err := cfg.Inhibition.ToInhibitionRules()
		require.NoError(t, err)
		require.Len(t, merged, len(wrapped))
		for i := range merged {
			assert.Equal(t, wrapped[i].Name, merged[i].Name)
			assert.Equal(t, wrapped[i].SourceMatch, merged[i].SourceMatch)
			assert.Equal(t, wrapped[i].TargetMatch, merged[i].TargetMatch)
		}
	})
}

// T3: a broken top-level rule fails LoadConfig and the error names where it
// came from; an empty list is fine.
func TestTopLevelInhibitRules_BrokenRuleFailsLoad(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "unnamed broken matcher",
			content: `
inhibit_rules:
  - source_matchers: ['alertname=~"("']
    target_matchers: ['severity="warning"']
`,
			want: `top-level inhibition rule "inhibit_rules[0]"`,
		},
		{
			name: "named broken regex",
			content: `
inhibit_rules:
  - name: bad-regex
    source_match_re:
      alertname: "("
    target_match:
      severity: warning
`,
			want: `top-level inhibition rule "bad-regex"`,
		},
		{
			name: "inline prefix unchanged",
			content: `
inhibition:
  inhibit_rules:
    - source_matchers: ['alertname=~"("']
      target_matchers: ['severity="warning"']
`,
			want: `inline inhibition rule "inhibit_rules[0]"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadTopLevelConfig(t, tt.content)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}

	t.Run("empty list", func(t *testing.T) {
		cfg, err := loadTopLevelConfig(t, "inhibit_rules: []\n")
		require.NoError(t, err)
		rules, err := cfg.InhibitionRules()
		require.NoError(t, err)
		assert.Empty(t, rules)
	})
}

// T4: with `route:` the top-level rules go through configvalidator. E-codes
// still fail the load; a rule with an empty side is legal upstream (W150/W151)
// and must load and inhibit.
func TestTopLevelInhibitRules_ConfigValidator(t *testing.T) {
	t.Run("E152 invalid equal label fails load", func(t *testing.T) {
		_, err := loadTopLevelConfig(t, topLevelRouteYAML+`
inhibit_rules:
  - source_match:
      alertname: NodeDown
    target_match:
      severity: warning
    equal: ["not a label!"]
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "E152")
	})

	t.Run("target-only rule loads and inhibits", func(t *testing.T) {
		// kube-prometheus-stack's InfoInhibitor rule, verbatim.
		cfg, err := loadTopLevelConfig(t, topLevelRouteYAML+`
inhibit_rules:
  - target_matchers:
      - alertname = InfoInhibitor
`)
		require.NoError(t, err)

		rules, err := cfg.InhibitionRules()
		require.NoError(t, err)
		require.Len(t, rules, 1)

		source := &core.Alert{Fingerprint: "src", Labels: map[string]string{"alertname": "Anything"}}
		target := &core.Alert{Fingerprint: "tgt", Labels: map[string]string{"alertname": "InfoInhibitor"}}
		assert.True(t, inhibits(t, rules, source, target), "an empty source side matches any firing alert, as upstream")
	})
}

// T8 (review F1): viper lower-cases map keys, which used to turn
// `clusterName` into `clustername` and silently break the rule. Both
// placements must keep the label name as written.
func TestTopLevelInhibitRules_LabelNameCasePreserved(t *testing.T) {
	cfg, err := loadTopLevelConfig(t, `
inhibit_rules:
  - source_match:
      clusterName: Prod
    target_match:
      severity: warning
    equal: [clusterName]

inhibition:
  inhibit_rules:
    - source_match_re:
        clusterName: Prod.*
      target_match:
        severity: info
      equal: [clusterName]
`)
	require.NoError(t, err)
	require.Len(t, cfg.InhibitRules, 1)
	require.Len(t, cfg.Inhibition.Rules, 1)
	assert.Equal(t, map[string]string{"clusterName": "Prod"}, cfg.InhibitRules[0].SourceMatch)
	assert.Equal(t, map[string]string{"clusterName": "Prod.*"}, cfg.Inhibition.Rules[0].SourceMatchRE)

	rules, err := cfg.InhibitionRules()
	require.NoError(t, err)

	source := &core.Alert{Fingerprint: "src", Labels: map[string]string{"clusterName": "Prod"}}
	warning := &core.Alert{Fingerprint: "t1", Labels: map[string]string{"severity": "warning", "clusterName": "Prod"}}
	info := &core.Alert{Fingerprint: "t2", Labels: map[string]string{"severity": "info", "clusterName": "Prod"}}
	assert.True(t, inhibits(t, rules, source, warning), "top-level rule with a mixed-case label must inhibit")
	assert.True(t, inhibits(t, rules, source, info), "nested rule with a mixed-case label must inhibit")
}

// T5: a hot reload that only adds or edits a top-level rule is not a no-op
// (the diff sees Config.InhibitRules) and the new config carries the rule.
func TestTopLevelInhibitRules_Reload(t *testing.T) {
	v1 := `
inhibit_rules:
  - name: r
    source_matchers: ['alertname="ClusterDown"']
    target_matchers: ['severity="warning"']
`
	v2 := `
inhibit_rules:
  - name: r
    source_matchers: ['alertname="ClusterDown"']
    target_matchers: ['severity=~"warning|info"']
`
	tests := []struct {
		name     string
		from, to string
		want     []string
	}{
		{name: "rule added", from: "", to: v1, want: []string{`severity="warning"`}},
		{name: "only target_matchers changed", from: v1, to: v2, want: []string{`severity=~"warning|info"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempYAML(t, tt.from)
			resetViper()
			unsetEnvKeys("SERVER_PORT")
			initial, err := LoadConfig(path)
			require.NoError(t, err)

			require.NoError(t, os.WriteFile(path, []byte(tt.to), 0o600))
			resetViper()

			coordinator := NewReloadCoordinator(
				initial,
				path,
				&MockConfigValidator{},
				NewConfigComparator(),
				NewConfigReloader(slog.Default()),
				nil,
				nil,
				slog.Default(),
			)
			result, err := coordinator.ReloadFromFile(context.Background(), path)
			require.NoError(t, err)
			require.True(t, result.Success)
			assert.Equal(t, int64(2), result.Version, "a top-level inhibit_rules change must not be treated as no-change")

			rules, err := coordinator.GetCurrentConfig().InhibitionRules()
			require.NoError(t, err)
			require.Len(t, rules, 1)
			assert.Equal(t, tt.want, rules[0].TargetMatchers)
		})
	}
}
