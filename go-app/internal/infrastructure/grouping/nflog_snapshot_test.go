package grouping

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Wave 6 (FU-LITE-FILE-SNAPSHOT): notifyDedupLog file-snapshot support ===

func TestNflogSnapshot_RoundTrip(t *testing.T) {
	ctx := context.Background()
	log := newNotifyDedupLog()
	now := time.Now().UTC()

	require.NoError(t, log.RecordSent(ctx, "gk-1", "target-a", "sig-1", now, 5*time.Minute))
	require.NoError(t, log.RecordSent(ctx, "gk-2", "target-b", "sig-2", now, 0)) // fallback TTL path
	require.NoError(t, log.RecordPartialDelivery(ctx, "gk-3", "target-c", []string{"fp1:firing", "fp2:resolved"}, 5*time.Minute))

	snap := log.SnapshotNflog()
	require.Len(t, snap.Entries, 2)
	require.Len(t, snap.Delivered, 1)

	restored := newNotifyDedupLog()
	require.NoError(t, restored.LoadNflogSnapshot(snap, now))

	// IsDuplicate/DeliveredAlerts must see identical state to the original.
	dup, err := restored.IsDuplicate(ctx, "gk-1", "target-a", "sig-1", now.Add(-time.Second))
	require.NoError(t, err)
	assert.True(t, dup, "restored entry should report as duplicate for a cutoff before sentAt")

	dup, err = restored.IsDuplicate(ctx, "gk-2", "target-b", "sig-2", now.Add(-time.Second))
	require.NoError(t, err)
	assert.True(t, dup)

	delivered, err := restored.DeliveredAlerts(ctx, "gk-3", "target-c")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"fp1:firing", "fp2:resolved"}, delivered)

	// A snapshot taken from the restored log must match the original
	// (order-independent — both are built from Go maps).
	roundTripped := restored.SnapshotNflog()
	assert.ElementsMatch(t, snap.Entries, roundTripped.Entries)
	assertDeliveredSnapshotsMatch(t, snap.Delivered, roundTripped.Delivered)
}

func assertDeliveredSnapshotsMatch(t *testing.T, want, got []NflogDeliveredSnapshot) {
	t.Helper()
	require.Len(t, got, len(want))
	byKey := make(map[string]NflogDeliveredSnapshot, len(got))
	for _, d := range got {
		byKey[string(d.GroupKey)+"/"+d.Target] = d
	}
	for _, w := range want {
		g, ok := byKey[string(w.GroupKey)+"/"+w.Target]
		require.True(t, ok, "missing (%s,%s) in round-tripped snapshot", w.GroupKey, w.Target)
		assert.Equal(t, w.Statuses, g.Statuses)
		assert.True(t, w.RecordedAt.Equal(g.RecordedAt))
		assert.Equal(t, w.TTL, g.TTL)
	}
}

func TestNflogSnapshot_TTLExpiryAtLoad_Entries(t *testing.T) {
	ctx := context.Background()
	log := newNotifyDedupLog()
	writtenAt := time.Now().UTC()

	require.NoError(t, log.RecordSent(ctx, "gk-fresh", "target-a", "sig", writtenAt, time.Minute))
	require.NoError(t, log.RecordSent(ctx, "gk-stale", "target-a", "sig", writtenAt, time.Minute))

	snap := log.SnapshotNflog()
	require.Len(t, snap.Entries, 2)

	// Load as if the process was down long enough for gk-stale's window
	// (repeat_interval=1m + 60s grace = 2m) to have elapsed, but well within
	// gk-fresh's identical window computed from a LATER writtenAt... instead
	// simulate by loading far enough in the future that BOTH would expire
	// under a shared TTL, then verify the per-entry TTL from a shorter-lived
	// write actually gets dropped while a freshly-reset one survives.
	restored := newNotifyDedupLog()
	loadTime := writtenAt.Add(90 * time.Second) // inside gk-fresh/gk-stale's 2m window
	require.NoError(t, restored.LoadNflogSnapshot(snap, loadTime))
	restoredSnap := restored.SnapshotNflog()
	assert.Len(t, restoredSnap.Entries, 2, "both entries still within their TTL window at 90s")

	// Now load past the window (repeat_interval 1m + 60s grace = 2m) — both
	// must be dropped.
	restored2 := newNotifyDedupLog()
	pastLoadTime := writtenAt.Add(3 * time.Minute)
	require.NoError(t, restored2.LoadNflogSnapshot(snap, pastLoadTime))
	restored2Snap := restored2.SnapshotNflog()
	assert.Empty(t, restored2Snap.Entries, "entries past their TTL window must be dropped at load")
}

func TestNflogSnapshot_TTLExpiryAtLoad_MixedFreshAndStale(t *testing.T) {
	ctx := context.Background()
	log := newNotifyDedupLog()
	base := time.Now().UTC()

	// Short-lived entry (repeat_interval=10s -> ttl=70s) recorded early.
	require.NoError(t, log.RecordSent(ctx, "gk-short", "target-a", "sig", base, 10*time.Second))
	// Long-lived entry (repeat_interval=1h -> ttl=1h+60s) recorded at the
	// same instant.
	require.NoError(t, log.RecordSent(ctx, "gk-long", "target-a", "sig", base, time.Hour))

	snap := log.SnapshotNflog()
	require.Len(t, snap.Entries, 2)

	restored := newNotifyDedupLog()
	// 5 minutes later: short-lived entry's 70s window has elapsed, long-lived
	// entry's ~61m window has not.
	require.NoError(t, restored.LoadNflogSnapshot(snap, base.Add(5*time.Minute)))

	restoredSnap := restored.SnapshotNflog()
	require.Len(t, restoredSnap.Entries, 1)
	assert.Equal(t, GroupKey("gk-long"), restoredSnap.Entries[0].GroupKey)
}

func TestNflogSnapshot_TTLExpiryAtLoad_DeliveredState(t *testing.T) {
	ctx := context.Background()
	log := newNotifyDedupLog()
	base := time.Now().UTC()

	require.NoError(t, log.RecordPartialDelivery(ctx, "gk-1", "target-a", []string{"fp1:firing"}, 10*time.Second))

	snap := log.SnapshotNflog()
	require.Len(t, snap.Delivered, 1)

	restored := newNotifyDedupLog()
	require.NoError(t, restored.LoadNflogSnapshot(snap, base.Add(5*time.Minute)))

	delivered, err := restored.DeliveredAlerts(ctx, "gk-1", "target-a")
	require.NoError(t, err)
	assert.Empty(t, delivered, "delivered state past its TTL window must be dropped at load")
}

func TestNflogSnapshot_MissingTTLFallsBackToDefault(t *testing.T) {
	// A record with TTL==0 (e.g. an older snapshot format) must not be
	// treated as "never expires" — it falls back to deliveredStateTTL(0).
	now := time.Now().UTC()
	snap := NflogSnapshot{
		Entries: []NflogEntrySnapshot{
			{GroupKey: "gk", Target: "t", Signature: "s", SentAt: now.Add(-5 * time.Hour), TTL: 0},
		},
	}

	restored := newNotifyDedupLog()
	require.NoError(t, restored.LoadNflogSnapshot(snap, now))

	restoredSnap := restored.SnapshotNflog()
	assert.Empty(t, restoredSnap.Entries, "a 5h-old entry must be dropped under the ~4h1m fallback TTL")
}

func TestNewMemoryNotifyLog_ImplementsSnapshotter(t *testing.T) {
	log := NewMemoryNotifyLog()
	_, ok := log.(NflogSnapshotter)
	require.True(t, ok, "NewMemoryNotifyLog's return value must implement NflogSnapshotter")
}

func TestRedisNotifyLog_DoesNotImplementSnapshotter(t *testing.T) {
	redisLog, _, cleanup := setupTestRedisNotifyLog(t)
	t.Cleanup(cleanup)

	_, ok := interface{}(redisLog).(NflogSnapshotter)
	assert.False(t, ok, "RedisNotifyLog must not implement NflogSnapshotter — Redis owns its own durability")
}
