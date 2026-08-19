package grouping

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: see storage_manager.go's own import
)

// === alertmanager-parity wave-5 item FU-STORAGEMANAGER-FAILBACK ===
//
// These tests exercise the runtime health probe StorageManager already had
// (TN-125) but that newGroupingStorage never actually wired up — see
// storage_manager.go's package doc for the "why now" and the "what recovery
// does NOT do" scope boundary. Two things are asserted: the probe flips the
// backend-active gauge on loss/recovery, and Store/Load actually keep
// working (via the fallback) while primary is down, then resume through
// primary once it's back — via a real miniredis Stop/Start, not a mock.

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
// them, with a fast HealthCheckInterval so tests don't wait out the 30s
// production default.
type testStorageManagerHarness struct {
	mr      *miniredis.Miniredis
	sm      *StorageManager
	metrics *metrics.BusinessMetrics
}

func newTestStorageManagerHarness(t *testing.T) *testStorageManagerHarness {
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

	sm := NewStorageManager(StorageManagerConfig{
		Primary:             primary,
		Fallback:            fallback,
		Logger:              slog.Default(),
		Metrics:             bm,
		HealthCheckInterval: 20 * time.Millisecond,
	})
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
func TestStorageManager_ResumePathAfterRecovery(t *testing.T) {
	h := newTestStorageManagerHarness(t)
	ctx := context.Background()

	h.mr.Close()
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "fallback"
	})

	duringOutage := &AlertGroup{Key: "during-outage", Metadata: &GroupMetadata{}}
	require.NoError(t, h.sm.Store(ctx, duringOutage), "Store must keep working against the fallback while primary is down")

	// The fallback-resident group is NOT expected to reappear through the
	// manager after recovery — see storage_manager.go's package doc
	// ("What recovery does NOT do, and why"). This is the documented
	// boundary, not an oversight: assert it explicitly so a future change
	// that silently starts merging state doesn't go unnoticed either way.
	require.NoError(t, h.mr.Restart())
	waitForCondition(t, 2*time.Second, func() bool {
		return h.sm.GetCurrentStorage() == "primary"
	})
	_, err := h.sm.Load(ctx, "during-outage")
	require.Error(t, err, "a group written only to the fallback during the outage must not silently appear as findable through primary after recovery")

	afterRecovery := &AlertGroup{Key: "after-recovery", Metadata: &GroupMetadata{}}
	require.NoError(t, h.sm.Store(ctx, afterRecovery))
	got, err := h.sm.Load(ctx, "after-recovery")
	require.NoError(t, err, "a group stored after recovery must be readable again — the manager must have actually resumed using primary")
	require.Equal(t, GroupKey("after-recovery"), got.Key)
}

// TestStorageManager_StorePerCallFallbackFlipsGaugeImmediately covers the
// pre-existing (not new to this task) per-call fallback path — Store
// failing against primary switches immediately, without waiting for the
// next health-check tick — and asserts the gauge added by this task follows
// that path too, not just the periodic probe.
func TestStorageManager_StorePerCallFallbackFlipsGaugeImmediately(t *testing.T) {
	h := newTestStorageManagerHarness(t)
	ctx := context.Background()

	// Close the connection but don't let the periodic probe run first —
	// SetError makes the very next Store call itself fail, forcing the
	// per-call fallback branch specifically (not checkHealthAndSwitch).
	h.mr.SetError("simulated redis failure")
	defer h.mr.SetError("")

	err := h.sm.Store(ctx, &AlertGroup{Key: "per-call-fallback", Metadata: &GroupMetadata{}})
	require.NoError(t, err, "Store must still succeed via the fallback even though primary's Store failed")

	require.Equal(t, "fallback", h.sm.GetCurrentStorage())
	require.Equal(t, float64(0), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("redis")))
	require.Equal(t, float64(1), testutil.ToFloat64(h.metrics.GroupStorageBackendGauge().WithLabelValues("memory")))
}
