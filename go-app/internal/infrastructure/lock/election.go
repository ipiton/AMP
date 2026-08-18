package lock

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Task 6.4 (alertmanager-parity): leader election for GC/sync-style workers
// that mutate a shared store (e.g. DefaultSilenceManager's GC worker expires
// and deletes silence rows in PostgreSQL). Running such a worker on every
// replica is redundant load, not a correctness bug — this package makes it
// run on exactly one replica at a time, on top of the existing
// DistributedLock (SET NX / Lua-CAS release, same as everywhere else in
// this package).
//
// Two implementations share the Elector interface so callers don't need to
// branch on deployment profile:
//   - LeaderElector: real Redis-backed election (standard profile).
//   - AlwaysLeader: no-op, always-leader (lite profile / no coordination
//     backend available) — same call site, same semantics as "this replica
//     is the only one, so it always wins."

// Elector is the leadership-state hook exposed to callers. IsLeader() is the
// hook task 6.5's status endpoint reads to report which replica currently
// owns leader-only work.
type Elector interface {
	// Start begins (or resumes) leader election. Non-blocking. Safe to call
	// only once per Elector; returns an error if already started.
	Start(ctx context.Context) error

	// Stop ends leader election: cancels the background loop and, if this
	// replica currently holds leadership, runs the loss path (OnLost
	// callback, then releases the underlying lock) before returning. Blocks
	// until fully stopped or ctx is done, whichever comes first.
	Stop(ctx context.Context) error

	// IsLeader reports whether this replica currently holds leadership.
	// Safe for concurrent use.
	IsLeader() bool
}

// Default election timings (task 6.4). TTL sits in the 15-30s window the
// brief calls out: long enough that normal renewal jitter/network blips
// don't cause flapping, short enough that a crashed leader's slot frees up
// quickly. RenewInterval is TTL/3 so at least two consecutive renewals can
// fail before the lock actually expires. RetryInterval governs how often a
// non-leader replica checks whether the slot has opened up.
const (
	DefaultElectionTTL           = 20 * time.Second
	DefaultElectionRenewInterval = DefaultElectionTTL / 3
	DefaultElectionRetryInterval = 2 * time.Second
)

// LeaderElectorConfig configures a LeaderElector. Zero-value fields fall
// back to the Default* constants above (see withDefaults).
type LeaderElectorConfig struct {
	TTL           time.Duration
	RenewInterval time.Duration
	RetryInterval time.Duration
}

func (c *LeaderElectorConfig) withDefaults() LeaderElectorConfig {
	resolved := LeaderElectorConfig{
		TTL:           DefaultElectionTTL,
		RenewInterval: DefaultElectionRenewInterval,
		RetryInterval: DefaultElectionRetryInterval,
	}
	if c == nil {
		return resolved
	}
	if c.TTL > 0 {
		resolved.TTL = c.TTL
	}
	if c.RenewInterval > 0 {
		resolved.RenewInterval = c.RenewInterval
	}
	if c.RetryInterval > 0 {
		resolved.RetryInterval = c.RetryInterval
	}
	return resolved
}

// LeaderElector runs a background acquire+renew loop against a Redis-backed
// DistributedLock and drives onAcquired/onLost so a caller can start/stop
// leader-only background work.
//
// onAcquired is called synchronously, from the elector's own loop
// goroutine, the moment this replica wins the lock. It must return promptly
// — spawn a goroutine for any long-running work — since it blocks the loop
// (and therefore the next renewal tick) until it returns. It receives a
// context that is cancelled the instant leadership is lost, before onLost
// runs, so long-running work started from onAcquired should watch it.
//
// onLost is called synchronously once leadership ends, either because
// renewal failed (lock lost to TTL expiry/another replica) or Stop was
// called while this replica was leader. It should block until the
// leader-only work it is responsible for has actually stopped — the next
// acquire attempt happens right after it returns.
//
// Either callback may be nil.
type LeaderElector struct {
	key    string
	cfg    LeaderElectorConfig
	lock   *DistributedLock
	logger *slog.Logger

	onAcquired func(ctx context.Context)
	onLost     func()

	isLeader     atomic.Bool
	leaderCancel context.CancelFunc // set/read only from the run() goroutine

	mu     sync.Mutex
	cancel context.CancelFunc
	doneCh chan struct{}
}

// NewLeaderElector creates a LeaderElector for the given Redis key. Start
// must be called to begin the acquire+renew loop.
func NewLeaderElector(
	redisClient *redis.Client,
	key string,
	cfg *LeaderElectorConfig,
	logger *slog.Logger,
	onAcquired func(ctx context.Context),
	onLost func(),
) *LeaderElector {
	if logger == nil {
		logger = slog.Default()
	}
	resolved := cfg.withDefaults()

	lockCfg := &LockConfig{
		TTL:            resolved.TTL,
		MaxRetries:     3,
		RetryInterval:  100 * time.Millisecond,
		AcquireTimeout: resolved.TTL,
		ReleaseTimeout: 2 * time.Second,
		ValuePrefix:    "leader",
	}

	return &LeaderElector{
		key:        key,
		cfg:        resolved,
		lock:       NewDistributedLock(redisClient, key, lockCfg, logger),
		logger:     logger,
		onAcquired: onAcquired,
		onLost:     onLost,
	}
}

// Start implements Elector.
func (e *LeaderElector) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.cancel != nil {
		e.mu.Unlock()
		return fmt.Errorf("leader elector already started for key %q", e.key)
	}
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.doneCh = make(chan struct{})
	done := e.doneCh
	e.mu.Unlock()

	go e.run(runCtx, done)

	e.logger.Info("leader election started", "key", e.key, "ttl", e.cfg.TTL)
	return nil
}

// Stop implements Elector.
func (e *LeaderElector) Stop(ctx context.Context) error {
	e.mu.Lock()
	cancel := e.cancel
	done := e.doneCh
	e.cancel = nil
	e.mu.Unlock()

	if cancel == nil {
		return nil // never started, or already stopped
	}
	cancel()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// IsLeader implements Elector.
func (e *LeaderElector) IsLeader() bool {
	return e.isLeader.Load()
}

// run is the main election loop. Exits when ctx is cancelled (via Stop),
// releasing leadership first if held.
func (e *LeaderElector) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer e.loseLeadership() // no-op if not currently leader

	for {
		if ctx.Err() != nil {
			return
		}

		if e.isLeader.Load() {
			if !e.sleep(ctx, e.cfg.RenewInterval) {
				return
			}
			if ctx.Err() != nil {
				return
			}
			if err := e.lock.Extend(ctx, e.cfg.TTL); err != nil {
				e.logger.Warn("leader election: renew failed, leadership lost", "key", e.key, "error", err)
				e.loseLeadership()
			}
			continue
		}

		acquired, err := e.lock.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			e.logger.Warn("leader election: acquire attempt failed", "key", e.key, "error", err)
		} else if acquired {
			e.becomeLeader(ctx)
			continue
		}

		if !e.sleep(ctx, e.cfg.RetryInterval) {
			return
		}
	}
}

// sleep waits for d or ctx cancellation, whichever comes first. Returns
// false if ctx was cancelled first (caller should stop looping).
func (e *LeaderElector) sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *LeaderElector) becomeLeader(ctx context.Context) {
	leaderCtx, cancel := context.WithCancel(ctx)
	e.leaderCancel = cancel
	e.isLeader.Store(true)
	e.logger.Info("leader election: acquired leadership", "key", e.key)
	if e.onAcquired != nil {
		e.onAcquired(leaderCtx)
	}
}

// loseLeadership is idempotent: safe to call whether or not this replica is
// currently leader.
//
// onLost is called synchronously and this function returns only once it
// does — i.e. this WAITS for onLost to finish rather than forcing it to
// abandon whatever it's doing. leaderCancel is cancelled first so
// long-running work watching it can start winding down, but onLost itself
// (e.g. DefaultSilenceManager.StopGC) is free to block until an in-flight
// pass completes rather than interrupting it. A caller that just became
// leader elsewhere can therefore briefly overlap with this replica's
// leader-only work finishing up; that's only safe because such work is
// expected to be idempotent (see StopGC's own doc comment for the concrete
// GC case) — this package has no way to enforce that for callback-supplied
// work in general.
func (e *LeaderElector) loseLeadership() {
	if !e.isLeader.CompareAndSwap(true, false) {
		return
	}
	if e.leaderCancel != nil {
		e.leaderCancel()
		e.leaderCancel = nil
	}
	e.logger.Warn("leader election: lost leadership", "key", e.key)
	if e.onLost != nil {
		e.onLost()
	}
	// Best-effort: release the underlying key so the next replica doesn't
	// have to wait out the full TTL. Safe even if we no longer actually
	// hold it (renewal failure case) — Release's Lua script is a CAS on the
	// stored value and no-ops if it doesn't match ours.
	releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := e.lock.Release(releaseCtx); err != nil {
		e.logger.Warn("leader election: release on leadership loss failed", "key", e.key, "error", err)
	}
}

// AlwaysLeader is a no-op Elector for deployment profiles where distributed
// coordination has no meaning (lite profile: a single replica, or standard
// profile with no live coordination backend) — IsLeader is always true, and
// Start/Stop run onAcquired/onLost exactly once each, synchronously, so the
// call site is identical to the real LeaderElector.
type AlwaysLeader struct {
	onAcquired func(ctx context.Context)
	onLost     func()

	started atomic.Bool
	mu      sync.Mutex
	cancel  context.CancelFunc
}

// NewAlwaysLeader creates an AlwaysLeader. Either callback may be nil.
func NewAlwaysLeader(onAcquired func(ctx context.Context), onLost func()) *AlwaysLeader {
	return &AlwaysLeader{onAcquired: onAcquired, onLost: onLost}
}

// IsLeader implements Elector: always true.
func (a *AlwaysLeader) IsLeader() bool { return true }

// Start implements Elector: runs onAcquired once, immediately. Idempotent.
func (a *AlwaysLeader) Start(ctx context.Context) error {
	if !a.started.CompareAndSwap(false, true) {
		return nil
	}
	leaderCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	if a.onAcquired != nil {
		a.onAcquired(leaderCtx)
	}
	return nil
}

// Stop implements Elector: runs onLost once. Idempotent.
func (a *AlwaysLeader) Stop(ctx context.Context) error {
	if !a.started.CompareAndSwap(true, false) {
		return nil
	}
	a.mu.Lock()
	cancel := a.cancel
	a.cancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if a.onLost != nil {
		a.onLost()
	}
	return nil
}
