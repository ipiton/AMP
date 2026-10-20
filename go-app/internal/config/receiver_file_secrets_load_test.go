package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSecretFile writes one credential file the way a mounted Kubernetes
// Secret holds it (`kubectl create secret --from-file`, often with a trailing
// newline) and returns its absolute path.
func writeSecretFile(t *testing.T, dir, name, value string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(value+"\n"), 0o400))
	return path
}

// TestLoadConfig_ReceiverSecretsFromFiles is HELM-EXTRA-VOLUMES review F1
// through the real LoadConfig path: pkg/configvalidator runs before
// routing.Parse, and before the fix it had no *_file fields, so a config that
// took its Slack URL from api_url_file failed startup with E115 although the
// loader could read the file. Every *_file twin must now load, with the file
// content (trailing newline trimmed) in the resolved receiver.
func TestLoadConfig_ReceiverSecretsFromFiles(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")
	// The resolved URLs must also pass the loader's https_production check.
	t.Setenv("ENVIRONMENT", "")

	dir := t.TempDir()
	slackGlobal := writeSecretFile(t, dir, "slack-global", "https://example.com/slack-global")
	slackOwn := writeSecretFile(t, dir, "slack-own", "https://example.com/slack-own")
	webhook := writeSecretFile(t, dir, "webhook-url", "https://example.com/webhook")
	routingKey := writeSecretFile(t, dir, "pd-routing-key", "fixture-routing-key")
	botToken := writeSecretFile(t, dir, "tg-bot-token", "123456:fixture-bot-token")

	path := writeTempYAML(t, `
global:
  slack_api_url_file: `+slackGlobal+`
route:
  receiver: slack
  routes:
    - receiver: other
      matchers: ['team="other"']
receivers:
  - name: slack
    slack_configs:
      - channel: '#global'
      - api_url_file: `+slackOwn+`
        channel: '#own'
  - name: other
    webhook_configs:
      - url_file: `+webhook+`
    pagerduty_configs:
      - routing_key_file: `+routingKey+`
    telegram_configs:
      - bot_token_file: `+botToken+`
        chat_id: "-1001234567890"
`)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)
	require.Len(t, cfg.Routing.Receivers, 2)

	slack, other := cfg.Routing.Receivers[0], cfg.Routing.Receivers[1]
	require.Len(t, slack.SlackConfigs, 2)
	assert.Equal(t, "https://example.com/slack-global", slack.SlackConfigs[0].APIURL, "global.slack_api_url_file fallback")
	assert.Equal(t, "https://example.com/slack-own", slack.SlackConfigs[1].APIURL, "api_url_file")
	require.Len(t, other.WebhookConfigs, 1)
	assert.Equal(t, "https://example.com/webhook", other.WebhookConfigs[0].URL, "url_file")
	require.Len(t, other.PagerDutyConfigs, 1)
	assert.Equal(t, "fixture-routing-key", other.PagerDutyConfigs[0].RoutingKey, "routing_key_file")
	require.Len(t, other.TelegramConfigs, 1)
	assert.Equal(t, "123456:fixture-bot-token", other.TelegramConfigs[0].BotToken, "bot_token_file")
}

// TestLoadConfig_ReceiverSecretMissing keeps the required-value errors: with
// neither the value nor its *_file twin, startup still fails with the
// validator's code.
func TestLoadConfig_ReceiverSecretMissing(t *testing.T) {
	tests := []struct {
		name     string
		receiver string
		wantCode string
	}{
		{"webhook", "webhook_configs:\n      - send_resolved: true", "E113"},
		{"slack", "slack_configs:\n      - channel: '#alerts'", "E115"},
		{"pagerduty", "pagerduty_configs:\n      - send_resolved: true", "E122"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetViper()
			unsetEnvKeys("SERVER_PORT")

			path := writeTempYAML(t, `
route:
  receiver: default
receivers:
  - name: default
    `+tt.receiver+`
`)

			cfg, err := LoadConfig(path)
			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tt.wantCode)
		})
	}
}
