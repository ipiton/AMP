package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testData(now time.Time) Data {
	return Data{
		Version:   CurrentVersion,
		WrittenAt: now,
		Silences: []core.APISilence{
			{
				ID:       "silence-1",
				Matchers: []core.APISilenceMatcher{{Name: "alertname", Value: "TestAlert", IsEqual: true}},
				StartsAt: now.Format(time.RFC3339),
				EndsAt:   now.Add(time.Hour).Format(time.RFC3339),
				Status:   core.APISilenceStatus{State: "active"},
			},
		},
		Nflog: grouping.NflogSnapshot{
			Entries: []grouping.NflogEntrySnapshot{
				{GroupKey: "gk-1", Target: "target-a", Signature: "sig-1", SentAt: now, TTL: 5 * time.Minute},
			},
			Delivered: []grouping.NflogDeliveredSnapshot{
				{GroupKey: "gk-2", Target: "target-b", Statuses: map[string]string{"fp1": "firing"}, RecordedAt: now, TTL: 5 * time.Minute},
			},
		},
	}
}

func TestWriteLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "snapshot.json")
	now := time.Now().UTC().Truncate(time.Second)

	want := testData(now)
	require.NoError(t, Write(path, want))

	got, err := Load(path)
	require.NoError(t, err)

	assert.Equal(t, want.Version, got.Version)
	assert.True(t, want.WrittenAt.Equal(got.WrittenAt))
	require.Len(t, got.Silences, 1)
	assert.Equal(t, want.Silences[0].ID, got.Silences[0].ID)
	assert.Equal(t, want.Silences[0].Matchers, got.Silences[0].Matchers)
	require.Len(t, got.Nflog.Entries, 1)
	assert.Equal(t, want.Nflog.Entries[0].Signature, got.Nflog.Entries[0].Signature)
	require.Len(t, got.Nflog.Delivered, 1)
	assert.Equal(t, want.Nflog.Delivered[0].Statuses, got.Nflog.Delivered[0].Statuses)
}

func TestWrite_CreatesDirectoryIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does", "not", "exist", "yet", "snapshot.json")

	require.NoError(t, Write(path, testData(time.Now().UTC())))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, info.IsDir())
}

func TestWrite_FilePermissionsAre0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	require.NoError(t, Write(path, testData(time.Now().UTC())))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWrite_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	require.NoError(t, Write(path, testData(time.Now().UTC())))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the final snapshot file should remain, no .snapshot-*.tmp leftovers")
	assert.Equal(t, "snapshot.json", entries[0].Name())
}

func TestLoad_MissingFileReturnsErrNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "never-written.json")

	_, err := Load(path)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotExist))
}

func TestLoad_CorruptFileErrorsWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0o600))

	_, err := Load(path)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotExist))
}

func TestLoad_TruncatedFileErrorsWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	// Write valid JSON, then truncate mid-stream — simulates a crash during
	// a non-atomic write (never happens via Write itself, but a hand-edited
	// or externally-corrupted file must still be tolerated).
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"silences":[{"id":"a"`), 0o600))

	_, err := Load(path)
	require.Error(t, err)
}

func TestLoad_EmptyFileErrorsWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))

	_, err := Load(path)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrNotExist))
}

func TestLoad_UnsupportedVersionRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	future := testData(time.Now().UTC())
	future.Version = CurrentVersion + 1
	require.NoError(t, Write(path, future))

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported version")
}

// TestWrite_ConcurrentWritesToSamePath proves the atomic tmp-file+rename
// scheme survives many concurrent writers targeting the same path (the
// periodic snapshot writer racing the final shutdown write, or — as a
// stress case — far more concurrency than production ever has): every
// individual Write call must either fully succeed or fully fail, and a Load
// immediately after must NEVER observe a partially-written/corrupt file.
// Run with -race.
func TestWrite_ConcurrentWritesToSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")

	const writers = 20
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			data := testData(time.Now().UTC())
			data.Nflog.Entries[0].Target = "target-" + string(rune('a'+i%26))
			errs[i] = Write(path, data)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "writer %d", i)
	}

	got, err := Load(path)
	require.NoError(t, err, "the file left behind by concurrent writers must still be valid JSON")
	assert.Equal(t, CurrentVersion, got.Version)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no leftover temp files after concurrent writers")
}
