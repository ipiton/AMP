package application

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
)

// Task 6.5 (alertmanager-parity, Phase 6): ServiceRegistry wiring for the
// cluster heartbeat backing /api/v2/status's `cluster` field. Mirrors
// silence_gc_election_test.go's (task 6.4) split between "does the right
// profile/backend combination wire the right thing" here and
// mechanism-level correctness in the heartbeat package's own tests
// (internal/infrastructure/cluster/heartbeat_test.go).

func newTestClusterReplica(t *testing.T, mr *miniredis.Miniredis) (*ServiceRegistry, *cache.RedisCache) {
	t.Helper()

	redisCache, err := cache.NewRedisCache(&cache.CacheConfig{
		Addr:        mr.Addr(),
		DB:          0,
		PoolSize:    5,
		DialTimeout: time.Second,
		ReadTimeout: time.Second,
	}, slog.Default())
	require.NoError(t, err)

	return &ServiceRegistry{
		logger: slog.Default(),
		config: &appconfig.Config{Profile: appconfig.ProfileStandard},
		cache:  redisCache,
	}, redisCache
}

func TestInitializeClusterHeartbeat_LiteProfile_StaysDisabled(t *testing.T) {
	r := &ServiceRegistry{
		logger: slog.Default(),
		config: &appconfig.Config{Profile: appconfig.ProfileLite},
	}

	require.NoError(t, r.initializeClusterHeartbeat(context.Background()))
	require.Nil(t, r.clusterHeartbeat)

	status := r.ClusterStatus(context.Background())
	require.Equal(t, "disabled", status.Status)
	require.Empty(t, status.Name)
	require.Empty(t, status.Peers)
}

func TestInitializeClusterHeartbeat_StandardProfileWithoutRedis_StaysDisabled(t *testing.T) {
	r := &ServiceRegistry{
		logger: slog.Default(),
		config: &appconfig.Config{Profile: appconfig.ProfileStandard},
		cache:  nil, // no live Redis backend
	}

	require.NoError(t, r.initializeClusterHeartbeat(context.Background()))
	require.Nil(t, r.clusterHeartbeat)

	status := r.ClusterStatus(context.Background())
	require.Equal(t, "disabled", status.Status)
}

func TestInitializeClusterHeartbeat_StandardProfileWithRedis_ReadyWithSelf(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	r, redisCache := newTestClusterReplica(t, mr)
	defer func() { _ = redisCache.Close() }()

	require.NoError(t, r.initializeClusterHeartbeat(context.Background()))
	defer func() {
		if r.clusterHeartbeat != nil {
			_ = r.clusterHeartbeat.Stop(context.Background())
		}
	}()
	require.NotNil(t, r.clusterHeartbeat)

	status := r.ClusterStatus(context.Background())
	require.Equal(t, "ready", status.Status)
	require.Equal(t, r.clusterHeartbeat.SelfID(), status.Name)
	require.Len(t, status.Peers, 1)
	require.Equal(t, r.clusterHeartbeat.SelfID(), status.Peers[0].Name)
}

func TestInitializeClusterHeartbeat_TwoReplicasSeeEachOtherInStatus(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	replicaA, cacheA := newTestClusterReplica(t, mr)
	defer func() { _ = cacheA.Close() }()
	replicaB, cacheB := newTestClusterReplica(t, mr)
	defer func() { _ = cacheB.Close() }()

	ctx := context.Background()
	require.NoError(t, replicaA.initializeClusterHeartbeat(ctx))
	require.NoError(t, replicaB.initializeClusterHeartbeat(ctx))
	defer func() {
		for _, r := range []*ServiceRegistry{replicaA, replicaB} {
			if r.clusterHeartbeat != nil {
				_ = r.clusterHeartbeat.Stop(context.Background())
			}
		}
	}()

	statusA := replicaA.ClusterStatus(ctx)
	require.Equal(t, "ready", statusA.Status)
	require.Len(t, statusA.Peers, 2, "each replica's status should list both peers")

	statusB := replicaB.ClusterStatus(ctx)
	require.Len(t, statusB.Peers, 2)
}

// TestClusterStatus_RaceSafeAgainstShutdown proves ClusterStatus() (called
// from request-serving goroutines, same as the real /api/v2/status
// handler) never races with Shutdown() tearing down clusterHeartbeat — the
// write-once field discipline task 6.4 established for leaderElector,
// applied here for the same reason (finding 2 of that task's review).
// Run with -race; a bare pass without -race proves nothing.
func TestClusterStatus_RaceSafeAgainstShutdown(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	r, redisCache := newTestClusterReplica(t, mr)
	defer func() { _ = redisCache.Close() }()
	// Shutdown reaches other fields too; give it the minimum this test's
	// registry needs to run Shutdown() without touching anything nil'd out
	// by a real Initialize() this test never calls.
	r.initialized = true

	require.NoError(t, r.initializeClusterHeartbeat(context.Background()))

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = r.ClusterStatus(context.Background())
			}
		}
	}()

	// Give the reader goroutine a head start so Shutdown genuinely races
	// with in-flight reads rather than running before any read starts.
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, r.Shutdown(context.Background()))

	close(stop)
	wg.Wait()

	// Post-shutdown, ClusterStatus must report "disabled" (the field is
	// deliberately not nil'd, but IsRegistered() is false).
	require.Equal(t, "disabled", r.ClusterStatus(context.Background()).Status)
}
