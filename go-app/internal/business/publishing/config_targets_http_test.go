package publishing

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// ============================================================================
// FU-HTTP-CONFIG: config `http_config` -> PublishingTarget.HTTPConfig
// ============================================================================
//
// quietLogger() and mail() are shared with config_targets_test.go.

func followRedirects(v bool) *bool { return &v }

func TestBuildConfigTargets_MapsPerIntegrationHTTPConfig(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "corp",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL: "https://hooks.internal/alerts",
						HTTPConfig: &infraroute.HTTPConfig{
							ProxyURL: "http://proxy.corp:8080",
							TLSConfig: &infraroute.TLSConfig{
								CAFile:     "/etc/ssl/ca.pem",
								CertFile:   "/etc/ssl/client.pem",
								KeyFile:    "/etc/ssl/client-key.pem",
								ServerName: "hooks.internal",
							},
							BasicAuth:       &infraroute.BasicAuth{Username: "amp", Password: "pw"},
							Authorization:   &infraroute.Authorization{Type: "Token", Credentials: "tok"},
							FollowRedirects: followRedirects(false),
						},
					},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)

	hc := targets[0].HTTPConfig
	require.NotNil(t, hc, "webhook target must carry the integration's http_config")
	assert.Equal(t, "http://proxy.corp:8080", hc.ProxyURL)

	require.NotNil(t, hc.TLSConfig)
	assert.Equal(t, "/etc/ssl/ca.pem", hc.TLSConfig.CAFile)
	assert.Equal(t, "/etc/ssl/client.pem", hc.TLSConfig.CertFile)
	assert.Equal(t, "/etc/ssl/client-key.pem", hc.TLSConfig.KeyFile)
	assert.Equal(t, "hooks.internal", hc.TLSConfig.ServerName)

	require.NotNil(t, hc.BasicAuth)
	assert.Equal(t, "amp", hc.BasicAuth.Username)
	assert.Equal(t, "pw", hc.BasicAuth.Password)

	require.NotNil(t, hc.Authorization)
	assert.Equal(t, "Token", hc.Authorization.Type)
	assert.Equal(t, "tok", hc.Authorization.Credentials)

	require.NotNil(t, hc.FollowRedirects)
	assert.False(t, *hc.FollowRedirects)
}

// Every HTTP-carrying integration kind must be wired, not just webhook.
func TestBuildConfigTargets_MapsHTTPConfigForEveryHTTPKind(t *testing.T) {
	hc := func() *infraroute.HTTPConfig {
		return &infraroute.HTTPConfig{ProxyURL: "http://proxy.corp:8080"}
	}

	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name:             "all",
				WebhookConfigs:   []*infraroute.WebhookConfig{{URL: "https://hooks.example.com/a", HTTPConfig: hc()}},
				SlackConfigs:     []*infraroute.SlackConfig{{APIURL: "https://hooks.slack.com/services/a", HTTPConfig: hc()}},
				PagerDutyConfigs: []*infraroute.PagerDutyConfig{{RoutingKey: "rk", HTTPConfig: hc()}},
				TelegramConfigs:  []*infraroute.TelegramConfig{{BotToken: "bt", ChatID: "1", HTTPConfig: hc()}},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 4)

	for _, target := range targets {
		require.NotNil(t, target.HTTPConfig, "target %s must carry http_config", target.Name)
		assert.Equal(t, "http://proxy.corp:8080", target.HTTPConfig.ProxyURL, "target %s", target.Name)
	}
}

// The builder applies the global fallback itself, so a programmatically built
// RouteConfig (never through Parse) still inherits it.
func TestBuildConfigTargets_GlobalHTTPConfigFallback(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			HTTPConfig: &infraroute.HTTPConfig{
				ProxyURL:  "http://global-proxy:8080",
				TLSConfig: &infraroute.TLSConfig{CAFile: "/global-ca.pem"},
			},
		},
		Receivers: []*infraroute.Receiver{
			{
				Name:           "inherits",
				WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://hooks.example.com/a"}},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)

	hc := targets[0].HTTPConfig
	require.NotNil(t, hc)
	assert.Equal(t, "http://global-proxy:8080", hc.ProxyURL)
	require.NotNil(t, hc.TLSConfig)
	assert.Equal(t, "/global-ca.pem", hc.TLSConfig.CAFile)
}

// Upstream does NOT deep-merge: an integration that sets http_config at all
// replaces the global block wholesale.
func TestBuildConfigTargets_PerIntegrationOverridesGlobalWholesale(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			HTTPConfig: &infraroute.HTTPConfig{
				ProxyURL:  "http://global-proxy:8080",
				TLSConfig: &infraroute.TLSConfig{CAFile: "/global-ca.pem"},
				BasicAuth: &infraroute.BasicAuth{Username: "global-user", Password: "gp"},
			},
		},
		Receivers: []*infraroute.Receiver{
			{
				Name: "overrides",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL:        "https://hooks.example.com/a",
						HTTPConfig: &infraroute.HTTPConfig{ProxyURL: "http://own-proxy:3128"},
					},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)

	hc := targets[0].HTTPConfig
	require.NotNil(t, hc)
	assert.Equal(t, "http://own-proxy:3128", hc.ProxyURL)
	assert.Nil(t, hc.TLSConfig, "global tls_config must NOT be merged in")
	assert.Nil(t, hc.BasicAuth, "global basic_auth must NOT be merged in")
}

// A config with no http_config anywhere must leave HTTPConfig nil, so the
// publisher keeps its built-in client and cache keys keep their shape.
func TestBuildConfigTargets_NoHTTPConfigLeavesNil(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name:           "plain",
				WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://hooks.example.com/a"}},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)
	assert.Nil(t, targets[0].HTTPConfig)
	assert.True(t, targets[0].HTTPConfig.IsZero())
	assert.Empty(t, targets[0].HTTPConfig.Fingerprint(), "cache keys must be unchanged for targets without http_config")
}

// routing.HTTPConfig.Defaults() materialises follow_redirects: true for every
// parsed config. Carrying that redundant default through would give EVERY
// target a non-empty fingerprint and a dedicated client for no reason.
func TestBuildConfigTargets_DefaultedFollowRedirectsIsNotCarried(t *testing.T) {
	yamlDoc := `
route:
  receiver: team
receivers:
  - name: team
    webhook_configs:
      - url: https://hooks.example.com/alerts
        http_config:
          follow_redirects: true
`
	cfg, err := infraroute.NewRouteConfigParser().Parse([]byte(yamlDoc))
	require.NoError(t, err)

	parsed := cfg.Receivers[0].WebhookConfigs[0].HTTPConfig
	require.NotNil(t, parsed)
	require.NotNil(t, parsed.FollowRedirects, "the parser materialises the default")
	require.True(t, *parsed.FollowRedirects)

	targets := BuildConfigTargets(cfg, quietLogger())
	require.Len(t, targets, 1)
	assert.Nil(t, targets[0].HTTPConfig,
		"follow_redirects: true is the default; it must not create a distinct client")
}

// Empty sub-blocks must normalise back to nil rather than buying a dedicated
// client that behaves exactly like the built-in one.
func TestBuildConfigTargets_EmptySubBlocksNormaliseToNil(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "r",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL: "https://hooks.example.com/a",
						HTTPConfig: &infraroute.HTTPConfig{
							TLSConfig: &infraroute.TLSConfig{},
							BasicAuth: &infraroute.BasicAuth{},
						},
					},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)
	assert.Nil(t, targets[0].HTTPConfig)
}

// `authorization:` without any credential renders as a bare scheme — dropped
// with a WARN rather than sent.
func TestBuildConfigTargets_AuthorizationWithoutCredentialsIsDropped(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "r",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL:        "https://hooks.example.com/a",
						HTTPConfig: &infraroute.HTTPConfig{Authorization: &infraroute.Authorization{Type: "Bearer"}},
					},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)
	assert.Nil(t, targets[0].HTTPConfig)
}

// oauth2 is unsupported. The target must still be built and deliver (without
// OAuth2 credentials) rather than being dropped, and the rest of its
// http_config must survive.
func TestBuildConfigTargets_OAuth2IsIgnoredButTargetSurvives(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "r",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL: "https://hooks.example.com/a",
						HTTPConfig: &infraroute.HTTPConfig{
							ProxyURL: "http://proxy.corp:8080",
							OAuth2:   &infraroute.OAuth2Config{ClientID: "id", TokenURL: "https://idp.example.com/token"},
						},
					},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)
	require.NotNil(t, targets[0].HTTPConfig)
	assert.Equal(t, "http://proxy.corp:8080", targets[0].HTTPConfig.ProxyURL)
}

// email_configs have no HTTP transport at all (SMTP), so they must never
// acquire an http_config — not even from global.
func TestBuildConfigTargets_EmailTargetsCarryNoHTTPConfig(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			SMTPSmartHost: "smtp.example.com:587",
			SMTPFrom:      mail("amp", "example.com"),
			HTTPConfig:    &infraroute.HTTPConfig{ProxyURL: "http://global-proxy:8080"},
		},
		Receivers: []*infraroute.Receiver{
			{
				Name:         "mail",
				EmailConfigs: []*infraroute.EmailConfig{{To: mail("oncall", "example.com")}},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)
	assert.Nil(t, targets[0].HTTPConfig, "an SMTP target has no HTTP client to configure")
}

// Two integrations inheriting the same global block must fingerprint
// identically (so they share one client) while differing ones must not.
func TestBuildConfigTargets_FingerprintGroupsAndSeparates(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			HTTPConfig: &infraroute.HTTPConfig{ProxyURL: "http://global-proxy:8080"},
		},
		Receivers: []*infraroute.Receiver{
			{
				Name: "r",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{URL: "https://hooks.example.com/a"},
					{URL: "https://hooks.example.com/b"},
					{URL: "https://hooks.example.com/c", HTTPConfig: &infraroute.HTTPConfig{ProxyURL: "http://other:3128"}},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 3)

	assert.Equal(t, targets[0].HTTPConfig.Fingerprint(), targets[1].HTTPConfig.Fingerprint(),
		"two integrations inheriting the same global http_config must share one cached client")
	assert.NotEqual(t, targets[0].HTTPConfig.Fingerprint(), targets[2].HTTPConfig.Fingerprint(),
		"a different proxy must produce a different client")
}
