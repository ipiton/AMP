package config

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureDefaultLogger redirects slog's default logger into a buffer for the
// duration of the test (logUnsupportedReceiverIntegrations and
// logConfigValidatorWarnings both log through it).
func captureDefaultLogger(t *testing.T) *strings.Builder {
	t.Helper()

	var buf strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// TestLoadConfig_UnsupportedIntegrationOnlyReceiver_LoadsWithWarning is
// slice-1 review finding I1: a receiver whose ONLY block is an integration AMP
// cannot deliver (opsgenie_configs here) is legal upstream, so the config must
// LOAD with a warning naming it — not fail. Before the fix, yaml.v3 dropped the
// unknown block and Receiver.Validate() then rejected the "empty" receiver,
// failing the whole load right after emitting the warning.
func TestLoadConfig_UnsupportedIntegrationOnlyReceiver_LoadsWithWarning(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")
	logs := captureDefaultLogger(t)

	path := writeTempYAML(t, `
server:
  port: 8080

route:
  receiver: default
  group_by: [alertname]
  routes:
    - receiver: pager-og
      matchers: ['severity="critical"']

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
  - name: pager-og
    opsgenie_configs:
      - api_key: fixture-opsgenie-key
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err, "an upstream config with an unsupported integration must still load")
	require.NotNil(t, cfg.Routing)
	require.Len(t, cfg.Routing.Receivers, 2)

	// The unsupported block is dropped by the routing schema, so the receiver
	// behaves as a blackhole: it exists, routes resolve to it, and it carries
	// no integration AMP could deliver through.
	assert.Equal(t, "pager-og", cfg.Routing.Receivers[1].Name)
	assert.Equal(t, 0, cfg.Routing.Receivers[1].GetConfigCount())

	logged := logs.String()
	assert.Contains(t, logged, "opsgenie_configs")
	assert.Contains(t, logged, "pager-og")
}

// TestLoadConfig_BlackholeReceiverLoads: the classic upstream `- name: 'null'`
// receiver with no integrations at all (review finding I1). Used to fail with
// configvalidator's E024; now a W024 warning that does not block.
func TestLoadConfig_BlackholeReceiverLoads(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")
	logs := captureDefaultLogger(t)

	path := writeTempYAML(t, `
server:
  port: 8080

route:
  receiver: default
  group_by: [alertname]
  routes:
    - receiver: "null"
      matchers: ['severity="info"']

receivers:
  - name: default
    webhook_configs:
      - url: https://example.com/webhook
  - name: "null"
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err, "upstream's blackhole receiver must load")
	require.NotNil(t, cfg.Routing)
	require.Len(t, cfg.Routing.Receivers, 2)
	assert.Equal(t, 0, cfg.Routing.Receivers[1].GetConfigCount())

	assert.Contains(t, logs.String(), "W024",
		"an integration-less receiver is legal but must still be reported as a blackhole")
}

// TestLoadConfig_DuplicateReceiverNames is slice-1 review finding I3: upstream
// rejects duplicate receiver names, and AMP must too — the receiver index and
// the config-provisioned target names are both keyed by name, so a duplicate
// silently shadows one receiver's integrations.
func TestLoadConfig_DuplicateReceiverNames(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	path := writeTempYAML(t, `
server:
  port: 8080

route:
  receiver: dup
  group_by: [alertname]

receivers:
  - name: dup
    webhook_configs:
      - url: https://a.example.com/webhook
  - name: dup
    webhook_configs:
      - url: https://b.example.com/webhook
`)

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "duplicate receiver name")
	assert.Contains(t, err.Error(), "dup")
}
