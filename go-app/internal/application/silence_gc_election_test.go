package application

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	businesssilencing "github.com/ipiton/AMP/internal/business/silencing"
	appconfig "github.com/ipiton/AMP/internal/config"
	coresilencing "github.com/ipiton/AMP/internal/core/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
)

// Task 6.4 (alertmanager-parity): ServiceRegistry wiring for silence GC
// leader election. Covers initializeSilenceManager/initializeSilenceGCElection
// end to end — profile/backend-driven skip behavior, and (with a real Redis
// backend) that exactly one of several replicas sharing one repository
// actually runs the GC worker. The election mechanics themselves (renewal,
// failover within TTL) are covered at the lock package level
// (internal/infrastructure/lock/election_test.go); these tests are about
// the plumbing — does the right profile/backend combination end up calling
// EnableLeaderGatedGC and wiring a real elector — not re-proving election
// correctness.

// gcCountingSilenceRepo is a minimal infrasilencing.SilenceRepository fake:
// ListSilences returns nothing (satisfies the manager's initial cache sync
// and the sync worker), ExpireSilences counts calls under a mutex so tests
// can assert how many times (and whether more than one replica) the GC
// worker actually ran against a "shared database." Everything else panics
// via the embedded nil interface if called — the GC/manager wiring paths
// under test never touch it.
type gcCountingSilenceRepo struct {
	infrasilencing.SilenceRepository

	mu          sync.Mutex
	expireCalls int
}

func (f *gcCountingSilenceRepo) ListSilences(_ context.Context, _ infrasilencing.SilenceFilter) ([]*coresilencing.Silence, error) {
	return nil, nil
}

func (f *gcCountingSilenceRepo) ExpireSilences(_ context.Context, _ time.Time, _ bool) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expireCalls++
	return 0, nil
}

func (f *gcCountingSilenceRepo) expireCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.expireCalls
}

func newTestGCReplica(t *testing.T, mr *miniredis.Miniredis) (*ServiceRegistry, *cache.RedisCache) {
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

func TestInitializeSilenceManager_StandardProfileWithoutRedis_GCRunsUnconditionally(t *testing.T) {
	r := &ServiceRegistry{
		logger:      slog.Default(),
		config:      &appconfig.Config{Profile: appconfig.ProfileStandard},
		silenceRepo: &gcCountingSilenceRepo{},
		cache:       nil, // no live Redis backend — same skip as initializeSilenceEventSync
	}

	require.NoError(t, r.initializeSilenceManager(context.Background()))
	defer func() {
		if r.silenceManager != nil {
			_ = r.silenceManager.Stop(context.Background())
		}
	}()

	require.Nil(t, r.LeaderElector(), "no Redis backend means no elector to wire")
	require.True(t, r.IsLeader(), "unwired election reports every replica as trivially leader")

	manager, ok := r.silenceManager.(*businesssilencing.DefaultSilenceManager)
	require.True(t, ok)
	require.True(t, manager.IsGCRunning(), "without an elector, GC must run unconditionally exactly as before task 6.4")
}

func TestInitializeSilenceManager_StandardProfileWithRedis_SingleReplicaBecomesLeaderAndRunsGC(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	r, redisCache := newTestGCReplica(t, mr)
	defer func() { _ = redisCache.Close() }()
	r.silenceRepo = &gcCountingSilenceRepo{}

	require.NoError(t, r.initializeSilenceManager(context.Background()))
	defer func() {
		if r.leaderElector != nil {
			_ = r.leaderElector.Stop(context.Background())
		}
		if r.silenceManager != nil {
			_ = r.silenceManager.Stop(context.Background())
		}
	}()

	require.NotNil(t, r.LeaderElector(), "a live Redis backend must wire a real elector")

	manager, ok := r.silenceManager.(*businesssilencing.DefaultSilenceManager)
	require.True(t, ok)

	require.Eventually(t, func() bool { return r.IsLeader() }, 2*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool { return manager.IsGCRunning() }, 2*time.Second, 20*time.Millisecond,
		"the only replica in the race should win leadership and run GC")
}

func TestInitializeSilenceManager_StandardProfileWithRedis_TwoReplicasOnlyOneRunsGC(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	sharedRepo := &gcCountingSilenceRepo{}

	replicaA, cacheA := newTestGCReplica(t, mr)
	defer func() { _ = cacheA.Close() }()
	replicaA.silenceRepo = sharedRepo

	replicaB, cacheB := newTestGCReplica(t, mr)
	defer func() { _ = cacheB.Close() }()
	replicaB.silenceRepo = sharedRepo

	ctx := context.Background()
	require.NoError(t, replicaA.initializeSilenceManager(ctx))
	require.NoError(t, replicaB.initializeSilenceManager(ctx))
	defer func() {
		for _, r := range []*ServiceRegistry{replicaA, replicaB} {
			if r.leaderElector != nil {
				_ = r.leaderElector.Stop(context.Background())
			}
			if r.silenceManager != nil {
				_ = r.silenceManager.Stop(context.Background())
			}
		}
	}()

	managerA := replicaA.silenceManager.(*businesssilencing.DefaultSilenceManager)
	managerB := replicaB.silenceManager.(*businesssilencing.DefaultSilenceManager)

	require.Eventually(t, func() bool {
		return managerA.IsGCRunning() != managerB.IsGCRunning()
	}, 3*time.Second, 20*time.Millisecond, "exactly one replica should be running GC")

	// The winner should have actually run its first pass against the
	// shared repository.
	require.Eventually(t, func() bool { return sharedRepo.expireCallCount() >= 1 }, 2*time.Second, 20*time.Millisecond)
}
