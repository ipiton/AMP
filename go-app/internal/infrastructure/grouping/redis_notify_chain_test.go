package grouping

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	"github.com/stretchr/testify/require"
)

// createTestManagerWithRedisNotifyLog builds a DefaultGroupManager wired
// with the given publisher and GroupNotifyLog (task 6.1) — mirrors
// createTestManagerWithChain (notify_chain_test.go) but lets the caller
// inject a Redis-backed notify log instead of accepting the in-memory
// default, so two managers can simulate two HA replicas that only share
// state via Redis.
func createTestManagerWithRedisNotifyLog(t *testing.T, pub GroupNotificationPublisher, notifyLog GroupNotifyLog) *DefaultGroupManager {
	t.Helper()
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

	// Each simulated replica keeps its own local group storage (group
	// storage/timer ownership replication is out of scope for task 6.1 —
	// see redis_notify_log.go's package doc comment on why this is
	// deliberately narrower than full distributed timer ownership, task
	// 6.2). What task 6.1 shares across replicas is only the nflog.
	storage := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})

	manager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Logger:       slog.Default(),
		Storage:      storage,
		Publisher:    pub,
		NotifyLog:    notifyLog,
	})
	require.NoError(t, err)
	return manager
}

// newReplicaRedisNotifyLog builds a RedisNotifyLog with its own Redis
// client/connection against mr, simulating a distinct replica process.
func newReplicaRedisNotifyLog(t *testing.T, mr *miniredis.Miniredis) (*RedisNotifyLog, func()) {
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

// TestPublishGroupAlerts_CrossReplicaRedisClaim_ConcurrentFiringsDedupToOnePublish
// is task 6.1's headline correctness test: two DefaultGroupManager
// instances — simulating two HA replicas, each with its own local group
// storage and its own RedisNotifyLog connection, sharing only the Redis
// backend — both fire publishGroupAlerts for the SAME group (same
// GroupKey, same alert set/signature) concurrently and repeatedly. Without
// the cross-replica publish claim, both would independently observe
// "not a duplicate" (each has its own empty in-memory notifyDedupLog / its
// own process-local publishLocks) and both would publish. With
// RedisNotifyLog's TryClaim + shared entry, only one publish must ever go
// through.
func TestPublishGroupAlerts_CrossReplicaRedisClaim_ConcurrentFiringsDedupToOnePublish(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	notifyLogA, cleanupA := newReplicaRedisNotifyLog(t, mr)
	defer cleanupA()
	notifyLogB, cleanupB := newReplicaRedisNotifyLog(t, mr)
	defer cleanupB()

	pub := &mockPublisher{} // shared "receiver endpoint" both replicas publish through
	replicaA := createTestManagerWithRedisNotifyLog(t, pub, notifyLogA)
	replicaB := createTestManagerWithRedisNotifyLog(t, pub, notifyLogB)

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})

	_, err = replicaA.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)
	_, err = replicaB.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	groupA, err := replicaA.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	groupB, err := replicaB.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	const firingsPerReplica = 15
	var wg sync.WaitGroup
	wg.Add(2 * firingsPerReplica)
	for i := 0; i < firingsPerReplica; i++ {
		go func() {
			defer wg.Done()
			replicaA.publishGroupAlerts(ctx, groupA)
		}()
		go func() {
			defer wg.Done()
			replicaB.publishGroupAlerts(ctx, groupB)
		}()
	}
	wg.Wait()

	require.Len(t, pub.calls(), 1,
		"two replicas concurrently firing the same unchanged group must dedup to exactly one publish via the shared Redis nflog+claim")
}

// TestPublishGroupAlerts_RedisNotifyLog_RedisDownFailsOpenAndStillPublishes
// verifies the documented fail-open posture (redis_notify_log.go's package
// doc comment / publishGroupAlerts): if Redis is unreachable at fire time,
// the notification is still published (favoring delivery over strict dedup
// during an outage) rather than being silently dropped.
func TestPublishGroupAlerts_RedisNotifyLog_RedisDownFailsOpenAndStillPublishes(t *testing.T) {
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

	notifyLog, err := NewRedisNotifyLog(context.Background(), &RedisNotifyLogConfig{
		Client: redisCache.GetClient(),
		Logger: slog.Default(),
	})
	require.NoError(t, err)

	// Kill Redis AFTER construction succeeded — mirrors the existing
	// service_registry_grouping_test.go pattern for the same reason
	// (isolates "Redis died mid-run" from "Redis was never reachable").
	mr.Close()

	pub := &mockPublisher{}
	manager := createTestManagerWithRedisNotifyLog(t, pub, notifyLog)

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err = manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	require.Len(t, pub.calls(), 1, "a Redis-down nflog must fail OPEN — the notification must still be published")
}
