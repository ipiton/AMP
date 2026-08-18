package grouping

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/stretchr/testify/require"
)

// Task 6.2: distributed group timers — exactly-once firing across replicas,
// plus liveness after a replica crash. RedisTimerStorage.AcquireLock and the
// AcquireLock-gated onTimerExpired already existed before this task (see
// timer_manager_impl.go); what this file newly proves and newly adds is:
//
//  1. TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires
//     — the pre-existing per-fire lock actually arbitrates correctly between
//     two independent DefaultTimerManager instances sharing one Redis
//     backend (no test previously exercised this at the manager level, only
//     at RedisTimerStorage's own unit level).
//  2. TestDefaultTimerManager_ReconciliationLoop_AdoptsOrphanedTimer — the
//     NEW periodic reconciliation loop (task 6.2) that adopts a timer left
//     behind by a crashed replica, closing the liveness gap RestoreTimers
//     (startup-only) cannot: see reconciliationLoop/reconcileOrphanedTimers
//     in timer_manager_impl.go.
//  3. TestPublishCallbacks_FreshLoadAtFireTime_SeesAlertAddedByOtherReplica
//     — onGroupWaitExpired/onGroupIntervalExpired/onRepeatIntervalExpired
//     (manager_impl.go) re-Load the group from shared storage at fire time
//     rather than publishing the (possibly stale) snapshot handed to the
//     TimerCallback — a pre-existing property, now covered by a regression
//     test using storage actually SHARED across two replicas (task 6.1's
//     equivalent tests deliberately used separate per-replica storage; see
//     redis_notify_chain_test.go's doc comment).
//  4. TestDefaultTimerManager_ReconciliationDisabledByDefault_OrphanNotAdopted
//     — ReconciliationInterval's zero-value default (what the lite profile
//     and ServiceRegistry's Redis-fallback path both leave it at) is a full,
//     clean no-op.

// syncBuffer is a concurrency-safe io.Writer for capturing slog output from
// multiple DefaultTimerManager goroutines in the same test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newTimerStorageGroupManagerStub builds a minimal DefaultGroupManager
// backed by its own in-memory GroupStorage, seeded with one firing alert
// under groupKey — enough for onTimerExpired's gm.GetGroup call and a
// registered fake TimerCallback, without needing the full notify chain
// (publishLocks/notifyLog are deliberately left nil, matching the existing
// setupTestTimerManager helper in timer_manager_impl_test.go — the tests
// here register their own OnTimerExpired callbacks directly, never routing
// through DefaultGroupManager.registerTimerCallbacks/publishGroupAlerts).
func newTimerStorageGroupManagerStub(t *testing.T, logger *slog.Logger, groupKey GroupKey) *DefaultGroupManager {
	t.Helper()
	gm := &DefaultGroupManager{
		storage:          NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: logger}),
		fingerprintIndex: make(map[string]GroupKey),
		logger:           logger,
	}
	group := &AlertGroup{
		Key:    groupKey,
		Alerts: map[string]*core.Alert{"fp": createTestAlert("A", core.StatusFiring, map[string]string{"alertname": string(groupKey)})},
		Metadata: &GroupMetadata{
			State:     GroupStateFiring,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	require.NoError(t, gm.storage.Store(context.Background(), group))
	return gm
}

// TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires
// simulates the real HA race this mechanism exists for: two replicas both
// believe they need to start a group_wait timer for the SAME group at
// (almost) the same time — e.g. both independently observed the group as
// "not yet created" against shared Redis GroupStorage and both raced to
// create it — so both end up with their own local Go timer for the same
// GroupKey. When both fire, only the RedisTimerStorage.AcquireLock winner
// must run its callback; the loser must skip quietly (not error).
func TestDefaultTimerManager_TwoReplicasRaceSameGroupTimer_OnlyLockWinnerFires(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	var logBuf syncBuffer
	// Level: Debug — onTimerExpired logs the AcquireLock loser's quiet skip
	// at Debug (see timer_manager_impl.go), which the default Info level
	// would otherwise filter out of logBuf before the assertion below.
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	const groupKey = GroupKey("alertname=RaceGroup")
	var publishCount atomic.Int64

	newReplica := func() (*DefaultTimerManager, func()) {
		redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
			Addr:        mr.Addr(),
			DB:          0,
			PoolSize:    5,
			DialTimeout: time.Second,
			ReadTimeout: time.Second,
		}, logger)
		require.NoError(t, err)

		storage, err := NewRedisTimerStorage(redisCache, logger)
		require.NoError(t, err)

		gm := newTimerStorageGroupManagerStub(t, logger, groupKey)

		tm, err := NewDefaultTimerManager(TimerManagerConfig{
			Storage:      storage,
			GroupManager: gm,
			Logger:       logger,
		})
		require.NoError(t, err)

		// Stands in for the notify-chain's eventual publisher call — the
		// point under test is whether this callback runs at all on this
		// replica, not the chain behind it (task 6.1 already covers that).
		tm.OnTimerExpired(func(_ context.Context, _ GroupKey, _ TimerType, _ *AlertGroup) error {
			publishCount.Add(1)
			return nil
		})

		return tm, func() { _ = redisCache.Close() }
	}

	tmA, closeA := newReplica()
	defer closeA()
	tmB, closeB := newReplica()
	defer closeB()

	ctx := context.Background()
	const fireIn = 80 * time.Millisecond
	_, err = tmA.StartTimer(ctx, groupKey, GroupWaitTimer, fireIn)
	require.NoError(t, err)
	_, err = tmB.StartTimer(ctx, groupKey, GroupWaitTimer, fireIn)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return publishCount.Load() >= 1 }, 2*time.Second, 10*time.Millisecond,
		"at least one replica must fire")

	// Give the loser's goroutine time to reach (and lose) its own
	// AcquireLock attempt before asserting the final count.
	time.Sleep(200 * time.Millisecond)

	require.EqualValues(t, 1, publishCount.Load(),
		"two replicas racing the same group's timer must fire exactly once — the AcquireLock loser must skip, not also run the callback")
	require.Contains(t, logBuf.String(), "Lock already acquired by another instance",
		"the losing replica must log a quiet skip via onTimerExpired's existing ErrLockAlreadyAcquired branch")

	require.NoError(t, tmA.Shutdown(context.Background()))
	require.NoError(t, tmB.Shutdown(context.Background()))
}

// TestDefaultTimerManager_ReconciliationLoop_AdoptsOrphanedTimer simulates a
// replica that started a group timer and then crashed before it fired: the
// timer entry sits in shared Redis storage, its ExpiresAt now well in the
// past, with no lock held and no local Go timer anywhere (the crashed
// replica's process is simply gone). A fresh DefaultTimerManager — standing
// in for a SURVIVING replica — must adopt and fire it via its
// reconciliation loop, without ever having called StartTimer for this group
// itself.
func TestDefaultTimerManager_ReconciliationLoop_AdoptsOrphanedTimer(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(&syncBuffer{}, nil))

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	require.NoError(t, err)
	defer func() { _ = redisCache.Close() }()

	storage, err := NewRedisTimerStorage(redisCache, logger)
	require.NoError(t, err)

	const groupKey = GroupKey("alertname=OrphanGroup")
	ctx := context.Background()

	// Seed a timer as if a NOW-DEAD replica had started it: ExpiresAt is
	// safely in the past, and nothing here ever calls StartTimer, so this
	// manager's own tm.timers map has no entry for it either.
	startedAt := time.Now().Add(-5 * time.Minute)
	duration := 3 * time.Minute
	orphan := &GroupTimer{
		GroupKey:  groupKey,
		TimerType: GroupWaitTimer,
		Duration:  duration,
		StartedAt: startedAt,
		ExpiresAt: startedAt.Add(duration), // 2 minutes in the past
		State:     TimerStateActive,
		Metadata:  &TimerMetadata{Version: 1, CreatedBy: "crashed-replica"},
	}
	require.NoError(t, storage.SaveTimer(ctx, orphan))

	gm := newTimerStorageGroupManagerStub(t, logger, groupKey)

	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:                storage,
		GroupManager:           gm,
		Logger:                 logger,
		ReconciliationInterval: 50 * time.Millisecond,
		ReconciliationGrace:    10 * time.Millisecond,
	})
	require.NoError(t, err)
	defer func() { _ = tm.Shutdown(context.Background()) }()

	var fired atomic.Bool
	tm.OnTimerExpired(func(_ context.Context, gk GroupKey, _ TimerType, _ *AlertGroup) error {
		if gk == groupKey {
			fired.Store(true)
		}
		return nil
	})

	require.Eventually(t, fired.Load, 3*time.Second, 20*time.Millisecond,
		"the reconciliation loop must adopt and fire the orphaned timer left by the crashed replica")

	_, loadErr := storage.LoadTimer(ctx, groupKey)
	require.ErrorIs(t, loadErr, ErrTimerNotFound,
		"an adopted-and-fired timer must be deleted from shared storage, same as any normal fire")

	stats, err := tm.GetStats(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, stats.ReconciledTimers, int64(1),
		"GetStats must surface the reconciliation adoption for observability")
}

// TestDefaultTimerManager_ReconciliationDisabledByDefault_OrphanNotAdopted
// verifies ReconciliationInterval's zero-value default — what the lite
// profile's in-memory storage wiring and ServiceRegistry's standard-profile
// Redis-fallback path both leave it at (service_registry.go's
// initializeGrouping only sets it when timerStorage is a genuine
// *RedisTimerStorage) — is a full, clean no-op: an orphaned timer must sit
// untouched, not be silently adopted.
func TestDefaultTimerManager_ReconciliationDisabledByDefault_OrphanNotAdopted(t *testing.T) {
	storage := NewInMemoryTimerStorage(nil)
	const groupKey = GroupKey("alertname=NoReconcileGroup")
	ctx := context.Background()

	startedAt := time.Now().Add(-time.Hour)
	duration := 30 * time.Second
	require.NoError(t, storage.SaveTimer(ctx, &GroupTimer{
		GroupKey:  groupKey,
		TimerType: GroupWaitTimer,
		Duration:  duration,
		StartedAt: startedAt,
		ExpiresAt: startedAt.Add(duration),
		State:     TimerStateActive,
		Metadata:  &TimerMetadata{Version: 1},
	}))

	gm := newTimerStorageGroupManagerStub(t, slog.Default(), groupKey)

	// ReconciliationInterval left at its zero value — the lite-profile /
	// Redis-fallback posture.
	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:      storage,
		GroupManager: gm,
		Logger:       slog.Default(),
	})
	require.NoError(t, err)

	var fired atomic.Bool
	tm.OnTimerExpired(func(_ context.Context, _ GroupKey, _ TimerType, _ *AlertGroup) error {
		fired.Store(true)
		return nil
	})

	time.Sleep(200 * time.Millisecond)

	require.False(t, fired.Load(),
		"ReconciliationInterval=0 must disable the loop entirely — an orphaned timer must never be silently adopted")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, tm.Shutdown(shutdownCtx),
		"Shutdown must complete promptly with no reconciliation goroutine left running")
}

// TestPublishCallbacks_FreshLoadAtFireTime_SeesAlertAddedByOtherReplica
// covers task 6.2 correctness requirement 1: a replica that ingests an
// alert into a group ALREADY owned (timer started) by a different replica
// must still be reflected in that owner's next fire. onGroupWaitExpired
// (manager_impl.go) re-Loads the group from storage rather than trusting
// the (possibly stale) snapshot the TimerCallback signature hands it — this
// test drives that exact call with a deliberately stale snapshot to prove
// it, using GroupStorage genuinely SHARED across two DefaultGroupManager
// instances (unlike task 6.1's redis_notify_chain_test.go tests, which used
// separate per-replica storage on purpose — see that file's doc comment).
func TestPublishCallbacks_FreshLoadAtFireTime_SeesAlertAddedByOtherReplica(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(&syncBuffer{}, nil))

	newReplicaGroupStorage := func() *RedisGroupStorage {
		redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
			Addr:        mr.Addr(),
			DB:          0,
			PoolSize:    5,
			DialTimeout: time.Second,
			ReadTimeout: time.Second,
		}, logger)
		require.NoError(t, err)
		t.Cleanup(func() { _ = redisCache.Close() })

		gs, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
			Client: redisCache.GetClient(),
			Logger: logger,
		})
		require.NoError(t, err)
		return gs
	}

	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{time.Hour},
			GroupInterval:  &Duration{time.Hour},
			RepeatInterval: &Duration{time.Hour},
		},
	}
	pub := &mockPublisher{}

	replicaA, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Storage:      newReplicaGroupStorage(),
		Publisher:    pub,
		Logger:       logger,
	})
	require.NoError(t, err)

	replicaB, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Storage:      newReplicaGroupStorage(),
		Publisher:    pub,
		Logger:       logger,
	})
	require.NoError(t, err)

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alertA := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	alertB := createTestAlert("B", core.StatusFiring, map[string]string{"alertname": "TestAlert"})

	_, err = replicaA.AddAlertToGroup(ctx, alertA, groupKey)
	require.NoError(t, err)

	// This is the STALE snapshot a TimerCallback would have been handed if
	// it captured the group at timer-START time rather than fire time —
	// exactly one alert, before B's ingest below.
	staleSnapshot, err := replicaA.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	require.Len(t, staleSnapshot.Alerts, 1)

	// A DIFFERENT replica ingests a second alert into the SAME group,
	// through the SAME shared Redis GroupStorage, AFTER the stale snapshot
	// above was captured.
	_, err = replicaB.AddAlertToGroup(ctx, alertB, groupKey)
	require.NoError(t, err)

	// A's group_wait timer "fires" now, handed the stale one-alert
	// snapshot — mirrors DefaultTimerManager.onTimerExpired's call shape
	// (gm.GetGroup snapshot -> registered callback), except invoked
	// directly to isolate the fresh-Load behavior from timer plumbing.
	err = replicaA.onGroupWaitExpired(ctx, groupKey, GroupWaitTimer, staleSnapshot)
	require.NoError(t, err)

	calls := pub.calls()
	require.Len(t, calls, 1)
	require.Len(t, calls[0], 2,
		"onGroupWaitExpired must re-Load the group from shared storage at fire time — B's alert, ingested on a different replica after the stale snapshot was captured, must be included")
}

// TestDefaultTimerManager_ReconciliationLoop_DeletedGroupCleansUpLeftoverTimer
// covers fix round 1, Finding 2: a timer left behind in shared storage
// after its group was already deleted (e.g. RemoveAlertFromGroup ran
// before this timer fired) must be cleaned up ONCE — deleted from storage
// and from tm.timers, logged at Warn — not left for the reconciliation
// loop to keep re-adopting and Error-logging every tick until Redis's own
// TTL eventually reaps it.
func TestDefaultTimerManager_ReconciliationLoop_DeletedGroupCleansUpLeftoverTimer(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	var logBuf syncBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	require.NoError(t, err)
	defer func() { _ = redisCache.Close() }()

	storage, err := NewRedisTimerStorage(redisCache, logger)
	require.NoError(t, err)

	const groupKey = GroupKey("alertname=DeletedGroup")
	ctx := context.Background()

	// Group manager whose storage does NOT contain groupKey — as if the
	// group was already deleted before this leftover timer fired. Unlike
	// newTimerStorageGroupManagerStub, deliberately do not seed a group.
	gm := &DefaultGroupManager{
		storage:          NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: logger}),
		fingerprintIndex: make(map[string]GroupKey),
		logger:           logger,
	}

	startedAt := time.Now().Add(-5 * time.Minute)
	duration := 3 * time.Minute
	require.NoError(t, storage.SaveTimer(ctx, &GroupTimer{
		GroupKey:  groupKey,
		TimerType: GroupWaitTimer,
		Duration:  duration,
		StartedAt: startedAt,
		ExpiresAt: startedAt.Add(duration), // well overdue
		State:     TimerStateActive,
		Metadata:  &TimerMetadata{Version: 1, CreatedBy: "crashed-replica"},
	}))

	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:                storage,
		GroupManager:           gm,
		Logger:                 logger,
		ReconciliationInterval: 40 * time.Millisecond,
		ReconciliationGrace:    10 * time.Millisecond,
	})
	require.NoError(t, err)
	defer func() { _ = tm.Shutdown(context.Background()) }()

	require.Eventually(t, func() bool {
		_, loadErr := storage.LoadTimer(ctx, groupKey)
		return errors.Is(loadErr, ErrTimerNotFound)
	}, 2*time.Second, 20*time.Millisecond,
		"the leftover timer for a confirmed-deleted group must be removed from storage by the first reconciliation tick that adopts it")

	// Let several more ticks pass. If the fix regressed (cleanup not
	// actually removing the timer, or ListOverdueTimers somehow still
	// surfacing it), this would keep re-triggering the cleanup/error path.
	time.Sleep(200 * time.Millisecond)

	logs := logBuf.String()
	const cleanupMsg = "group no longer exists for timer expiration, removing leftover timer"
	require.Contains(t, logs, cleanupMsg)
	require.Equal(t, 1, strings.Count(logs, cleanupMsg),
		"cleanup must happen exactly once — a repeat count means the loop is still re-adopting the same confirmed-deleted group's timer")
	require.NotContains(t, logs, "Failed to get group for timer expiration",
		"a confirmed not-found must take the Warn cleanup path, not the generic transient-error Error log")
}
