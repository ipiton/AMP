package handlers

import (
	"os"
	"path/filepath"
	"testing"

	appconfig "github.com/ipiton/AMP/internal/config"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAlertmanagerConfigYAML_FileSecretPathVisibleContentRedacted is the
// FU7-B acceptance test for requirement 4: the status API must show the
// `*_file` field's PATH (harmless — a filesystem path leaks nothing an
// attacker can use) while never leaking the secret CONTENT it resolves to.
func TestAlertmanagerConfigYAML_FileSecretPathVisibleContentRedacted(t *testing.T) {
	dir := t.TempDir()
	botTokenPath := filepath.Join(dir, "telegram-bot-token")
	botTokenContent := "sup3r-secret-bot-token-from-file"
	require.NoError(t, os.WriteFile(botTokenPath, []byte(botTokenContent+"\n"), 0o600))

	yamlConfig := `
route:
  receiver: tg
  group_by: [alertname]
receivers:
  - name: tg
    telegram_configs:
      - bot_token_file: ` + botTokenPath + `
        chat_id: "-1001234567890"
`

	parsed, err := infraroute.NewRouteConfigParser().Parse([]byte(yamlConfig))
	require.NoError(t, err)

	out := AlertmanagerConfigYAML(&appconfig.Config{Routing: parsed})

	assert.Contains(t, out, botTokenPath, "the *_file field's PATH must stay visible")
	assert.NotContains(t, out, botTokenContent, "the resolved secret CONTENT must never leak into the status payload")
	assert.Contains(t, out, RedactedSecretPlaceholder,
		"the resolved bot_token field must still be present but redacted, not silently dropped")
}

// TestIsSecretKey_FileSecretRefKeysStayVisible locks in the exception list
// isSecretKey consults before the substring/section rules: several *_file
// key names would otherwise trip an unrelated substring by coincidence
// (routing_key_file contains "routing_key", bot_token_file contains "token")
// and get redacted right along with the content they name.
func TestIsSecretKey_FileSecretRefKeysStayVisible(t *testing.T) {
	for _, key := range []string{
		"api_url_file", "routing_key_file", "bot_token_file",
		"url_file", "slack_api_url_file", "smtp_auth_password_file",
	} {
		assert.False(t, isSecretKey(key, ""), "%q is a path, not a secret, and must stay visible", key)
		assert.False(t, isSecretKey(key, "slack_configs"), "%q must stay visible in any section", key)
		assert.False(t, isSecretKey(key, "global"), "%q must stay visible in any section", key)
	}
}
