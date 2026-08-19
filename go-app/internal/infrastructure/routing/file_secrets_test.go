package routing

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSecretFile writes content to a fresh file under t.TempDir() and
// returns its path.
func writeSecretFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// ============================================================================
// resolveFileSecret (unit level)
// ============================================================================

func TestResolveFileSecret_InlineOnly(t *testing.T) {
	got, err := resolveFileSecret("inline-value", "")
	require.NoError(t, err)
	assert.Equal(t, "inline-value", got)
}

func TestResolveFileSecret_NeitherSet(t *testing.T) {
	got, err := resolveFileSecret("", "")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestResolveFileSecret_FileOnly_TrimsTrailingWhitespace(t *testing.T) {
	path := writeSecretFile(t, "file-secret-value\n")
	got, err := resolveFileSecret("", path)
	require.NoError(t, err)
	assert.Equal(t, "file-secret-value", got, "trailing newline must be trimmed")
}

func TestResolveFileSecret_FileOnly_TrimsTrailingSpacesAndCRLF(t *testing.T) {
	path := writeSecretFile(t, "file-secret-value \r\n")
	got, err := resolveFileSecret("", path)
	require.NoError(t, err)
	assert.Equal(t, "file-secret-value", got)
}

func TestResolveFileSecret_LeadingWhitespacePreserved(t *testing.T) {
	// Not upstream's documented behavior to strip leading whitespace - only
	// trailing. A leading-whitespace credential is unusual but not this
	// function's business to silently rewrite.
	path := writeSecretFile(t, "  file-secret-value\n")
	got, err := resolveFileSecret("", path)
	require.NoError(t, err)
	assert.Equal(t, "  file-secret-value", got)
}

func TestResolveFileSecret_BothSet_IsError(t *testing.T) {
	path := writeSecretFile(t, "file-secret-value")
	_, err := resolveFileSecret("inline-value", path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")
}

func TestResolveFileSecret_MissingFile_IsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := resolveFileSecret("", missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), missing, "error must name the PATH, not any secret content")
}

// ============================================================================
// Per-integration: file variant produces an identical target/parse result to
// the inline variant, both-set is a load error, missing file is a load error
// naming the path.
// ============================================================================

func TestParse_Webhook_URLFile_MatchesInline(t *testing.T) {
	path := writeSecretFile(t, "https://webhook.example.com/alerts\n")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: hook
  group_by: [alertname]
receivers:
  - name: hook
    webhook_configs:
      - url_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "https://webhook.example.com/alerts", config.Receivers[0].WebhookConfigs[0].URL)
	assert.Equal(t, path, config.Receivers[0].WebhookConfigs[0].URLFile, "the path itself must survive resolution")
}

func TestParse_Webhook_URLAndURLFile_BothSet_Fails(t *testing.T) {
	path := writeSecretFile(t, "https://webhook.example.com/alerts")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: hook
  group_by: [alertname]
receivers:
  - name: hook
    webhook_configs:
      - url: https://webhook.example.com/other
        url_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "webhook_configs[0].url")
	assert.Contains(t, err.Error(), "exactly one")
}

func TestParse_Webhook_URLFile_MissingFile_Fails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: hook
  group_by: [alertname]
receivers:
  - name: hook
    webhook_configs:
      - url_file: %s
`, missing)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), missing)
}

func TestParse_PagerDuty_RoutingKeyFile_MatchesInline(t *testing.T) {
	path := writeSecretFile(t, "rk-from-file\n")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: pd
  group_by: [alertname]
receivers:
  - name: pd
    pagerduty_configs:
      - routing_key_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "rk-from-file", config.Receivers[0].PagerDutyConfigs[0].RoutingKey)
}

func TestParse_PagerDuty_RoutingKeyAndFile_BothSet_Fails(t *testing.T) {
	path := writeSecretFile(t, "rk-from-file")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: pd
  group_by: [alertname]
receivers:
  - name: pd
    pagerduty_configs:
      - routing_key: rk-inline
        routing_key_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "pagerduty_configs[0].routing_key")
}

func TestParse_PagerDuty_RoutingKeyFile_MissingFile_Fails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: pd
  group_by: [alertname]
receivers:
  - name: pd
    pagerduty_configs:
      - routing_key_file: %s
`, missing)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), missing)
}

func TestParse_Slack_APIURLFile_MatchesInline(t *testing.T) {
	path := writeSecretFile(t, "https://hooks.slack.com/services/FILE/HOOK/URL\n")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - api_url_file: %s
        channel: "#ops"
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/services/FILE/HOOK/URL", config.Receivers[0].SlackConfigs[0].APIURL)
}

func TestParse_Slack_APIURLAndFile_BothSet_Fails(t *testing.T) {
	path := writeSecretFile(t, "https://hooks.slack.com/services/FILE/HOOK/URL")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - api_url: https://hooks.slack.com/services/INLINE/HOOK/URL
        api_url_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "slack_configs[0].api_url")
}

func TestParse_Slack_APIURLFile_MissingFile_Fails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - api_url_file: %s
`, missing)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), missing)
}

func TestParse_Telegram_BotTokenFile_MatchesInline(t *testing.T) {
	path := writeSecretFile(t, "bot-token-from-file\n")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: tg
  group_by: [alertname]
receivers:
  - name: tg
    telegram_configs:
      - bot_token_file: %s
        chat_id: "-100"
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "bot-token-from-file", config.Receivers[0].TelegramConfigs[0].BotToken)
}

func TestParse_Telegram_BotTokenAndFile_BothSet_Fails(t *testing.T) {
	path := writeSecretFile(t, "bot-token-from-file")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: tg
  group_by: [alertname]
receivers:
  - name: tg
    telegram_configs:
      - bot_token: bot-token-inline
        bot_token_file: %s
        chat_id: "-100"
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "telegram_configs[0].bot_token")
}

func TestParse_Telegram_BotTokenFile_MissingFile_Fails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: tg
  group_by: [alertname]
receivers:
  - name: tg
    telegram_configs:
      - bot_token_file: %s
        chat_id: "-100"
`, missing)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), missing)
}

// ============================================================================
// Global fallback interaction (requirement 5): global.slack_api_url_file
// participates in the same fallback chain as global.slack_api_url.
// ============================================================================

func TestParse_GlobalSlackAPIURLFile_FallsThroughToReceiver(t *testing.T) {
	path := writeSecretFile(t, "https://hooks.slack.com/services/GLOBAL/FILE/HOOK\n")
	yamlConfig := fmt.Sprintf(`
global:
  slack_api_url_file: %s

route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - channel: "#ops"
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/services/GLOBAL/FILE/HOOK", config.Receivers[0].SlackConfigs[0].APIURL)
	assert.Equal(t, "https://hooks.slack.com/services/GLOBAL/FILE/HOOK", config.Global.SlackAPIURL)
}

func TestParse_PerIntegrationAPIURLFile_WinsOverGlobalFile(t *testing.T) {
	globalPath := writeSecretFile(t, "https://hooks.slack.com/services/GLOBAL/FILE/HOOK")
	ownPath := writeSecretFile(t, "https://hooks.slack.com/services/OWN/FILE/HOOK")
	yamlConfig := fmt.Sprintf(`
global:
  slack_api_url_file: %s

route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - api_url_file: %s
`, globalPath, ownPath)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.slack.com/services/OWN/FILE/HOOK", config.Receivers[0].SlackConfigs[0].APIURL)
}

func TestParse_GlobalSlackAPIURLAndFile_BothSet_Fails(t *testing.T) {
	path := writeSecretFile(t, "https://hooks.slack.com/services/GLOBAL/FILE/HOOK")
	yamlConfig := fmt.Sprintf(`
global:
  slack_api_url: https://hooks.slack.com/services/GLOBAL/INLINE/HOOK
  slack_api_url_file: %s

route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - channel: "#ops"
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "global.slack_api_url")
}

func TestParse_GlobalSMTPAuthPasswordFile_Resolves(t *testing.T) {
	path := writeSecretFile(t, "smtp-pass-from-file\n")
	yamlConfig := fmt.Sprintf(`
global:
  smtp_smarthost: smtp.example.com:587
  smtp_from: %s
  smtp_auth_username: amp
  smtp_auth_password_file: %s

route:
  receiver: mail
  group_by: [alertname]
receivers:
  - name: mail
    email_configs:
      - to: %s
`, mailAddr("alerts", "example.com"), path, mailAddr("ops", "example.com"))

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "smtp-pass-from-file", config.Global.SMTPAuthPassword)
}

// ============================================================================
// Reload picks up changed file content: two successive Parse() calls against
// the SAME file path see the current content each time (AMP re-reads at
// load/reload rather than lazily per-publish — see file_secrets.go's
// package doc comment for the honesty note on this divergence from
// upstream).
// ============================================================================

func TestParse_FileSecret_ReloadPicksUpChangedContent(t *testing.T) {
	path := writeSecretFile(t, "bot-token-v1\n")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: tg
  group_by: [alertname]
receivers:
  - name: tg
    telegram_configs:
      - bot_token_file: %s
        chat_id: "-100"
`, path)

	parser := NewRouteConfigParser()

	config1, err := parser.Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "bot-token-v1", config1.Receivers[0].TelegramConfigs[0].BotToken)

	require.NoError(t, os.WriteFile(path, []byte("bot-token-v2\n"), 0o600))

	config2, err := parser.Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "bot-token-v2", config2.Receivers[0].TelegramConfigs[0].BotToken,
		"a reload (fresh Parse call) must re-read the file, not cache the first content")
}

// ============================================================================
// Clone must carry every new *_file field - a missed field here would
// silently drop it on any cloned/hot-reloaded config.
// ============================================================================

func TestClone_CarriesFileSecretFields(t *testing.T) {
	global := &GlobalConfig{SlackAPIURLFile: "/etc/amp/slack-url", SMTPAuthPasswordFile: "/etc/amp/smtp-pass"}
	cloneGlobal := global.Clone()
	assert.Equal(t, global.SlackAPIURLFile, cloneGlobal.SlackAPIURLFile)
	assert.Equal(t, global.SMTPAuthPasswordFile, cloneGlobal.SMTPAuthPasswordFile)

	webhook := &WebhookConfig{URL: "https://example.com", URLFile: "/etc/amp/url"}
	assert.Equal(t, webhook.URLFile, webhook.Clone().URLFile)

	pagerduty := &PagerDutyConfig{RoutingKey: "rk", RoutingKeyFile: "/etc/amp/rk"}
	assert.Equal(t, pagerduty.RoutingKeyFile, pagerduty.Clone().RoutingKeyFile)

	slack := &SlackConfig{APIURL: "https://hooks.slack.com/x", APIURLFile: "/etc/amp/slack"}
	assert.Equal(t, slack.APIURLFile, slack.Clone().APIURLFile)

	telegram := &TelegramConfig{BotToken: "tok", BotTokenFile: "/etc/amp/bot-token", ChatID: "-100"}
	assert.Equal(t, telegram.BotTokenFile, telegram.Clone().BotTokenFile)
}
