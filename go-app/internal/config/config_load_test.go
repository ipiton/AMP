package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PROD-CONFIG-FILE-FALLBACK: a missing config file is not an error — the
// config comes from viper defaults plus the environment. This is how the Helm
// chart runs by default (configFile.enabled: false, everything via env).
func TestLoadConfig_MissingFile_UsesEnv(t *testing.T) {
	resetViper()
	t.Setenv("PROFILE", "lite")
	t.Setenv("STORAGE_BACKEND", "filesystem")
	t.Setenv("SERVER_PORT", "18080")
	// false, because true is the default and would prove nothing.
	t.Setenv("GROUPING_ENABLED", "false")
	t.Setenv("SERVER_GRACEFUL_SHUTDOWN_TIMEOUT", "12s")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)

	assert.Equal(t, ProfileLite, cfg.Profile)
	assert.Equal(t, 18080, cfg.Server.Port)
	assert.False(t, cfg.Grouping.Enabled)
	assert.Equal(t, "12s", cfg.Server.GracefulShutdownTimeout.String())
	// main no longer patches these in; they must come from viper's defaults,
	// or the kubelet probes would need credentials once auth is on.
	assert.Equal(t, DefaultUnauthenticatedPaths(), cfg.Server.Auth.UnauthenticatedPaths)
}

// grouping.reconciliation_grace has no viper default on purpose (unset means
// "derive from the delivery-confirmation timeout"), so the env var reaches
// the config only through BindEnv.
func TestLoadConfig_ReconciliationGraceFromEnv(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")

	resetViper()
	t.Setenv("GROUPING_RECONCILIATION_GRACE", "120s")
	cfg, err := LoadConfig(missing)
	require.NoError(t, err)
	assert.Equal(t, "2m0s", cfg.Grouping.ReconciliationGrace.String())

	resetViper()
	unsetEnvKeys("GROUPING_RECONCILIATION_GRACE")
	cfg, err = LoadConfig(missing)
	require.NoError(t, err)
	assert.Zero(t, cfg.Grouping.ReconciliationGrace, "unset must stay zero so ServiceRegistry derives it")
}

// A dangling symlink (e.g. a ConfigMap mount whose target is gone) reads as
// "missing", same as an absent file. main rejects it when the path came from
// AMP_CONFIG_FILE (checkConfigPath); LoadConfig alone does not.
func TestLoadConfig_DanglingSymlink_TreatedAsMissing(t *testing.T) {
	resetViper()
	dir := t.TempDir()
	link := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.Symlink(filepath.Join(dir, "gone.yaml"), link))

	_, err := LoadConfig(link)
	require.NoError(t, err)
}

// Every failure other than "file does not exist" must surface: main exits on
// it instead of starting without the operator's routes, receivers and auth.
func TestLoadConfig_Errors(t *testing.T) {
	tests := []struct {
		name    string
		path    func(t *testing.T) string
		wantErr string
	}{
		{
			name:    "malformed YAML",
			path:    func(t *testing.T) string { return writeTempYAML(t, "server:\n  port: [unclosed\n") },
			wantErr: "failed to read config file",
		},
		{
			name:    "failed validation",
			path:    func(t *testing.T) string { return writeTempYAML(t, "server:\n  external_url: \"::bad\"\n") },
			wantErr: "config validation failed",
		},
		{
			name:    "path is a directory",
			path:    func(t *testing.T) string { return t.TempDir() },
			wantErr: "failed to read config file",
		},
		{
			name: "path under a regular file",
			path: func(t *testing.T) string {
				return filepath.Join(writeTempYAML(t, "server: {}\n"), "config.yaml")
			},
			wantErr: "failed to read config file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetViper()
			unsetEnvKeys("SERVER_PORT", "SERVER_EXTERNAL_URL")
			_, err := LoadConfig(tt.path(t))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestLoadConfig_UnreadableFile_ReturnsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of mode")
	}
	resetViper()
	path := writeTempYAML(t, "server: {}\n")
	require.NoError(t, os.Chmod(path, 0o000))

	_, err := LoadConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

// chartEnvNotConfigKeys are env names the chart sets that are deliberately
// not viper config keys.
var chartEnvNotConfigKeys = map[string]string{
	"AMP_CONFIG_FILE":        "path of the config file itself, read by cmd/server",
	"SERVER_WEB_CONFIG_FILE": "also a key (server.web_config_file); listed for the direct read in cmd/server",
	"SERVICE_NAME":           "no reader; informational",
	"SERVICE_VERSION":        "no reader; informational",
}

// Without a config file the chart configures AMP only through env names in
// templates/deployment.yaml and templates/configmap.yaml. viper's
// AutomaticEnv only resolves names of keys it already knows (SetDefault or a
// file), so a name without a default would be silently dropped.
func TestChartEnvKeysKnownToViper(t *testing.T) {
	resetViper()
	setDefaults()
	known := map[string]bool{}
	for _, k := range viper.AllKeys() {
		known[strings.ToUpper(strings.ReplaceAll(k, ".", "_"))] = true
	}

	templates := filepath.Join("..", "..", "..", "helm", "amp", "templates")
	sources := map[string]*regexp.Regexp{
		"deployment.yaml": regexp.MustCompile(`(?m)^\s+- name: ([A-Z][A-Z0-9_]+)\s*$`),
		"configmap.yaml":  regexp.MustCompile(`(?m)^\s+([A-Z][A-Z0-9_]+):`),
	}
	seen := 0
	for file, re := range sources {
		data, err := os.ReadFile(filepath.Join(templates, file))
		require.NoError(t, err)
		matches := re.FindAllStringSubmatch(string(data), -1)
		require.NotEmpty(t, matches, "no env names found in %s — regex out of date?", file)
		for _, m := range matches {
			name := m[1]
			seen++
			if _, ok := chartEnvNotConfigKeys[name]; ok {
				continue
			}
			assert.True(t, known[name], "%s sets %s, which is not a config key with a default: AMP would ignore it", file, name)
		}
	}
	assert.Greater(t, seen, 40, "expected the chart's full env set")
}
