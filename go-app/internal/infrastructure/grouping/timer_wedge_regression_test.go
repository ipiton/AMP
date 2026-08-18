package grouping

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review findings 2 and 3 — two independent ways a group could stop
// notifying forever, both invisible in metrics and logs:
//
//	Finding 2: the reconciliation ADOPTION WINDOW was ~0s. A timer became
//	eligible for adoption at ExpiresAt + reconciliation_grace (60s default),
//	while its Redis record was deleted at ExpiresAt + timerTTLGracePeriod
//	(also 60s) — so ListOverdueTimers found the timer gone at the exact
//	moment it became adoptable, and a crashed replica's groups were never
//	picked up by the survivors.
//
//	Finding 3: three early returns in onTimerExpired left a DEAD handle in
//	tm.timers. reconcileOrphanedTimers reads that map (trackedLocally) as
//	"this replica owns the group and will fire it itself", so the group was
//	skipped on every subsequent tick — and AddAlertToGroup only arms
//	group_wait for BRAND NEW groups, so nothing else ever re-armed it.

// TestAdoptionWindow_ConstantsInvariant pins the relationship between the two
// constants whose collision caused finding 2. The compile-time check in
// redis_timer_storage.go enforces it too; this test states it in terms a
// reader can act on, and documents the window's actual size.
func TestAdoptionWindow_ConstantsInvariant(t *testing.T) {
	window := timerTTLGracePeriod - defaultReconciliationGracePeriod
	require.Positive(t, window,
		"the shared timer record must outlive the adoption grace, or nothing is ever adoptable")
	assert.GreaterOrEqual(t, window, 4*defaultReconciliationInterval,
		"the adoption window must fit several reconciliation ticks so one missed tick cannot lose a group")
}

// TestSaveTimer_LeavesAdoptionWindowForOverdueTimer proves the invariant at
// the storage level, where the bug actually bit: a timer already overdue by
// more than the reconciliation grace must still be READABLE from storage.
// Before the fix, an overdue timer's TTL floor was timerTTLGracePeriod == the
// grace period, so by the time it was adoptable its key was gone.
func TestSaveTimer_LeavesAdoptionWindowForOverdueTimer(t *testing.T) {
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

	ctx := context.Background()
	const groupKey = GroupKey("alertname=AdoptionWindowGroup")

	// Overdue by exactly the eligibility threshold plus a second.
	expiresAt := time.Now().Add(-(defaultReconciliationGracePeriod + time.Second))
	require.NoError(t, storage.SaveTimer(ctx, &GroupTimer{
		GroupKey:  groupKey,
		TimerType: GroupWaitTimer,
		Duration:  time.Minute,
		StartedAt: expiresAt.Add(-time.Minute),
		ExpiresAt: expiresAt,
		State:     TimerStateActive,
		Metadata:  &TimerMetadata{Version: 1, CreatedBy: "crashed-replica"},
	}))

	// Fast-forward miniredis to the moment the timer becomes adoptable.
	mr.FastForward(defaultReconciliationGracePeriod)

	overdue, err := storage.ListOverdueTimers(ctx, time.Now().Add(-defaultReconciliationGracePeriod))
	require.NoError(t, err)
	require.Len(t, overdue, 1,
		"an overdue timer must still be listable once it becomes adoptable (adoption window > 0)")
	assert.Equal(t, groupKey, overdue[0].GroupKey)
}

// TestReconciliation_DefaultGraceAdoptsOrphan is the end-to-end version of
// finding 2: unlike the existing adoption test (which passes explicit
// millisecond-scale grace values and therefore could never see the bug), this
// one leaves ReconciliationGrace UNSET so the production default applies.
func TestReconciliation_DefaultGraceAdoptsOrphan(t *testing.T) {
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

	const groupKey = GroupKey("alertname=DefaultGraceOrphanGroup")
	ctx := context.Background()

	// Overdue by more than the default grace, so it is eligible on the very
	// first tick — but far less than timerTTLGracePeriod, so its record is
	// still there to be found.
	expiresAt := time.Now().Add(-(defaultReconciliationGracePeriod + 10*time.Second))
	require.NoError(t, storage.SaveTimer(ctx, &GroupTimer{
		GroupKey:  groupKey,
		TimerType: GroupWaitTimer,
		Duration:  time.Minute,
		StartedAt: expiresAt.Add(-time.Minute),
		ExpiresAt: expiresAt,
		State:     TimerStateActive,
		Metadata:  &TimerMetadata{Version: 1, CreatedBy: "crashed-replica"},
	}))

	gm := newTimerStorageGroupManagerStub(t, logger, groupKey)

	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:      storage,
		GroupManager: gm,
		Logger:       logger,
		// Short interval so the test does not wait 45s, but grace left at
		// ZERO on purpose: that is the code path that applies the default.
		ReconciliationInterval: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	defer func() { _ = tm.Shutdown(context.Background()) }()

	require.Equal(t, defaultReconciliationGracePeriod, tm.reconciliationGrace,
		"an enabled reconciliation loop with no explicit grace must fall back to the default, not to timerTTLGracePeriod")

	var fired atomic.Bool
	tm.OnTimerExpired(func(_ context.Context, gk GroupKey, _ TimerType, _ *AlertGroup) error {
		if gk == groupKey {
			fired.Store(true)
		}
		return nil
	})

	require.Eventually(t, fired.Load, 3*time.Second, 20*time.Millisecond,
		"with the DEFAULT reconciliation grace, an orphan overdue by grace+10s must be adopted")
}

// ---------------------------------------------------------------------------
// Finding 3: early-return paths must not leave a dead local handle behind.
// ---------------------------------------------------------------------------

// lockFailingTimerStorage makes AcquireLock fail on demand, so the two
// lock-related early returns in onTimerExpired can be driven deterministically.
type lockFailingTimerStorage struct {
	TimerStorage
	acquireErr atomic.Pointer[error]
	acquires   atomic.Int64
}

func (s *lockFailingTimerStorage) AcquireLock(ctx context.Context, groupKey GroupKey, ttl time.Duration) (string, func() error, error) {
	s.acquires.Add(1)
	if errPtr := s.acquireErr.Load(); errPtr != nil {
		return "", nil, *errPtr
	}
	return s.TimerStorage.AcquireLock(ctx, groupKey, ttl)
}

// loadFailingGroupStorage makes Load fail with a TRANSIENT error (not
// GroupNotFoundError), driving onTimerExpired's third early return.
type loadFailingGroupStorage struct {
	GroupStorage
	loadErr atomic.Pointer[error]
}

func (s *loadFailingGroupStorage) Load(ctx context.Context, key GroupKey) (*AlertGroup, error) {
	if errPtr := s.loadErr.Load(); errPtr != nil {
		return nil, *errPtr
	}
	return s.GroupStorage.Load(ctx, key)
}

func (tm *DefaultTimerManager) hasLocalHandle(groupKey GroupKey) bool {
	tm.timersMu.RLock()
	defer tm.timersMu.RUnlock()
	_, ok := tm.timers[groupKey]
	return ok
}

// assertHandleDroppedAndAdoptable is the shared assertion for all three
// early-return paths: the local handle must be gone (so trackedLocally stops
// lying to reconcileOrphanedTimers) while the SHARED storage entry survives
// (so there is still something to adopt).
func assertHandleDroppedAndAdoptable(t *testing.T, tm *DefaultTimerManager, storage TimerStorage, groupKey GroupKey) {
	t.Helper()

	require.Eventually(t, func() bool { return !tm.hasLocalHandle(groupKey) }, 3*time.Second, 10*time.Millisecond,
		"the dead local handle must be dropped, otherwise reconciliation skips this group forever")

	timer, err := storage.LoadTimer(context.Background(), groupKey)
	require.NoError(t, err, "the shared timer entry must survive so another fire can adopt it")
	require.NotNil(t, timer)
}

func newWedgeTestManager(t *testing.T, storage TimerStorage, gm *DefaultGroupManager) *DefaultTimerManager {
	t.Helper()
	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:      storage,
		GroupManager: gm,
		Logger:       slog.New(slog.NewTextHandler(&syncBuffer{}, nil)),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tm.Shutdown(context.Background()) })
	return tm
}

func TestOnTimerExpired_LockHeldElsewhere_DropsDeadLocalHandle(t *testing.T) {
	const groupKey = GroupKey("alertname=LockHeldGroup")

	lockHeld := error(ErrLockAlreadyAcquired)
	storage := &lockFailingTimerStorage{TimerStorage: NewInMemoryTimerStorage(nil)}
	storage.acquireErr.Store(&lockHeld)

	gm := newTimerStorageGroupManagerStub(t, slog.Default(), groupKey)
	tm := newWedgeTestManager(t, storage, gm)

	_, err := tm.StartTimer(context.Background(), groupKey, GroupWaitTimer, 30*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return storage.acquires.Load() > 0 }, 3*time.Second, 10*time.Millisecond)
	assertHandleDroppedAndAdoptable(t, tm, storage, groupKey)
}

func TestOnTimerExpired_LockStoreError_DropsDeadLocalHandle(t *testing.T) {
	const groupKey = GroupKey("alertname=LockErrorGroup")

	lockErr := errors.New("redis: connection refused")
	storage := &lockFailingTimerStorage{TimerStorage: NewInMemoryTimerStorage(nil)}
	storage.acquireErr.Store(&lockErr)

	gm := newTimerStorageGroupManagerStub(t, slog.Default(), groupKey)
	tm := newWedgeTestManager(t, storage, gm)

	_, err := tm.StartTimer(context.Background(), groupKey, GroupWaitTimer, 30*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return storage.acquires.Load() > 0 }, 3*time.Second, 10*time.Millisecond)
	assertHandleDroppedAndAdoptable(t, tm, storage, groupKey)
}

func TestOnTimerExpired_TransientGroupLoadError_DropsDeadLocalHandle(t *testing.T) {
	const groupKey = GroupKey("alertname=TransientLoadGroup")

	logger := slog.Default()
	gm := newTimerStorageGroupManagerStub(t, logger, groupKey)
	failing := &loadFailingGroupStorage{GroupStorage: gm.storage}
	loadErr := errors.New("redis: i/o timeout")
	failing.loadErr.Store(&loadErr)
	gm.storage = failing

	storage := NewInMemoryTimerStorage(nil)
	tm := newWedgeTestManager(t, storage, gm)

	_, err := tm.StartTimer(context.Background(), groupKey, GroupWaitTimer, 30*time.Millisecond)
	require.NoError(t, err)

	assertHandleDroppedAndAdoptable(t, tm, storage, groupKey)
}

// TestOnTimerExpired_WedgedGroupStillNotifiesLater is the behavioural payoff
// of the two fixes together: a fire that hits the transient-error early return
// must not end the group's life. Once storage recovers, the reconciliation
// loop adopts the surviving timer entry and the group finally notifies.
func TestOnTimerExpired_WedgedGroupStillNotifiesLater(t *testing.T) {
	const groupKey = GroupKey("alertname=RecoversGroup")

	logger := slog.New(slog.NewTextHandler(&syncBuffer{}, nil))
	gm := newTimerStorageGroupManagerStub(t, logger, groupKey)
	failing := &loadFailingGroupStorage{GroupStorage: gm.storage}
	loadErr := errors.New("redis: i/o timeout")
	failing.loadErr.Store(&loadErr)
	gm.storage = failing

	storage := NewInMemoryTimerStorage(nil)
	tm, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage:                storage,
		GroupManager:           gm,
		Logger:                 logger,
		ReconciliationInterval: 50 * time.Millisecond,
		ReconciliationGrace:    10 * time.Millisecond,
	})
	require.NoError(t, err)
	defer func() { _ = tm.Shutdown(context.Background()) }()

	var fired atomic.Int64
	tm.OnTimerExpired(func(_ context.Context, gk GroupKey, _ TimerType, _ *AlertGroup) error {
		if gk == groupKey {
			fired.Add(1)
		}
		return nil
	})

	_, err = tm.StartTimer(context.Background(), groupKey, GroupWaitTimer, 30*time.Millisecond)
	require.NoError(t, err)

	// The first fire dies in the transient-error branch: no callback ran.
	require.Eventually(t, func() bool { return !tm.hasLocalHandle(groupKey) }, 3*time.Second, 10*time.Millisecond)
	require.Zero(t, fired.Load(), "the transient-error path must not dispatch callbacks")

	// Storage recovers; reconciliation must now adopt the surviving entry.
	failing.loadErr.Store(nil)
	require.Eventually(t, func() bool { return fired.Load() > 0 }, 5*time.Second, 25*time.Millisecond,
		"a group wedged by a transient error must still notify once storage recovers")
}
