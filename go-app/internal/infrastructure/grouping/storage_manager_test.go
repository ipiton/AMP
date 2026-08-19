package grouping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: see storage_manager.go's own import
)

// === alertmanager-parity wave-5 item FU-STORAGEMANAGER-FAILBACK ===
//
// These tests exercise the runtime health probe StorageManager already had
// (TN-125) but that newGroupingStorage never actually wired up — see
// storage_manager.go's package doc for the "why now", the hysteresis/error
// classification fix (fix-round finding I-2), and the write-through +
// deletion-replay reconciliation fix (fix-round finding I-1).

// sharedTestBusinessMetrics returns one *metrics.BusinessMetrics for the
// whole test binary run: BusinessMetrics registers its collectors into the
// default Prometheus registry via promauto, so a second construction in the
// same process panics with AlreadyRegisteredError. sync.Once keeps every
// test in this file (and any other test in this package that needs metrics)
// sharing the one instance instead of each grabbing its own.
var (
	sharedTestMetricsOnce sync.Once
	sharedTestMetrics     *metrics.BusinessMetrics
)

func sharedTestBusinessMetrics() *metrics.BusinessMetrics {
	sharedTestMetricsOnce.Do(func() {
		sharedTestMetrics = metrics.NewBusinessMetrics()
	})
	return sharedTestMetrics
}

// testStorageManagerHarness bundles a real miniredis-backed RedisGroupStorage
// primary, a MemoryGroupStorage fallback, and the StorageManager wrapping
// them, with a fast HealthCheckInterval AND hysteresis effectively disabled
// (DegradeThreshold/RecoverThreshold=1, MinHoldDuration~0) so tests that only
// care about "does the mechanism work at all" don't have to wait out the
// production hysteresis defaults (3 consecutive probes either way, 30s
// minimum hold). Tests that specifically exercise the hysteresis itself
// (fix-round finding I-2) build their own StorageManager instead — see
// newHysteresisTestManager below.
type testStorageManagerHarness struct {
	mr      *miniredis.Miniredis
	sm      *StorageManager
	metrics *metrics.BusinessMetrics
}

func newTestStorageManagerHarness(t *testing.T, opts ...func(*StorageManagerConfig)) *testStorageManagerHarness {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: 200 * time.Millisecond,
		ReadTimeout: 200 * time.Millisecond,
	}, slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { _ = redisCache.Close() })

	bm := sharedTestBusinessMetrics()

	primary, err := NewRedisGroupStorage(context.Background(), &RedisGroupStorageConfig{
		Client:  redisCache.GetClient(),
		Logger:  slog.Default(),
		Metrics: bm,
	})
	require.NoError(t, err)

	fallback := NewMemoryGroupStorage(&MemoryGroupStorageConfig{
		Logger:  slog.Default(),
		Metrics: bm,
	})

	cfg := StorageManagerConfig{
		Primary:             primary,
		Fallback:            fallback,
		Logger:              slog.Default(),
		Metrics:             bm,
		HealthCheckInterval: 20 * time.Millisecond,
		DegradeThreshold:    1,
		RecoverThreshold:    1,
		MinHoldDuration:     time.Millisecond,
		ReconcileTimeout:    5 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	sm := NewStorageManager(cfg)
	t.Cleanup(sm.Stop)

	return &testStorageManagerHarness{mr: mr, sm: sm, metrics: bm}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func TestStorageManager_InitialBackendIsRedis(t *testing.T) {
	h := newTestStorageManagerHarness(t)

	require.Equal(t, "primary", h.sm.GetCurrentStorage())
	require.Equal(t, float64(1), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("redis")))
	require.Equal(t, float64(0), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("memory")))
}

// TestStorageManager_ProbeFlipsGaugeOnRedisLossAndRecovery is the core
// probe test: a real miniredis Close()/Restart() (not a mock Ping error)
// drives the health-check goroutine through both transitions.
func TestStorageManager_ProbeFlipsGaugeOnRedisLossAndRecovery(t *testing.T) {
	h := newTestStorageManagerHarness(t)

	h.mr.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "fallback"
	})
	require.Equal(t, float64(0), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("redis")))
	require.Equal(t, float64(1), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("memory")))

	require.NoError(t, h.mr.Restart())

	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "primary"
	})
	require.Equal(t, float64(1), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("redis")))
	require.Equal(t, float64(0), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("memory")))
}

// TestStorageManager_ResumePathAfterRecovery is the "resume path" half of
// the brief's test ask: while Redis is down, Store keeps succeeding
// (against the fallback); once Redis recovers, a NEW Store lands back in
// Redis, not the fallback — i.e. the manager genuinely resumes using
// primary rather than getting stuck on fallback after one switch.
//
// Fix-round finding I-1: a group written ONLY to the fallback during the
// outage (no pre-outage primary counterpart) is now expected to survive
// through primary after recovery, because reconcileFallbackIntoPrimary
// writes every fallback-resident group through before flipping — this used
// to be the documented "does NOT survive" boundary; it is now closed as a
// side effect of closing the (worse) stale-primary-shadows-fallback
// direction. See TestStorageManager_RecoveryWriteThroughPrefersFallback...
// below for the case that actually exercises overwriting a stale primary
// copy.
func TestStorageManager_ResumePathAfterRecovery(t *testing.T) {
	h := newTestStorageManagerHarness(t)
	ctx := context.Background()

	h.mr.Close()
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "fallback"
	})

	duringOutage := &AlertGroup{Key: "during-outage", Metadata: &GroupMetadata{}}
	require.NoError(t, h.sm.Store(ctx, duringOutage), "Store must keep working against the fallback while primary is down")

	require.NoError(t, h.mr.Restart())
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "primary"
	})

	got, err := h.sm.Load(ctx, "during-outage")
	require.NoError(t, err, "reconcileFallbackIntoPrimary's write-through must have carried this group into primary before the flip")
	require.Equal(t, GroupKey("during-outage"), got.Key)

	afterRecovery := &AlertGroup{Key: "after-recovery", Metadata: &GroupMetadata{}}
	require.NoError(t, h.sm.Store(ctx, afterRecovery))
	got2, err := h.sm.Load(ctx, "after-recovery")
	require.NoError(t, err, "a group stored after recovery must be readable again — the manager must have actually resumed using primary")
	require.Equal(t, GroupKey("after-recovery"), got2.Key)
}

// TestStorageManager_RecoveryWriteThroughPrefersFallbackOverStalePrimary is
// fix-round finding I-1's core regression test: a group that existed in
// primary BEFORE the outage (the common case), and was then updated only in
// the fallback DURING the outage, must have the fallback's fresher copy
// win on recovery — not the stale pre-outage primary copy.
func TestStorageManager_RecoveryWriteThroughPrefersFallbackOverStalePrimary(t *testing.T) {
	h := newTestStorageManagerHarness(t)
	ctx := context.Background()

	preOutage := &AlertGroup{
		Key:      "g1",
		Alerts:   map[string]*core.Alert{"fpA": createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "A"})},
		Metadata: &GroupMetadata{},
	}
	require.NoError(t, h.sm.Store(ctx, preOutage), "seed the pre-outage copy in primary while healthy")

	h.mr.Close()
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "fallback"
	})

	// DefaultGroupManager cannot Load the pre-outage copy while degraded
	// (primary is unreachable, and Load never falls back to the other
	// store — see Load's own doc comment), so a real "update during the
	// outage" starts from scratch, exactly like this.
	duringOutage := &AlertGroup{
		Key:      "g1",
		Alerts:   map[string]*core.Alert{"fpB": createTestAlert("B", core.StatusFiring, map[string]string{"alertname": "B"})},
		Metadata: &GroupMetadata{},
	}
	require.NoError(t, h.sm.Store(ctx, duringOutage), "Store must succeed against the fallback while degraded")

	require.NoError(t, h.mr.Restart())
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "primary"
	})

	got, err := h.sm.Load(ctx, "g1")
	require.NoError(t, err)
	_, hasStaleAlert := got.Alerts["fpA"]
	_, hasFreshAlert := got.Alerts["fpB"]
	require.False(t, hasStaleAlert, "the stale pre-outage alert must NOT survive the write-through — the fallback's copy must win, not shadow it")
	require.True(t, hasFreshAlert, "the fresher, outage-window alert (from the fallback) must be what primary holds after recovery")
}

// TestStorageManager_RecoveryReplaysDegradedDeletion is fix-round finding
// I-1's "zombie group" regression test: a group deleted while degraded
// must not reappear from a stale pre-outage primary copy after recovery.
func TestStorageManager_RecoveryReplaysDegradedDeletion(t *testing.T) {
	h := newTestStorageManagerHarness(t)
	ctx := context.Background()

	require.NoError(t, h.sm.Store(ctx, &AlertGroup{Key: "g2", Metadata: &GroupMetadata{}}), "seed a pre-outage copy in primary")

	h.mr.Close()
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "fallback"
	})

	require.NoError(t, h.sm.Delete(ctx, "g2"), "Delete must succeed against the fallback while degraded")

	require.NoError(t, h.mr.Restart())
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "primary"
	})

	_, err := h.sm.Load(ctx, "g2")
	require.Error(t, err, "the degraded-window deletion must have been replayed against primary — g2 must not resurface as a zombie")
}

// TestStorageManager_StorePerCallFallbackFlipsGaugeImmediately covers the
// pre-existing (not new to this task) per-call fallback path — Store
// failing against primary with a connectivity-class error (fix-round
// finding I-2) switches immediately, without waiting for the next
// health-check tick — and asserts the gauge added by this task follows
// that path too, not just the periodic probe.
//
// Uses a real closed connection (mr.Close()), not miniredis's generic
// SetError: SetError injects an arbitrary RESP-level error string that
// isn't a connectivity failure by the classification this task added, so
// it no longer forces the per-call branch under the new (correct, narrower)
// semantics. A long HealthCheckInterval keeps the periodic probe from
// racing this assertion (the attribution issue flagged in review as Minor
// #7) — with it, the ONLY thing that can flip current here is the per-call
// path this test targets.
func TestStorageManager_StorePerCallFallbackFlipsGaugeImmediately(t *testing.T) {
	h := newTestStorageManagerHarness(t, func(cfg *StorageManagerConfig) {
		cfg.HealthCheckInterval = time.Hour
	})
	ctx := context.Background()

	h.mr.Close()

	err := h.sm.Store(ctx, &AlertGroup{Key: "per-call-fallback", Metadata: &GroupMetadata{}})
	require.NoError(t, err, "Store must still succeed via the fallback for a connectivity-class error")

	require.Equal(t, "fallback", h.sm.GetCurrentStorage())
	require.Equal(t, float64(0), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("redis")))
	require.Equal(t, float64(1), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("memory")))
}

// === Fix-round finding I-2: hysteresis + error classification ===

// fakePingStorage wraps a real GroupStorage delegate (so Store/Load/Delete/
// LoadAll behave normally, including for reconciliation) with a
// controllable Ping, for deterministic hysteresis tests that don't depend
// on miniredis's real connection timing.
type fakePingStorage struct {
	GroupStorage
	pingErr func(callNum int) error
	calls   atomic.Int64
}

func (f *fakePingStorage) Ping(_ context.Context) error {
	n := int(f.calls.Add(1))
	if f.pingErr == nil {
		return nil
	}
	return f.pingErr(n)
}

func newHysteresisTestManager(t *testing.T, pingErr func(int) error, opts ...func(*StorageManagerConfig)) (*StorageManager, *fakePingStorage) {
	t.Helper()

	fakePrimary := &fakePingStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		pingErr:      pingErr,
	}
	fallback := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})

	cfg := StorageManagerConfig{
		Primary:             fakePrimary,
		Fallback:            fallback,
		Logger:              slog.Default(),
		HealthCheckInterval: 10 * time.Millisecond,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	sm := NewStorageManager(cfg)
	t.Cleanup(sm.Stop)
	return sm, fakePrimary
}

func TestStorageManager_SingleTransientPingFailureDoesNotDegrade(t *testing.T) {
	sm, fake := newHysteresisTestManager(t, func(n int) error {
		if n == 2 { // exactly one blip, surrounded by successes
			return errors.New("transient blip")
		}
		return nil
	}, func(cfg *StorageManagerConfig) {
		cfg.DegradeThreshold = 3
	})

	time.Sleep(150 * time.Millisecond) // ~15 ticks at 10ms
	require.Equal(t, "primary", sm.GetCurrentStorage(),
		"a single transient Ping failure below DegradeThreshold must not flip to fallback")
	require.GreaterOrEqual(t, fake.calls.Load(), int64(10), "sanity: the probe must have actually run repeatedly")
}

func TestStorageManager_DegradesAfterConsecutiveFailureThreshold(t *testing.T) {
	sm, _ := newHysteresisTestManager(t, func(_ int) error {
		return errors.New("down")
	}, func(cfg *StorageManagerConfig) {
		cfg.DegradeThreshold = 3
	})

	waitForCondition(t, 2*time.Second, func() bool { return sm.GetCurrentStorage() == "fallback" })
}

// TestStorageManager_FlappingPingNeverAccumulatesEnoughConsecutiveFailures
// is the "flapping -> bounded transitions" half of the brief's test ask:
// a Ping that fails on every OTHER tick never strings together
// DegradeThreshold consecutive failures (each failure is immediately
// followed by a success that resets the counter), so it must produce ZERO
// transitions here — the correct, expected bound for this exact flap
// pattern, not an approximation.
func TestStorageManager_FlappingPingNeverAccumulatesEnoughConsecutiveFailures(t *testing.T) {
	sm, fake := newHysteresisTestManager(t, func(n int) error {
		if n%2 == 1 {
			return errors.New("flap")
		}
		return nil
	}, func(cfg *StorageManagerConfig) {
		cfg.DegradeThreshold = 3
	})

	time.Sleep(300 * time.Millisecond) // ~30 ticks of alternating fail/success
	require.Equal(t, "primary", sm.GetCurrentStorage(),
		"alternating fail/success never reaches 3 consecutive failures, so it must never degrade")
	require.GreaterOrEqual(t, fake.calls.Load(), int64(15), "sanity: the probe must have actually run many times")
}

// TestStorageManager_RecoveryRequiresMinHoldDurationAfterConsecutiveSuccesses
// proves MinHoldDuration is enforced independently of RecoverThreshold: a
// primary that "recovers" immediately after a degrade accumulates
// RecoverThreshold successes almost instantly, but must still not fail
// forward until MinHoldDuration has elapsed since the degrade transition.
func TestStorageManager_RecoveryRequiresMinHoldDurationAfterConsecutiveSuccesses(t *testing.T) {
	var down atomic.Bool
	down.Store(true)

	sm, _ := newHysteresisTestManager(t, func(_ int) error {
		if down.Load() {
			return errors.New("down")
		}
		return nil
	}, func(cfg *StorageManagerConfig) {
		cfg.DegradeThreshold = 1
		cfg.RecoverThreshold = 2
		cfg.MinHoldDuration = 300 * time.Millisecond
	})

	waitForCondition(t, 1*time.Second, func() bool { return sm.GetCurrentStorage() == "fallback" })

	down.Store(false) // primary "recovers" on the very next probe tick

	// RecoverThreshold=2 needs only ~20ms of successes at a 10ms tick, but
	// MinHoldDuration is 300ms since the degrade transition.
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, "fallback", sm.GetCurrentStorage(),
		"must not fail forward before MinHoldDuration has elapsed, even once RecoverThreshold successes have accumulated")

	waitForCondition(t, 2*time.Second, func() bool { return sm.GetCurrentStorage() == "primary" })
}

// === Fix-round finding I-2: per-call error classification ===

// fakeStoreErrStorage wraps a real GroupStorage delegate with a
// controllable Store error, for deterministic per-call classification
// tests.
type fakeStoreErrStorage struct {
	GroupStorage
	storeErr error
}

func (f *fakeStoreErrStorage) Store(ctx context.Context, group *AlertGroup) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	return f.GroupStorage.Store(ctx, group)
}

func (f *fakeStoreErrStorage) Ping(_ context.Context) error { return nil } // healthy for these tests

func TestStorageManager_Store_NonConnectivityErrorDoesNotDegrade(t *testing.T) {
	fakePrimary := &fakeStoreErrStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		storeErr:     NewVersionMismatchError("g1", 1, 2),
	}
	sm := NewStorageManager(StorageManagerConfig{
		Primary:             fakePrimary,
		Fallback:            NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		Logger:              slog.Default(),
		HealthCheckInterval: time.Hour, // isolate the per-call path from the periodic probe
	})
	t.Cleanup(sm.Stop)

	err := sm.Store(context.Background(), &AlertGroup{Key: "g1", Metadata: &GroupMetadata{}})
	require.Error(t, err, "the original error must be returned to the caller unchanged")
	require.Equal(t, "primary", sm.GetCurrentStorage(),
		"a version-mismatch error is not a connectivity failure and must not degrade the storage layer")
}

func TestStorageManager_Store_ConnectivityErrorDegradesImmediately(t *testing.T) {
	fakePrimary := &fakeStoreErrStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		storeErr:     fmt.Errorf("redis transaction for g1: %w", errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")),
	}
	sm := NewStorageManager(StorageManagerConfig{
		Primary:             fakePrimary,
		Fallback:            NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		Logger:              slog.Default(),
		HealthCheckInterval: time.Hour,
	})
	t.Cleanup(sm.Stop)

	err := sm.Store(context.Background(), &AlertGroup{Key: "g1", Metadata: &GroupMetadata{}})
	require.NoError(t, err, "Store must still succeed via the fallback for a connectivity-class error")
	require.Equal(t, "fallback", sm.GetCurrentStorage())
}

// TestIsConnectivityError covers fix-round 2 finding I-6: a
// context.Canceled/DeadlineExceeded error only counts as connectivity when
// the CALLER's own ctx is still live — i.e. some other, Redis-call-scoped
// deadline fired. A caller whose own context is already done (a
// disconnected client, a client-side timeout) must not look like a Redis
// outage.
func TestIsConnectivityError(t *testing.T) {
	liveCtx := context.Background()

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	expiredCtx, expiredCancel := context.WithTimeout(context.Background(), -time.Second)
	defer expiredCancel()

	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"nil error", liveCtx, nil, false},
		{"version mismatch", liveCtx, NewVersionMismatchError("g", 1, 2), false},
		{
			name: "context deadline exceeded, caller ctx still live -> a Redis-scoped deadline fired (I-6)",
			ctx:  liveCtx, err: context.DeadlineExceeded, want: true,
		},
		{
			name: "context canceled, caller ctx still live -> some OTHER context died (I-6)",
			ctx:  liveCtx, err: context.Canceled, want: true,
		},
		{
			name: "context canceled, but it's the CALLER's own ctx that's canceled -> not a Redis problem (I-6)",
			ctx:  cancelledCtx, err: context.Canceled, want: false,
		},
		{
			name: "context deadline exceeded, but it's the CALLER's own ctx that expired -> not a Redis problem (I-6)",
			ctx:  expiredCtx, err: context.DeadlineExceeded, want: false,
		},
		{"wrapped connection refused text", liveCtx, fmt.Errorf("redis transaction: %w", errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")), true},
		{"EOF text", liveCtx, errors.New("EOF"), true},
		{"unrecognized storage error", liveCtx, NewStorageError("store", errors.New("weird encoding issue")), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isConnectivityError(tc.ctx, tc.err))
		})
	}
}

// TestStorageManager_Store_CancelledCallerContextDoesNotDegrade is the
// end-to-end (real Store call, not just isConnectivityError in isolation)
// regression test for fix-round 2 finding I-6.
func TestStorageManager_Store_CancelledCallerContextDoesNotDegrade(t *testing.T) {
	fakePrimary := &fakeStoreErrStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		storeErr:     context.Canceled,
	}
	sm := NewStorageManager(StorageManagerConfig{
		Primary:             fakePrimary,
		Fallback:            NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		Logger:              slog.Default(),
		HealthCheckInterval: time.Hour,
	})
	t.Cleanup(sm.Stop)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sm.Store(cancelledCtx, &AlertGroup{Key: "g1", Metadata: &GroupMetadata{}})
	require.Error(t, err, "a cancelled caller context must surface as an error, not be silently absorbed by a fallback retry")
	require.Equal(t, "primary", sm.GetCurrentStorage(), "a cancelled CALLER context must not degrade the storage layer")
}

// TestStorageManager_Store_RedisScopedDeadlineWithLiveCallerContextDegrades
// is the companion case: a live caller context but a DeadlineExceeded from
// some OTHER (Redis-call-scoped) context is a genuine connectivity signal
// and must still degrade.
func TestStorageManager_Store_RedisScopedDeadlineWithLiveCallerContextDegrades(t *testing.T) {
	fakePrimary := &fakeStoreErrStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		storeErr:     fmt.Errorf("redis call: %w", context.DeadlineExceeded),
	}
	sm := NewStorageManager(StorageManagerConfig{
		Primary:             fakePrimary,
		Fallback:            NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		Logger:              slog.Default(),
		HealthCheckInterval: time.Hour,
	})
	t.Cleanup(sm.Stop)

	err := sm.Store(context.Background(), &AlertGroup{Key: "g1", Metadata: &GroupMetadata{}})
	require.NoError(t, err, "must succeed via the fallback: a Redis-scoped deadline with a still-live caller ctx is a genuine connectivity signal")
	require.Equal(t, "fallback", sm.GetCurrentStorage())
}

// groupWithAlertCount builds a minimal *AlertGroup with n distinguishable
// alerts, for tests that need to tell "which generation of this GroupKey
// is this" apart by a simple count rather than deep-inspecting content.
func groupWithAlertCount(key GroupKey, n int) *AlertGroup {
	alerts := make(map[string]*core.Alert, n)
	for i := 0; i < n; i++ {
		fp := fmt.Sprintf("fp%d", i)
		alerts[fp] = createTestAlert(fp, core.StatusFiring, map[string]string{"alertname": fp})
	}
	return &AlertGroup{Key: key, Alerts: alerts, Metadata: &GroupMetadata{}}
}

// TestStorageManager_TwoOutages_SecondOutageDoesNotSeeOrOverwriteFirstOutageData
// is the fix-round 2 finding C-1 regression test: the fallback used to
// never be pruned after a successful reconcile, so a SECOND outage could
// (1) serve a degraded Load from the FIRST outage's leftover, and (2)
// write that leftover back over fresh Redis state on its own recovery.
func TestStorageManager_TwoOutages_SecondOutageDoesNotSeeOrOverwriteFirstOutageData(t *testing.T) {
	h := newTestStorageManagerHarness(t)
	ctx := context.Background()

	// Outage 1: g1 is written with 1 alert while degraded.
	h.mr.Close()
	waitForCondition(t, 2*time.Second, func() bool { return h.sm.GetCurrentStorage() == "fallback" })
	require.NoError(t, h.sm.Store(ctx, groupWithAlertCount("g1", 1)))

	// Recover from outage 1: write-through carries g1 (1 alert) into
	// primary and — the fix under test — prunes it from the fallback.
	require.NoError(t, h.mr.Restart())
	waitForCondition(t, 2*time.Second, func() bool { return h.sm.GetCurrentStorage() == "primary" })

	// While healthy: g1 legitimately moves on to a fresh generation with
	// 42 alerts. Version is aligned to primary's current value first,
	// exactly like the real DefaultGroupManager's Load-then-mutate-then-
	// Store, so the optimistic-lock check lines up.
	fresh := groupWithAlertCount("g1", 42)
	fresh.Version = mustLoadVersion(t, h.sm, ctx, "g1")
	require.NoError(t, h.sm.Store(ctx, fresh))

	// Outage 2: unrelated to g1 this time — nothing touches it.
	h.mr.Close()
	waitForCondition(t, 2*time.Second, func() bool { return h.sm.GetCurrentStorage() == "fallback" })

	// DEFECT (C-1): a degraded Load must not resurrect outage-1's leftover.
	_, err := h.sm.Load(ctx, "g1")
	require.Error(t, err, "the fallback must be empty for g1 entering outage 2 — a degraded Load must not return outage-1's pruned leftover")

	// Recover from outage 2.
	require.NoError(t, h.mr.Restart())
	waitForCondition(t, 2*time.Second, func() bool { return h.sm.GetCurrentStorage() == "primary" })

	// DEFECT (C-1): outage 2's write-through must not have had outage-1's
	// leftover to write, so the fresh 42-alert generation must survive.
	got, err := h.sm.Load(ctx, "g1")
	require.NoError(t, err)
	require.Len(t, got.Alerts, 42, "outage 2's reconcile must not overwrite the fresh Redis state with outage-1's stale leftover")
}

// mustLoadVersion is a small test helper: Load g1 and return its current
// Version, so a test can build a "next generation" object that keeps
// RedisGroupStorage's optimistic-lock check happy.
func mustLoadVersion(t *testing.T, sm *StorageManager, ctx context.Context, key GroupKey) int64 {
	t.Helper()
	g, err := sm.Load(ctx, key)
	require.NoError(t, err)
	return g.Version
}

// TestStorageManager_RecoveryDeleteThenRecreateDuringSameOutage_GroupSurvives
// is the fix-round 2 finding I-5 regression test: a group deleted and then
// re-created under the SAME key during one outage used to be deleted from
// primary on recovery, because the deletion replay ran AFTER the
// write-through and nothing removed the stale "still deleted" entry.
func TestStorageManager_RecoveryDeleteThenRecreateDuringSameOutage_GroupSurvives(t *testing.T) {
	h := newTestStorageManagerHarness(t)
	ctx := context.Background()

	require.NoError(t, h.sm.Store(ctx, groupWithAlertCount("g1", 1)), "seed a pre-outage copy in primary")

	h.mr.Close()
	waitForCondition(t, 2*time.Second, func() bool { return h.sm.GetCurrentStorage() == "fallback" })

	require.NoError(t, h.sm.Delete(ctx, "g1"), "delete while degraded")
	require.NoError(t, h.sm.Store(ctx, groupWithAlertCount("g1", 2)), "re-create the SAME key later in the same outage")

	require.NoError(t, h.mr.Restart())
	waitForCondition(t, 2*time.Second, func() bool { return h.sm.GetCurrentStorage() == "primary" })

	got, err := h.sm.Load(ctx, "g1")
	require.NoError(t, err, "the re-created group must survive recovery, not be deleted by a stale replay entry from the earlier delete")
	require.Len(t, got.Alerts, 2)
}

// === fu6-mic item 1: FU-STORAGE-RECONCILE-SIGNAL ===
//
// Before this item, "stuck on fallback" looked identical on the
// backend-active gauge whether the periodic probe itself kept failing
// (Redis genuinely down, reconciliation never even attempted) or the probe
// reported primary healthy while reconcileFallbackIntoPrimary itself kept
// failing. These two tests are the brief's "reconcile error path increments;
// probe-fail path doesn't" pair.

// fakeLoadAllErrStorage wraps a GroupStorage delegate and forces LoadAll to
// always fail, independent of the delegate's real behavior — used to
// simulate "reconciliation itself is broken" deterministically, without
// depending on any real Redis/miniredis error path.
type fakeLoadAllErrStorage struct {
	GroupStorage
}

func (f *fakeLoadAllErrStorage) LoadAll(_ context.Context) ([]*AlertGroup, error) {
	return nil, errors.New("simulated fallback LoadAll failure")
}

// TestStorageManager_ReconcileFailure_IncrementsReconcileFailuresCounter is
// the positive case: the probe reports primary healthy again, but
// reconcileFallbackIntoPrimary itself fails (fallback.LoadAll erroring) —
// this must increment IncStorageReconcileFailure and must NOT flip current
// back to primary despite the probe being healthy.
func TestStorageManager_ReconcileFailure_IncrementsReconcileFailuresCounter(t *testing.T) {
	bm := sharedTestBusinessMetrics()
	before := testutil.ToFloat64(bm.StorageReconcileFailuresCounter())

	var primaryDown atomic.Bool
	primaryDown.Store(true)

	fakePrimary := &fakePingStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		pingErr: func(_ int) error {
			if primaryDown.Load() {
				return errors.New("down")
			}
			return nil
		},
	}
	fallback := &fakeLoadAllErrStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
	}

	sm := NewStorageManager(StorageManagerConfig{
		Primary:             fakePrimary,
		Fallback:            fallback,
		Logger:              slog.Default(),
		Metrics:             bm,
		HealthCheckInterval: 10 * time.Millisecond,
		DegradeThreshold:    1,
		RecoverThreshold:    1,
		MinHoldDuration:     time.Millisecond,
		ReconcileTimeout:    time.Second,
	})
	t.Cleanup(sm.Stop)

	waitForCondition(t, 2*time.Second, func() bool { return sm.GetCurrentStorage() == "fallback" })

	primaryDown.Store(false) // probe now reports healthy; fallback.LoadAll still always fails

	waitForCondition(t, 2*time.Second, func() bool {
		return testutil.ToFloat64(bm.StorageReconcileFailuresCounter()) > before
	})

	require.Equal(t, "fallback", sm.GetCurrentStorage(),
		"a failed reconciliation must not flip current back to primary even though the probe is healthy")
}

// TestStorageManager_ProbeFailure_DoesNotIncrementReconcileFailuresCounter is
// the negative case: while the probe itself keeps failing (primary
// genuinely down), reconciliation is never even attempted, so the
// reconcile-failures counter must stay untouched — this is exactly the
// distinction the counter exists to draw.
func TestStorageManager_ProbeFailure_DoesNotIncrementReconcileFailuresCounter(t *testing.T) {
	bm := sharedTestBusinessMetrics()
	before := testutil.ToFloat64(bm.StorageReconcileFailuresCounter())

	fakePrimary := &fakePingStorage{
		GroupStorage: NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()}),
		pingErr: func(_ int) error {
			return errors.New("down")
		},
	}
	fallback := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})

	sm := NewStorageManager(StorageManagerConfig{
		Primary:             fakePrimary,
		Fallback:            fallback,
		Logger:              slog.Default(),
		Metrics:             bm,
		HealthCheckInterval: 10 * time.Millisecond,
		DegradeThreshold:    1,
		RecoverThreshold:    1,
		MinHoldDuration:     time.Millisecond,
		ReconcileTimeout:    time.Second,
	})
	t.Cleanup(sm.Stop)

	waitForCondition(t, 2*time.Second, func() bool { return sm.GetCurrentStorage() == "fallback" })

	time.Sleep(200 * time.Millisecond) // ~20 more failed probes; reconcile must never be attempted

	require.Equal(t, before, testutil.ToFloat64(bm.StorageReconcileFailuresCounter()),
		"the probe-failing path must never attempt reconciliation, so the counter must not move")
}
