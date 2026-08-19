package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
)

// ============================================================================
// FU-HTTP-CONFIG: relative http_config file paths rebase onto the config dir
// ============================================================================
//
// Upstream's resolveFilepaths joins every relative *_file path onto
// filepath.Dir(configPath). AMP read them against the process CWD ("/" in most
// containers), so the canonical upstream idiom `ca_file: certs/internal-ca.pem`
// resolved to /certs/internal-ca.pem and the target failed CLOSED — it stopped
// delivering, with an ERROR naming a path the operator never wrote.

const httpConfigRelPathYAML = `
app:
  environment: development
server:
  port: 9093
route:
  receiver: team
receivers:
  - name: team
    webhook_configs:
      - url: https://hooks.example.com/alerts
        http_config:
          tls_config:
            ca_file: certs/internal-ca.pem
            cert_file: certs/client.pem
            key_file: certs/client-key.pem
          basic_auth:
            username: amp
            password_file: secrets/webhook-pw
`

// The end-to-end case the addendum asked for: a RELATIVE ca_file written in a
// config that lives in a temp directory must resolve inside that directory.
func TestLoadConfig_RebasesRelativeHTTPConfigFilePaths(t *testing.T) {
	resetViper()

	path := writeTempYAML(t, httpConfigRelPathYAML)
	baseDir := filepath.Dir(path)

	// The files exist on disk relative to the CONFIG, not to the process CWD —
	// which is what makes this a real reproduction rather than a string check.
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "certs"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(baseDir, "secrets"), 0o750))
	for _, rel := range []string{"certs/internal-ca.pem", "certs/client.pem", "certs/client-key.pem", "secrets/webhook-pw"} {
		require.NoError(t, os.WriteFile(filepath.Join(baseDir, rel), []byte("x"), 0o600))
	}

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)

	hc := cfg.Routing.Receivers[0].WebhookConfigs[0].HTTPConfig
	require.NotNil(t, hc)
	require.NotNil(t, hc.TLSConfig)

	assert.Equal(t, filepath.Join(baseDir, "certs/internal-ca.pem"), hc.TLSConfig.CAFile)
	assert.Equal(t, filepath.Join(baseDir, "certs/client.pem"), hc.TLSConfig.CertFile)
	assert.Equal(t, filepath.Join(baseDir, "certs/client-key.pem"), hc.TLSConfig.KeyFile)
	require.NotNil(t, hc.BasicAuth)
	assert.Equal(t, filepath.Join(baseDir, "secrets/webhook-pw"), hc.BasicAuth.PasswordFile)

	// The rebased paths must actually be readable — the whole point.
	for _, p := range []string{hc.TLSConfig.CAFile, hc.TLSConfig.CertFile, hc.TLSConfig.KeyFile, hc.BasicAuth.PasswordFile} {
		_, statErr := os.Stat(p)
		assert.NoError(t, statErr, "rebased path must exist: %s", p)
	}
}

// Absolute paths and the empty string are left alone. The empty-string guard is
// load-bearing: filepath.Join(base, "") returns base, i.e. it would invent a
// ca_file pointing at a DIRECTORY where the operator set none.
func TestResolveHTTPConfigFilepaths_LeavesAbsoluteAndEmptyAlone(t *testing.T) {
	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			{
				Name: "team",
				WebhookConfigs: []*infraroute.WebhookConfig{
					{
						URL: "https://hooks.example.com/a",
						HTTPConfig: &infraroute.HTTPConfig{
							TLSConfig: &infraroute.TLSConfig{
								CAFile:   "/etc/ssl/absolute-ca.pem",
								CertFile: "", // must stay empty, NOT become the base dir
							},
							BasicAuth: &infraroute.BasicAuth{Username: "amp"}, // no password_file
						},
					},
				},
			},
		},
	}

	resolveHTTPConfigFilepaths(rc, "/opt/amp/config.yaml")

	hc := rc.Receivers[0].WebhookConfigs[0].HTTPConfig
	assert.Equal(t, "/etc/ssl/absolute-ca.pem", hc.TLSConfig.CAFile, "absolute paths must not be rebased")
	assert.Empty(t, hc.TLSConfig.CertFile, "an unset path must not become the config directory")
	assert.Empty(t, hc.BasicAuth.PasswordFile)
}

// Every HTTP-carrying integration kind must be rebased, not just webhook.
func TestResolveHTTPConfigFilepaths_CoversEveryIntegrationKind(t *testing.T) {
	relTLS := func() *infraroute.HTTPConfig {
		return &infraroute.HTTPConfig{TLSConfig: &infraroute.TLSConfig{CAFile: "certs/ca.pem"}}
	}

	rc := &infraroute.RouteConfig{
		Global: &infraroute.GlobalConfig{HTTPConfig: relTLS()},
		Receivers: []*infraroute.Receiver{
			{
				Name:             "team",
				WebhookConfigs:   []*infraroute.WebhookConfig{{URL: "https://h/a", HTTPConfig: relTLS()}},
				SlackConfigs:     []*infraroute.SlackConfig{{APIURL: "https://h/s", HTTPConfig: relTLS()}},
				PagerDutyConfigs: []*infraroute.PagerDutyConfig{{RoutingKey: "rk", HTTPConfig: relTLS()}},
				TelegramConfigs:  []*infraroute.TelegramConfig{{BotToken: "bt", ChatID: "1", HTTPConfig: relTLS()}},
			},
		},
	}

	resolveHTTPConfigFilepaths(rc, "/opt/amp/config.yaml")

	want := filepath.Join("/opt/amp", "certs/ca.pem")
	receiver := rc.Receivers[0]
	for name, hc := range map[string]*infraroute.HTTPConfig{
		"global":    rc.Global.HTTPConfig,
		"webhook":   receiver.WebhookConfigs[0].HTTPConfig,
		"slack":     receiver.SlackConfigs[0].HTTPConfig,
		"pagerduty": receiver.PagerDutyConfigs[0].HTTPConfig,
		"telegram":  receiver.TelegramConfigs[0].HTTPConfig,
	} {
		require.NotNil(t, hc, name)
		assert.Equal(t, want, hc.TLSConfig.CAFile, "%s must be rebased", name)
	}
}

// A path inherited from global.http_config must be rebased through the
// per-integration CLONE the parser created, not just on the global block. This
// is why the rebase runs AFTER Parse().
func TestLoadConfig_RebasesGlobalInheritedHTTPConfigPaths(t *testing.T) {
	resetViper()

	yamlDoc := `
app:
  environment: development
server:
  port: 9093
global:
  http_config:
    tls_config:
      ca_file: certs/global-ca.pem
route:
  receiver: team
receivers:
  - name: team
    webhook_configs:
      - url: https://hooks.example.com/alerts
`
	path := writeTempYAML(t, yamlDoc)
	baseDir := filepath.Dir(path)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Routing)

	want := filepath.Join(baseDir, "certs/global-ca.pem")

	inherited := cfg.Routing.Receivers[0].WebhookConfigs[0].HTTPConfig
	require.NotNil(t, inherited, "the parser resolves global.http_config into the integration")
	assert.Equal(t, want, inherited.TLSConfig.CAFile,
		"the inherited clone must be rebased, or a global relative ca_file breaks every integration")

	assert.Equal(t, want, cfg.Routing.Global.HTTPConfig.TLSConfig.CAFile,
		"the global block itself must agree, for the status API and any direct consumer")
}

// Nil-safety: no config path (env-only load) and no routing section must both be
// no-ops rather than panics.
func TestResolveHTTPConfigFilepaths_NilSafe(t *testing.T) {
	resolveHTTPConfigFilepaths(nil, "/opt/amp/config.yaml")

	rc := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{
			nil,
			{Name: "no-http-config", WebhookConfigs: []*infraroute.WebhookConfig{nil, {URL: "https://h/a"}}},
		},
	}
	resolveHTTPConfigFilepaths(rc, "")
	resolveHTTPConfigFilepaths(rc, "/opt/amp/config.yaml")
	assert.Nil(t, rc.Receivers[1].WebhookConfigs[1].HTTPConfig)
}
