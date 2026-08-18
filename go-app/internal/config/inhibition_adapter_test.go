package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Task 5.4 (carried fix): ToInhibitionRules used to swallow a failing
// inhibition.config_file ParseFile error (`if err == nil && cfg != nil`),
// silently dropping all file-based inhibition rules. These tests pin the
// fixed contract: the error must propagate.

func TestInhibitionConfig_ToInhibitionRules_InlineOnly(t *testing.T) {
	cfg := InhibitionConfig{
		Rules: []InhibitionRuleConfig{
			{
				SourceMatch: map[string]string{"severity": "critical"},
				TargetMatch: map[string]string{"severity": "warning"},
				Equal:       []string{"alertname"},
			},
		},
	}

	rules, err := cfg.ToInhibitionRules()
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, "critical", rules[0].SourceMatch["severity"])
}

func TestInhibitionConfig_ToInhibitionRules_ConfigFileMissing(t *testing.T) {
	cfg := InhibitionConfig{
		ConfigFile: filepath.Join(t.TempDir(), "does-not-exist.yaml"),
	}

	rules, err := cfg.ToInhibitionRules()
	require.Error(t, err)
	assert.Nil(t, rules)
	assert.Contains(t, err.Error(), "does-not-exist.yaml")
}

func TestInhibitionConfig_ToInhibitionRules_ConfigFileMalformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inhibit.yaml")
	// Missing source_match/source_match_re: fails
	// inhibition.Parser.Validate's "at least one condition per side" rule.
	content := `
inhibit_rules:
  - target_match:
      severity: warning
    equal:
      - alertname
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	cfg := InhibitionConfig{ConfigFile: path}

	rules, err := cfg.ToInhibitionRules()
	require.Error(t, err)
	assert.Nil(t, rules)
	assert.True(t, strings.Contains(err.Error(), path), "error should reference the config_file path: %v", err)
}

func TestInhibitionConfig_ToInhibitionRules_ConfigFileMergesWithInline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inhibit.yaml")
	content := `
inhibit_rules:
  - source_match:
      severity: critical
    target_match:
      severity: warning
    equal:
      - alertname
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	cfg := InhibitionConfig{
		ConfigFile: path,
		Rules: []InhibitionRuleConfig{
			{
				SourceMatch: map[string]string{"severity": "high"},
				TargetMatch: map[string]string{"severity": "low"},
				Equal:       []string{"cluster"},
			},
		},
	}

	rules, err := cfg.ToInhibitionRules()
	require.NoError(t, err)
	require.Len(t, rules, 2, "inline rule + file rule must both be present")
}

// TestLoadConfig_InhibitionConfigFile_FailsFast proves the startup-level
// wiring (task 5.4): a broken inhibition.config_file rejects the whole
// LoadConfig call, not just a warning buried in a degraded-reasons log.
func TestLoadConfig_InhibitionConfigFile_FailsFast(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
inhibition:
  config_file: /nonexistent/path/inhibit.yaml
`
	path := writeTempYAML(t, yaml)

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inhibition config validation failed")
}
