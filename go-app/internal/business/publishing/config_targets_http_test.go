package publishing

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

	require.NotNil(t, hc.FollowRedirects)
	assert.False(t, *hc.FollowRedirects)

	// authorization is mutually exclusive with basic_auth (review M7), so it gets
	// its own target rather than being stacked onto the one above.
	assert.Nil(t, hc.Authorization)
}

// authorization maps the same way basic_auth does, on its own target.
func TestBuildConfigTargets_MapsAuthorization(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "bearer",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL: "https://hooks.internal/alerts",
						HTTPConfig: &infraroute.HTTPConfig{
							Authorization: &infraroute.Authorization{Type: "Token", Credentials: "tok"},
						},
					},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)

	hc := targets[0].HTTPConfig
	require.NotNil(t, hc)
	require.NotNil(t, hc.Authorization)
	assert.Equal(t, "Token", hc.Authorization.Type)
	assert.Equal(t, "tok", hc.Authorization.Credentials)
	assert.Nil(t, hc.BasicAuth)
}

// Review M7: basic_auth AND authorization together used to reload fine and then
// fail every publish job into the DLQ. Rejected at build time now, like upstream.
func TestBuildConfigTargets_BothAuthMethodsSkipsTarget(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "ambiguous",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL: "https://hooks.internal/alerts",
						HTTPConfig: &infraroute.HTTPConfig{
							BasicAuth:     &infraroute.BasicAuth{Username: "amp", Password: "pw"},
							Authorization: &infraroute.Authorization{Credentials: "tok"},
						},
					},
				},
				// A sibling integration with a clean config must still deliver:
				// the skip is per integration, never per receiver.
				SlackConfigs: []*infraroute.SlackConfig{{APIURL: "https://hooks.slack.com/services/a"}},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1, "the ambiguous-auth webhook must be skipped, its sibling must survive")
	assert.Equal(t, ConfigTargetName("ambiguous", configKindSlack, 0), targets[0].Name)
}

// Review M7: half a client certificate pair and an unroutable proxy_url are
// structural errors decidable without touching the filesystem, so they fail at
// build time rather than on every job.
func TestBuildConfigTargets_StructurallyInvalidHTTPConfigSkipsTarget(t *testing.T) {
	for name, hc := range map[string]*infraroute.HTTPConfig{
		"cert without key": {TLSConfig: &infraroute.TLSConfig{CertFile: "/c.pem"}},
		"key without cert": {TLSConfig: &infraroute.TLSConfig{KeyFile: "/k.pem"}},
		"proxy not a url":  {ProxyURL: "proxy.example:8080"},
	} {
		t.Run(name, func(t *testing.T) {
			rc := &infraroute.RouteConfig{
				Receivers: []*infraroute.Receiver{
					{
						Name:           "bad",
						WebhookConfigs: []*infraroute.WebhookConfig{{URL: "https://hooks.internal/a", HTTPConfig: hc}},
					},
				},
			}
			assert.Empty(t, BuildConfigTargets(rc, quietLogger()),
				"a structurally invalid http_config must skip the target at build time")
		})
	}
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

// ============================================================================
// Review I1: oauth2 provenance split
// ============================================================================
//
// oauth2 is unsupported either way; what differs is the blast radius of
// refusing. An OWN-block oauth2 is an explicit per-endpoint auth requirement, so
// that target fails closed. A GLOBAL-inherited one must not blackhole
// integrations that never asked for OAuth2 — global propagates wholesale, so one
// block would otherwise take down every webhook/Slack/PagerDuty/Telegram target
// in the process, most of which authenticate through their own credential.

// Own-block oauth2 -> that target is skipped, loudly. Its siblings survive.
func TestBuildConfigTargets_OwnBlockOAuth2SkipsTargetOnly(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "r",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL: "https://hooks.example.com/oauth",
						HTTPConfig: &infraroute.HTTPConfig{
							ProxyURL: "http://proxy.corp:8080",
							OAuth2:   &infraroute.OAuth2Config{ClientID: "id", TokenURL: "https://idp.example.com/token"},
						},
					},
					// Clean sibling: the skip must be per integration.
					{URL: "https://hooks.example.com/plain"},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1, "only the oauth2 integration may be skipped")
	assert.Equal(t, ConfigTargetName("r", configKindWebhook, 1), targets[0].Name)
	assert.Nil(t, targets[0].HTTPConfig)
}

// Global-inherited oauth2 -> every integration still DELIVERS (WARN + metric),
// because refusing would be a process-wide notification outage.
func TestBuildConfigTargets_GlobalOAuth2DeliversWithWarning(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			HTTPConfig: &infraroute.HTTPConfig{
				ProxyURL: "http://proxy.corp:8080",
				OAuth2:   &infraroute.OAuth2Config{ClientID: "id", TokenURL: "https://idp.example.com/token"},
			},
		},
		Receivers: []*infraroute.Receiver{
			{
				Name:             "all",
				WebhookConfigs:   []*infraroute.WebhookConfig{{URL: "https://hooks.example.com/a"}},
				SlackConfigs:     []*infraroute.SlackConfig{{APIURL: "https://hooks.slack.com/services/a"}},
				PagerDutyConfigs: []*infraroute.PagerDutyConfig{{RoutingKey: "rk"}},
				TelegramConfigs:  []*infraroute.TelegramConfig{{BotToken: "bt", ChatID: "1"}},
			},
		},
	}

	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))

	targets := BuildConfigTargets(rc, logger)
	require.Len(t, targets, 4,
		"one global oauth2 block must NOT blackhole every integration in the process")

	for _, target := range targets {
		require.NotNil(t, target.HTTPConfig, "target %s", target.Name)
		assert.Equal(t, "http://proxy.corp:8080", target.HTTPConfig.ProxyURL,
			"the supported half of the inherited block must still apply")
	}

	out := logged.String()
	assert.Contains(t, out, "oauth2", "the ignored block must be named in a WARN")
	assert.Contains(t, out, "WITHOUT OAuth2 credentials",
		"the WARN must state the consequence, not just the field name")
	assert.Equal(t, 4, strings.Count(out, "level=WARN"),
		"one WARN per affected integration, at config load only — not per alert")
}

// An integration that sets its OWN http_config overrides global wholesale, so it
// does not inherit global's oauth2 and must not be skipped for it.
func TestBuildConfigTargets_OwnBlockEscapesGlobalOAuth2(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{
			HTTPConfig: &infraroute.HTTPConfig{OAuth2: &infraroute.OAuth2Config{ClientID: "id"}},
		},
		Receivers: []*infraroute.Receiver{
			{
				Name: "r",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{URL: "https://hooks.example.com/a", HTTPConfig: &infraroute.HTTPConfig{ProxyURL: "http://own:3128"}},
				},
			},
		},
	}

	targets := BuildConfigTargets(rc, quietLogger())
	require.Len(t, targets, 1)
	require.NotNil(t, targets[0].HTTPConfig)
	assert.Equal(t, "http://own:3128", targets[0].HTTPConfig.ProxyURL)
}

// Provenance must survive the PARSED path too, where resolution has already
// erased the structural difference between own and inherited.
func TestBuildConfigTargets_GlobalOAuth2ThroughParserStillDelivers(t *testing.T) {
	yamlDoc := `
global:
  http_config:
    proxy_url: http://proxy.corp:8080
    oauth2:
      client_id: amp
      token_url: https://idp.example.com/token
route:
  receiver: team
receivers:
  - name: team
    webhook_configs:
      - url: https://hooks.example.com/alerts
`
	cfg, err := infraroute.NewRouteConfigParser().Parse([]byte(yamlDoc))
	require.NoError(t, err)

	parsed := cfg.Receivers[0].WebhookConfigs[0].HTTPConfig
	require.NotNil(t, parsed, "the parser resolves global.http_config into the integration")
	require.NotNil(t, parsed.OAuth2)
	require.True(t, parsed.InheritedFromGlobal(),
		"the resolved clone must remember it came from global, or the split collapses")

	targets := BuildConfigTargets(cfg, quietLogger())
	require.Len(t, targets, 1, "a parsed global oauth2 must not blackhole the integration")
	require.NotNil(t, targets[0].HTTPConfig)
	assert.Equal(t, "http://proxy.corp:8080", targets[0].HTTPConfig.ProxyURL)
}

// Own-block oauth2 through the parser must still be skipped: the parser leaves
// an integration's own block untagged.
func TestBuildConfigTargets_OwnOAuth2ThroughParserSkipsTarget(t *testing.T) {
	yamlDoc := `
route:
  receiver: team
receivers:
  - name: team
    webhook_configs:
      - url: https://hooks.example.com/alerts
        http_config:
          oauth2:
            client_id: amp
            token_url: https://idp.example.com/token
`
	cfg, err := infraroute.NewRouteConfigParser().Parse([]byte(yamlDoc))
	require.NoError(t, err)
	require.False(t, cfg.Receivers[0].WebhookConfigs[0].HTTPConfig.InheritedFromGlobal())

	assert.Empty(t, BuildConfigTargets(cfg, quietLogger()),
		"an integration that declares oauth2 itself must fail closed")
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

// The WARN fires once per config load and is then invisible for the life of the
// process, so the durable signal is the counter (review I1).
func TestBuildConfigTargets_GlobalOAuth2IncrementsMetric(t *testing.T) {
	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())
	targetName := ConfigTargetName("all", configKindWebhook, 0)

	before := testutil.ToFloat64(metrics.UnsupportedHTTPConfigCounter("oauth2", targetName))
	metrics.RecordUnsupportedHTTPConfig("oauth2", targetName)
	after := testutil.ToFloat64(metrics.UnsupportedHTTPConfigCounter("oauth2", targetName))

	assert.Equal(t, before+1, after,
		"an ignored oauth2 block must be observable after the load-time WARN has scrolled away")
}

// The metric must be labelled with the config TARGET name, so an operator can
// tell WHICH integration is sending unauthenticated requests.
func TestBuildConfigTargets_OAuth2MetricIsPerTarget(t *testing.T) {
	metrics := v2.NewPublishingMetrics(prometheus.NewRegistry())

	first := ConfigTargetName("all", configKindWebhook, 0)
	second := ConfigTargetName("all", configKindSlack, 0)

	metrics.RecordUnsupportedHTTPConfig("oauth2", first)
	metrics.RecordUnsupportedHTTPConfig("oauth2", first)
	metrics.RecordUnsupportedHTTPConfig("oauth2", second)

	assert.InDelta(t, 2.0, testutil.ToFloat64(metrics.UnsupportedHTTPConfigCounter("oauth2", first)), 0.001)
	assert.InDelta(t, 1.0, testutil.ToFloat64(metrics.UnsupportedHTTPConfigCounter("oauth2", second)), 0.001)
}
