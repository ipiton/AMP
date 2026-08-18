package lock

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeWorker is a minimal stand-in for a leader-only background worker (e.g.
// DefaultSilenceManager's GC worker): OnAcquired increments runs and marks
// itself active, OnLost marks itself inactive. Used to assert leader-only
// work executes on exactly the elector that currently holds the lock.
type fakeWorker struct {
	mu     sync.Mutex
	active bool
	starts int
	stops  int
}

func (w *fakeWorker) start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active = true
	w.starts++
}

func (w *fakeWorker) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.active = false
	w.stops++
}

func (w *fakeWorker) isActive() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active
}

func (w *fakeWorker) counts() (starts, stops int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.starts, w.stops
}

func setupElectionTestRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})
	return client, mr
}

// TestLeaderElector_ExactlyOneLeader proves that of two electors racing for
// the same Redis key, exactly one becomes leader and only that one's
// leader-only worker runs.
func TestLeaderElector_ExactlyOneLeader(t *testing.T) {
	client, _ := setupElectionTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workerA := &fakeWorker{}
	workerB := &fakeWorker{}

	cfg := &LeaderElectorConfig{TTL: 2 * time.Second, RenewInterval: 300 * time.Millisecond, RetryInterval: 100 * time.Millisecond}
	electorA := NewLeaderElector(client, "test-leader", cfg, nil, workerA.start, workerA.stop)
	electorB := NewLeaderElector(client, "test-leader", cfg, nil, workerB.start, workerB.stop)

	require.NoError(t, electorA.Start(ctx))
	require.NoError(t, electorB.Start(ctx))

	require.Eventually(t, func() bool {
		return electorA.IsLeader() != electorB.IsLeader()
	}, 3*time.Second, 20*time.Millisecond, "exactly one elector should become leader")

	// Give the loser a couple of retry cycles to (correctly) fail to
	// acquire, and the winner to run its worker at least once.
	time.Sleep(300 * time.Millisecond)

	leaderIsA := electorA.IsLeader()
	assert.NotEqual(t, leaderIsA, electorB.IsLeader())

	if leaderIsA {
		assert.True(t, workerA.isActive(), "leader's worker should be active")
		assert.False(t, workerB.isActive(), "non-leader's worker must not run")
	} else {
		assert.True(t, workerB.isActive(), "leader's worker should be active")
		assert.False(t, workerA.isActive(), "non-leader's worker must not run")
	}

	require.NoError(t, electorA.Stop(context.Background()))
	require.NoError(t, electorB.Stop(context.Background()))
}

// TestLeaderElector_FailoverWithinTTL proves that when the leader stops
// (graceful stop or context cancellation — both go through the same code
// path here), the other instance takes over well within the lock's TTL.
func TestLeaderElector_FailoverWithinTTL(t *testing.T) {
	client, _ := setupElectionTestRedis(t)

	workerA := &fakeWorker{}
	workerB := &fakeWorker{}

	ttl := 1 * time.Second
	cfg := &LeaderElectorConfig{TTL: ttl, RenewInterval: 200 * time.Millisecond, RetryInterval: 100 * time.Millisecond}

	ctxA, cancelA := context.WithCancel(context.Background())
	electorA := NewLeaderElector(client, "failover-leader", cfg, nil, workerA.start, workerA.stop)
	require.NoError(t, electorA.Start(ctxA))

	require.Eventually(t, func() bool { return electorA.IsLeader() }, 2*time.Second, 20*time.Millisecond)
	require.True(t, workerA.isActive())

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	electorB := NewLeaderElector(client, "failover-leader", cfg, nil, workerB.start, workerB.stop)
	require.NoError(t, electorB.Start(ctxB))

	require.Never(t, func() bool { return electorB.IsLeader() }, 200*time.Millisecond, 20*time.Millisecond,
		"B must not acquire while A is alive and renewing")

	// Simulate A crashing/stopping: cancel its context (its own run loop
	// releases the lock on the way out — see LeaderElector.run's deferred
	// loseLeadership — so this exercises the "graceful or crash, either
	// way it's a context cancellation" path called out in the brief).
	start := time.Now()
	cancelA()

	require.Eventually(t, func() bool { return electorB.IsLeader() }, ttl, 20*time.Millisecond,
		"B should acquire leadership after A stops")
	elapsed := time.Since(start)
	assert.Less(t, elapsed, ttl, "failover should complete well within the lock TTL")

	require.Eventually(t, func() bool { return workerB.isActive() }, 500*time.Millisecond, 20*time.Millisecond)
	assert.False(t, workerA.isActive(), "A's worker must have stopped")
}

// TestLeaderElector_RenewalKeepsLeadership proves that under normal
// operation the leader keeps renewing and never flaps (OnLost never fires,
// the worker never restarts) across multiple renewal intervals.
func TestLeaderElector_RenewalKeepsLeadership(t *testing.T) {
	client, _ := setupElectionTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker := &fakeWorker{}
	cfg := &LeaderElectorConfig{TTL: 500 * time.Millisecond, RenewInterval: 100 * time.Millisecond, RetryInterval: 100 * time.Millisecond}
	elector := NewLeaderElector(client, "renew-leader", cfg, nil, worker.start, worker.stop)

	require.NoError(t, elector.Start(ctx))
	require.Eventually(t, func() bool { return elector.IsLeader() }, 1*time.Second, 20*time.Millisecond)

	// Live through several renewal intervals.
	time.Sleep(700 * time.Millisecond)

	assert.True(t, elector.IsLeader(), "leadership should not flap under normal operation")
	starts, stops := worker.counts()
	assert.Equal(t, 1, starts, "worker should have started exactly once, not restarted")
	assert.Equal(t, 0, stops, "OnLost should never fire while renewal keeps succeeding")

	require.NoError(t, elector.Stop(context.Background()))
	_, stopsAfter := worker.counts()
	assert.Equal(t, 1, stopsAfter, "Stop should cleanly stop the worker exactly once")
}

// TestAlwaysLeader_LiteProfileNoop proves the lite-profile stand-in always
// reports leadership and runs the worker exactly once on Start.
func TestAlwaysLeader_LiteProfileNoop(t *testing.T) {
	worker := &fakeWorker{}
	elector := NewAlwaysLeader(worker.start, worker.stop)

	assert.True(t, elector.IsLeader(), "AlwaysLeader must report leader before Start too")

	ctx := context.Background()
	require.NoError(t, elector.Start(ctx))
	assert.True(t, elector.IsLeader())
	assert.True(t, worker.isActive())

	// Idempotent: a second Start must not restart the worker.
	require.NoError(t, elector.Start(ctx))
	starts, _ := worker.counts()
	assert.Equal(t, 1, starts)

	require.NoError(t, elector.Stop(ctx))
	assert.False(t, worker.isActive())
	assert.True(t, elector.IsLeader(), "IsLeader stays true even after Stop — it is a lite-profile constant, not a lifecycle flag")

	// Idempotent stop.
	require.NoError(t, elector.Stop(ctx))
	_, stops := worker.counts()
	assert.Equal(t, 1, stops)
}

// TestLeaderElector_StartTwiceErrors proves Start is not safe to call twice
// on the same instance (mirrors DistributedLock/manager idempotency guards
// elsewhere in this codebase, which return an error rather than silently
// leaking a second goroutine).
func TestLeaderElector_StartTwiceErrors(t *testing.T) {
	client, _ := setupElectionTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	elector := NewLeaderElector(client, "double-start", nil, nil, nil, nil)
	require.NoError(t, elector.Start(ctx))
	err := elector.Start(ctx)
	assert.Error(t, err)

	require.NoError(t, elector.Stop(context.Background()))
}

// TestLeaderElector_ConcurrentRace runs many electors against one key to
// make sure the underlying SET NX arbitration holds under real contention
// (not just two instances).
func TestLeaderElector_ConcurrentRace(t *testing.T) {
	client, _ := setupElectionTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const n = 5
	cfg := &LeaderElectorConfig{TTL: 1 * time.Second, RenewInterval: 200 * time.Millisecond, RetryInterval: 50 * time.Millisecond}

	electors := make([]*LeaderElector, n)
	var activeCount atomic.Int32
	for i := 0; i < n; i++ {
		electors[i] = NewLeaderElector(client, "race-leader", cfg, nil,
			func(context.Context) { activeCount.Add(1) },
			func() { activeCount.Add(-1) },
		)
		require.NoError(t, electors[i].Start(ctx))
	}

	require.Eventually(t, func() bool { return activeCount.Load() == 1 }, 3*time.Second, 20*time.Millisecond,
		"exactly one leader-only worker should be active at a time")

	leaders := 0
	for _, e := range electors {
		if e.IsLeader() {
			leaders++
		}
	}
	assert.Equal(t, 1, leaders)

	for _, e := range electors {
		require.NoError(t, e.Stop(context.Background()))
	}
}
