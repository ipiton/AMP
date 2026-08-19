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

// ServiceKey (legacy, still live as a routing-key fallback in
// config_targets.go's pagerDutyTarget) gets the same *_file twin upstream
// carries (fix round I1): service_key/service_key_file alongside
// routing_key/routing_key_file.

func TestParse_PagerDuty_ServiceKeyFile_MatchesInline(t *testing.T) {
	path := writeSecretFile(t, "sk-from-file\n")
	// RoutingKey carries validate:"required" unconditionally (a pre-existing
	// quirk, not introduced here: even an INLINE service_key-only
	// pagerduty_config already fails to load today, so a service_key-only
	// config was never reachable before this fix round either) — routing_key
	// is set here purely to get past that gate, isolating the assertion to
	// ServiceKeyFile's own resolution.
	yamlConfig := fmt.Sprintf(`
route:
  receiver: pd
  group_by: [alertname]
receivers:
  - name: pd
    pagerduty_configs:
      - routing_key: rk-placeholder
        service_key_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)
	assert.Equal(t, "sk-from-file", config.Receivers[0].PagerDutyConfigs[0].ServiceKey)
}

func TestParse_PagerDuty_ServiceKeyAndFile_BothSet_Fails(t *testing.T) {
	path := writeSecretFile(t, "sk-from-file")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: pd
  group_by: [alertname]
receivers:
  - name: pd
    pagerduty_configs:
      - routing_key: rk-inline
        service_key: sk-inline
        service_key_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "pagerduty_configs[0].service_key")
	assert.Contains(t, err.Error(), "exactly one")
}

func TestParse_PagerDuty_ServiceKeyFile_MissingFile_Fails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: pd
  group_by: [alertname]
receivers:
  - name: pd
    pagerduty_configs:
      - service_key_file: %s
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
// Empty/whitespace-only *_file content against a `required` inline field
// (fix round M1, brief scrutiny point 4). resolveFileSecret trims trailing
// whitespace only, so a file holding nothing but blank lines/spaces resolves
// to "" - the SAME as the field never having been set at all - and the
// inline field's own `validate:"required"` tag must reject it with a clear
// error naming the field, not a confusing pass or a silent empty credential.
// ============================================================================

func TestParse_FileSecret_WhitespaceOnlyFile_FailsRequiredValidation(t *testing.T) {
	tests := []struct {
		name          string
		yamlTemplate  string
		wantFieldPath string
	}{
		{
			name: "webhook url_file",
			yamlTemplate: `
route:
  receiver: hook
  group_by: [alertname]
receivers:
  - name: hook
    webhook_configs:
      - url_file: %s
`,
			wantFieldPath: "WebhookConfigs[0].URL",
		},
		{
			name: "pagerduty routing_key_file",
			yamlTemplate: `
route:
  receiver: pd
  group_by: [alertname]
receivers:
  - name: pd
    pagerduty_configs:
      - routing_key_file: %s
`,
			wantFieldPath: "PagerDutyConfigs[0].RoutingKey",
		},
		{
			name: "telegram bot_token_file",
			yamlTemplate: `
route:
  receiver: tg
  group_by: [alertname]
receivers:
  - name: tg
    telegram_configs:
      - bot_token_file: %s
        chat_id: "-100"
`,
			wantFieldPath: "TelegramConfigs[0].BotToken",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSecretFile(t, "   \n\t\n   \n")
			yamlConfig := fmt.Sprintf(tc.yamlTemplate, path)

			config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
			require.Error(t, err, "a whitespace-only secret file must resolve to empty and trip the required tag, not silently load")
			assert.Nil(t, config)
			assert.Contains(t, err.Error(), tc.wantFieldPath,
				"the error must clearly name the field left empty by the whitespace-only file")
			assert.Contains(t, err.Error(), "required")
		})
	}
}

// Slack's api_url is OPTIONAL at the field level (it can fall back to
// global.slack_api_url), so a whitespace-only api_url_file takes the OTHER
// documented path: validateReceiverEndpoints' "no api_url and no global
// slack_api_url fallback" error, not a struct-tag `required` failure.
func TestParse_FileSecret_WhitespaceOnlySlackAPIURLFile_FailsEndpointCheck(t *testing.T) {
	path := writeSecretFile(t, "   \n")
	yamlConfig := fmt.Sprintf(`
route:
  receiver: slack
  group_by: [alertname]
receivers:
  - name: slack
    slack_configs:
      - api_url_file: %s
`, path)

	config, err := NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.Error(t, err)
	assert.Nil(t, config)
	assert.Contains(t, err.Error(), "no api_url and no global slack_api_url")
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

	pagerduty := &PagerDutyConfig{
		RoutingKey:     "rk",
		RoutingKeyFile: "/etc/amp/rk",
		ServiceKey:     "sk",
		ServiceKeyFile: "/etc/amp/sk",
	}
	pagerdutyClone := pagerduty.Clone()
	assert.Equal(t, pagerduty.RoutingKeyFile, pagerdutyClone.RoutingKeyFile)
	assert.Equal(t, pagerduty.ServiceKeyFile, pagerdutyClone.ServiceKeyFile)

	slack := &SlackConfig{APIURL: "https://hooks.slack.com/x", APIURLFile: "/etc/amp/slack"}
	assert.Equal(t, slack.APIURLFile, slack.Clone().APIURLFile)

	telegram := &TelegramConfig{BotToken: "tok", BotTokenFile: "/etc/amp/bot-token", ChatID: "-100"}
	assert.Equal(t, telegram.BotTokenFile, telegram.Clone().BotTokenFile)
}
