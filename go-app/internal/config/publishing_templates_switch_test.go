package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// publishing.templates.enabled — the TEMPLATES-EPIC kill switch (slice-2 review
// C1c)
// ============================================================================
//
// Templating is ON by default and that CHANGES notification shape for every
// `receivers:`-provisioned slack/pagerduty/telegram/email target (upstream's own
// defaults are materialized, so an untouched upstream config renders upstream's
// output instead of AMP's). This switch is the documented revert, so its default
// is the load-bearing part: absent must mean ON, and only an explicit `false`
// may turn it off.

func templatesSwitchConfig() *Config {
	cfg := publishingTestConfig(45 * time.Second)
	return cfg
}

// TestPublishingTemplates_AbsentMeansEnabled is the tri-state contract: a plain
// bool would have made every hand-built config (tests, embedded profiles) and
// every config file that omits the key silently DISABLE templating, which is the
// opposite of the epic's default.
func TestPublishingTemplates_AbsentMeansEnabled(t *testing.T) {
	cfg := templatesSwitchConfig()

	require.Nil(t, cfg.Publishing.Templates.Enabled, "the fixture must not set it")
	assert.True(t, cfg.Publishing.Templates.IsEnabled(), "absent = upstream rendering")
}

func TestPublishingTemplates_ExplicitValuesWin(t *testing.T) {
	on, off := true, false

	cfg := templatesSwitchConfig()
	cfg.Publishing.Templates.Enabled = &on
	assert.True(t, cfg.Publishing.Templates.IsEnabled())

	cfg.Publishing.Templates.Enabled = &off
	assert.False(t, cfg.Publishing.Templates.IsEnabled())
}

// TestPublishingTemplates_ViperDefaultIsEnabled pins the OTHER default: viper's,
// which is what a real config file load goes through. If these two ever disagree,
// a deployment's behaviour depends on whether it loaded a file or not.
func TestPublishingTemplates_ViperDefaultIsEnabled(t *testing.T) {
	cfg, err := LoadConfigFromEnv()
	require.NoError(t, err)

	require.NotNil(t, cfg.Publishing.Templates.Enabled,
		"viper must materialize the default so the loaded config states it explicitly")
	assert.True(t, cfg.Publishing.Templates.IsEnabled())
}

// TestPublishingTemplates_ConfigFileCanDisableIt is the operator-facing half: the
// one line in a config file that restores AMP's pre-epic formatters.
func TestPublishingTemplates_ConfigFileCanDisableIt(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
publishing:
  enabled: true
  templates:
    enabled: false
`), 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	require.NotNil(t, cfg.Publishing.Templates.Enabled)
	assert.False(t, cfg.Publishing.Templates.IsEnabled())
}

// TestValidatePublishingTemplates_GuardsRejectNegatives: zero means "use the
// built-in default", so only a negative value is a mistake — and a negative one
// would otherwise make every render fail its own guard and fall back silently.
func TestValidatePublishingTemplates_GuardsRejectNegatives(t *testing.T) {
	cfg := templatesSwitchConfig()
	cfg.Publishing.Templates.RenderTimeout = -time.Second
	err := cfg.validatePublishing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publishing.templates.render_timeout")

	cfg = templatesSwitchConfig()
	cfg.Publishing.Templates.MaxOutputBytes = -1
	err = cfg.validatePublishing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "publishing.templates.max_output_bytes")
}

func TestValidatePublishingTemplates_ZeroGuardsAndBothSwitchStatesAreValid(t *testing.T) {
	off := false

	cfg := templatesSwitchConfig()
	assert.NoError(t, cfg.validatePublishing(), "zero guards = built-in defaults")

	cfg.Publishing.Templates.Enabled = &off
	assert.NoError(t, cfg.validatePublishing(),
		"turning templating off is a supported configuration, not an error")

	cfg.Publishing.Templates.RenderTimeout = 2 * time.Second
	cfg.Publishing.Templates.MaxOutputBytes = 1 << 20
	assert.NoError(t, cfg.validatePublishing())
}
