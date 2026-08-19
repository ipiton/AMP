package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === alertmanager-parity wave-6 item FU-LITE-FILE-SNAPSHOT ===
//
// storage.path / storage.snapshot_interval configure the lite profile's
// file-snapshot persistence for silences + the notification log. An empty
// path (the default) must keep snapshotting fully disabled — this is a
// deliberate divergence from upstream Alertmanager, whose --storage.path
// defaults to "data/" (see StorageConfig.SnapshotPath's doc comment).

func storageSnapshotTestConfig(path string, interval time.Duration) *Config {
	cfg := &Config{}
	cfg.Storage.SnapshotPath = path
	cfg.Storage.SnapshotInterval = interval
	return cfg
}

func TestValidateStorageSnapshot_EmptyPathSkipsValidation(t *testing.T) {
	// Path unset: interval being zero/negative must NOT be an error — the
	// feature is off, so the interval is moot.
	assert.NoError(t, storageSnapshotTestConfig("", 0).validateStorageSnapshot())
	assert.NoError(t, storageSnapshotTestConfig("", -1).validateStorageSnapshot())
}

func TestValidateStorageSnapshot_IntervalMustBePositiveWhenPathSet(t *testing.T) {
	err := storageSnapshotTestConfig("/data/snapshots", 0).validateStorageSnapshot()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.snapshot_interval must be positive")

	err = storageSnapshotTestConfig("/data/snapshots", -5*time.Second).validateStorageSnapshot()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage.snapshot_interval must be positive")
}

func TestValidateStorageSnapshot_ValidConfigAccepted(t *testing.T) {
	assert.NoError(t, storageSnapshotTestConfig("/data/snapshots", 5*time.Minute).validateStorageSnapshot())
}

// TestSetDefaults_StorageSnapshotDisabledByDefault pins the "off by
// default" invariant: LoadConfigFromEnv (real viper defaults, no env
// overrides) must resolve storage.path to empty and still validate cleanly.
func TestSetDefaults_StorageSnapshotDisabledByDefault(t *testing.T) {
	cfg, err := LoadConfigFromEnv()
	require.NoError(t, err)
	assert.Equal(t, "", cfg.Storage.SnapshotPath)
	assert.Equal(t, 5*time.Minute, cfg.Storage.SnapshotInterval)
	assert.NoError(t, cfg.validateStorageSnapshot())
}
