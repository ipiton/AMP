// Package grouping provides automatic fallback/recovery coordination for group storage.
//
// StorageManager coordinates between RedisGroupStorage (primary) and MemoryGroupStorage (fallback),
// providing seamless automatic switching when Redis becomes unhealthy or recovers.
//
// Features:
//   - Automatic fallback to MemoryGroupStorage on Redis failure, gated by
//     consecutive-failure hysteresis and connectivity-error classification
//     (see "Hysteresis and error classification" below)
//   - Automatic recovery to RedisGroupStorage when Redis restored, gated by
//     consecutive-success hysteresis, a minimum hold time, and a
//     write-through + deletion-replay reconciliation pass (see "Runtime
//     failback/failforward" and "What recovery does (and does not) do" below)
//   - Health check polling every 30s (configurable, see StorageManagerConfig)
//   - Metrics for fallback/recovery events, plus a backend-active gauge
//     (BusinessMetrics.SetActiveGroupStorageBackend)
//   - Graceful shutdown
//
// TN-125: Group Storage (Redis Backend)
// Target Quality: 150%
// Date: 2025-11-04
//
// # Runtime failback/failforward (alertmanager-parity wave-5,
// FU-STORAGEMANAGER-FAILBACK)
//
// This type existed fully built — health probe, per-call fallback, recovery
// switch, fallback/recovery counters — since 2025-11-04, but
// ServiceRegistry.newGroupingStorage (added by the alertmanager-parity epic,
// 2026-08-18) never actually constructed one: it picked RedisGroupStorage or
// MemoryGroupStorage ONCE at startup and returned that value directly. A
// Redis outage after startup therefore had no runtime detection at all — the
// per-call fallback in GroupManager's callers (none; GroupManager itself
// holds no fallback logic) never engaged, and requests just got the
// underlying Redis error. wave-5 wires this type into newGroupingStorage's
// standard-profile Redis path (service_registry.go) to close that gap.
//
// # Hysteresis and error classification (fix-round finding I-2)
//
// The first wave-5 pass flipped sm.current on a single failed/succeeded
// Ping, and on ANY error from a per-call Store/Delete/StoreAll — including
// non-connectivity errors like ErrVersionMismatch (an expected, healthy
// outcome of two concurrent updates) or a JSON marshal failure. That made a
// flapping Redis oscillate on every probe tick, and made one optimistic-lock
// retry look identical to Redis being down.
//
//   - The periodic probe now requires degradeThreshold consecutive Ping
//     failures before switching to fallback, and BOTH recoverThreshold
//     consecutive Ping successes AND minHoldDuration elapsed since the last
//     transition before switching back — the hold time is independent of
//     the success streak, so a Redis that flaps a few successes right after
//     a transition still cannot flip back immediately.
//   - Per-call Store/Delete/StoreAll only switch to fallback for
//     isConnectivityError(err) — a real transport/timeout failure. Any
//     other error (version mismatch, serialization) is returned to the
//     caller unchanged, exactly as it would be with no StorageManager
//     wrapper at all; degrading the whole storage layer over it would be a
//     serious overreaction. This does NOT weaken detection of a real outage:
//     the periodic probe's Ping-based path is independent of this
//     classification and still catches a genuinely down Redis within one
//     health-check interval even if a specific error string isn't recognized.
//
// # What recovery does (and does not) do (fix-round finding I-1)
//
// Before the fix-round, checkHealthAndSwitch flipped sm.current back to
// primary immediately on recovery, with no reconciliation at all. That had
// TWO silent-loss directions, only one of which the original docs admitted:
//
//  1. (originally documented) A group created fresh in the fallback store
//     during the outage was simply invisible to primary after the flip —
//     Load returned GroupNotFoundError and a new group was started,
//     "duplicate notification possible, not silent data loss".
//  2. (NOT originally documented, and strictly worse) For a group that
//     already existed in Redis BEFORE the outage — the common case — the
//     stale pre-outage Redis copy would resurface after the flip and every
//     alert added during the outage would be silently dropped from the
//     group's membership on the next Store, because DefaultGroupManager
//     mutates whatever Load returns. And a Delete issued while degraded
//     only ever reached the fallback, so the pre-outage Redis copy came
//     back as a "zombie" group after recovery, capable of firing a
//     notification for alerts that had already resolved.
//
// The fix: reconcileFallbackIntoPrimary now runs BEFORE the flip whenever
// recovery is otherwise ready (probe hysteresis satisfied):
//
//   - Write-through: every group in the fallback store is Stored into
//     primary, with its Version aligned to whatever primary currently holds
//     for that key (a best-effort Load first) so RedisGroupStorage.Store's
//     optimistic-lock check does not reject the write-through as a spurious
//     "concurrent update" — see that function's own doc comment for the
//     mechanics. This makes the fallback's copy win over a stale primary
//     copy instead of the reverse, closing loss direction 2 above.
//   - Deletion replay: every GroupKey deleted while running on the fallback
//     (tracked in degradedDeletions, bounded) is also Deleted from primary,
//     so a pre-outage Redis copy of a group removed during the outage
//     cannot come back as a zombie — closing the second half of loss
//     direction 2.
//   - If the write-through phase itself fails, the flip is skipped entirely
//     — sm.current stays on fallback for another probe cycle rather than
//     failing forward onto a half-reconciled primary. Deletion replay is
//     best-effort (logged, not fatal) since a failure there only risks the
//     lesser, already-improved zombie-group case, not the write-through's
//     data-loss case.
//
// This is still NOT full state-merge machinery — the brief excludes that —
// and real limitations remain, stated plainly rather than folded into the
// same paragraph as a footnote:
//
//   - Loss direction 1 above (a group created fresh in the fallback, with no
//     pre-outage Redis counterpart) is unchanged: the write-through DOES
//     copy it into primary (it is in fallback.LoadAll()'s result too), so
//     that specific case is actually now closed as a side effect — but if
//     an alert for that key resolves and the group is cleaned up entirely
//     before the next reconciliation runs, there is nothing left to write
//     through; this is expected, not a bug, since a resolved/cleaned-up
//     group has nothing to reconcile.
//   - The write-through's own read-modify-write ("read primary's current
//     version, then Store the fallback's copy") runs WITHOUT sm.mu held —
//     see reconcileFallbackIntoPrimary's doc comment for why (holding the
//     lock for the whole pass would block every Store/Load/Delete call for
//     as long as reconciliation takes, which is an availability regression
//     of its own). This leaves a narrow race: a write that lands in the
//     fallback AFTER reconcileFallbackIntoPrimary's snapshot of it but
//     BEFORE the flip is not part of the write-through and is shadowed by
//     it once current points at primary again. This window is bounded by
//     one reconciliation pass (milliseconds to low seconds for a realistic
//     group count), not by the whole outage, but it is not zero.
//   - Multi-replica HA: MemoryGroupStorage is per-process. If TWO replicas
//     both degraded and both received writes for the SAME GroupKey during
//     the outage (possible if a load balancer does not pin a given alert's
//     requests to one replica), each replica's fallback holds a DIFFERENT
//     view of that group, and each replica's own reconciliation only knows
//     about its own fallback — whichever replica's checkHealthAndSwitch
//     reconciles that key last simply overwrites the other's outage-window
//     data via the same optimistic-lock alignment described above, with no
//     merge between the two replicas' views. This is the same
//     last-writer-wins limitation the brief already excludes full
//     state-merge machinery for — the fix above removes it for the
//     single-writer-per-key case (one replica, or a load balancer that
//     pins by GroupKey/alert), which is the common case, but does not (and
//     per the brief's scope, cannot) extend it to genuinely concurrent
//     cross-replica writes during a shared outage.
//
// # Fix-round 2 (finding C-1): the fallback is pruned after every successful
// reconcile, so a SECOND outage starts from empty
//
// The first fix-round's reconcileFallbackIntoPrimary wrote every group
// fallback.LoadAll() returned into primary, but never removed anything from
// the fallback afterward. MemoryGroupStorage has no TTL/eviction of its own
// (see its own package doc), so every outage's groups accumulated in the
// fallback for the lifetime of the process. A SECOND, later outage then hit
// three compounding problems: (1) a degraded Load during outage 2 could
// return an outage-1 leftover instead of GroupNotFoundError, resuming a
// group whose data was hours or days stale; (2) outage 2's reconcile would
// write-through THAT leftover too, aligning its Version to primary's
// current value and silently overwriting fresh Redis state with stale
// outage-1 data — reproduced directly: a fresh count of 42 replaced by a
// stale 1, with no error and no log beyond the ordinary recovery Info line;
// (3) reconciliation cost grew monotonically with every outage, so a large
// enough accumulated fallback could blow the reconcileTimeout and get stuck
// re-failing the flip indefinitely while Redis was actually healthy.
//
// The fix: reconcileFallbackIntoPrimary now deletes every key it just wrote
// through from the fallback once the write-through succeeds, so the next
// degraded window starts from an empty fallback — exactly like the very
// first outage does. Deletion-replay's degradedDeletions list was already
// cleared unconditionally on every reconcile attempt (see
// replayDegradedDeletions); this closes the other half by pruning the
// write-through side too.
//
// # Fix-round 2 (finding I-5): deletion replay now runs BEFORE the
// write-through
//
// The first fix-round replayed degradedDeletions AFTER writing through
// every fallback group. For the ordinary sequence "a group resolves and is
// deleted, then a new alert re-creates the SAME GroupKey, all during one
// outage", degradedDeletions still held that key (nothing removed it when
// the later Store re-created the group), so replaying it after the
// write-through deleted the just-written-through, freshly re-created group
// right back out of primary — reproduced directly: the re-created group
// came back as "group not found" after recovery. Replay now runs first, so
// a stale deletion entry for a key that was later re-created only ever
// deletes what write-through is about to correctly re-add — never the
// other way around. Store/StoreAll additionally remove a key from
// degradedDeletions the moment they successfully write it back to the
// fallback (see unrecordDegradedDeletion), so the list stays an accurate
// "still absent as of the last fallback write" set instead of accumulating
// stale entries across a long outage.
//
// # Fix-round 2 (finding I-6): a canceled/timed-out CALLER context no longer
// looks like a Redis outage
//
// isConnectivityError originally treated context.Canceled/DeadlineExceeded
// as connectivity failures unconditionally. The alert-ingest path passes a
// request-scoped context all the way down to Store, so one client
// disconnect or one client-side timeout produced a context.Canceled from
// the Redis call and degraded the WHOLE process to memory — for up to
// minHoldDuration + recoverThreshold probes (~2 minutes at the defaults),
// followed by a full write-through reconcile — over an event that said
// nothing about Redis's health. isConnectivityError now takes the same ctx
// the caller passed to Store/Delete/StoreAll and checks ctx.Err() first: if
// the caller's own context is already done, the cancellation/deadline is
// the caller's, not Redis's, and does not count. Only a
// context.DeadlineExceeded surfacing while the caller's context is still
// live — meaning some OTHER, Redis-call-scoped deadline fired — counts as
// connectivity.
package grouping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: deprecated pkg/metrics kept until v2 migration (v2 lacks BusinessMetrics)
)

// StorageManager coordinates between primary (Redis) and fallback (Memory) storage.
//
// Thread-safety: All methods are thread-safe via sync.RWMutex.
//
// Behavior:
//   - Starts with primary storage (Redis)
//   - Polls health every 30s via Ping()
//   - On sustained primary failure: switches to fallback (Memory), records metric
//   - On sustained primary recovery: reconciles fallback into primary, then
//     switches back, records metric
//   - All storage operations automatically use current active storage
//
// Example:
//
//	primary, _ := grouping.NewRedisGroupStorage(redisConfig)
//	fallback := grouping.NewMemoryGroupStorage(logger)
//
//	manager := grouping.NewStorageManager(grouping.StorageManagerConfig{
//		Primary:  primary,
//		Fallback: fallback,
//		Logger:   logger,
//		Metrics:  metrics,
//	})
//	defer manager.Stop()
//
//	// Automatically uses Redis (or Memory if Redis fails)
//	err := manager.Store(ctx, group)
type StorageManager struct {
	// primary storage (typically RedisGroupStorage)
	primary GroupStorage

	// fallback storage (typically MemoryGroupStorage)
	fallback GroupStorage

	// current active storage (primary or fallback)
	current GroupStorage

	// primaryLabel/fallbackLabel name the two backends for the
	// backend-active gauge (task fu5-cfg item 2) and log lines — e.g.
	// "redis"/"memory". Defaulted by NewStorageManager when left empty.
	primaryLabel  string
	fallbackLabel string

	// mu protects every mutable field below, including current.
	mu sync.RWMutex

	// logger for structured logging
	logger *slog.Logger

	// metrics for observability
	metrics *metrics.BusinessMetrics

	// healthCheckInterval controls how often checkHealthAndSwitch polls
	// primary.Ping (task fu5-cfg item 2: made configurable, default
	// unchanged at 30s, so tests can inject a short interval instead of
	// sleeping through a real 30s tick).
	healthCheckInterval time.Duration

	// degradeThreshold/recoverThreshold/minHoldDuration implement the
	// hysteresis fix-round finding I-2 asked for. See the package doc's
	// "Hysteresis and error classification" section.
	degradeThreshold int
	recoverThreshold int
	minHoldDuration  time.Duration

	// reconcileTimeout bounds reconcileFallbackIntoPrimary (fix-round
	// finding I-1).
	reconcileTimeout time.Duration

	// consecutiveFailures/consecutiveSuccesses count consecutive
	// primary.Ping outcomes since the last reset (on any transition, or on
	// the opposite outcome). Read/written only under mu.
	consecutiveFailures  int
	consecutiveSuccesses int

	// lastTransitionAt is when current last actually changed (either
	// direction). Used to enforce minHoldDuration before failing forward.
	lastTransitionAt time.Time

	// degradedDeletions tracks GroupKeys deleted while current was the
	// fallback, so reconcileFallbackIntoPrimary can replay those deletions
	// against primary on recovery — otherwise a group deleted while
	// degraded reappears with its pre-outage alert set as soon as primary
	// recovers, because primary's own copy was never touched (fix-round
	// finding I-1's "zombie group" case). A later successful Store/StoreAll
	// of the same key removes it again (fix-round 2 finding I-5's
	// unrecordDegradedDeletion), so this only ever holds keys genuinely
	// still absent as of the most recent fallback write.
	//
	// Bounded: maxDegradedDeletions caps it FIFO — recordDegradedDeletion
	// drops the OLDEST entry (logged at Warn, not silent) once an
	// unusually long outage with more deletions than the cap would
	// otherwise grow it further. A dropped entry means that one specific
	// deletion will not be replayed on recovery — the same zombie-group
	// risk this list exists to close, reopened only for whichever key gets
	// evicted.
	degradedDeletions []GroupKey

	// healthTicker for periodic health checks
	healthTicker *time.Ticker

	// stopChan signals health check goroutine to stop
	stopChan chan struct{}

	// stopped indicates if manager has been stopped
	stopped bool
}

// StorageManagerConfig configures NewStorageManager.
type StorageManagerConfig struct {
	// Primary is the primary storage (typically RedisGroupStorage). Required.
	Primary GroupStorage

	// Fallback is the fallback storage (typically MemoryGroupStorage). Required.
	Fallback GroupStorage

	// Logger for structured logging. Optional, defaults to slog.Default().
	Logger *slog.Logger

	// Metrics for fallback/recovery/backend-active tracking. Optional (nil
	// disables metrics, same posture as the rest of this package).
	Metrics *metrics.BusinessMetrics

	// HealthCheckInterval overrides the default 30s primary.Ping poll
	// interval (task fu5-cfg item 2). Optional; <= 0 uses the default.
	HealthCheckInterval time.Duration

	// PrimaryLabel/FallbackLabel name the two backends for the
	// backend-active gauge and log lines (task fu5-cfg item 2). Optional;
	// empty defaults to "redis"/"memory" — this type's only real-world
	// pairing (newGroupingStorage in service_registry.go).
	PrimaryLabel  string
	FallbackLabel string

	// DegradeThreshold is the number of consecutive primary.Ping failures
	// required before the periodic probe switches to fallback (fix-round
	// finding I-2). Optional; <= 0 uses the default (3). A single transient
	// Ping failure must not flip the whole storage layer to memory.
	DegradeThreshold int

	// RecoverThreshold is the number of consecutive primary.Ping successes
	// required before switching back from fallback (fix-round finding
	// I-2), in addition to MinHoldDuration below. Optional; <= 0 uses the
	// default (3).
	RecoverThreshold int

	// MinHoldDuration is the minimum time that must have elapsed since the
	// last backend transition before switching back to primary (fix-round
	// finding I-2) — independent of RecoverThreshold, so a Redis that
	// strings together RecoverThreshold successes right after a transition
	// still cannot flip back immediately. Optional; <= 0 uses the default
	// (30s).
	MinHoldDuration time.Duration

	// ReconcileTimeout bounds the write-through + deletion-replay pass
	// that runs before a failforward (fix-round finding I-1). Optional;
	// <= 0 uses the default (30s).
	ReconcileTimeout time.Duration
}

// Defaults for the fields StorageManagerConfig leaves unset. The health
// check interval default is unchanged from before task fu5-cfg item 2's
// config knob was added; the hysteresis/reconcile defaults are new in the
// fix-round (finding I-1/I-2).
const (
	defaultStorageManagerHealthCheckInterval = 30 * time.Second
	defaultDegradeThreshold                  = 3
	defaultRecoverThreshold                  = 3
	defaultMinHoldDuration                   = 30 * time.Second
	defaultReconcileTimeout                  = 30 * time.Second

	// maxDegradedDeletions bounds degradedDeletions — see that field's doc
	// comment. Matches the wave-4 "capped at 500" convention already used
	// elsewhere in this package's cross-replica bookkeeping (see
	// redis_notify_log.go's delivered-state cap).
	maxDegradedDeletions = 500
)

// NewStorageManager creates a new StorageManager with automatic fallback/recovery.
//
// The health check goroutine starts immediately and polls every
// cfg.HealthCheckInterval (default 30s). Call Stop() to gracefully shutdown
// when done.
//
// Example:
//
//	manager := grouping.NewStorageManager(grouping.StorageManagerConfig{
//		Primary: redisStorage, Fallback: memoryStorage, Logger: logger, Metrics: metrics,
//	})
//	defer manager.Stop()
//
// TN-125: Group Storage (Redis Backend)
// Date: 2025-11-04
func NewStorageManager(cfg StorageManagerConfig) *StorageManager {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	interval := cfg.HealthCheckInterval
	if interval <= 0 {
		interval = defaultStorageManagerHealthCheckInterval
	}

	primaryLabel := cfg.PrimaryLabel
	if primaryLabel == "" {
		primaryLabel = "redis"
	}
	fallbackLabel := cfg.FallbackLabel
	if fallbackLabel == "" {
		fallbackLabel = "memory"
	}

	degradeThreshold := cfg.DegradeThreshold
	if degradeThreshold <= 0 {
		degradeThreshold = defaultDegradeThreshold
	}
	recoverThreshold := cfg.RecoverThreshold
	if recoverThreshold <= 0 {
		recoverThreshold = defaultRecoverThreshold
	}
	minHoldDuration := cfg.MinHoldDuration
	if minHoldDuration <= 0 {
		minHoldDuration = defaultMinHoldDuration
	}
	reconcileTimeout := cfg.ReconcileTimeout
	if reconcileTimeout <= 0 {
		reconcileTimeout = defaultReconcileTimeout
	}

	sm := &StorageManager{
		primary:             cfg.Primary,
		fallback:            cfg.Fallback,
		current:             cfg.Primary, // Start with primary (Redis)
		primaryLabel:        primaryLabel,
		fallbackLabel:       fallbackLabel,
		logger:              logger,
		metrics:             cfg.Metrics,
		healthCheckInterval: interval,
		degradeThreshold:    degradeThreshold,
		recoverThreshold:    recoverThreshold,
		minHoldDuration:     minHoldDuration,
		reconcileTimeout:    reconcileTimeout,
		lastTransitionAt:    time.Now(),
		stopChan:            make(chan struct{}),
	}

	if sm.metrics != nil {
		sm.metrics.SetActiveGroupStorageBackend(primaryLabel)
	}

	// Start health check poller
	sm.startHealthCheck()

	logger.Info("Initialized storage manager with automatic fallback/recovery",
		"initial_storage", primaryLabel,
		"health_check_interval", interval,
		"degrade_threshold", degradeThreshold,
		"recover_threshold", recoverThreshold,
		"min_hold_duration", minHoldDuration,
	)

	return sm
}

// startHealthCheck polls primary storage health every healthCheckInterval
// and switches storage accordingly.
//
// Goroutine lifecycle:
//   - Starts immediately in NewStorageManager
//   - Polls every healthCheckInterval
//   - Stops when Stop() is called
func (sm *StorageManager) startHealthCheck() {
	sm.healthTicker = time.NewTicker(sm.healthCheckInterval)

	go func() {
		for {
			select {
			case <-sm.healthTicker.C:
				sm.checkHealthAndSwitch()
			case <-sm.stopChan:
				sm.logger.Debug("Health check goroutine stopped")
				return
			}
		}
	}()

	sm.logger.Debug("Started health check poller", "interval", sm.healthCheckInterval)
}

// checkHealthAndSwitch checks primary storage health and switches if the
// hysteresis in the package doc's "Hysteresis and error classification"
// section is satisfied. See "What recovery does (and does not) do" for what
// runs before a failforward.
func (sm *StorageManager) checkHealthAndSwitch() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pingErr := sm.primary.Ping(ctx)

	sm.mu.Lock()
	if pingErr != nil {
		sm.consecutiveFailures++
		sm.consecutiveSuccesses = 0

		if sm.current == sm.primary && sm.consecutiveFailures >= sm.degradeThreshold {
			sm.logger.Warn("Primary storage unhealthy for consecutive probes, switching to fallback",
				"error", pingErr, "backend", sm.fallbackLabel, "consecutive_failures", sm.consecutiveFailures)
			sm.current = sm.fallback
			sm.lastTransitionAt = time.Now()
			sm.consecutiveFailures = 0

			if sm.metrics != nil {
				sm.metrics.IncStorageFallback("health_check_failed")
				sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
			}
		}
		sm.mu.Unlock()
		return
	}

	sm.consecutiveSuccesses++
	sm.consecutiveFailures = 0

	readyToRecover := sm.current == sm.fallback &&
		sm.consecutiveSuccesses >= sm.recoverThreshold &&
		time.Since(sm.lastTransitionAt) >= sm.minHoldDuration
	reconcileTimeout := sm.reconcileTimeout
	sm.mu.Unlock()

	if !readyToRecover {
		return
	}

	// Reconciliation deliberately runs WITHOUT sm.mu held — see the package
	// doc's "What recovery does (and does not) do" for the race/blocking
	// trade-off this accepts.
	reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), reconcileTimeout)
	defer reconcileCancel()
	if reconcileErr := sm.reconcileFallbackIntoPrimary(reconcileCtx); reconcileErr != nil {
		sm.logger.Warn("Primary storage recovered but write-through reconciliation failed, staying on fallback",
			"error", reconcileErr)
		sm.mu.Lock()
		sm.consecutiveSuccesses = 0 // require the streak to rebuild before retrying
		sm.mu.Unlock()
		return
	}

	sm.mu.Lock()
	if sm.current == sm.fallback {
		sm.logger.Info("Primary storage recovered and reconciled, switching back from fallback",
			"backend", sm.primaryLabel, "consecutive_successes", sm.consecutiveSuccesses)
		sm.current = sm.primary
		sm.lastTransitionAt = time.Now()
		sm.consecutiveSuccesses = 0

		if sm.metrics != nil {
			sm.metrics.IncStorageRecovery()
			sm.metrics.SetActiveGroupStorageBackend(sm.primaryLabel)
		}
	}
	sm.mu.Unlock()
}

// reconcileFallbackIntoPrimary runs once, immediately before
// checkHealthAndSwitch flips sm.current back to primary. See the package
// doc's "What recovery does (and does not) do" and its two fix-round-2
// addenda (findings C-1 and I-5) for the full rationale; summary:
//
//  1. Deletion replay (best-effort, via replayDegradedDeletions) runs
//     FIRST (fix-round 2, finding I-5): every key recorded in
//     degradedDeletions is Deleted from primary, so a group removed during
//     the outage cannot reappear as a zombie. Running this before the
//     write-through means a key that was deleted and then re-created
//     during the SAME outage — degradedDeletions still names it, since
//     nothing removes an entry except a LATER Store of the same key (see
//     unrecordDegradedDeletion) — only ever gets a stale primary copy
//     deleted, never the fresh one write-through is about to add.
//  2. Write-through: every group present in the fallback store is Stored
//     into primary, so the fallback's (fresher, during the outage) copy —
//     not a stale pre-outage Redis copy — is what the next Load/Store sees.
//  3. Pruning (fix-round 2, finding C-1): every key just written through is
//     Deleted from the fallback, so the NEXT degraded window starts empty
//     instead of accumulating this outage's groups for the life of the
//     process — see the package doc for the two-outage data-loss sequence
//     this closes.
//
// Deliberately runs WITHOUT sm.mu held: holding it for the whole pass would
// block every Store/Load/Delete call for as long as reconciliation takes
// (proportional to how many groups accumulated during the outage), which
// trades the data-correctness risk this closes for a fresh availability
// risk. The accepted residual is a narrow race — a write landing in the
// fallback between this function's LoadAll snapshot and the caller's flip
// is not part of the write-through and can be shadowed by it — bounded by
// one reconciliation pass, not by the whole outage.
//
// Returns an error if the write-through phase itself fails (a Redis read/
// write failing here means Redis is not actually healthy enough to trust
// yet); the caller must NOT flip sm.current on error. A failure to prune
// the fallback after a successful write-through is logged, not returned:
// the data is already safely in primary at that point, so a stray leftover
// in the fallback is a smaller residual than aborting an otherwise-successful
// recovery over cleanup.
func (sm *StorageManager) reconcileFallbackIntoPrimary(ctx context.Context) error {
	sm.replayDegradedDeletions(ctx)

	groups, err := sm.fallback.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load fallback groups for write-through: %w", err)
	}

	written := make([]GroupKey, 0, len(groups))
	for _, group := range groups {
		if group == nil {
			continue
		}

		// fix-round 2, Minor #1: LoadAll returns the fallback's own live
		// pointers (MemoryGroupStorage.LoadAll's "Returns copies" doc
		// comment is wrong, pre-existing) — Clone() before mutating
		// Version so this never writes into an object the fallback store
		// itself might still be concurrently reading/writing; no lock is
		// held here. Clone() does not carry Version, so it is restored
		// explicitly before the alignment switch below.
		g := group.Clone()
		g.Version = group.Version

		existing, loadErr := sm.primary.Load(ctx, g.Key)
		var notFound *GroupNotFoundError
		switch {
		case loadErr == nil && existing != nil:
			// Align so RedisGroupStorage.Store's optimistic-lock check
			// (existing.Version != group.Version) does not reject this
			// write-through as a spurious "concurrent update" — the
			// fallback's copy was recreated from scratch in memory when
			// the outage started, so it legitimately has a different
			// (almost always lower) version than a pre-outage primary copy.
			g.Version = existing.Version
		case errors.As(loadErr, &notFound):
			// No pre-outage copy to align to — this group is brand new to
			// primary; g.Version stays whatever the fallback gave it.
		default:
			return fmt.Errorf("load existing primary group %s before write-through: %w", g.Key, loadErr)
		}

		if err := sm.primary.Store(ctx, g); err != nil {
			return fmt.Errorf("write-through store for %s: %w", g.Key, err)
		}
		written = append(written, g.Key)
	}

	// fix-round 2, finding C-1: prune everything just reconciled from the
	// fallback so it does not keep accumulating across outages.
	for _, key := range written {
		if err := sm.fallback.Delete(ctx, key); err != nil {
			sm.logger.Warn("Failed to prune reconciled group from fallback after write-through; it may resurface on a future outage",
				"group_key", key, "error", err)
		}
	}

	return nil
}

// replayDegradedDeletions is the best-effort half of
// reconcileFallbackIntoPrimary — see that function's doc comment. Always
// clears degradedDeletions, success or not: a delete that fails here either
// means the key was already gone (GroupNotFoundError, harmless) or Redis
// has a fresh problem that the next probe cycle's checkHealthAndSwitch will
// catch on its own Ping — retrying the same delete forever across every
// future recovery attempt would not fix a persistent failure and would
// grow this list unboundedly across repeated outages.
func (sm *StorageManager) replayDegradedDeletions(ctx context.Context) {
	sm.mu.Lock()
	deletions := sm.degradedDeletions
	sm.degradedDeletions = nil
	sm.mu.Unlock()

	for _, key := range deletions {
		if err := sm.primary.Delete(ctx, key); err != nil {
			var notFound *GroupNotFoundError
			if !errors.As(err, &notFound) {
				sm.logger.Warn("Failed to replay degraded-window deletion against recovered primary storage",
					"group_key", key, "error", err)
			}
		}
	}
}

// recordDegradedDeletion appends groupKey to degradedDeletions (bounded,
// FIFO — see that field's doc comment). Logs at Warn when the cap forces
// the oldest entry out, since a dropped entry is a deletion that will not
// be replayed on recovery — the exact zombie-group risk this list exists
// to close, reopened for whichever key gets evicted.
func (sm *StorageManager) recordDegradedDeletion(groupKey GroupKey) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.degradedDeletions) >= maxDegradedDeletions {
		dropped := sm.degradedDeletions[0]
		sm.degradedDeletions = sm.degradedDeletions[1:]
		sm.logger.Warn("degradedDeletions cap reached; dropping the oldest pending deletion replay — it will NOT be replayed on recovery",
			"dropped_group_key", dropped, "cap", maxDegradedDeletions)
	}
	sm.degradedDeletions = append(sm.degradedDeletions, groupKey)
}

// unrecordDegradedDeletion removes every occurrence of groupKey from
// degradedDeletions (fix-round 2, finding I-5): called on a later
// successful Store/StoreAll of the same key while degraded, so a group
// that was deleted and then re-created during the SAME outage does not
// leave a stale "still deleted" entry behind — reconcileFallbackIntoPrimary
// would otherwise have nothing wrong to replay for that key by the time it
// runs (since replay now runs before the write-through, the ordering fix
// alone already prevents the concrete data-loss case), but this keeps the
// list an accurate reflection of "genuinely absent as of the last write"
// rather than carrying dead entries for the rest of the outage.
func (sm *StorageManager) unrecordDegradedDeletion(groupKey GroupKey) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.degradedDeletions) == 0 {
		return
	}
	filtered := sm.degradedDeletions[:0]
	for _, k := range sm.degradedDeletions {
		if k != groupKey {
			filtered = append(filtered, k)
		}
	}
	sm.degradedDeletions = filtered
}

// unrecordDegradedDeletions is unrecordDegradedDeletion for StoreAll's
// batch of groups.
func (sm *StorageManager) unrecordDegradedDeletions(groups []*AlertGroup) {
	if len(groups) == 0 {
		return
	}

	keys := make(map[GroupKey]struct{}, len(groups))
	for _, g := range groups {
		if g != nil {
			keys[g.Key] = struct{}{}
		}
	}
	if len(keys) == 0 {
		return
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if len(sm.degradedDeletions) == 0 {
		return
	}
	filtered := sm.degradedDeletions[:0]
	for _, k := range sm.degradedDeletions {
		if _, deleted := keys[k]; !deleted {
			filtered = append(filtered, k)
		}
	}
	sm.degradedDeletions = filtered
}

// isConnectivityError classifies whether err indicates the primary storage
// is actually unreachable/broken (fix-round finding I-2), as opposed to a
// logical/data-level failure (an optimistic-locking conflict, a malformed
// payload) that says nothing about Redis's health. Only connectivity-class
// errors should trigger a per-call failback — degrading the whole group
// storage layer over, say, one ErrVersionMismatch (an expected outcome of
// two concurrent updates) would be a serious overreaction.
//
// ctx must be the SAME context the caller passed to Store/Delete/StoreAll
// (fix-round 2, finding I-6): a context.Canceled/DeadlineExceeded error
// only counts as a connectivity failure when ctx itself is still live at
// the time of the check. If ctx.Err() is already non-nil, the
// cancellation/deadline is the CALLER's — a request-scoped context from
// the alert-ingest path, for instance — not Redis's, and must not degrade
// the storage layer. Only a context.DeadlineExceeded surfacing while the
// caller's own context is still fine indicates some OTHER, Redis-call-
// scoped deadline fired, which is a genuine connectivity signal.
//
// Otherwise deliberately conservative: anything not positively recognized
// as connectivity-related returns false (do not degrade on this call
// alone). That is safe because the periodic probe (checkHealthAndSwitch)
// is the primary detection path and does not depend on this classification
// at all — a genuinely down Redis is still caught within one health-check
// interval even if a specific per-call error's wording is not recognized
// here.
func isConnectivityError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}

	var versionErr *ErrVersionMismatch
	if errors.As(err, &versionErr) {
		return false
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		if ctx.Err() != nil {
			// The caller's own context is done; this is the caller giving
			// up (or its deadline), not a Redis-side problem.
			return false
		}
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	msg := err.Error()
	for _, marker := range connectivityErrorMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}

	return false
}

// connectivityErrorMarkers are substrings of common go-redis/net-level
// failure messages this codebase has actually seen wrapped inside
// *StorageError (redis_group_storage.go wraps the raw redis client error
// with fmt.Errorf("redis transaction for %s: %w", ...)), used as a
// pragmatic fallback when the error isn't a typed net.Error/context error.
var connectivityErrorMarkers = []string{
	"connection refused",
	"no such host",
	"i/o timeout",
	"EOF",
	"broken pipe",
	"client is closed",
	"use of closed network connection",
	"connection reset by peer",
}

// Stop gracefully stops the health check goroutine.
//
// Thread-safe: Can be called multiple times safely.
//
// Example:
//
//	manager := grouping.NewStorageManager(...)
//	defer manager.Stop()
func (sm *StorageManager) Stop() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.stopped {
		return
	}

	// Stop ticker
	if sm.healthTicker != nil {
		sm.healthTicker.Stop()
	}

	// Signal goroutine to stop
	close(sm.stopChan)

	sm.stopped = true
	sm.logger.Info("Stopped storage manager")
}

// === Storage Interface Implementation with Automatic Fallback ===

// Store delegates to current storage with automatic fallback on error.
//
// Behavior:
//   - Try current storage (primary or fallback)
//   - On a connectivity-class error (fix-round finding I-2, ctx-aware per
//     fix-round 2 finding I-6) AND using primary: switch to fallback, retry
//   - Any other error against primary (e.g. ErrVersionMismatch), or any
//     error against fallback, is returned to the caller unchanged
//   - A successful write to the fallback un-registers groupKey from
//     degradedDeletions (fix-round 2, finding I-5): this key is no longer
//     "still deleted" once it has been re-written.
func (sm *StorageManager) Store(ctx context.Context, group *AlertGroup) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	err := storage.Store(ctx, group)
	if err == nil {
		if storage == sm.fallback {
			sm.unrecordDegradedDeletion(group.Key)
		}
		return nil
	}
	if storage != sm.primary || !isConnectivityError(ctx, err) {
		return err
	}

	sm.mu.Lock()
	if sm.current == sm.primary {
		sm.logger.Warn("Primary storage Store failed with a connectivity error, falling back to memory",
			"group_key", group.Key, "error", err)
		sm.current = sm.fallback
		sm.lastTransitionAt = time.Now()
		sm.consecutiveFailures = 0
		sm.consecutiveSuccesses = 0

		if sm.metrics != nil {
			sm.metrics.IncStorageFallback("store_error")
			sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
		}
	}
	sm.mu.Unlock()

	// Retry with fallback
	if err := sm.fallback.Store(ctx, group); err != nil {
		return err
	}
	sm.unrecordDegradedDeletion(group.Key)
	return nil
}

// Load delegates to current storage (no automatic fallback for reads).
//
// Rationale: Load failures typically indicate group doesn't exist (ErrNotFound),
// not storage failure. Fallback would return inconsistent data.
func (sm *StorageManager) Load(ctx context.Context, groupKey GroupKey) (*AlertGroup, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.Load(ctx, groupKey)
}

// Delete delegates to current storage with automatic fallback on a
// connectivity-class error (fix-round finding I-2). Every delete that
// actually reaches the fallback — whether because we were already degraded
// or because this call just degraded — is recorded via
// recordDegradedDeletion so a later recovery can replay it against primary
// (fix-round finding I-1's "zombie group" fix).
func (sm *StorageManager) Delete(ctx context.Context, groupKey GroupKey) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	err := storage.Delete(ctx, groupKey)
	if err == nil {
		if storage == sm.fallback {
			sm.recordDegradedDeletion(groupKey)
		}
		return nil
	}
	if storage != sm.primary || !isConnectivityError(ctx, err) {
		return err
	}

	sm.mu.Lock()
	if sm.current == sm.primary {
		sm.logger.Warn("Primary storage Delete failed with a connectivity error, falling back to memory",
			"group_key", groupKey, "error", err)
		sm.current = sm.fallback
		sm.lastTransitionAt = time.Now()
		sm.consecutiveFailures = 0
		sm.consecutiveSuccesses = 0

		if sm.metrics != nil {
			sm.metrics.IncStorageFallback("delete_error")
			sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
		}
	}
	sm.mu.Unlock()

	sm.recordDegradedDeletion(groupKey)

	// Retry with fallback
	return sm.fallback.Delete(ctx, groupKey)
}

// ListKeys delegates to current storage.
func (sm *StorageManager) ListKeys(ctx context.Context) ([]GroupKey, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.ListKeys(ctx)
}

// Size delegates to current storage.
func (sm *StorageManager) Size(ctx context.Context) (int, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.Size(ctx)
}

// LoadAll delegates to current storage.
//
// Important: Typically called on startup before manager is initialized,
// or when explicitly loading from primary storage for recovery.
func (sm *StorageManager) LoadAll(ctx context.Context) ([]*AlertGroup, error) {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.LoadAll(ctx)
}

// StoreAll delegates to current storage with automatic fallback on a
// connectivity-class error (fix-round finding I-2, ctx-aware per fix-round
// 2 finding I-6). A successful write to the fallback un-registers every
// group's key from degradedDeletions (fix-round 2, finding I-5) — see
// Store's doc comment.
func (sm *StorageManager) StoreAll(ctx context.Context, groups []*AlertGroup) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	err := storage.StoreAll(ctx, groups)
	if err == nil {
		if storage == sm.fallback {
			sm.unrecordDegradedDeletions(groups)
		}
		return nil
	}
	if storage != sm.primary || !isConnectivityError(ctx, err) {
		return err
	}

	sm.mu.Lock()
	if sm.current == sm.primary {
		sm.logger.Warn("Primary storage StoreAll failed with a connectivity error, falling back to memory",
			"count", len(groups), "error", err)
		sm.current = sm.fallback
		sm.lastTransitionAt = time.Now()
		sm.consecutiveFailures = 0
		sm.consecutiveSuccesses = 0

		if sm.metrics != nil {
			sm.metrics.IncStorageFallback("store_all_error")
			sm.metrics.SetActiveGroupStorageBackend(sm.fallbackLabel)
		}
	}
	sm.mu.Unlock()

	// Retry with fallback
	if err := sm.fallback.StoreAll(ctx, groups); err != nil {
		return err
	}
	sm.unrecordDegradedDeletions(groups)
	return nil
}

// Ping checks current storage health.
//
// Returns the health status of the currently active storage (primary or
// fallback) — while degraded, this reports the fallback's own Ping (always
// nil for MemoryGroupStorage), NOT primary's real liveness. Anything that
// needs the actual Redis liveness signal while possibly degraded should use
// the backend-active gauge (BusinessMetrics.SetActiveGroupStorageBackend)
// or GetCurrentStorage() below, not this method.
func (sm *StorageManager) Ping(ctx context.Context) error {
	sm.mu.RLock()
	storage := sm.current
	sm.mu.RUnlock()

	return storage.Ping(ctx)
}

// GetCurrentStorage returns which storage is currently active (for monitoring).
//
// Returns:
//   - "primary" if using RedisGroupStorage
//   - "fallback" if using MemoryGroupStorage
func (sm *StorageManager) GetCurrentStorage() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sm.current == sm.primary {
		return "primary"
	}
	return "fallback"
}
