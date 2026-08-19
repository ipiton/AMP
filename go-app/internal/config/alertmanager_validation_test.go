package config

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// Task 5.4: pkg/configvalidator wiring into LoadConfig (startup + /-/reload)
// ================================================================================

const validAlertmanagerConfigYAML = `
route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

// TestLoadConfig_AlertmanagerSubset_BadReceiverRef proves LoadConfig fails
// fast (startup refuses) with configvalidator's detailed message when the
// route tree references a receiver that does not exist (E102) - the kind
// of error that, before task 5.4, "any config passes" would have let
// through.
func TestLoadConfig_AlertmanagerSubset_BadReceiverRef(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
route:
  receiver: default
  routes:
    - receiver: does-not-exist

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "alertmanager config validation failed")
	assert.Contains(t, err.Error(), "E102")
}

// TestLoadConfig_AlertmanagerSubset_ValidConfig proves a well-formed
// Alertmanager-shaped config still loads cleanly through the new check.
func TestLoadConfig_AlertmanagerSubset_ValidConfig(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	path := writeTempYAML(t, validAlertmanagerConfigYAML)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.True(t, cfg.HasRouteTree())
}

// TestLoadConfig_AlertmanagerSubset_QuotedOperatorInMatcherValue is the
// fix-round finding I-4 regression test, through the REAL LoadConfig path
// the reviewer reproduced it against: before the fix,
// pkg/configvalidator/matcher.Parse located the operator via
// strings.Index over the WHOLE matcher string, so a quoted value
// containing an operator-looking substring (here: `summary="a!=b"`) split
// INSIDE the quotes and hard-failed startup with a nonsensical E104
// ("invalid label name 'summary=\"a'"), while
// business/routing.parseMatcherExpr's anchored regex parsed the identical
// YAML `matchers:` entry fine when building the actual route tree. A
// config that the runtime route tree accepts must not fail
// configvalidator's startup gate for the same entry.
func TestLoadConfig_AlertmanagerSubset_QuotedOperatorInMatcherValue(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
route:
  receiver: default
  routes:
    - receiver: default
      matchers:
        - 'summary="a!=b"'
        - 'note="a=~b"'

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err, "a quoted value containing an operator token must not fail startup validation")
	require.NotNil(t, cfg)
	assert.True(t, cfg.HasRouteTree())
}

// TestLoadConfig_AlertmanagerSubset_WarningsOnlyStillLoads proves a config
// that only trips a W-code (here: W300, a secret set directly in
// global.smtp_auth_password instead of via env/secret-manager) still
// loads successfully - warnings are logged, never blocking. The fixture
// deliberately avoids anything infraroute's own (stricter) schema would
// also reject (e.g. plaintext webhook URLs trip its https_production
// validator too), to isolate "configvalidator warns, doesn't block".
func TestLoadConfig_AlertmanagerSubset_WarningsOnlyStillLoads(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
global:
  smtp_auth_password: hardcoded-secret-placeholder

route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.True(t, cfg.HasRouteTree())
}

// TestLoadConfig_AlertmanagerSubset_TelegramOnlyReceiver is the end-to-end
// (not just unit-level, see pkg/configvalidator/validators/receiver_test.go)
// regression for the E024 false positive fixed in task 5.4: a
// telegram-only receiver must load cleanly through the real LoadConfig
// path, not just the validator in isolation.
func TestLoadConfig_AlertmanagerSubset_TelegramOnlyReceiver(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
route:
  receiver: default

receivers:
  - name: default
    telegram_configs:
      - bot_token: placeholder-bot-token
        chat_id: "-1001234567890"
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.True(t, cfg.HasRouteTree())
}

// TestLoadConfig_AlertmanagerSubset_LegacyModeSkipsCheck proves a legacy
// single-receiver config (no `route:` section) is not run through
// configvalidator at all - it has no Alertmanager-shaped receivers section
// to validate (AMP's own name-only ReceiverConfig list has a different
// shape entirely) and would otherwise false-positive on E100/E021.
func TestLoadConfig_AlertmanagerSubset_LegacyModeSkipsCheck(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	yaml := `
receivers:
  - name: default
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.False(t, cfg.HasRouteTree())
}

// TestReloadFromFile_InvalidAlertmanagerConfig_RejectsAndKeepsOldConfig
// exercises the full /-/reload wiring with real components (not the
// MockConfigValidator used by reload_coordinator_test.go, which only
// covers ReloadCoordinator's own Phase 2 - this proves the Phase 1
// loadAndParse -> LoadConfig path that now embeds configvalidator):
// reloading from a file with a bad receiver reference must fail with
// configvalidator's error and leave the previously active config in
// place.
func TestReloadFromFile_InvalidAlertmanagerConfig_RejectsAndKeepsOldConfig(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	path := writeTempYAML(t, validAlertmanagerConfigYAML)

	initialCfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.True(t, initialCfg.HasRouteTree())

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	coordinator := NewReloadCoordinator(
		initialCfg,
		path,
		NewConfigValidator(),
		NewConfigComparator(),
		NewConfigReloader(logger),
		nil,
		nil,
		logger,
	)

	// Overwrite the file with an invalid Alertmanager-shaped config (bad
	// receiver reference, E102).
	invalidYAML := `
route:
  receiver: default
  routes:
    - receiver: does-not-exist

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
	require.NoError(t, os.WriteFile(path, []byte(invalidYAML), 0o600))

	result, err := coordinator.ReloadFromFile(context.Background(), path)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "phase 1 (load) failed")
	assert.Contains(t, err.Error(), "E102")

	// Old config must still be active: the swap in atomicApply (Phase 4)
	// never ran because Phase 1 failed first.
	current := coordinator.GetCurrentConfig()
	require.NotNil(t, current)
	assert.True(t, current.HasRouteTree())
	assert.Equal(t, "default", current.Routing.Route.Receiver)
	assert.Empty(t, current.Routing.Route.Routes, "old config's routes must be untouched")
}
