package config

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// HELM-DEFAULTS-VALIDATE: database.allow_insecure_ssl_in_production lifts the
// production ban on ssl_mode "disable" and nothing else.

func productionDBConfig() *Config {
	return &Config{
		Profile: ProfileStandard,
		App:     AppConfig{Environment: "production"},
		Database: DatabaseConfig{
			Username: "amp",
			Password: "a-long-enough-password",
			SSLMode:  "disable",
		},
	}
}

func TestValidateDatabaseCredentials_SSLDisableRejectedWithoutFlag(t *testing.T) {
	err := productionDBConfig().validateDatabaseCredentials()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "database SSL mode 'disable' is not allowed in production")
}

func TestValidateDatabaseCredentials_InsecureSSLFlagAllowsDisable(t *testing.T) {
	cfg := productionDBConfig()
	cfg.Database.AllowInsecureSSLInProduction = true
	assert.NoError(t, cfg.validateDatabaseCredentials())
}

// The flag must not weaken any other production check.
func TestValidateDatabaseCredentials_InsecureSSLFlagKeepsCredentialChecks(t *testing.T) {
	cases := map[string]struct {
		username, password, wantErr string
	}{
		"weak password":  {"amp", "postgres", "weak database password"},
		"short password": {"amp", "short-pass1", "at least 12 characters"},
		"no credentials": {"", "", "credentials (username/password) are required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := productionDBConfig()
			cfg.Database.AllowInsecureSSLInProduction = true
			cfg.Database.Username = tc.username
			cfg.Database.Password = tc.password

			err := cfg.validateDatabaseCredentials()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			if tc.password != "" {
				assert.NotContains(t, err.Error(), tc.password, "the error is logged; it must not echo the password")
			}
		})
	}
}

// The chart passes the flag as an env var; viper must know the key, or
// AutomaticEnv never resolves it.
func TestLoadConfig_InsecureSSLFlagFromEnv(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	lite := func(t *testing.T) {
		t.Setenv("PROFILE", "lite")
		t.Setenv("STORAGE_BACKEND", "filesystem")
	}

	t.Run("true", func(t *testing.T) {
		resetViper()
		lite(t)
		t.Setenv("DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION", "true")
		cfg, err := LoadConfig(missing)
		require.NoError(t, err)
		assert.True(t, cfg.Database.AllowInsecureSSLInProduction)
	})

	t.Run("unset", func(t *testing.T) {
		resetViper()
		lite(t)
		unsetEnvKeys("DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION")
		cfg, err := LoadConfig(missing)
		require.NoError(t, err)
		assert.False(t, cfg.Database.AllowInsecureSSLInProduction)
	})

	// Not a bool: the config fails to load instead of guessing either way.
	t.Run("not a bool", func(t *testing.T) {
		resetViper()
		lite(t)
		t.Setenv("DATABASE_ALLOW_INSECURE_SSL_IN_PRODUCTION", "yes")
		_, err := LoadConfig(missing)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal config")
	})
}
