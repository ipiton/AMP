package handlers

import (
	"strings"
	"testing"

	appconfig "github.com/ipiton/AMP/internal/config"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Final review finding 15: /api/v2/status's config.original returned the RAW
// config file — unauthenticated, unredacted (database/redis passwords, LLM API
// keys, webhook secrets) — and in the wrong shape for `amtool config routes
// show`, which re-parses this field as an Alertmanager config.

// secretBearingConfigYAML is a full AMP config: an Alertmanager section with
// secrets inside it, plus AMP-only sections whose credentials must never appear
// in the status payload at all.
const secretBearingConfigYAML = `
app:
  name: amp
  environment: development
server:
  port: 9093

database:
  driver: postgres
  host: db.internal
  password: sup3r-secret-db-pw

redis:
  addr: redis.internal:6379
  password: sup3r-secret-redis-pw

route:
  receiver: default
  group_by: [alertname, cluster]
  group_wait: 30s
  routes:
    - receiver: pager
      matchers: ['severity="critical"']

receivers:
  - name: default
    slack_configs:
      - api_url: https://hooks.slack.com/services/T000/B000/sup3r-secret-slack
        channel: "#alerts"
  - name: pager
    pagerduty_configs:
      # 32 chars: AMP's PagerDutyConfig.RoutingKey carries validate:"len=32"
      - routing_key: sup3rsecretpdkey0123456789abcdef
    telegram_configs:
      - bot_token: 123456:sup3r-secret-bot-token
        chat_id: "-1001234567890"

inhibit_rules:
  - source_match:
      severity: critical
    target_match:
      severity: warning
    equal: [alertname]

time_intervals:
  - name: business-hours
    time_intervals:
      - weekdays: ['monday:friday']
`

// allSecrets are the literal credential values that must not appear anywhere in
// the rendered payload.
var allSecrets = []string{
	"sup3r-secret-db-pw",
	"sup3r-secret-redis-pw",
	"sup3r-secret-slack",
	"sup3rsecretpdkey0123456789abcdef",
	"sup3r-secret-bot-token",
}

func loadStatusTestConfig(t *testing.T) *appconfig.Config {
	t.Helper()
	parsed, err := infraroute.NewRouteConfigParser().Parse([]byte(secretBearingConfigYAML))
	require.NoError(t, err)
	return &appconfig.Config{Routing: parsed}
}

func TestAlertmanagerConfigYAML_RedactsEverySecret(t *testing.T) {
	out := AlertmanagerConfigYAML(loadStatusTestConfig(t))

	for _, secret := range allSecrets {
		assert.NotContains(t, out, secret,
			"secret %q leaked into the unauthenticated /api/v2/status payload", secret)
	}
	assert.Contains(t, out, RedactedSecretPlaceholder,
		"secret-bearing fields must be present but redacted, not silently dropped")

	// No AMP-only section may appear at all — the payload is the Alertmanager
	// config, nothing else.
	for _, section := range []string{"database:", "redis:", "server:", "app:", "llm:"} {
		assert.NotContains(t, out, section, "non-Alertmanager section %q must not be exposed", section)
	}
}

// TestAlertmanagerConfigYAML_ParsesAsAlertmanagerConfig is the parity half: the
// output must deserialize into the Alertmanager config SHAPE that
// `amtool config routes show` re-parses.
//
// Deliberately a plain yaml.Unmarshal into RouteConfig rather than
// RouteConfigParser.Parse: Parse additionally runs AMP's own structural
// validators, some of which are stricter than upstream's. PagerDutyConfig
// .RoutingKey carries validate:"len=32" whereas upstream types it as a plain
// `Secret` with no length rule — so ANY redaction placeholder necessarily fails
// AMP's rule while remaining perfectly valid upstream. Asserting shape (which is
// what parity means here) rather than AMP's stricter validation keeps this test
// about finding 15 instead of about that unrelated validator.
func TestAlertmanagerConfigYAML_ParsesAsAlertmanagerConfig(t *testing.T) {
	out := AlertmanagerConfigYAML(loadStatusTestConfig(t))

	var reparsed infraroute.RouteConfig
	err := yaml.Unmarshal([]byte(out), &reparsed)
	require.NoError(t, err, "the emitted config must deserialize as an Alertmanager config:\n%s", out)

	require.NotNil(t, reparsed.Route)
	assert.Equal(t, "default", reparsed.Route.Receiver)
	assert.Equal(t, []string{"alertname", "cluster"}, reparsed.Route.GroupBy)
	require.Len(t, reparsed.Route.Routes, 1, "the child route must survive so `amtool config routes` sees the real tree")
	assert.Equal(t, "pager", reparsed.Route.Routes[0].Receiver)

	names := make([]string, 0, len(reparsed.Receivers))
	for _, r := range reparsed.Receivers {
		names = append(names, r.Name)
	}
	assert.ElementsMatch(t, []string{"default", "pager"}, names)

	require.Len(t, reparsed.InhibitRules, 1, "inhibit_rules must survive")
	require.Len(t, reparsed.TimeIntervals, 1, "time_intervals must survive")
	assert.Equal(t, "business-hours", reparsed.TimeIntervals[0].Name)
}

// TestAlertmanagerConfigYAML_KeepsNonSecretStructure guards against
// over-redaction: routing decisions must stay inspectable.
func TestAlertmanagerConfigYAML_KeepsNonSecretStructure(t *testing.T) {
	out := AlertmanagerConfigYAML(loadStatusTestConfig(t))

	assert.Contains(t, out, "#alerts", "a Slack channel is not a secret and must stay visible")
	assert.Contains(t, out, "-1001234567890", "a Telegram chat_id is not a secret and must stay visible")
	assert.Contains(t, out, `severity="critical"`, "route matchers must stay visible")
}

func TestAlertmanagerConfigYAML_NoRoutingTreeSynthesizesParsableDoc(t *testing.T) {
	out := AlertmanagerConfigYAML(&appconfig.Config{
		Receivers: []appconfig.ReceiverConfig{{Name: "ops"}, {Name: "paging"}},
	})

	var doc struct {
		Route     map[string]any   `yaml:"route"`
		Receivers []map[string]any `yaml:"receivers"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(out), &doc), "output must be valid YAML:\n%s", out)
	assert.Equal(t, "ops", doc.Route["receiver"])
	require.Len(t, doc.Receivers, 2)
	assert.Equal(t, "ops", doc.Receivers[0]["name"])
	assert.Equal(t, "paging", doc.Receivers[1]["name"])
}

func TestAlertmanagerConfigYAML_NilConfig(t *testing.T) {
	out := AlertmanagerConfigYAML(nil)
	assert.True(t, strings.HasPrefix(out, "#"), "must degrade to a YAML comment, not empty output: %q", out)

	var anything any
	require.NoError(t, yaml.Unmarshal([]byte(out), &anything))
}

func TestRedactSecrets_DescendsIntoSecretNamedContainers(t *testing.T) {
	doc := map[string]any{
		"http_config": map[string]any{
			"authorization": map[string]any{
				"type":        "Bearer",
				"credentials": "leak-me",
			},
		},
		"tokens": []any{
			map[string]any{"api_key": "leak-me-too", "name": "keep-me"},
		},
	}

	redactSecrets(doc)

	auth := doc["http_config"].(map[string]any)["authorization"].(map[string]any)
	assert.Equal(t, "Bearer", auth["type"], "structure under a secret-named container must stay visible")
	assert.Equal(t, RedactedSecretPlaceholder, auth["credentials"])

	entry := doc["tokens"].([]any)[0].(map[string]any)
	assert.Equal(t, RedactedSecretPlaceholder, entry["api_key"])
	assert.Equal(t, "keep-me", entry["name"])
}

func TestIsSecretKey(t *testing.T) {
	for _, key := range []string{
		"password", "auth_password", "api_key", "apiKey", "bot_token",
		"routing_key", "service_key", "api_url", "webhook_url",
		"credentials", "api_key_file", "authorization", "bearer_token",
	} {
		assert.True(t, isSecretKey(key), "%q must be treated as secret-bearing", key)
	}

	for _, key := range []string{"name", "channel", "chat_id", "severity", "group_by", "send_resolved"} {
		assert.False(t, isSecretKey(key), "%q is not a secret and must stay visible", key)
	}
}
