package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetViper clears viper's global state between tests.
// Note: environment variables are read at runtime via AutomaticEnv,
// so we also unset any vars we set in tests to avoid cross-test pollution.
func resetViper() {
	viper.Reset()
}

// unsetEnvKeys unsets provided environment variable keys.
func unsetEnvKeys(keys ...string) {
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}
}

// writeTempYAML writes a temporary YAML file with given content and returns its path.
func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(content), 0o600)
	require.NoError(t, err)
	return path
}

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	resetViper()
	// Ensure relevant env vars do not affect defaults
	unsetEnvKeys(
		"SERVER_PORT",
		"SERVER_HOST",
		"DATABASE_HOST",
		"DATABASE_PORT",
		"DATABASE_DATABASE",
		"DATABASE_USERNAME",
		"DATABASE_PASSWORD",
		"REDIS_ADDR",
		"APP_ENVIRONMENT",
		"APP_DEBUG",
	)

	cfg, err := LoadConfigFromEnv()
	require.NoError(t, err)

	// Check a few representative defaults
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, "localhost:6379", cfg.Redis.Addr)
	assert.Equal(t, "development", cfg.App.Environment)
	assert.Equal(t, false, cfg.App.Debug)
	assert.Equal(t, "postgres", cfg.Database.Driver)
	assert.Equal(t, "localhost", cfg.Database.Host)
	assert.Equal(t, "alerthistory", cfg.Database.Database)
}

func TestLoadConfig_File(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT", "DATABASE_HOST", "APP_ENVIRONMENT", "APP_DEBUG")

	yaml := `
app:
  environment: "production"
  debug: false
server:
  port: 9090
  host: "127.0.0.1"
database:
  driver: "postgres"
  host: "db.local"
  port: 5433
  database: "testdb"
  username: "user"
  password: "pass"
  ssl_mode: "disable"
redis:
  addr: "redis:6379"
log:
  level: "debug"
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	assert.Equal(t, "production", cfg.App.Environment)
	assert.False(t, cfg.App.Debug)

	assert.Equal(t, 9090, cfg.Server.Port)
	assert.Equal(t, "127.0.0.1", cfg.Server.Host)

	assert.Equal(t, "postgres", cfg.Database.Driver)
	assert.Equal(t, "db.local", cfg.Database.Host)
	assert.Equal(t, 5433, cfg.Database.Port)
	assert.Equal(t, "testdb", cfg.Database.Database)
	assert.Equal(t, "user", cfg.Database.Username)
	assert.Equal(t, "pass", cfg.Database.Password)
	assert.Equal(t, "disable", cfg.Database.SSLMode)

	assert.Equal(t, "redis:6379", cfg.Redis.Addr)
	assert.Equal(t, "debug", cfg.Log.Level)
}

func TestLoadConfig_EnvOverridesFile(t *testing.T) {
	resetViper()
	// Base file values
	yaml := `
server:
  port: 8080
database:
  host: "file-db.local"
app:
  environment: "development"
  debug: true
`
	path := writeTempYAML(t, yaml)

	// Env overrides
	require.NoError(t, os.Setenv("SERVER_PORT", "9091"))
	require.NoError(t, os.Setenv("DATABASE_HOST", "env-db.local"))
	require.NoError(t, os.Setenv("APP_ENVIRONMENT", "production"))
	require.NoError(t, os.Setenv("APP_DEBUG", "false"))
	t.Cleanup(func() {
		unsetEnvKeys("SERVER_PORT", "DATABASE_HOST", "APP_ENVIRONMENT", "APP_DEBUG")
	})

	cfg, err := LoadConfig(path)
	require.NoError(t, err)

	assert.Equal(t, 9091, cfg.Server.Port, "env should override file")
	assert.Equal(t, "env-db.local", cfg.Database.Host, "env should override file")
	assert.Equal(t, "production", cfg.App.Environment, "env should override file")
	assert.Equal(t, false, cfg.App.Debug, "env should override file")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	invalid := `
server:
  port: : invalid
`
	path := writeTempYAML(t, invalid)

	cfg, err := LoadConfig(path)
	require.Error(t, err)
	assert.Nil(t, cfg)
}

func TestLoadConfig_ValidationError(t *testing.T) {
	resetViper()
	unsetEnvKeys("SERVER_PORT")

	// server.port invalid (-1) should trigger validation error
	yaml := `
server:
  port: -1
`
	path := writeTempYAML(t, yaml)

	cfg, err := LoadConfig(path)
	require.Error(t, err, "validation should fail for invalid server.port")
	assert.Nil(t, cfg)
}
