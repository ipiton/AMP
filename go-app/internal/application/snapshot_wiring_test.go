package application

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/internal/infrastructure/snapshot"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Wave 6 (FU-LITE-FILE-SNAPSHOT): ServiceRegistry file-snapshot wiring ===
//
// These construct a minimal *ServiceRegistry directly (same pattern as
// rehydration_test.go / silence_rehydration_test.go) rather than going
// through the full Initialize(), which needs a real database/storage.

func newTestRegistryForSnapshot(profile appconfig.DeploymentProfile, path string, interval time.Duration) *ServiceRegistry {
	cfg := &appconfig.Config{}
	cfg.Profile = profile
	cfg.Storage.SnapshotPath = path
	cfg.Storage.SnapshotInterval = interval

	notifyLog := grouping.NewMemoryNotifyLog()
	snapshotter, _ := notifyLog.(grouping.NflogSnapshotter)

	return &ServiceRegistry{
		config:          cfg,
		logger:          slog.Default(),
		silenceStore:    memory.NewSilenceStore(),
		memoryNotifyLog: snapshotter,
	}
}

func stopSnapshotWriter(t *testing.T, r *ServiceRegistry) {
	t.Helper()
	if r.snapshotCancel == nil {
		return
	}
	r.snapshotCancel()
	<-r.snapshotDone
	r.snapshotCancel = nil
	r.snapshotDone = nil
}

func TestInitializeSnapshotting_DisabledByDefault(t *testing.T) {
	r := newTestRegistryForSnapshot(appconfig.ProfileLite, "", time.Minute)

	require.NoError(t, r.initializeSnapshotting(context.Background()))

	assert.Nil(t, r.snapshotCancel, "no periodic writer should start when storage.path is empty")
	assert.Equal(t, "", r.snapshotPath)
}

func TestInitializeSnapshotting_StandardProfileSkipsEvenIfPathSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	r := newTestRegistryForSnapshot(appconfig.ProfileStandard, path, time.Minute)

	require.NoError(t, r.initializeSnapshotting(context.Background()))

	assert.Nil(t, r.snapshotCancel, "standard profile must never engage file snapshotting")
	assert.Equal(t, "", r.snapshotPath)

	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err), "no file should be written for the standard profile")
}

func TestInitializeSnapshotting_LitePofile_MissingFileStartsEmptyAndStartsWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	r := newTestRegistryForSnapshot(appconfig.ProfileLite, path, time.Hour) // long interval: the tick must not fire during the test

	require.NoError(t, r.initializeSnapshotting(context.Background()))
	defer stopSnapshotWriter(t, r)

	assert.NotNil(t, r.snapshotCancel, "the periodic writer must start once snapshotting is engaged")
	assert.Equal(t, path, r.snapshotPath)
	assert.Empty(t, r.silenceStore.List(time.Now().UTC()), "no snapshot existed yet, silences must start empty")
}

func TestInitializeSnapshotting_LiteProfile_CorruptFileStartsEmptyNoCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0o600))

	r := newTestRegistryForSnapshot(appconfig.ProfileLite, path, time.Hour)

	require.NotPanics(t, func() {
		require.NoError(t, r.initializeSnapshotting(context.Background()))
	})
	defer stopSnapshotWriter(t, r)

	assert.Empty(t, r.silenceStore.List(time.Now().UTC()))
}

func TestInitializeSnapshotting_LiteProfile_RestoresExistingSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	now := time.Now().UTC()

	pre := snapshot.Data{
		Version:   snapshot.CurrentVersion,
		WrittenAt: now,
		Silences: []core.APISilence{
			{
				ID:       "silence-restore",
				Matchers: []core.APISilenceMatcher{{Name: "alertname", Value: "RestoreMe", IsEqual: true}},
				StartsAt: now.Add(-time.Minute).Format(time.RFC3339),
				EndsAt:   now.Add(time.Hour).Format(time.RFC3339),
			},
		},
		Nflog: grouping.NflogSnapshot{
			Entries: []grouping.NflogEntrySnapshot{
				{GroupKey: "gk-restore", Target: "target-a", Signature: "sig", SentAt: now, TTL: 5 * time.Minute},
			},
		},
	}
	require.NoError(t, snapshot.Write(path, pre))

	r := newTestRegistryForSnapshot(appconfig.ProfileLite, path, time.Hour)
	require.NoError(t, r.initializeSnapshotting(context.Background()))
	defer stopSnapshotWriter(t, r)

	silences := r.silenceStore.List(now)
	require.Len(t, silences, 1)
	assert.Equal(t, "silence-restore", silences[0].ID)

	dup, err := r.memoryNotifyLog.(grouping.GroupNotifyLog).IsDuplicate(context.Background(), "gk-restore", "target-a", "sig", now.Add(-time.Second))
	require.NoError(t, err)
	assert.True(t, dup, "restored nflog entry must report as a duplicate")
}

func TestWriteSnapshot_PersistsSilencesAndNflog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	now := time.Now().UTC()

	r := newTestRegistryForSnapshot(appconfig.ProfileLite, path, time.Hour)

	_, err := r.silenceStore.CreateOrUpdate(&core.SilenceInput{
		Matchers: []core.SilenceMatcherInput{{Name: "alertname", Value: "WriteMe"}},
		EndsAt:   now.Add(time.Hour).Format(time.RFC3339),
	}, now)
	require.NoError(t, err)

	notifyLog := r.memoryNotifyLog.(grouping.GroupNotifyLog)
	require.NoError(t, notifyLog.RecordSent(context.Background(), "gk-write", "target-a", "sig", now, 5*time.Minute))

	require.NoError(t, r.writeSnapshot(path))

	data, err := snapshot.Load(path)
	require.NoError(t, err)
	require.Len(t, data.Silences, 1)
	require.Len(t, data.Nflog.Entries, 1)
	assert.Equal(t, "gk-write", string(data.Nflog.Entries[0].GroupKey))
}

func TestShutdown_WritesFinalSnapshotAndStopsWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	now := time.Now().UTC()

	r := newTestRegistryForSnapshot(appconfig.ProfileLite, path, time.Hour)
	require.NoError(t, r.initializeSnapshotting(context.Background()))

	_, err := r.silenceStore.CreateOrUpdate(&core.SilenceInput{
		Matchers: []core.SilenceMatcherInput{{Name: "alertname", Value: "ShutdownFlush"}},
		EndsAt:   now.Add(time.Hour).Format(time.RFC3339),
	}, now)
	require.NoError(t, err)

	require.NoError(t, r.Shutdown(context.Background()))

	assert.Nil(t, r.snapshotCancel, "Shutdown must stop the periodic writer")
	assert.Equal(t, "", r.snapshotPath, "Shutdown must clear snapshotPath after the final write")

	data, err := snapshot.Load(path)
	require.NoError(t, err, "Shutdown must have written a final snapshot to disk")
	require.Len(t, data.Silences, 1)
	assert.Equal(t, "ShutdownFlush", data.Silences[0].Matchers[0].Value)
}

func TestShutdown_NoSnapshotPathIsNoop(t *testing.T) {
	r := &ServiceRegistry{
		config: &appconfig.Config{},
		logger: slog.Default(),
	}
	require.NoError(t, r.Shutdown(context.Background()))
}
