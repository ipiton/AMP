package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PROD-CONFIG-FILE-FALLBACK: a missing config file is not an error — the
// config comes from viper defaults plus the environment. This is how the Helm
// chart runs by default (configFile.enabled: false, everything via env).
func TestLoadConfig_MissingFile_UsesEnv(t *testing.T) {
	resetViper()
	t.Setenv("PROFILE", "lite")

	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)

	assert.Equal(t, ProfileLite, cfg.Profile)
	// main no longer patches these in; they must come from viper's defaults,
	// or the kubelet probes would need credentials once auth is on.
	assert.Equal(t, DefaultUnauthenticatedPaths(), cfg.Server.Auth.UnauthenticatedPaths)
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

// UPSTREAM-AI-CLASSIFY: llm.notify_wait defaults to 5s and llm.drop_noise to
// false; a negative wait fails the load.
func TestLoadConfig_LLMNotifyWaitAndDropNoise(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")

	resetViper()
	unsetEnvKeys("LLM_NOTIFY_WAIT", "LLM_DROP_NOISE")
	cfg, err := LoadConfig(missing)
	require.NoError(t, err)
	assert.Equal(t, "5s", cfg.LLM.NotifyWait.String())
	assert.False(t, cfg.LLM.DropNoise)

	resetViper()
	t.Setenv("LLM_NOTIFY_WAIT", "0s")
	t.Setenv("LLM_DROP_NOISE", "true")
	cfg, err = LoadConfig(missing)
	require.NoError(t, err)
	assert.Zero(t, cfg.LLM.NotifyWait)
	assert.True(t, cfg.LLM.DropNoise)

	resetViper()
	t.Setenv("LLM_NOTIFY_WAIT", "-1s")
	_, err = LoadConfig(missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm.notify_wait cannot be negative")
}
