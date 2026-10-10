package config

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildGroupingConfig_NoRouteTree verifies the clean-skip error path
// (task 2.2): grouping has no config of its own for group_by/group_wait/
// group_interval/repeat_interval, so it cannot build a grouping.GroupingConfig
// without a route: tree.
func TestBuildGroupingConfig_NoRouteTree(t *testing.T) {
	cfg := &Config{}

	got, err := cfg.BuildGroupingConfig()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrGroupingRequiresRouteTree))
	assert.Nil(t, got)
}

// TestBuildGroupingConfig_WithRouteTree verifies the adapter reuses the
// route tree's *grouping.Route pointer directly rather than re-mapping a
// second copy of the same fields.
func TestBuildGroupingConfig_WithRouteTree(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
route:
  receiver: default
  group_by: [alertname]
  group_wait: 15s
  group_interval: 2m
  repeat_interval: 1h

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.True(t, cfg.HasRouteTree())

	groupingCfg, err := cfg.BuildGroupingConfig()
	require.NoError(t, err)
	require.NotNil(t, groupingCfg)
	require.NotNil(t, groupingCfg.Route)

	// Same pointer, not a copy — infraroute.RouteConfig.Route already IS a
	// *grouping.Route (TN-121 backward compatibility).
	assert.Same(t, cfg.Routing.Route, groupingCfg.Route)
	assert.Equal(t, "default", groupingCfg.Route.Receiver)
	assert.Equal(t, []string{"alertname"}, groupingCfg.Route.GroupBy)
	assert.Equal(t, "15s", groupingCfg.Route.GroupWait.String())
}

// TestLoadConfig_GroupingDefaults verifies grouping.enabled defaults to
// true (PROD-GROUPING-DEFAULT): a verbatim alertmanager.yml must group the
// way upstream does without an AMP-specific key.
func TestLoadConfig_GroupingDefaults(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
server:
  port: 8080
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Grouping.Enabled)
}

// TestLoadConfig_GroupingEnabledOverride verifies the `grouping.enabled: true`
// YAML key is parsed into Config.Grouping.Enabled.
func TestLoadConfig_GroupingEnabledOverride(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
server:
  port: 8080

grouping:
  enabled: true
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	assert.True(t, cfg.Grouping.Enabled)
}

// TestLoadConfig_GroupingDisabledExplicitly verifies that the default (true)
// does not shadow an explicit opt-out, from the file or from the environment.
func TestLoadConfig_GroupingDisabledExplicitly(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		resetViper()
		unsetEnvKeys("SERVER_PORT", "GROUPING_ENABLED")

		path := writeTempYAML(t, `
server:
  port: 8080

grouping:
  enabled: false
`)

		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.False(t, cfg.Grouping.Enabled)
	})

	t.Run("env without a file", func(t *testing.T) {
		resetViper()
		t.Setenv("GROUPING_ENABLED", "false")

		cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
		require.NoError(t, err)
		assert.False(t, cfg.Grouping.Enabled)
	})

	// The Helm chart always sets GROUPING_ENABLED, so under the chart the
	// key inside the config file has no effect.
	t.Run("env overrides yaml", func(t *testing.T) {
		resetViper()
		unsetEnvKeys("SERVER_PORT")
		t.Setenv("GROUPING_ENABLED", "true")

		path := writeTempYAML(t, `
server:
  port: 8080

grouping:
  enabled: false
`)

		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.True(t, cfg.Grouping.Enabled)
	})
}
