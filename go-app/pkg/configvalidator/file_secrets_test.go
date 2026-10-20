package configvalidator

import "testing"

// TestValidator_FileSecretKeys runs the strict parser (unknown keys are
// errors) and every validator over a config whose receiver secrets all come
// from *_file fields (FU7-B). Before HELM-EXTRA-VOLUMES the types had no such
// fields, so the strict parser rejected the keys and E113/E115/E122 fired.
func TestValidator_FileSecretKeys(t *testing.T) {
	data := []byte(`
global:
  slack_api_url_file: /etc/amp/secrets/slack-webhook-url
  smtp_auth_password_file: /etc/amp/secrets/smtp-password
route:
  receiver: slack
  routes:
    - receiver: telegram
      matchers: ['severity="critical"']
    - receiver: pagerduty
      matchers: ['team="db"']
    - receiver: webhook
      matchers: ['team="web"']
receivers:
  - name: slack
    slack_configs:
      - channel: '#alerts'
      - api_url_file: /etc/amp/secrets/slack-webhook-url-2
        channel: '#alerts-2'
  - name: telegram
    telegram_configs:
      - bot_token_file: /etc/amp/secrets/telegram-bot-token
        chat_id: -1001234567890
  - name: pagerduty
    pagerduty_configs:
      - routing_key_file: /etc/amp/secrets/pagerduty-routing-key
  - name: webhook
    webhook_configs:
      - url_file: /etc/amp/secrets/webhook-url
`)

	v := New(strictOptions())
	result, err := v.ValidateBytes(data)
	if err != nil {
		t.Fatalf("ValidateBytes returned error: %v", err)
	}
	if result.HasErrors() {
		t.Fatalf("expected no errors, got %+v", result.Errors)
	}
}
