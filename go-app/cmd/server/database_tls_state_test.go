package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/ipiton/AMP/internal/config"
	"github.com/stretchr/testify/assert"
)

// HELM-DEFAULTS-VALIDATE: the startup warning fires only when
// database.allow_insecure_ssl_in_production is what let an unencrypted
// connection through, and it names the host, never the password.
func TestLogDatabaseTLSState(t *testing.T) {
	insecure := func() *config.Config {
		return &config.Config{
			Profile: config.ProfileStandard,
			App:     config.AppConfig{Environment: "production"},
			Database: config.DatabaseConfig{
				Host:                         "amp-postgresql",
				Password:                     "do-not-log-this-password",
				SSLMode:                      "disable",
				AllowInsecureSSLInProduction: true,
			},
		}
	}

	cases := map[string]struct {
		mutate   func(*config.Config)
		wantWarn bool
	}{
		"flag lets disable through": {func(*config.Config) {}, true},
		"ssl required":              {func(c *config.Config) { c.Database.SSLMode = "require" }, false},
		"development":               {func(c *config.Config) { c.App.Environment = "development" }, false},
		"lite profile":              {func(c *config.Config) { c.Profile = config.ProfileLite }, false},
		"flag off":                  {func(c *config.Config) { c.Database.AllowInsecureSSLInProduction = false }, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := insecure()
			tc.mutate(cfg)
			var buf bytes.Buffer
			logDatabaseTLSState(slog.New(slog.NewTextHandler(&buf, nil)), cfg)

			out := buf.String()
			assert.NotContains(t, out, "do-not-log-this-password")
			if !tc.wantWarn {
				assert.Empty(t, out)
				return
			}
			assert.Contains(t, out, "level=WARN")
			assert.Contains(t, out, "NOT encrypted in production")
			assert.Contains(t, out, "host=amp-postgresql")
		})
	}
}
