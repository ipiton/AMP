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
