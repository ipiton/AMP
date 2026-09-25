package config

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func webAuthCfg(path string, exempt ...string) *Config {
	cfg := &Config{}
	cfg.Server.WebConfigFile = path
	cfg.Server.Auth.UnauthenticatedPaths = exempt
	return cfg
}

func TestWebAuthReloadable_Contract(t *testing.T) {
	reloadable := NewWebAuthReloadable(webAuthCfg(""), "", NewRestartWarnings(), slog.Default())

	assert.Equal(t, "web_auth", reloadable.Name())
	assert.Equal(t, []string{"server"}, reloadable.RelevantSections())
	assert.False(t, reloadable.IsCritical())
	assert.Equal(t, 20, reloadable.ReloadPriority())
}

// T7 (AC8): the auth source is restart-only and says so via W605.
func TestWebAuthReloadable_DriftWarnsW605(t *testing.T) {
	tests := []struct {
		name       string
		boot       *Config
		pinned     string
		next       *Config
		wantFields []string // nil = no warning
	}{
		{
			name:       "path added",
			boot:       webAuthCfg("", "/-/healthy"),
			next:       webAuthCfg("/etc/amp/web.yml", "/-/healthy"),
			wantFields: []string{"server.web_config_file"},
		},
		{
			name:       "exempt paths changed",
			boot:       webAuthCfg("/etc/amp/web.yml", "/-/healthy", "/-/ready"),
			next:       webAuthCfg("/etc/amp/web.yml", "/-/healthy", "/-/ready", "/metrics"),
			wantFields: []string{"server.auth.unauthenticated_paths"},
		},
		{
			name:       "both changed",
			boot:       webAuthCfg("/a.yml", "/-/healthy"),
			next:       webAuthCfg("/b.yml"),
			wantFields: []string{"server.web_config_file", "server.auth.unauthenticated_paths"},
		},
		{
			name: "nothing changed",
			boot: webAuthCfg("/a.yml", "/-/healthy"),
			next: webAuthCfg("/a.yml", "/-/healthy"),
		},
		{
			name: "nil and empty exempt lists are equal",
			boot: webAuthCfg("/a.yml"),
			next: &Config{Server: ServerConfig{WebConfigFile: "/a.yml", Auth: ServerAuthConfig{UnauthenticatedPaths: []string{}}}},
		},
		{
			name:   "flag pins the path, config-side change is not drift",
			boot:   webAuthCfg("", "/-/healthy"),
			pinned: "/flag.yml",
			next:   webAuthCfg("/config.yml", "/-/healthy"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warnings := NewRestartWarnings()
			reloadable := NewWebAuthReloadable(tc.boot, tc.pinned, warnings, slog.Default())

			require.NoError(t, reloadable.Reload(context.Background(), tc.boot, tc.next))

			assert.Equal(t, tc.wantFields != nil, reloadable.NeedsResync(tc.next))
			if tc.wantFields == nil {
				assert.Empty(t, warnings.List())
				return
			}
			list := warnings.List()
			require.Len(t, list, 1)
			assert.Equal(t, WarnWebAuthRestartRequired, list[0].Code)
			assert.Equal(t, "W605", list[0].Code)
			assert.Equal(t, tc.wantFields, list[0].Fields)
		})
	}
}

func TestWebAuthReloadable_RevertResolvesWarning(t *testing.T) {
	warnings := NewRestartWarnings()
	boot := webAuthCfg("/a.yml", "/-/healthy")
	reloadable := NewWebAuthReloadable(boot, "", warnings, slog.Default())

	drifted := webAuthCfg("/b.yml", "/-/healthy")
	require.NoError(t, reloadable.Reload(context.Background(), boot, drifted))
	require.Len(t, warnings.List(), 1)

	require.NoError(t, reloadable.Reload(context.Background(), drifted, boot))
	assert.Empty(t, warnings.List(), "reverting the config clears W605")
}

func TestWebAuthReloadable_NilConfig(t *testing.T) {
	reloadable := NewWebAuthReloadable(webAuthCfg(""), "", NewRestartWarnings(), slog.Default())
	assert.Error(t, reloadable.Reload(context.Background(), nil, nil))
	assert.False(t, reloadable.NeedsResync(nil))
}
