package grouping

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestRedisNotifyLog creates a RedisNotifyLog backed by miniredis,
// mirroring setupTestRedisStorage in redis_timer_storage_test.go.
func setupTestRedisNotifyLog(t *testing.T) (*RedisNotifyLog, *miniredis.Miniredis, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	require.NoError(t, err)

	notifyLog, err := NewRedisNotifyLog(context.Background(), &RedisNotifyLogConfig{
		Client: redisCache.GetClient(),
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	cleanup := func() {
		_ = redisCache.Close()
		mr.Close()
	}

	return notifyLog, mr, cleanup
}

// newSecondReplicaNotifyLog builds a SECOND RedisNotifyLog instance sharing
// the same miniredis backend, simulating a second HA replica process that
// only shares state via Redis (no shared Go objects/locks).
func newSecondReplicaNotifyLog(t *testing.T, mr *miniredis.Miniredis) (*RedisNotifyLog, func()) {
	t.Helper()

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	require.NoError(t, err)

	notifyLog, err := NewRedisNotifyLog(context.Background(), &RedisNotifyLogConfig{
		Client: redisCache.GetClient(),
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	return notifyLog, func() { _ = redisCache.Close() }
}

func TestRedisNotifyLog_IsDuplicate_NoEntry_NotDuplicate(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	dup, err := notifyLog.IsDuplicate(ctx, GroupKey("receiver=default/alertname=X"), "t1", "sig-1", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.False(t, dup, "no entry ever recorded must never be a duplicate")
}

// TestRedisNotifyLog_DedupAcrossTwoReplicas_FirstRecordsSecondSeesDuplicate
// is the core task 6.1 cross-replica correctness test: one replica's
// RecordSent must be visible to a completely separate RedisNotifyLog
// instance (a second replica) sharing only the Redis backend.
func TestRedisNotifyLog_DedupAcrossTwoReplicas_FirstRecordsSecondSeesDuplicate(t *testing.T) {
	replicaA, mr, cleanupA := setupTestRedisNotifyLog(t)
	defer cleanupA()
	replicaB, cleanupB := newSecondReplicaNotifyLog(t, mr)
	defer cleanupB()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	signature := "fp1:firing"
	repeatInterval := time.Hour

	now := time.Now()
	require.NoError(t, replicaA.RecordSent(ctx, groupKey, "t1", signature, now, repeatInterval))

	ttl := now.Add(-repeatInterval).Add(-time.Second) // cutoff strictly before now: fresh send counts as duplicate
	dup, err := replicaB.IsDuplicate(ctx, groupKey, "t1", signature, ttl)
	require.NoError(t, err)
	assert.True(t, dup, "replica B must see replica A's RecordSent via the shared Redis backend")
}

func TestRedisNotifyLog_SignatureChange_NotDuplicate(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	now := time.Now()

	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "t1", "fp1:firing", now, time.Hour))

	ttl := now.Add(-time.Hour).Add(-time.Second)
	dup, err := notifyLog.IsDuplicate(ctx, groupKey, "t1", "fp1:firing|fp2:firing", ttl)
	require.NoError(t, err)
	assert.False(t, dup, "a changed alert set must never be treated as a duplicate")
}

// TestRedisNotifyLog_TTLExpiry_NotDuplicate proves that once the caller's
// cutoff (ttl) moves past the recorded SentAt — the same "repeat_interval
// elapsed" condition DefaultGroupManager computes on every fire — the same
// signature is no longer a duplicate.
func TestRedisNotifyLog_TTLExpiry_NotDuplicate(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	sentAt := time.Now().Add(-2 * time.Hour) // sent 2h ago
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "t1", "fp1:firing", sentAt, time.Hour))

	// Cutoff = now - 1h repeat_interval = 1h ago. sentAt (2h ago) is before
	// that cutoff, so it must no longer count as a duplicate.
	ttl := time.Now().Add(-time.Hour)
	dup, err := notifyLog.IsDuplicate(ctx, groupKey, "t1", "fp1:firing", ttl)
	require.NoError(t, err)
	assert.False(t, dup, "a send older than repeat_interval must not be deduped")
}

// TestRedisNotifyLog_EntryExpiresFromRedisAfterRepeatIntervalTTL proves the
// Redis key itself expires (not just the application-level cutoff check),
// so an abandoned group's entry doesn't outlive it indefinitely.
func TestRedisNotifyLog_EntryExpiresFromRedisAfterRepeatIntervalTTL(t *testing.T) {
	notifyLog, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "t1", "fp1:firing", time.Now(), time.Minute))

	// TTL = repeatInterval (1m) + grace period (60s) = 2m. Fast-forward past it.
	mr.FastForward(3 * time.Minute)

	dup, err := notifyLog.IsDuplicate(ctx, groupKey, "t1", "fp1:firing", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.False(t, dup, "the Redis key must have expired, so no entry is found at all")
}

// TestRedisNotifyLog_Forget_RemovesEntryOnly proves fix round 1, Finding 2:
// Forget must clear the entry but must NOT touch a live claim. Forget's
// callers (RemoveAlertFromGroup/CleanupExpiredGroups) run under a
// different lock than the claim->check->publish->record sequence in
// publishGroupAlerts, so a group can be deleted while another replica
// still holds a live claim for it — clearing that claim early would let a
// third replica race in and publish concurrently.
func TestRedisNotifyLog_Forget_RemovesEntryOnly(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "t1", "fp1:firing", time.Now(), time.Hour))

	acquired, _, err := notifyLog.TryClaim(ctx, groupKey, 30*time.Second)
	require.NoError(t, err)
	require.True(t, acquired) // leave the claim in place: Forget must not clear it

	require.NoError(t, notifyLog.Forget(ctx, groupKey))

	dup, err := notifyLog.IsDuplicate(ctx, groupKey, "t1", "fp1:firing", time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.False(t, dup, "Forget must remove the entry")

	// The claim must still be held: a concurrent TryClaim must fail.
	acquired2, _, err := notifyLog.TryClaim(ctx, groupKey, 30*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired2, "Forget must NOT clear a live claim (fix round 1, Finding 2)")
}

// === Task fwb: per-target nflog granularity ===

// TestRedisNotifyLog_PerTarget_IndependentDedup proves the core task fwb
// property: two DIFFERENT targets for the SAME group and the SAME alert set
// have independent dedup state. Recording a send for target "webhook-a"
// must not make target "webhook-b" read as a duplicate, and vice versa —
// this is what lets a retry after a partial failure resend to only the
// target that actually failed.
func TestRedisNotifyLog_PerTarget_IndependentDedup(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	signature := "fp1:firing"
	now := time.Now()
	ttl := now.Add(-time.Hour)

	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "webhook-a", signature, now, time.Hour))

	dupA, err := notifyLog.IsDuplicate(ctx, groupKey, "webhook-a", signature, ttl)
	require.NoError(t, err)
	assert.True(t, dupA, "webhook-a confirmed delivery and must dedup on the next fire")

	dupB, err := notifyLog.IsDuplicate(ctx, groupKey, "webhook-b", signature, ttl)
	require.NoError(t, err)
	assert.False(t, dupB, "webhook-b never confirmed delivery — it must NOT be deduped by webhook-a's entry")
}

// TestRedisNotifyLog_PerTarget_ChangedSignatureBustsDedupForThatTargetOnly
// pins that a changed alert set busts dedup per target, exactly like the
// pre-existing group-level TestRedisNotifyLog_SignatureChange_NotDuplicate,
// but confirms it holds independently for each target's own entry.
func TestRedisNotifyLog_PerTarget_ChangedSignatureBustsDedupForThatTargetOnly(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	now := time.Now()
	ttl := now.Add(-time.Hour)

	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "webhook-a", "fp1:firing", now, time.Hour))
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "webhook-b", "fp1:firing", now, time.Hour))

	// The alert set changes (e.g. a new alert joins the group). Both
	// targets' entries were recorded against the OLD signature, so neither
	// may dedup the new one.
	dupA, err := notifyLog.IsDuplicate(ctx, groupKey, "webhook-a", "fp1:firing|fp2:firing", ttl)
	require.NoError(t, err)
	assert.False(t, dupA, "a changed alert set must bust dedup even though webhook-a has a recorded entry")

	dupB, err := notifyLog.IsDuplicate(ctx, groupKey, "webhook-b", "fp1:firing|fp2:firing", ttl)
	require.NoError(t, err)
	assert.False(t, dupB, "a changed alert set must bust dedup for every target, not just one")
}

// TestRedisNotifyLog_PerTarget_KeyFormatIncludesTarget locks in the actual
// Redis key shape (nflog:entry:{groupKey}:{target}) so a regression back to
// the pre-task-fwb bare group-level key is caught directly, not just via its
// behavioural symptoms.
func TestRedisNotifyLog_PerTarget_KeyFormatIncludesTarget(t *testing.T) {
	notifyLog, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "webhook-a", "fp1:firing", time.Now(), time.Hour))

	assert.True(t, mr.Exists("nflog:entry:receiver=default/alertname=HighCPU:webhook-a"),
		"the entry key must be suffixed with the target name")
	assert.False(t, mr.Exists("nflog:entry:receiver=default/alertname=HighCPU"),
		"the old bare (pre-task-fwb) group-only key must never be written")
}

// TestRedisNotifyLog_Forget_RemovesEveryTargetEntry proves Forget now clears
// ALL of a group's per-target entries, not just one — a group can have
// accumulated one entry per target that ever confirmed delivery within the
// current repeat_interval.
func TestRedisNotifyLog_Forget_RemovesEveryTargetEntry(t *testing.T) {
	notifyLog, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "webhook-a", "fp1:firing", time.Now(), time.Hour))
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "webhook-b", "fp1:firing", time.Now(), time.Hour))
	require.NoError(t, notifyLog.RecordSent(ctx, groupKey, "webhook-c", "fp1:firing", time.Now(), time.Hour))

	require.NoError(t, notifyLog.Forget(ctx, groupKey))

	for _, target := range []string{"webhook-a", "webhook-b", "webhook-c"} {
		dup, err := notifyLog.IsDuplicate(ctx, groupKey, target, "fp1:firing", time.Now().Add(-time.Hour))
		require.NoError(t, err)
		assert.False(t, dup, "Forget must remove the entry for target %q", target)
	}

	assert.Empty(t, mr.Keys(), "Forget must also remove the per-group target-tracking set, leaving no orphaned keys")
}

// TestRedisNotifyLog_TryClaim_ConcurrentAcrossTwoReplicas_ExactlyOneWins is
// the task 6.1 correctness bar: two "replicas" (two independent
// RedisNotifyLog instances, sharing only the miniredis backend) racing
// TryClaim for the SAME groupKey at the same time — exactly one must
// acquire it.
func TestRedisNotifyLog_TryClaim_ConcurrentAcrossTwoReplicas_ExactlyOneWins(t *testing.T) {
	replicaA, mr, cleanupA := setupTestRedisNotifyLog(t)
	defer cleanupA()
	replicaB, cleanupB := newSecondReplicaNotifyLog(t, mr)
	defer cleanupB()

	groupKey := GroupKey("receiver=default/alertname=HighCPU")
	claimTTL := 30 * time.Second

	const attemptsPerReplica = 25
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0

	race := func(nl *RedisNotifyLog) {
		defer wg.Done()
		ctx := context.Background()
		for i := 0; i < attemptsPerReplica; i++ {
			acquired, _, err := nl.TryClaim(ctx, groupKey, claimTTL)
			require.NoError(t, err)
			if acquired {
				mu.Lock()
				wins++
				mu.Unlock()
				return // this "fire" won; a real caller would release later
			}
		}
	}

	wg.Add(2)
	go race(replicaA)
	go race(replicaB)
	wg.Wait()

	assert.Equal(t, 1, wins, "exactly one replica must win the claim for the same group at the same time")
}

func TestRedisNotifyLog_TryClaim_ReleaseThenReclaim(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")

	acquired, release, err := notifyLog.TryClaim(ctx, groupKey, 30*time.Second)
	require.NoError(t, err)
	require.True(t, acquired)

	// A second attempt while the claim is held must fail.
	acquired2, _, err := notifyLog.TryClaim(ctx, groupKey, 30*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired2, "claim must be held until released or expired")

	require.NoError(t, release())

	// After release, a fresh claim must succeed immediately.
	acquired3, _, err := notifyLog.TryClaim(ctx, groupKey, 30*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired3, "claim must be re-acquirable immediately after release")
}

// TestRedisNotifyLog_ClaimExpiresAfterCrash proves the "crashed replica's
// claim expires" requirement: a claim that is never released still frees
// up once claimTTL elapses.
func TestRedisNotifyLog_ClaimExpiresAfterCrash(t *testing.T) {
	notifyLog, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=HighCPU")

	acquired, _, err := notifyLog.TryClaim(ctx, groupKey, 5*time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	// Simulate a crash: never call release.

	mr.FastForward(6 * time.Second)

	acquired2, _, err := notifyLog.TryClaim(ctx, groupKey, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired2, "an expired (never-released) claim must free up after claimTTL")
}

// === Redis-down fail-open surface: RedisNotifyLog must return an error,
// not silently swallow it — DefaultGroupManager.publishGroupAlerts is
// responsible for the fail-open decision, not this type. ===

func TestRedisNotifyLog_RedisDown_IsDuplicateReturnsError(t *testing.T) {
	notifyLog, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()
	mr.Close()

	ctx := context.Background()
	_, err := notifyLog.IsDuplicate(ctx, GroupKey("receiver=default/alertname=X"), "t1", "sig", time.Now())
	assert.Error(t, err, "IsDuplicate must surface a Redis-down error rather than silently returning false")
}

func TestRedisNotifyLog_RedisDown_RecordSentReturnsError(t *testing.T) {
	notifyLog, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()
	mr.Close()

	ctx := context.Background()
	err := notifyLog.RecordSent(ctx, GroupKey("receiver=default/alertname=X"), "t1", "sig", time.Now(), time.Hour)
	assert.Error(t, err, "RecordSent must surface a Redis-down error")
}

func TestRedisNotifyLog_RedisDown_TryClaimReturnsError(t *testing.T) {
	notifyLog, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()
	mr.Close()

	ctx := context.Background()
	acquired, release, err := notifyLog.TryClaim(ctx, GroupKey("receiver=default/alertname=X"), 30*time.Second)
	assert.Error(t, err, "TryClaim must surface a Redis-down error")
	assert.False(t, acquired)
	assert.NoError(t, release(), "release must remain a safe no-op even after a failed claim attempt")
}

func TestNewRedisNotifyLog_NilConfig(t *testing.T) {
	_, err := NewRedisNotifyLog(context.Background(), nil)
	assert.Error(t, err)
}

func TestNewRedisNotifyLog_NilClient(t *testing.T) {
	_, err := NewRedisNotifyLog(context.Background(), &RedisNotifyLogConfig{})
	assert.Error(t, err)
}

func TestNewRedisNotifyLog_UnreachableRedis(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	require.NoError(t, err)
	defer func() { _ = redisCache.Close() }()
	mr.Close()

	_, err = NewRedisNotifyLog(context.Background(), &RedisNotifyLogConfig{Client: redisCache.GetClient()})
	assert.Error(t, err, "construction must fail fast when Redis is unreachable")
}
