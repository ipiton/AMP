package grouping

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// GROUPING-TIMER-LOCK-FIX: onTimerExpired re-reads the group's timer entry
// before taking the fire lock and again under it (skipHandledFire /
// fireStillDue), so a fire that another replica already handled is skipped
// instead of running the callbacks a second time — and a replica that skips
// never holds the lock while deciding, so the entry's writer still fires.

// newSharedRedisReplica builds one DefaultTimerManager over a RedisTimerStorage
// on mr, standing in for one replica of an HA deployment. Every callback run
// increments fires. wrap, if non-nil, decorates the replica's storage.
func newSharedRedisReplica(
	t *testing.T,
	mr *miniredis.Miniredis,
	logger *slog.Logger,
	groupKey GroupKey,
	fires *atomic.Int64,
	wrap func(TimerStorage) TimerStorage,
) (*DefaultTimerManager, TimerStorage) {
	t.Helper()

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	require.NoError(t, err)
	t.Cleanup(func() { _ = redisCache.Close() })

	var storage TimerStorage
	storage, err = NewRedisTimerStorage(redisCache, logger)
	require.NoError(t, err)
	if wrap != nil {
		storage = wrap(storage)
	}

	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:      storage,
		GroupManager: newTimerStorageGroupManagerStub(t, logger, groupKey),
		Logger:       logger,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tm.Shutdown(context.Background()) })

	tm.OnTimerExpired(func(_ context.Context, _ GroupKey, _ TimerType, _ *AlertGroup) error {
		fires.Add(1)
		return nil
	})
	return tm, storage
}

func (tm *DefaultTimerManager) localHandle(t *testing.T, groupKey GroupKey) *timerHandle {
	t.Helper()
	tm.timersMu.RLock()
	defer tm.timersMu.RUnlock()
	handle, ok := tm.timers[groupKey]
	require.True(t, ok, "expected a local timer handle for %s", groupKey)
	return handle
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&syncBuffer{}, nil))
}

// T1: the bug itself, deterministically. Replica A fires and fully handles
// the group's timer (entry deleted, lock released); replica B's fire for the
// same group arrives only afterwards. Before the fix B won the free lock and
// ran the callbacks again.
func TestOnTimerExpired_LateReplicaAfterRelease_DoesNotFireAgain(t *testing.T) {
	mr := miniredis.RunT(t)
	logger := quietLogger()
	const groupKey = GroupKey("alertname=LateReplicaGroup")
	ctx := context.Background()

	var fires atomic.Int64
	tmA, storage := newSharedRedisReplica(t, mr, logger, groupKey, &fires, nil)
	tmB, _ := newSharedRedisReplica(t, mr, logger, groupKey, &fires, nil)

	_, err := tmA.StartTimer(ctx, groupKey, GroupWaitTimer, 20*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, loadErr := storage.LoadTimer(ctx, groupKey)
		return fires.Load() == 1 &&
			errors.Is(loadErr, ErrTimerNotFound) &&
			!mr.Exists(lockKeyPrefix+string(groupKey))
	}, 3*time.Second, 10*time.Millisecond, "replica A must fire, delete the entry and release the lock")

	// nil handle, as from RestoreTimers/reconciliation; a replica's own stale
	// handle takes the same not_found branch.
	tmB.onTimerExpired(nil, groupKey, GroupWaitTimer)

	require.EqualValues(t, 1, fires.Load(),
		"a fire that arrives after another replica handled it must be skipped, not run again")
}

// blockingLoadTimerStorage parks the first LoadTimer call made after arm()
// until proceed is closed, so a test can act while a replica is mid-check.
type blockingLoadTimerStorage struct {
	TimerStorage
	armed   atomic.Bool
	once    sync.Once
	entered chan struct{}
	proceed chan struct{}
}

func newBlockingLoadTimerStorage(inner TimerStorage) *blockingLoadTimerStorage {
	return &blockingLoadTimerStorage{
		TimerStorage: inner,
		entered:      make(chan struct{}),
		proceed:      make(chan struct{}),
	}
}

func (s *blockingLoadTimerStorage) LoadTimer(ctx context.Context, groupKey GroupKey) (*GroupTimer, error) {
	if s.armed.Load() {
		s.once.Do(func() {
			close(s.entered)
			<-s.proceed
		})
	}
	return s.TimerStorage.LoadTimer(ctx, groupKey)
}

// T1b (deep-review R1 F1): replica A's entry was overwritten by replica B, so
// A is about to skip. While A is deciding, it must not hold the fire lock:
// otherwise B, firing its own entry at that moment, hits
// ErrLockAlreadyAcquired, drops its handle, and nobody fires the group.
func TestOnTimerExpired_SkipperDoesNotHoldLock_WriterStillFires(t *testing.T) {
	mr := miniredis.RunT(t)
	logger := quietLogger()
	const groupKey = GroupKey("alertname=SkipperGroup")
	ctx := context.Background()

	var fires atomic.Int64
	var blockingA *blockingLoadTimerStorage
	tmA, _ := newSharedRedisReplica(t, mr, logger, groupKey, &fires, func(s TimerStorage) TimerStorage {
		blockingA = newBlockingLoadTimerStorage(s)
		return blockingA
	})
	tmB, storageB := newSharedRedisReplica(t, mr, logger, groupKey, &fires, nil)

	// Long durations: the Go timers never fire on their own, the test drives
	// onTimerExpired directly.
	_, err := tmA.StartTimer(ctx, groupKey, GroupWaitTimer, time.Hour)
	require.NoError(t, err)
	_, err = tmB.StartTimer(ctx, groupKey, GroupWaitTimer, time.Hour)
	require.NoError(t, err)
	handleA := tmA.localHandle(t, groupKey)
	handleB := tmB.localHandle(t, groupKey)

	stored, err := storageB.LoadTimer(ctx, groupKey)
	require.NoError(t, err)
	require.True(t, stored.ExpiresAt.Equal(handleB.expiresAt), "B's StartTimer must have overwritten A's entry")
	require.False(t, stored.ExpiresAt.Equal(handleA.expiresAt))

	blockingA.armed.Store(true)
	doneA := make(chan struct{})
	go func() {
		defer close(doneA)
		tmA.onTimerExpired(handleA, groupKey, GroupWaitTimer)
	}()

	select {
	case <-blockingA.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("replica A never re-read its timer entry")
	}
	require.False(t, mr.Exists(lockKeyPrefix+string(groupKey)),
		"a replica re-reading its entry must not hold the fire lock while doing so")

	tmB.onTimerExpired(handleB, groupKey, GroupWaitTimer)
	require.EqualValues(t, 1, fires.Load(), "the entry's writer must fire while the other replica is deciding")

	close(blockingA.proceed)
	select {
	case <-doneA:
	case <-time.After(3 * time.Second):
		t.Fatal("replica A's onTimerExpired did not return")
	}

	require.EqualValues(t, 1, fires.Load(), "replica A must skip the fire B already handled")
	require.False(t, tmA.hasLocalHandle(groupKey))
	require.False(t, tmB.hasLocalHandle(groupKey))
}

// T1c (deep-review R1 F5b): end to end through real Go timers. B starts the
// same group's timer later and overwrites A's entry, so A's timer fires first
// against an entry that is not its own and not yet due ("rescheduled"). The
// group must fire exactly once — not twice, and not zero times.
func TestTwoReplicas_OverwrittenEntry_FiresExactlyOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	logger := quietLogger()
	const groupKey = GroupKey("alertname=OverwrittenGroup")
	ctx := context.Background()

	var fires atomic.Int64
	tmA, _ := newSharedRedisReplica(t, mr, logger, groupKey, &fires, nil)
	tmB, _ := newSharedRedisReplica(t, mr, logger, groupKey, &fires, nil)

	const fireIn = 60 * time.Millisecond
	_, err := tmA.StartTimer(ctx, groupKey, GroupWaitTimer, fireIn)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	_, err = tmB.StartTimer(ctx, groupKey, GroupWaitTimer, fireIn)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return fires.Load() >= 1 && !tmA.hasLocalHandle(groupKey) && !tmB.hasLocalHandle(groupKey)
	}, 3*time.Second, 10*time.Millisecond, "the group must fire and both replicas must drop their handles")

	time.Sleep(100 * time.Millisecond)
	require.EqualValues(t, 1, fires.Load())
}

// T2/T3 (+ R1 F5c): the entry no longer describes this fire — a continuation
// of another type, or the same type rescheduled by a peer — so the fire is
// skipped, the local handle dropped, and the entry left exactly as it is.
func TestOnTimerExpired_EntryReplaced_SkipsAndKeepsEntry(t *testing.T) {
	tests := []struct {
		name      string
		peerType  TimerType
		nilHandle bool
	}{
		{name: "continuation of another type", peerType: GroupIntervalTimer},
		{name: "same type rescheduled by a peer", peerType: GroupWaitTimer},
		{name: "reconcile path, peer entry not yet due", peerType: GroupWaitTimer, nilHandle: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const groupKey = GroupKey("alertname=ReplacedEntryGroup")
			ctx := context.Background()

			storage := NewInMemoryTimerStorage(nil)
			tm := newWedgeTestManager(t, storage, newTimerStorageGroupManagerStub(t, slog.Default(), groupKey))
			var fires atomic.Int64
			tm.OnTimerExpired(func(_ context.Context, _ GroupKey, _ TimerType, _ *AlertGroup) error {
				fires.Add(1)
				return nil
			})

			_, err := tm.StartTimer(ctx, groupKey, GroupWaitTimer, time.Hour)
			require.NoError(t, err)
			handle := tm.localHandle(t, groupKey)

			peerExpiresAt := time.Now().Add(2 * time.Hour)
			require.NoError(t, storage.SaveTimer(ctx, &GroupTimer{
				GroupKey:  groupKey,
				TimerType: tc.peerType,
				Duration:  2 * time.Hour,
				StartedAt: time.Now(),
				ExpiresAt: peerExpiresAt,
				State:     TimerStateActive,
				Metadata:  &TimerMetadata{Version: 1, CreatedBy: "peer-replica"},
			}))

			fired := handle
			if tc.nilHandle {
				fired = nil
			}
			tm.onTimerExpired(fired, groupKey, GroupWaitTimer)

			require.Zero(t, fires.Load(), "a fire whose entry was replaced must not run the callbacks")
			if !tc.nilHandle {
				require.False(t, tm.hasLocalHandle(groupKey), "the dead local handle must be dropped")
			}
			stored, err := storage.LoadTimer(ctx, groupKey)
			require.NoError(t, err, "the replacing entry must survive")
			require.Equal(t, tc.peerType, stored.TimerType)
			require.True(t, stored.ExpiresAt.Equal(peerExpiresAt))
		})
	}
}

// loadTimerFailingStorage makes LoadTimer fail and counts AcquireLock calls.
type loadTimerFailingStorage struct {
	TimerStorage
	loadErr  atomic.Pointer[error]
	acquires atomic.Int64
}

func (s *loadTimerFailingStorage) LoadTimer(ctx context.Context, groupKey GroupKey) (*GroupTimer, error) {
	if errPtr := s.loadErr.Load(); errPtr != nil {
		return nil, *errPtr
	}
	return s.TimerStorage.LoadTimer(ctx, groupKey)
}

func (s *loadTimerFailingStorage) AcquireLock(ctx context.Context, groupKey GroupKey, ttl time.Duration) (string, func() error, error) {
	s.acquires.Add(1)
	return s.TimerStorage.AcquireLock(ctx, groupKey, ttl)
}

// T5: the entry cannot be read, so whether the fire is still due is unknown.
// It is dropped without firing and without taking the lock, and the entry is
// left for reconciliation — the same posture as a lock-store failure.
func TestOnTimerExpired_LoadTimerError_SkipsAndKeepsEntry(t *testing.T) {
	const groupKey = GroupKey("alertname=LoadTimerErrorGroup")
	ctx := context.Background()

	inner := NewInMemoryTimerStorage(nil)
	storage := &loadTimerFailingStorage{TimerStorage: inner}
	tm := newWedgeTestManager(t, storage, newTimerStorageGroupManagerStub(t, slog.Default(), groupKey))
	var fires atomic.Int64
	tm.OnTimerExpired(func(_ context.Context, _ GroupKey, _ TimerType, _ *AlertGroup) error {
		fires.Add(1)
		return nil
	})

	_, err := tm.StartTimer(ctx, groupKey, GroupWaitTimer, time.Hour)
	require.NoError(t, err)
	handle := tm.localHandle(t, groupKey)

	loadErr := errors.New("redis: i/o timeout")
	storage.loadErr.Store(&loadErr)
	tm.onTimerExpired(handle, groupKey, GroupWaitTimer)

	require.Zero(t, fires.Load(), "an unreadable entry must not be fired blindly")
	require.Zero(t, storage.acquires.Load(), "the check fails before the lock is taken")
	assertHandleDroppedAndAdoptable(t, tm, inner, groupKey)
}

// T4 (+ R1 F5a): the decision table of fireStillDue.
func TestFireStillDue(t *testing.T) {
	now := time.Now()
	future := now.Add(time.Minute)
	past := now.Add(-time.Minute)
	own := &timerHandle{expiresAt: future}

	entry := func(timerType TimerType, expiresAt time.Time) *GroupTimer {
		return &GroupTimer{TimerType: timerType, ExpiresAt: expiresAt}
	}

	// The handle carries time.Now()'s monotonic reading; the entry comes
	// back from Redis as JSON without it. Equal must still match.
	lagging := &timerHandle{expiresAt: time.Now().Add(time.Minute)}
	raw, err := json.Marshal(entry(GroupWaitTimer, lagging.expiresAt))
	require.NoError(t, err)
	var roundTripped GroupTimer
	require.NoError(t, json.Unmarshal(raw, &roundTripped))

	tests := []struct {
		name       string
		stored     *GroupTimer
		handle     *timerHandle
		wantDue    bool
		wantReason string
	}{
		{"own entry not yet due", entry(GroupWaitTimer, future), own, true, ""},
		{"own entry after JSON round trip, clock lagging", &roundTripped, lagging, true, ""},
		{"peer entry overdue", entry(GroupWaitTimer, past), own, true, ""},
		{"peer entry exactly due", entry(GroupWaitTimer, now), own, true, ""},
		{"peer entry not yet due", entry(GroupWaitTimer, future.Add(time.Millisecond)), own, false, "rescheduled"},
		{"nil handle, entry overdue", entry(GroupWaitTimer, past), nil, true, ""},
		{"nil handle, entry not yet due", entry(GroupWaitTimer, future), nil, false, "rescheduled"},
		{"other type, own expiresAt", entry(GroupIntervalTimer, future), own, false, "type_changed"},
		{"other type, overdue", entry(GroupIntervalTimer, past), nil, false, "type_changed"},
		{"no entry", nil, own, false, "not_found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			due, reason := fireStillDue(tc.stored, tc.handle, GroupWaitTimer, now)
			require.Equal(t, tc.wantDue, due)
			require.Equal(t, tc.wantReason, reason)
		})
	}
}

func activeTimersGauge(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == "alert_history_timer_active_total" {
			return family.GetMetric()[0].GetGauge().GetValue()
		}
	}
	t.Fatal("alert_history_timer_active_total is not registered")
	return 0
}

// T5b (deep-review R1 F2): dropLocalHandle balances StartTimer's
// IncActiveTimers exactly once, and only when it actually removes the handle.
func TestDropLocalHandle_DecrementsActiveTimersOnlyOnRemoval(t *testing.T) {
	const groupKey = GroupKey("alertname=GaugeGroup")

	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:      NewInMemoryTimerStorage(nil),
		GroupManager: newTimerStorageGroupManagerStub(t, slog.Default(), groupKey),
		Logger:       quietLogger(),
		Metrics:      sharedTestBusinessMetrics(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tm.Shutdown(context.Background()) })

	base := activeTimersGauge(t)
	_, err = tm.StartTimer(context.Background(), groupKey, GroupWaitTimer, time.Hour)
	require.NoError(t, err)
	require.Equal(t, base+1, activeTimersGauge(t))
	handle := tm.localHandle(t, groupKey)

	tm.dropLocalHandle(nil, groupKey)
	tm.dropLocalHandle(&timerHandle{groupKey: groupKey}, groupKey)
	require.Equal(t, base+1, activeTimersGauge(t), "a handle that is not the current one must not be dropped or counted")

	tm.dropLocalHandle(handle, groupKey)
	require.Equal(t, base, activeTimersGauge(t))
	require.False(t, tm.hasLocalHandle(groupKey))

	tm.dropLocalHandle(handle, groupKey)
	require.Equal(t, base, activeTimersGauge(t), "a second drop of the same handle must not decrement again")
}

// R1 F5d: in the lite profile (in-memory storage, one replica) every entry
// is the replica's own, so the re-check must never break a continuation
// chain started from inside the callbacks.
func TestOnTimerExpired_InMemoryContinuationChainKeepsFiring(t *testing.T) {
	const groupKey = GroupKey("alertname=LiteChainGroup")

	tm := newWedgeTestManager(t, NewInMemoryTimerStorage(nil), newTimerStorageGroupManagerStub(t, slog.Default(), groupKey))
	var intervalFires atomic.Int64
	tm.OnTimerExpired(func(ctx context.Context, gk GroupKey, timerType TimerType, _ *AlertGroup) error {
		if timerType == GroupIntervalTimer && intervalFires.Add(1) >= 3 {
			return nil
		}
		_, err := tm.StartTimer(ctx, gk, GroupIntervalTimer, 20*time.Millisecond)
		return err
	})

	_, err := tm.StartTimer(context.Background(), groupKey, GroupWaitTimer, 20*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return intervalFires.Load() >= 3 }, 3*time.Second, 10*time.Millisecond,
		"group_wait -> group_interval -> group_interval chain must keep firing")
}
