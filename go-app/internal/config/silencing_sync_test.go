package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === alertmanager-parity wave-5 item FU-SILENCE-SYNC-INTERVALS ===
//
// silencing.subscribe_retry_backoff / silencing.periodic_resync_interval
// replace the hardcoded 2s/5m literals ServiceRegistry's
// runSilenceSubscribeLoop / runSilencePeriodicResync used before this task.

func silencingTestConfig(backoff, resync time.Duration) *Config {
	cfg := &Config{}
	cfg.Silencing.SubscribeRetryBackoff = backoff
	cfg.Silencing.PeriodicResyncInterval = resync
	return cfg
}

func TestValidateSilencing_BackoffMustBePositive(t *testing.T) {
	err := silencingTestConfig(0, 5*time.Minute).validateSilencing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "silencing.subscribe_retry_backoff must be positive")
}

func TestValidateSilencing_ResyncIntervalMustBePositive(t *testing.T) {
	err := silencingTestConfig(2*time.Second, 0).validateSilencing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "silencing.periodic_resync_interval must be positive")
}

func TestValidateSilencing_BackoffMustBeBelowResyncInterval(t *testing.T) {
	err := silencingTestConfig(5*time.Minute, 5*time.Minute).validateSilencing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be less than silencing.periodic_resync_interval")

	err = silencingTestConfig(10*time.Minute, 5*time.Minute).validateSilencing()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be less than silencing.periodic_resync_interval")
}

func TestValidateSilencing_DefaultsAreAccepted(t *testing.T) {
	assert.NoError(t, silencingTestConfig(2*time.Second, 5*time.Minute).validateSilencing())
}

// TestSetDefaults_SilencingSyncIntervalsMatchThePreConfigKnobLiterals keeps
// the viper defaults in sync with what runSilenceSubscribeLoop /
// runSilencePeriodicResync used before this task (2s / 5m).
func TestSetDefaults_SilencingSyncIntervalsMatchThePreConfigKnobLiterals(t *testing.T) {
	cfg, err := LoadConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, 2*time.Second, cfg.Silencing.SubscribeRetryBackoff)
	assert.Equal(t, 5*time.Minute, cfg.Silencing.PeriodicResyncInterval)
	assert.NoError(t, cfg.validateSilencing())
}
