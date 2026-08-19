package application

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	appconfig "github.com/ipiton/AMP/internal/config"
	infracache "github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: deprecated pkg/metrics, same posture as service_registry.go
)

// === alertmanager-parity wave-5 item FU-STORAGEMANAGER-FAILBACK ===
//
// newGroupingStorage must wrap the standard profile's live-Redis GroupStorage
// with grouping.StorageManager (giving it a runtime health probe instead of
// the previous startup-only, never-revisited choice), and Shutdown must stop
// that probe's background goroutine. See storage_manager_test.go in the
// grouping package for the probe/gauge/resume-path tests themselves — these
// only cover the wiring, not the probe mechanics.

// sharedGroupStorageTestMetrics mirrors the grouping package's own
// sharedTestBusinessMetrics: metrics.NewBusinessMetrics() registers into the
// default Prometheus registry via promauto, so a second construction in the
// same test binary panics with AlreadyRegisteredError.
var (
	sharedGroupStorageTestMetricsOnce sync.Once
	sharedGroupStorageTestMetrics     *metrics.BusinessMetrics
)

func groupStorageTestMetrics() *metrics.BusinessMetrics {
	sharedGroupStorageTestMetricsOnce.Do(func() {
		sharedGroupStorageTestMetrics = metrics.NewBusinessMetrics()
	})
	return sharedGroupStorageTestMetrics
}

func TestNewGroupingStorage_StandardRedisWrapsStorageManagerAndSetsBackendGauge(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	redisCache, err := infracache.NewRedisCache(&infracache.CacheConfig{
		Addr:        mr.Addr(),
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	require.NoError(t, err)
	defer func() { _ = redisCache.Close() }()

	bm := groupStorageTestMetrics()

	r := &ServiceRegistry{
		config:  &appconfig.Config{Profile: appconfig.ProfileStandard},
		logger:  logger,
		metrics: bm,
		cache:   redisCache,
	}

	groupStorage, _, err := r.newGroupingStorage(context.Background())
	require.NoError(t, err)

	_, isStorageManager := groupStorage.(*grouping.StorageManager)
	require.True(t, isStorageManager, "newGroupingStorage must wrap the Redis GroupStorage in a *grouping.StorageManager, got %T", groupStorage)
	require.NotNil(t, r.groupStorageManager, "the registry must keep a reference for Shutdown to Stop()")

	require.Equal(t, float64(1), testutil.ToFloat64(bm.GroupStorageBackendGauge().WithLabelValues("redis")))
	require.Equal(t, float64(0), testutil.ToFloat64(bm.GroupStorageBackendGauge().WithLabelValues("memory")))

	r.groupStorageManager.Stop()
}

func TestNewGroupingStorage_LiteProfileSetsMemoryBackendGaugeNoStorageManager(t *testing.T) {
	bm := groupStorageTestMetrics()

	r := &ServiceRegistry{
		config:  &appconfig.Config{Profile: appconfig.ProfileLite},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics: bm,
	}

	groupStorage, _, err := r.newGroupingStorage(context.Background())
	require.NoError(t, err)

	_, isStorageManager := groupStorage.(*grouping.StorageManager)
	require.False(t, isStorageManager, "the lite profile has no primary to probe — it must not be wrapped")
	require.Nil(t, r.groupStorageManager)

	require.Equal(t, float64(1), testutil.ToFloat64(bm.GroupStorageBackendGauge().WithLabelValues("memory")))
	require.Equal(t, float64(0), testutil.ToFloat64(bm.GroupStorageBackendGauge().WithLabelValues("redis")))
}

// TestServiceRegistryShutdown_StopsGroupStorageManager is the Shutdown-side
// half: the health-probe goroutine StorageManager owns must actually be
// asked to stop, not leaked, and the field must be cleared like every other
// grouping-subsystem field Shutdown tears down.
func TestServiceRegistryShutdown_StopsGroupStorageManager(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	redisCache, err := infracache.NewRedisCache(&infracache.CacheConfig{
		Addr:        mr.Addr(),
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, logger)
	require.NoError(t, err)
	defer func() { _ = redisCache.Close() }()

	cfg := &appconfig.Config{
		Profile:  appconfig.ProfileStandard,
		Grouping: appconfig.GroupingConfig{Enabled: true},
		Routing:  minimalRouteTree(),
	}
	r := newTestRegistryForGrouping(cfg)
	r.cache = redisCache

	ctx := context.Background()
	require.NoError(t, r.initializeGrouping(ctx))
	require.NotNil(t, r.groupStorageManager, "standard profile with a live Redis cache must wrap group storage in a StorageManager")

	require.NoError(t, r.Shutdown(ctx))
	require.Nil(t, r.groupStorageManager, "Shutdown must stop and clear the group storage manager")
}
