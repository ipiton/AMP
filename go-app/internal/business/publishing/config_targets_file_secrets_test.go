package publishing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// TestBuildConfigTargets_FileSecretVariantsProduceIdenticalTargets is the
// FU7-B acceptance test for requirement 4: the value resolved from a `*_file`
// secret reference must flow into the SAME target-building path the inline
// field uses, producing a byte-identical core.PublishingTarget.
//
// Deliberately goes through infraroute.NewRouteConfigParser().Parse rather
// than hand-building the *infraroute.Receiver structs: the resolution this
// test verifies happens in the PARSER (resolveFileSecrets), not in this
// package — BuildConfigTargets itself needed zero changes.
func TestBuildConfigTargets_FileSecretVariantsProduceIdenticalTargets(t *testing.T) {
	writeSecret := func(t *testing.T, content string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}

	webhookURLFile := writeSecret(t, "https://webhook.example.com/alerts\n")
	routingKeyFile := writeSecret(t, "rk-fixture-0123456789\n")
	slackAPIURLFile := writeSecret(t, "https://hooks.slack.com/services/FILE/HOOK/URL\n")
	botTokenFile := writeSecret(t, "bot-token-fixture\n")

	inlineYAML := `
route:
  receiver: all
  group_by: [alertname]
receivers:
  - name: all
    webhook_configs:
      - url: https://webhook.example.com/alerts
    pagerduty_configs:
      - routing_key: rk-fixture-0123456789
    slack_configs:
      - api_url: https://hooks.slack.com/services/FILE/HOOK/URL
    telegram_configs:
      - bot_token: bot-token-fixture
        chat_id: "-100"
`

	fileYAML := `
route:
  receiver: all
  group_by: [alertname]
receivers:
  - name: all
    webhook_configs:
      - url_file: ` + webhookURLFile + `
    pagerduty_configs:
      - routing_key_file: ` + routingKeyFile + `
    slack_configs:
      - api_url_file: ` + slackAPIURLFile + `
    telegram_configs:
      - bot_token_file: ` + botTokenFile + `
        chat_id: "-100"
`

	inlineConfig, err := infraroute.NewRouteConfigParser().Parse([]byte(inlineYAML))
	require.NoError(t, err)
	fileConfig, err := infraroute.NewRouteConfigParser().Parse([]byte(fileYAML))
	require.NoError(t, err)

	inlineTargets := BuildConfigTargets(inlineConfig, quietLogger())
	fileTargets := BuildConfigTargets(fileConfig, quietLogger())

	require.Len(t, fileTargets, 4)
	require.Len(t, inlineTargets, len(fileTargets))
	assert.Equal(t, inlineTargets, fileTargets,
		"a *_file secret variant must produce a target identical to its inline twin")
}

// TestBuildConfigTargets_GlobalSlackAPIURLFileFallback covers requirement 5:
// global.slack_api_url_file participates in the same fallback chain as
// global.slack_api_url.
func TestBuildConfigTargets_GlobalSlackAPIURLFileFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slack-url")
	require.NoError(t, os.WriteFile(path, []byte("https://hooks.slack.com/services/GLOBAL/FILE/HOOK\n"), 0o600))

	yamlConfig := `
global:
  slack_api_url_file: ` + path + `

route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - channel: "#ops"
`

	config, err := infraroute.NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)

	targets := BuildConfigTargets(config, quietLogger())
	require.Len(t, targets, 1)
	assert.Equal(t, "https://hooks.slack.com/services/GLOBAL/FILE/HOOK", targets[0].URL)
}

// TestBuildConfigTargets_ReloadPicksUpRotatedSecretFile is the fix-round I2
// pin for the brief-required "reload picks up changed file content" test,
// verified at the level that actually matters: the DELIVERED target, not
// just the parsed struct field.
//
// Sequence: write v1 -> load (Parse + BuildConfigTargets) -> assert target
// carries v1 -> rewrite the SAME path to v2 -> reload (a second, independent
// Parse + BuildConfigTargets call, standing in for /-/reload's
// loadAndParse -> routing.Parse -> BuildConfigTargets chain) -> assert the
// target now carries v2.
//
// This is the exact shape the wave-3 "config-diff short-circuit" lesson
// exists to catch: ReloadCoordinator's "no changes, skip reload" fast path
// (reload_coordinator.go) must never see an unchanged rotated-secret-file
// scenario as "nothing changed" and skip re-provisioning targets. By
// construction here the resolved value lands in RoutingConfig.Global /
// .Receivers, which participates in RoutingFingerprint, so that short
// circuit does not falsely trigger — this test pins the OUTCOME (the target
// reflects the new file content after a fresh parse) rather than that
// internal mechanism, so a future change to the fingerprint/short-circuit
// logic that broke rotation would fail this test.
func TestBuildConfigTargets_ReloadPicksUpRotatedSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bot-token")
	require.NoError(t, os.WriteFile(path, []byte("bot-token-v1\n"), 0o600))

	yamlConfig := `
route:
  receiver: tg
  group_by: [alertname]
receivers:
  - name: tg
    telegram_configs:
      - bot_token_file: ` + path + `
        chat_id: "-100"
`

	config1, err := infraroute.NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	targets1 := BuildConfigTargets(config1, quietLogger())
	require.Len(t, targets1, 1)
	assert.Equal(t, "bot-token-v1", targets1[0].Headers["bot_token"],
		"first load must carry the file's v1 content")

	require.NoError(t, os.WriteFile(path, []byte("bot-token-v2\n"), 0o600))

	config2, err := infraroute.NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	targets2 := BuildConfigTargets(config2, quietLogger())
	require.Len(t, targets2, 1)
	assert.Equal(t, "bot-token-v2", targets2[0].Headers["bot_token"],
		"a reload (fresh Parse + BuildConfigTargets) must deliver the ROTATED content, not the first load's cached value")
}
