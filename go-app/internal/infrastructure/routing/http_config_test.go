package routing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FU-HTTP-CONFIG: `global.http_config` fills a gap, and a per-integration
// block replaces it WHOLESALE — upstream Alertmanager assigns the global block
// only when the integration's own is nil, so it never deep-merges.
func TestResolveGlobalFallbacks_HTTPConfig(t *testing.T) {
	globalTLS := &TLSConfig{CAFile: "/global-ca.pem", ServerName: "global.example.com"}
	global := &GlobalConfig{
		HTTPConfig: &HTTPConfig{
			ProxyURL:  "http://global-proxy:8080",
			TLSConfig: globalTLS,
			BasicAuth: &BasicAuth{Username: "global-user", Password: "gp"},
		},
	}

	cfg := &RouteConfig{
		Global: global,
		Receivers: []*Receiver{
			{
				Name: "inherits",
				WebhookConfigs: []*WebhookConfig{
					{URL: "https://hooks.example.com/a"},
				},
				SlackConfigs:     []*SlackConfig{{APIURL: "https://hooks.slack.com/services/a"}},
				PagerDutyConfigs: []*PagerDutyConfig{{RoutingKey: "rk"}},
				TelegramConfigs:  []*TelegramConfig{{BotToken: "bt", ChatID: "1"}},
			},
			{
				Name: "overrides",
				WebhookConfigs: []*WebhookConfig{
					// Sets ONLY proxy_url: upstream discards global's
					// tls_config/basic_auth entirely rather than merging.
					{URL: "https://hooks.example.com/b", HTTPConfig: &HTTPConfig{ProxyURL: "http://own-proxy:3128"}},
				},
			},
		},
	}

	resolveGlobalFallbacks(cfg)

	inherits := cfg.Receivers[0]
	for name, got := range map[string]*HTTPConfig{
		"webhook":   inherits.WebhookConfigs[0].HTTPConfig,
		"slack":     inherits.SlackConfigs[0].HTTPConfig,
		"pagerduty": inherits.PagerDutyConfigs[0].HTTPConfig,
		"telegram":  inherits.TelegramConfigs[0].HTTPConfig,
	} {
		require.NotNil(t, got, "%s must inherit global.http_config", name)
		assert.Equal(t, "http://global-proxy:8080", got.ProxyURL, "%s", name)
		require.NotNil(t, got.BasicAuth, "%s", name)
		assert.Equal(t, "global-user", got.BasicAuth.Username, "%s", name)

		// Each integration gets its OWN copy: the config is reloadable, and one
		// integration must not be able to mutate another's HTTP config.
		assert.NotSame(t, global.HTTPConfig, got, "%s must not share the global pointer", name)
		require.NotNil(t, got.TLSConfig)
		assert.NotSame(t, globalTLS, got.TLSConfig, "%s must not share global's tls_config", name)
	}

	own := cfg.Receivers[1].WebhookConfigs[0].HTTPConfig
	require.NotNil(t, own)
	assert.Equal(t, "http://own-proxy:3128", own.ProxyURL)
	assert.Nil(t, own.TLSConfig, "per-integration http_config replaces global WHOLESALE, no deep merge")
	assert.Nil(t, own.BasicAuth, "per-integration http_config replaces global WHOLESALE, no deep merge")
}

func TestResolveGlobalFallbacks_NoGlobalHTTPConfigLeavesNil(t *testing.T) {
	cfg := &RouteConfig{
		Global: &GlobalConfig{SlackAPIURL: "https://hooks.slack.com/services/g"},
		Receivers: []*Receiver{
			{Name: "r", WebhookConfigs: []*WebhookConfig{{URL: "https://hooks.example.com/a"}}},
		},
	}

	resolveGlobalFallbacks(cfg)
	assert.Nil(t, cfg.Receivers[0].WebhookConfigs[0].HTTPConfig,
		"no global.http_config must leave the integration's own nil, i.e. the publisher keeps its built-in client")
}

func TestHTTPConfig_ParsesBasicAuthAuthorizationAndOAuth2(t *testing.T) {
	yamlDoc := `
global:
  http_config:
    proxy_url: http://proxy.corp:8080
route:
  receiver: team
receivers:
  - name: team
    webhook_configs:
      - url: https://hooks.example.com/alerts
        http_config:
          proxy_url: http://proxy.other:3128
          follow_redirects: false
          tls_config:
            ca_file: /etc/ssl/ca.pem
            cert_file: /etc/ssl/client.pem
            key_file: /etc/ssl/client-key.pem
            server_name: hooks.internal
            insecure_skip_verify: true
          basic_auth:
            username: amp
            password_file: /etc/amp/webhook-pw
          authorization:
            type: Token
            credentials_file: /etc/amp/webhook-token
          oauth2:
            client_id: amp-client
            token_url: https://idp.example.com/token
            scopes: [alerts]
`

	parser := NewRouteConfigParser()
	cfg, err := parser.Parse([]byte(yamlDoc))
	require.NoError(t, err)

	hc := cfg.Receivers[0].WebhookConfigs[0].HTTPConfig
	require.NotNil(t, hc)
	assert.Equal(t, "http://proxy.other:3128", hc.ProxyURL, "per-integration wins over global")
	require.NotNil(t, hc.FollowRedirects)
	assert.False(t, *hc.FollowRedirects)

	require.NotNil(t, hc.TLSConfig)
	assert.Equal(t, "/etc/ssl/ca.pem", hc.TLSConfig.CAFile)
	assert.Equal(t, "/etc/ssl/client.pem", hc.TLSConfig.CertFile)
	assert.Equal(t, "/etc/ssl/client-key.pem", hc.TLSConfig.KeyFile)
	assert.Equal(t, "hooks.internal", hc.TLSConfig.ServerName)
	assert.True(t, hc.TLSConfig.InsecureSkipVerify)

	require.NotNil(t, hc.BasicAuth)
	assert.Equal(t, "amp", hc.BasicAuth.Username)
	assert.Equal(t, "/etc/amp/webhook-pw", hc.BasicAuth.PasswordFile)

	require.NotNil(t, hc.Authorization)
	assert.Equal(t, "Token", hc.Authorization.Type)
	assert.Equal(t, "/etc/amp/webhook-token", hc.Authorization.CredentialsFile)

	// oauth2 is PARSED so its presence can be reported; it is not honoured.
	require.NotNil(t, hc.OAuth2)
	assert.Equal(t, "amp-client", hc.OAuth2.ClientID)
	assert.Equal(t, []string{"alerts"}, hc.OAuth2.Scopes)
}

func TestHTTPConfig_CloneIsDeep(t *testing.T) {
	original := &HTTPConfig{
		ProxyURL:      "http://proxy:8080",
		TLSConfig:     &TLSConfig{CAFile: "/ca.pem"},
		BasicAuth:     &BasicAuth{Username: "u", Password: "p", PasswordFile: "/pw"},
		Authorization: &Authorization{Type: "Bearer", Credentials: "c", CredentialsFile: "/cred"},
		OAuth2: &OAuth2Config{
			ClientID:       "id",
			ClientSecret:   "cs",
			Scopes:         []string{"a"},
			EndpointParams: map[string]string{"k": "v"},
		},
	}

	clone := original.Clone()
	assert.NotSame(t, original.BasicAuth, clone.BasicAuth)
	assert.NotSame(t, original.Authorization, clone.Authorization)
	assert.NotSame(t, original.OAuth2, clone.OAuth2)

	clone.BasicAuth.Password = "x"
	clone.Authorization.Credentials = "y"
	clone.OAuth2.Scopes[0] = "b"
	clone.OAuth2.EndpointParams["k"] = "z"

	assert.Equal(t, "p", original.BasicAuth.Password)
	assert.Equal(t, "c", original.Authorization.Credentials)
	assert.Equal(t, []string{"a"}, original.OAuth2.Scopes)
	assert.Equal(t, "v", original.OAuth2.EndpointParams["k"])
}

func TestHTTPConfig_SanitizeRedactsCredentialsOnly(t *testing.T) {
	secretValue := "s3cr3t-value-not-for-logs"
	hc := &HTTPConfig{
		ProxyURL:      "http://proxy:8080",
		TLSConfig:     &TLSConfig{CAFile: "/ca.pem", KeyFile: "/key.pem"},
		BasicAuth:     &BasicAuth{Username: "u", Password: secretValue, PasswordFile: "/pw"},
		Authorization: &Authorization{Type: "Bearer", Credentials: secretValue, CredentialsFile: "/cred"},
		OAuth2:        &OAuth2Config{ClientID: "id", ClientSecret: secretValue},
	}

	safe := hc.Sanitize()
	require.NotNil(t, safe)
	assert.Equal(t, RedactedValue, safe.BasicAuth.Password)
	assert.Equal(t, RedactedValue, safe.Authorization.Credentials)
	assert.Equal(t, RedactedValue, safe.OAuth2.ClientSecret)

	// Diagnostics survive.
	assert.Equal(t, "http://proxy:8080", safe.ProxyURL)
	assert.Equal(t, "u", safe.BasicAuth.Username)
	assert.Equal(t, "/pw", safe.BasicAuth.PasswordFile)
	assert.Equal(t, "/cred", safe.Authorization.CredentialsFile)
	assert.Equal(t, "/key.pem", safe.TLSConfig.KeyFile)

	// Original untouched.
	assert.Equal(t, secretValue, hc.BasicAuth.Password)

	var nilCfg *HTTPConfig
	assert.Nil(t, nilCfg.Sanitize())
}

// Every integration's Sanitize must reach into http_config — otherwise a
// sanitised receiver still carries a plaintext basic_auth password.
func TestReceiverSanitize_RedactsHTTPConfigCredentials(t *testing.T) {
	secretValue := "s3cr3t-value-not-for-logs"
	newHC := func() *HTTPConfig {
		return &HTTPConfig{
			BasicAuth:     &BasicAuth{Username: "u", Password: secretValue},
			Authorization: &Authorization{Credentials: secretValue},
		}
	}

	receiver := &Receiver{
		Name:             "r",
		WebhookConfigs:   []*WebhookConfig{{URL: "https://hooks.example.com/a", HTTPConfig: newHC()}},
		SlackConfigs:     []*SlackConfig{{APIURL: "https://hooks.slack.com/services/a", HTTPConfig: newHC()}},
		PagerDutyConfigs: []*PagerDutyConfig{{RoutingKey: "routing-key-value", HTTPConfig: newHC()}},
		TelegramConfigs:  []*TelegramConfig{{BotToken: "bt", ChatID: "1", HTTPConfig: newHC()}},
	}

	safe := receiver.Sanitize()

	for name, hc := range map[string]*HTTPConfig{
		"webhook":   safe.WebhookConfigs[0].HTTPConfig,
		"slack":     safe.SlackConfigs[0].HTTPConfig,
		"pagerduty": safe.PagerDutyConfigs[0].HTTPConfig,
		"telegram":  safe.TelegramConfigs[0].HTTPConfig,
	} {
		require.NotNil(t, hc, "%s", name)
		assert.Equal(t, RedactedValue, hc.BasicAuth.Password, "%s basic_auth password must be redacted", name)
		assert.Equal(t, RedactedValue, hc.Authorization.Credentials, "%s authorization credentials must be redacted", name)
	}

	// The originals must survive sanitisation untouched.
	assert.Equal(t, secretValue, receiver.WebhookConfigs[0].HTTPConfig.BasicAuth.Password)
}
