package config

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeOnlyBaseYAML is a minimal but complete Alertmanager-shaped config used
// as the "before" state by the routing-reload tests below.
const routeOnlyBaseYAML = `
app:
  name: test-app
  environment: development
server:
  host: localhost
  port: 8080

route:
  receiver: default
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`

// routeOnlyChangedYAML differs from routeOnlyBaseYAML ONLY inside the
// `route:` section (an added child route). Every non-routing field is
// byte-identical, so the JSON-based comparator sees nothing without the
// routing fingerprint.
const routeOnlyChangedYAML = `
app:
  name: test-app
  environment: development
server:
  host: localhost
  port: 8080

route:
  receiver: default
  group_by: [alertname]
  routes:
    - receiver: pager
      matchers: ['severity="critical"']

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
  - name: pager
    webhook_configs:
      - url: https://example.com/pager
`

// receiversOnlyChangedYAML differs from routeOnlyBaseYAML ONLY inside the
// `receivers:` section (the webhook URL of an existing receiver).
const receiversOnlyChangedYAML = `
app:
  name: test-app
  environment: development
server:
  host: localhost
  port: 8080

route:
  receiver: default
  group_by: [alertname]

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook-v2
`

func loadRoutingConfig(t *testing.T, yamlText string) *Config {
	t.Helper()
	resetViper()
	unsetEnvKeys("SERVER_PORT", "SERVER_HOST")
	cfg, err := LoadConfig(writeTempYAML(t, yamlText))
	require.NoError(t, err)
	require.True(t, cfg.HasRouteTree())
	return cfg
}

// TestRoutingFingerprint_StableAcrossMarshals pins the determinism assumption
// that makes the fingerprint usable as a diff input: the same routing tree
// must always hash to the same value, including the map-valued fields
// (match/match_re) whose Go iteration order is randomized.
func TestRoutingFingerprint_StableAcrossMarshals(t *testing.T) {
	yamlText := `
server:
  port: 8080
route:
  receiver: default
  group_by: [alertname]
  routes:
    - receiver: default
      match:
        severity: critical
        team: platform
        zone: eu-west-1
      match_re:
        service: "^(api|web|worker)$"
        cluster: "prod-.*"
receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
`
	cfg := loadRoutingConfig(t, yamlText)

	first := RoutingFingerprint(cfg.Routing)
	require.NotEqual(t, routingFingerprintNone, first)
	for i := 0; i < 50; i++ {
		assert.Equal(t, first, RoutingFingerprint(cfg.Routing), "fingerprint must not depend on map iteration order")
	}
}

func TestRoutingFingerprint_NilConfig(t *testing.T) {
	assert.Equal(t, routingFingerprintNone, RoutingFingerprint(nil))
}

// TestConfigComparator_SeesRouteOnlyChange is the direct regression test for
// final review finding 1: a config edit touching only `route:`/`receivers:`
// must produce a non-empty diff, otherwise ReloadFromFile short-circuits
// before the atomic config swap.
func TestConfigComparator_SeesRouteOnlyChange(t *testing.T) {
	comparator := NewConfigComparator()

	t.Run("route-only edit", func(t *testing.T) {
		oldCfg := loadRoutingConfig(t, routeOnlyBaseYAML)
		newCfg := loadRoutingConfig(t, routeOnlyChangedYAML)

		diff, err := comparator.Compare(oldCfg, newCfg, nil)
		require.NoError(t, err)
		require.Contains(t, diff.Modified, "routing.fingerprint")
		assert.Contains(t, comparator.IdentifyAffectedComponents(diff), "routing")
	})

	t.Run("receivers-only edit", func(t *testing.T) {
		oldCfg := loadRoutingConfig(t, routeOnlyBaseYAML)
		newCfg := loadRoutingConfig(t, receiversOnlyChangedYAML)

		diff, err := comparator.Compare(oldCfg, newCfg, nil)
		require.NoError(t, err)
		assert.Contains(t, diff.Modified, "routing.fingerprint")
	})

	t.Run("identical config produces no routing diff", func(t *testing.T) {
		cfg := loadRoutingConfig(t, routeOnlyBaseYAML)
		same := loadRoutingConfig(t, routeOnlyBaseYAML)

		diff, err := comparator.Compare(cfg, same, nil)
		require.NoError(t, err)
		assert.NotContains(t, diff.Modified, "routing.fingerprint")
		assert.Empty(t, diff.Modified, "no-op reload must stay a no-op")
		assert.Empty(t, diff.Added)
		assert.Empty(t, diff.Deleted)
	})
}

// TestReloadCoordinator_AppliesRouteOnlyChange exercises the whole reload
// pipeline end to end: without the fingerprint the pipeline reported
// Success:true from the "no config changes detected" branch WITHOUT ever
// storing the new config, so GetCurrentConfig kept returning the old tree and
// ServiceRegistry.reloadRoutingTree rebuilt from stale data.
func TestReloadCoordinator_AppliesRouteOnlyChange(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT", "SERVER_HOST")

	configPath := writeTempYAML(t, routeOnlyBaseYAML)
	initial, err := LoadConfig(configPath)
	require.NoError(t, err)
	require.Len(t, initial.Routing.Receivers, 1)
	require.Empty(t, initial.Routing.Route.Routes)

	coordinator := NewReloadCoordinator(
		initial,
		configPath,
		NewConfigValidator(),
		NewConfigComparator(),
		NewConfigReloader(slog.Default()),
		nil, // storage: not needed for this path
		nil, // lockManager: single-process test
		slog.Default(),
	)

	// Rewrite the SAME path with a route-only change.
	require.NoError(t, os.WriteFile(configPath, []byte(routeOnlyChangedYAML), 0o600))
	resetViper()

	result, err := coordinator.ReloadFromFile(context.Background(), configPath)
	require.NoError(t, err)
	require.True(t, result.Success)

	applied := coordinator.GetCurrentConfig()
	require.NotSame(t, initial, applied, "the new config pointer must have been stored")
	require.NotNil(t, applied.Routing)
	require.Len(t, applied.Routing.Route.Routes, 1, "the new child route must be live")
	assert.Equal(t, "pager", applied.Routing.Route.Routes[0].Receiver)
	_, ok := applied.Routing.GetReceiver("pager")
	assert.True(t, ok, "the new receiver must be resolvable after reload")
}

// TestReloadCoordinator_NoChangeStillShortCircuits guards the other side of
// the fix: reloading an unchanged file must still take the cheap "no changes"
// path instead of pointlessly swapping config and bumping the version.
func TestReloadCoordinator_NoChangeStillShortCircuits(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT", "SERVER_HOST")

	configPath := writeTempYAML(t, routeOnlyBaseYAML)
	initial, err := LoadConfig(configPath)
	require.NoError(t, err)

	coordinator := NewReloadCoordinator(
		initial,
		configPath,
		NewConfigValidator(),
		NewConfigComparator(),
		NewConfigReloader(slog.Default()),
		nil,
		nil,
		slog.Default(),
	)

	resetViper()
	result, err := coordinator.ReloadFromFile(context.Background(), configPath)
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Same(t, initial, coordinator.GetCurrentConfig())
}
