package grouping

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/stretchr/testify/require"
)

// Task 6.2 fix round 2 (P0, confirmed by task 6.5's reviewer reading this
// file): every group_wait->group_interval and group_interval->
// repeat_interval continuation was silently failing.
//
// Root cause: handleTimerExpiration called onTimerExpired(handle.ctx, ...)
// — the context of the timer that JUST fired. onTimerExpired derived its
// callbackCtx from that same ctx and invoked the registered TimerCallback
// (e.g. onGroupWaitExpired) with it. That callback calls
// startGroupIntervalTimer -> StartTimer for the SAME groupKey, still
// carrying callbackCtx. StartTimer finds the existing (not yet removed —
// removal happens AFTER the callback loop returns) handle for this group
// and calls existing.cancel(), which is exactly the handle whose ctx is
// callbackCtx's ancestor: it cancels callbackCtx out from under the very
// StartTimer call using it, so SaveTimer(callbackCtx, ...) fails with
// "context canceled" and StartTimer returns before ever creating the new
// Go timer or registering its handle. The continuation was silently never
// created — only the first notification (group_wait, no continuation
// involved) ever went out.
//
// Fix: onTimerExpired now roots every internal operation in tm.ctx (the
// manager's own lifetime context), never in the specific handle.ctx that
// triggered the fire — see its doc comment in timer_manager_impl.go.
//
// These tests use RedisTimerStorage (miniredis), not InMemoryTimerStorage:
// InMemoryTimerStorage.SaveTimer ignores its ctx parameter entirely, so it
// cannot reproduce the bug — only a storage backend that actually checks
// ctx cancellation (RedisTimerStorage's pipe.Exec) can.

// newContinuationTestManagers builds a fully-wired DefaultTimerManager +
// DefaultGroupManager pair (real registerTimerCallbacks chain, not a
// stubbed callback) against Redis-backed timer + group storage, with all
// three timing knobs set to duration for every leg of the
// group_wait -> group_interval -> repeat_interval chain.
func newContinuationTestManagers(t *testing.T, logger *slog.Logger, duration time.Duration) (*DefaultTimerManager, *DefaultGroupManager, *mockPublisher, func()) {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	require.NoError(t, err)

	timerStorage, err := NewRedisTimerStorage(redisCache, logger)
	require.NoError(t, err)

	groupStorage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: redisCache.GetClient(),
		Logger: logger,
	})
	require.NoError(t, err)

	timerManager, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage: timerStorage,
		Logger:  logger,
	})
	require.NoError(t, err)

	pub := &mockPublisher{}
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{duration},
			GroupInterval:  &Duration{duration},
			RepeatInterval: &Duration{duration},
		},
	}

	groupManager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Storage:      groupStorage,
		TimerManager: timerManager,
		Publisher:    pub,
		Logger:       logger,
	})
	require.NoError(t, err)
	require.NoError(t, timerManager.SetGroupManager(groupManager))

	cleanup := func() {
		_ = timerManager.Shutdown(context.Background())
		_ = redisCache.Close()
		mr.Close()
	}

	return timerManager, groupManager, pub, cleanup
}

// TestTimerContinuation_GroupWaitFireCreatesGroupIntervalTimer is the
// red->green regression for the P0 bug: after group_wait fires, the
// group_interval continuation timer must exist BOTH in shared storage and
// in the timer manager's own tm.timers map — GetTimer checks the in-memory
// map first (fast path) and only then loads from storage, so a successful
// GetTimer call proves both. Before the fix, StartTimer's SaveTimer call
// failed with "context canceled" and returned an error before either was
// ever written, so this GetTimer call would fail with ErrTimerNotFound.
//
// group_interval is deliberately long (not firing during this test) so
// the assertion below observes the continuation timer at rest, not racing
// its own next transition.
func TestTimerContinuation_GroupWaitFireCreatesGroupIntervalTimer(t *testing.T) {
	var logBuf syncBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	require.NoError(t, err)
	defer func() { _ = redisCache.Close() }()

	timerStorage, err := NewRedisTimerStorage(redisCache, logger)
	require.NoError(t, err)

	groupStorage, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client: redisCache.GetClient(),
		Logger: logger,
	})
	require.NoError(t, err)

	timerManager, err := NewDefaultTimerManager(TimerManagerConfig{
		Storage: timerStorage,
		Logger:  logger,
	})
	require.NoError(t, err)
	defer func() { _ = timerManager.Shutdown(context.Background()) }()

	pub := &mockPublisher{}
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{30 * time.Millisecond},
			GroupInterval:  &Duration{time.Hour}, // must not fire during this test
			RepeatInterval: &Duration{time.Hour},
		},
	}

	groupManager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Storage:      groupStorage,
		TimerManager: timerManager,
		Publisher:    pub,
		Logger:       logger,
	})
	require.NoError(t, err)
	require.NoError(t, timerManager.SetGroupManager(groupManager))

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=ContinuationRegression")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "ContinuationRegression"})
	_, err = groupManager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return len(pub.calls()) >= 1 }, 2*time.Second, 10*time.Millisecond,
		"group_wait must fire the first notification")

	// This is the exact assertion the P0 bug broke: GetTimer checks
	// tm.timers first, then storage — a successful call proves the
	// continuation is tracked in BOTH.
	var continuation *GroupTimer
	require.Eventually(t, func() bool {
		continuation, err = timerManager.GetTimer(ctx, groupKey)
		return err == nil
	}, time.Second, 10*time.Millisecond,
		"the group_interval continuation timer must be created in tm.timers AND storage after group_wait fires — "+
			"before the fix, StartTimer's SaveTimer call failed with \"context canceled\" and the continuation was never created")
	require.Equal(t, GroupIntervalTimer, continuation.TimerType)

	logs := logBuf.String()
	require.NotContains(t, logs, "context canceled",
		"the continuation must never self-cancel via the dying group_wait handle's context")
	require.NotContains(t, logs, "Failed to save timer to storage")
}

// TestTimerContinuation_FullChainFiresRepeatIntervalTwice drives the whole
// group_wait -> group_interval -> repeat_interval chain with short,
// identical timings and asserts the fake publisher observes at least 3
// publishes: #1 is group_wait's first notification, #2 is
// group_interval's follow-up, #3 is the first repeat_interval reminder —
// the earliest publish count that actually proves the repeat_interval leg
// itself fired, not just group_interval (fix round 3: with all three
// timings equal, require.Eventually could satisfy a ">= 2" assertion as
// soon as the group_interval leg alone completed, never reaching
// repeat_interval, and still report green). Before the fix, every
// continuation failed silently, so only the first publish ever happened,
// no matter how long the test waited.
func TestTimerContinuation_FullChainFiresRepeatIntervalTwice(t *testing.T) {
	var logBuf syncBuffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, groupManager, pub, cleanup := newContinuationTestManagers(t, logger, 30*time.Millisecond)
	defer cleanup()

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=ContinuationChain")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "ContinuationChain"})
	_, err := groupManager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	require.Eventually(t, func() bool { return len(pub.calls()) >= 3 }, 5*time.Second, 10*time.Millisecond,
		"the full group_wait->group_interval->repeat_interval chain must keep publishing through the repeat_interval leg — "+
			"before the fix, every continuation after the first notification failed silently")

	logs := logBuf.String()
	require.NotContains(t, logs, "context canceled",
		"no step of the continuation chain may self-cancel via a dying handle's context")
	require.NotContains(t, logs, "Failed to save timer to storage")
}
