package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_InvestigationRunbooksDefaults(t *testing.T) {
	resetViper()
	unsetEnvKeys("INVESTIGATION_RUNBOOKS_ENABLED", "INVESTIGATION_RUNBOOKS_PATH",
		"INVESTIGATION_RUNBOOKS_MAX_RUNBOOKS", "INVESTIGATION_RUNBOOKS_MAX_CHARS")

	cfg, err := LoadConfig(writeTempYAML(t, "log:\n  level: info\n"))
	require.NoError(t, err)

	rb := cfg.Investigation.Runbooks
	assert.False(t, rb.Enabled)
	assert.Equal(t, DefaultRunbooksPath, rb.Path)
	assert.Equal(t, DefaultRunbooksMaxRunbooks, rb.MaxRunbooks)
	assert.Equal(t, DefaultRunbooksMaxChars, rb.MaxChars)
}

func TestLoadConfig_InvestigationRunbooksFromYAML(t *testing.T) {
	resetViper()
	unsetEnvKeys("INVESTIGATION_RUNBOOKS_ENABLED", "INVESTIGATION_RUNBOOKS_PATH",
		"INVESTIGATION_RUNBOOKS_MAX_RUNBOOKS", "INVESTIGATION_RUNBOOKS_MAX_CHARS")

	yaml := `
investigation:
  runbooks:
    enabled: true
    path: /srv/runbooks
    max_runbooks: 5
    max_chars: 1200
`
	cfg, err := LoadConfig(writeTempYAML(t, yaml))
	require.NoError(t, err)

	rb := cfg.Investigation.Runbooks
	assert.True(t, rb.Enabled)
	assert.Equal(t, "/srv/runbooks", rb.Path)
	assert.Equal(t, 5, rb.EffectiveMaxRunbooks())
	assert.Equal(t, 1200, rb.EffectiveMaxChars())
}

func TestInvestigationRunbooksConfig_EffectiveLimits(t *testing.T) {
	for _, v := range []int{0, -1} {
		c := InvestigationRunbooksConfig{MaxRunbooks: v, MaxChars: v}
		assert.Equal(t, DefaultRunbooksMaxRunbooks, c.EffectiveMaxRunbooks())
		assert.Equal(t, DefaultRunbooksMaxChars, c.EffectiveMaxChars())
	}
}
