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

// ============================================================================
// FU-HTTP-CONFIG: http_config credentials must not leak through /api/v2/status
// ============================================================================
//
// `http_config` (wave 7 track C) adds three new places a credential can sit in
// the Alertmanager config document this unauthenticated endpoint renders:
// basic_auth.password, authorization.credentials and oauth2.client_secret — at
// `global:` scope AND inside every integration, since global.http_config is
// resolved into each integration at parse time.
//
// The substring-based redaction in status_config.go was designed to cover new
// integration fields by DEFAULT rather than needing a list update per feature.
// This test proves it actually does for these fields, in both scopes, rather
// than asserting it from the shape of the substring list.

const httpConfigSecretYAML = `
global:
  http_config:
    proxy_url: http://proxy.corp:8080
    basic_auth:
      username: amp-global
      password: sup3r-secret-global-basic-pw

route:
  receiver: default

receivers:
  - name: default
    webhook_configs:
      - url: https://hooks.example.com/ingest
        http_config:
          proxy_url: http://proxy.other:3128
          tls_config:
            ca_file: /etc/ssl/ca.pem
            server_name: hooks.internal
            insecure_skip_verify: false
          basic_auth:
            username: amp-webhook
            password: sup3r-secret-webhook-basic-pw
  - name: bearer
    webhook_configs:
      - url: https://hooks.example.com/bearer
        http_config:
          authorization:
            type: Bearer
            credentials: sup3r-secret-bearer-credential
  - name: oauth
    webhook_configs:
      - url: https://hooks.example.com/oauth
        http_config:
          oauth2:
            client_id: amp-client
            client_secret: sup3r-secret-oauth-client-secret
            token_url: https://idp.example.com/token
`

func loadHTTPConfigStatusConfig(t *testing.T) *appconfig.Config {
	t.Helper()
	parsed, err := infraroute.NewRouteConfigParser().Parse([]byte(httpConfigSecretYAML))
	require.NoError(t, err)
	return &appconfig.Config{Routing: parsed}
}

func TestAlertmanagerConfigYAML_RedactsHTTPConfigCredentials(t *testing.T) {
	out := AlertmanagerConfigYAML(loadHTTPConfigStatusConfig(t))

	for _, secret := range []string{
		"sup3r-secret-global-basic-pw",
		"sup3r-secret-webhook-basic-pw",
		"sup3r-secret-bearer-credential",
		"sup3r-secret-oauth-client-secret",
	} {
		assert.NotContains(t, out, secret,
			"http_config credential %q leaked into the unauthenticated /api/v2/status payload", secret)
	}

	assert.Contains(t, out, RedactedSecretPlaceholder,
		"credentials must be redacted in place, not silently dropped")
}

// The redaction must not blank the whole block: a config whose http_config is
// unreadable in the status output cannot be diagnosed.
func TestAlertmanagerConfigYAML_KeepsHTTPConfigDiagnostics(t *testing.T) {
	out := AlertmanagerConfigYAML(loadHTTPConfigStatusConfig(t))

	for _, visible := range []string{
		"proxy_url",
		"http://proxy.corp:8080",
		"http://proxy.other:3128",
		"amp-global",  // basic_auth.username is not a credential
		"amp-webhook", // ditto
		"hooks.internal",
		"/etc/ssl/ca.pem",
	} {
		assert.Contains(t, out, visible,
			"non-secret http_config field %q must stay visible for diagnosis", visible)
	}
}

// Whatever the redaction pass emits must still be a parseable Alertmanager
// config shape — amtool re-parses this field.
func TestAlertmanagerConfigYAML_HTTPConfigOutputStillParses(t *testing.T) {
	out := AlertmanagerConfigYAML(loadHTTPConfigStatusConfig(t))
	require.False(t, strings.HasPrefix(strings.TrimSpace(out), "#"),
		"rendering must not degrade to a comment: %q", out)

	var reparsed infraroute.RouteConfig
	require.NoError(t, yaml.Unmarshal([]byte(out), &reparsed))
	require.NotNil(t, reparsed.Global)
	require.NotNil(t, reparsed.Global.HTTPConfig)
	require.Len(t, reparsed.Receivers, 3)

	hc := reparsed.Receivers[0].WebhookConfigs[0].HTTPConfig
	require.NotNil(t, hc, "the http_config block must survive redaction as a block")
	require.NotNil(t, hc.BasicAuth)
	assert.Equal(t, "amp-webhook", hc.BasicAuth.Username)
	assert.Equal(t, RedactedSecretPlaceholder, hc.BasicAuth.Password)
}

// Explicit coverage of isSecretKey for the new field names, so a future edit to
// secretKeySubstrings that drops one of these fails here rather than in
// production.
func TestIsSecretKey_HTTPConfigFields(t *testing.T) {
	for _, key := range []string{
		"password", "password_file",
		"credentials", "credentials_file",
		"client_secret", "client_secret_file",
	} {
		assert.True(t, isSecretKey(key, "http_config"),
			"http_config.%s must be treated as a secret", key)
	}

	// These are not credentials and must stay readable.
	for _, key := range []string{
		"proxy_url", "ca_file", "server_name", "insecure_skip_verify",
		"follow_redirects", "username", "type", "client_id",
	} {
		assert.False(t, isSecretKey(key, "http_config"),
			"http_config.%s is not a credential and must stay visible", key)
	}

	// ACKNOWLEDGED OVER-REDACTION, asserted so it is a documented decision
	// rather than a surprise: these three are PATHS or a public endpoint, not
	// credentials, but they match the always-secret substrings ("cert",
	// "key_file", "token"). Loosening the substring list to spare them would
	// weaken a public unauthenticated endpoint's default-deny posture for every
	// future integration field, which is a far worse trade than a redacted path
	// in the status view. Redaction never affects DELIVERY — the publisher reads
	// the real config, not this rendering.
	for _, key := range []string{"cert_file", "key_file", "token_url"} {
		assert.True(t, isSecretKey(key, "http_config"),
			"http_config.%s is redacted by the conservative substring policy", key)
	}
}
