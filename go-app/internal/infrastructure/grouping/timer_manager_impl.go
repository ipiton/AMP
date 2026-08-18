// Package grouping implements the default timer manager for alert groups.
//
// DefaultTimerManager manages the lifecycle of group timers, handling:
//   - Timer creation and cancellation
//   - Expiration detection and callback invocation
//   - Redis persistence for High Availability
//   - Distributed locking for exactly-once delivery
//   - Graceful shutdown and recovery
//
// Thread-safety: All public methods are thread-safe via sync.RWMutex.
//
// TN-124: Group Wait/Interval Timers
// Target Quality: 150%
// Date: 2025-11-03
package grouping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: deprecated pkg/metrics kept until v2 migration (v2 lacks BusinessMetrics)
)

// Reconciliation (orphan-adoption) tuning constants. See
// timerTTLGracePeriod in redis_timer_storage.go for the invariant tying
// these to the storage TTL, and the compile-time check that enforces it.
const (
	// defaultReconciliationGracePeriod is how far past a timer's ExpiresAt
	// reconcileOrphanedTimers waits before treating it as orphaned rather
	// than "still being processed by its owning replica". Used as the
	// fallback when grouping.reconciliation_grace is unset but the loop is
	// enabled; keep it in sync with config.setDefaults.
	//
	// MUST stay well below timerTTLGracePeriod: the difference between the
	// two IS the adoption window (final review finding 2).
	defaultReconciliationGracePeriod = 20 * time.Second

	// defaultReconciliationInterval mirrors config.setDefaults'
	// grouping.reconciliation_interval default. Referenced here only by the
	// compile-time adoption-window invariant, which requires the window to
	// fit several reconciliation ticks so one missed tick cannot lose a
	// group.
	defaultReconciliationInterval = 45 * time.Second
)

// DefaultTimerManager implements GroupTimerManager using Go timers + Redis persistence.
//
// Architecture:
//   - In-memory map of active timers (groupKey → timerHandle)
//   - Each timer runs in a separate goroutine
//   - Redis storage for persistence across restarts
//   - Distributed locks for exactly-once callback execution
//
// Lifecycle:
//  1. StartTimer → save to Redis → start Go timer → goroutine waits
//  2. Timer expires → acquire lock → invoke callbacks → cleanup
//  3. CancelTimer → stop Go timer → delete from Redis
//  4. Shutdown → cancel all timers → wait for goroutines
type DefaultTimerManager struct {
	// Storage layer (Redis or in-memory)
	storage TimerStorage

	// Active timers map: groupKey → timer handle
	// Protected by timersMu for thread-safety
	timers   map[GroupKey]*timerHandle
	timersMu sync.RWMutex

	// Registered callbacks for timer expiration
	// Protected by callbacksMu
	callbacks   []TimerCallback
	callbacksMu sync.RWMutex

	// Group manager for retrieving group snapshots.
	//
	// May be nil after construction — see SetGroupManager (Task 2.2,
	// alertmanager-parity), which breaks the GroupManager<->TimerManager
	// construction cycle: TimerManager must exist before GroupManager can be
	// built with it injected, but TimerManager needs a GroupManager reference
	// for onTimerExpired's group snapshot lookup. Protected by groupManagerMu
	// since it is written once after construction but read concurrently by
	// timer-expiration goroutines.
	groupManager   *DefaultGroupManager
	groupManagerMu sync.RWMutex

	// Configuration
	config *TimerManagerConfig

	// Observability
	logger  *slog.Logger
	metrics *metrics.BusinessMetrics

	// Statistics (in-memory, for GetStats)
	stats   *timerStats
	statsMu sync.RWMutex

	// Lifecycle management
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	shutdown   bool
	shutdownMu sync.RWMutex

	// Instance ID for distributed debugging
	instanceID string

	// Reconciliation loop settings (task 6.2). reconciliationInterval <= 0
	// means the loop is disabled — see TimerManagerConfig.ReconciliationInterval.
	reconciliationInterval time.Duration
	reconciliationGrace    time.Duration
}

// timerHandle represents an active timer's runtime state.
type timerHandle struct {
	// Go standard library timer
	timer *time.Timer

	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc

	// Timer metadata
	groupKey  GroupKey
	timerType TimerType
	expiresAt time.Time
}

// timerStats tracks operation statistics.
type timerStats struct {
	totalStarted    int64
	totalExpired    int64
	totalCancelled  int64
	totalReset      int64
	totalMissed     int64
	totalReconciled int64

	// Duration tracking for average calculation
	durationSum   map[TimerType]time.Duration
	durationCount map[TimerType]int64
}

// TimerManagerConfig configures DefaultTimerManager.
type TimerManagerConfig struct {
	// Storage implementation (Redis or in-memory)
	Storage TimerStorage

	// GroupManager for retrieving alert group snapshots.
	//
	// Optional at construction time (Task 2.2, alertmanager-parity): pass nil
	// here and call SetGroupManager once the GroupManager has been built with
	// this TimerManager injected into DefaultGroupManagerConfig.TimerManager.
	// This breaks the construction-time cycle between the two managers. It
	// MUST be set (via this field or SetGroupManager) before any timer can
	// expire — onTimerExpired logs an error and skips callback dispatch if it
	// is still nil when a timer fires.
	GroupManager *DefaultGroupManager

	// Performance tuning
	MaxConcurrentTimers int // Maximum active timers (default: 10000)

	// ReconciliationInterval enables the periodic orphan-adoption loop (task
	// 6.2, distributed timer liveness) when positive: every interval, this
	// replica scans Storage.ListTimers for group timers whose ExpiresAt has
	// passed by more than ReconciliationGrace and adopts them via the same
	// onTimerExpired path RestoreTimers uses for timers missed at startup —
	// acquiring Storage.AcquireLock first, so a timer still being processed
	// by its rightful owner (lock held, not actually orphaned yet) is left
	// alone. This is what lets a SURVIVING replica take over a group's
	// firing after the replica that started its timer crashes mid-interval;
	// RestoreTimers alone cannot help here because it only runs once, at
	// each replica's OWN startup, not the crashed replica's.
	//
	// 0 (the default) disables the loop entirely — correct for the lite
	// profile's InMemoryTimerStorage, which is never shared across replicas
	// (there is only one replica by definition, so "orphaned" is
	// meaningless), and is what ServiceRegistry leaves this field at unless
	// the standard profile is running with a live Redis-backed
	// TimerStorage.
	ReconciliationInterval time.Duration

	// ReconciliationGrace is how far past ExpiresAt a timer must be before
	// the reconciliation loop treats it as orphaned rather than "still being
	// processed by its owning replica right now." Fire processing (lock
	// acquire, group load, notify-chain, storage delete) normally completes
	// in well under a second — see onTimerExpired — so this only needs to
	// absorb scheduling jitter and Redis latency, not notification delivery
	// time (PublishGroup only enqueues, per notifyLogClaimTTL's doc
	// comment in manager_impl.go). Defaults to 60s (timerTTLGracePeriod)
	// when ReconciliationInterval is positive and this is left at its
	// zero-value default.
	ReconciliationGrace time.Duration

	// Observability
	Logger  *slog.Logger
	Metrics *metrics.BusinessMetrics
}

// NewDefaultTimerManager creates a new timer manager.
//
// Parameters:
//   - config: Manager configuration
//
// Returns:
//   - *DefaultTimerManager: Configured manager
//   - error: If validation fails
//
// Example:
//
//	manager, err := NewDefaultTimerManager(TimerManagerConfig{
//	    Storage:      redisStorage,
//	    GroupManager: groupManager,
//	    Logger:       slog.Default(),
//	    Metrics:      businessMetrics,
//	})
func NewDefaultTimerManager(config TimerManagerConfig) (*DefaultTimerManager, error) {
	// Validation
	if config.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}
	// GroupManager is optional here (Task 2.2): the caller may construct the
	// TimerManager first and inject the GroupManager afterwards via
	// SetGroupManager, breaking the GroupManager<->TimerManager construction
	// cycle. A nil GroupManager at this point is not an error, only a
	// deferred wiring step; onTimerExpired guards against it.

	// Apply defaults
	if config.MaxConcurrentTimers == 0 {
		config.MaxConcurrentTimers = 10000
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	// Reconciliation loop (task 6.2): a grace period only makes sense once
	// the loop itself is enabled — an explicit ReconciliationGrace with
	// ReconciliationInterval left at 0 is still fully disabled, not "enabled
	// with a custom grace."
	if config.ReconciliationInterval > 0 && config.ReconciliationGrace <= 0 {
		// NOT timerTTLGracePeriod (final review finding 2): using the
		// storage TTL grace as the adoption grace made a timer eligible for
		// adoption at the exact moment its Redis key expired, so nothing
		// was ever adoptable.
		config.ReconciliationGrace = defaultReconciliationGracePeriod
	}

	// Create context for lifecycle management
	ctx, cancel := context.WithCancel(context.Background())

	// Generate instance ID
	instanceID := fmt.Sprintf("%s:%d", getHostname(), os.Getpid())

	manager := &DefaultTimerManager{
		storage:      config.Storage,
		timers:       make(map[GroupKey]*timerHandle),
		callbacks:    make([]TimerCallback, 0),
		groupManager: config.GroupManager,
		config:       &config,
		logger:       config.Logger,
		metrics:      config.Metrics,
		stats: &timerStats{
			durationSum:   make(map[TimerType]time.Duration),
			durationCount: make(map[TimerType]int64),
		},
		ctx:        ctx,
		cancel:     cancel,
		instanceID: instanceID,

		reconciliationInterval: config.ReconciliationInterval,
		reconciliationGrace:    config.ReconciliationGrace,
	}

	manager.logger.Info("Timer manager initialized",
		"instance_id", instanceID,
		"max_concurrent_timers", config.MaxConcurrentTimers,
		"reconciliation_interval", config.ReconciliationInterval,
		"reconciliation_grace", config.ReconciliationGrace)

	// Start the orphan-adoption loop (task 6.2) if enabled. Safe to start
	// before SetGroupManager/RestoreTimers run: onTimerExpired already
	// guards against a nil groupManager (logs and skips), and the first
	// tick only fires after reconciliationInterval elapses (time.Ticker,
	// not immediately), which construction callers reach well before that.
	if manager.reconciliationInterval > 0 {
		manager.wg.Add(1)
		go manager.reconciliationLoop()
	}

	return manager, nil
}

// SetGroupManager injects the GroupManager after construction, breaking the
// GroupManager<->TimerManager construction cycle (Task 2.2,
// alertmanager-parity): GroupManager's constructor accepts an already-built
// TimerManager (DefaultGroupManagerConfig.TimerManager, optional), but
// TimerManager needs a GroupManager reference for onTimerExpired's group
// snapshot lookup. Expected construction order:
//
//  1. timerManager, _  := NewDefaultTimerManager(TimerManagerConfig{...})       // GroupManager left nil
//  2. groupManager, _  := NewDefaultGroupManager(ctx, DefaultGroupManagerConfig{TimerManager: timerManager, ...})
//  3. _ = timerManager.SetGroupManager(groupManager)
//
// Must be called before RestoreTimers or any timer can expire; onTimerExpired
// logs an error and skips callback dispatch (without panicking) if a timer
// fires while groupManager is still nil.
//
// Thread-safe: safe to call concurrently with StartTimer/onTimerExpired.
func (tm *DefaultTimerManager) SetGroupManager(gm *DefaultGroupManager) error {
	if gm == nil {
		return fmt.Errorf("group manager cannot be nil")
	}
	tm.groupManagerMu.Lock()
	tm.groupManager = gm
	tm.groupManagerMu.Unlock()
	return nil
}

// StartTimer creates and starts a new timer for a group.
//
// If a timer already exists for the group, it is cancelled first.
//
// Algorithm:
//  1. Validate inputs
//  2. Check shutdown state
//  3. Cancel existing timer (if any)
//  4. Create GroupTimer struct
//  5. Save to Redis
//  6. Start Go timer
//  7. Create timer handle
//  8. Launch goroutine for expiration handling
//  9. Update metrics
//
// Performance target: <1ms (150%)
func (tm *DefaultTimerManager) StartTimer(
	ctx context.Context,
	groupKey GroupKey,
	timerType TimerType,
	duration time.Duration,
) (*GroupTimer, error) {
	startTime := time.Now()

	// Validation
	if err := timerType.Validate(); err != nil {
		return nil, err
	}
	if duration <= 0 {
		return nil, &InvalidDurationError{Duration: duration}
	}
	if groupKey == "" {
		return nil, fmt.Errorf("group key cannot be empty")
	}

	// Check shutdown state
	tm.shutdownMu.RLock()
	if tm.shutdown {
		tm.shutdownMu.RUnlock()
		return nil, ErrManagerShutdown
	}
	tm.shutdownMu.RUnlock()

	// Cancel existing timer if present
	tm.timersMu.Lock()
	if existing, ok := tm.timers[groupKey]; ok {
		existing.cancel()
		existing.timer.Stop()
		delete(tm.timers, groupKey)

		tm.logger.Debug("Cancelled existing timer for new timer",
			"group_key", groupKey,
			"old_type", existing.timerType,
			"new_type", timerType)
	}
	tm.timersMu.Unlock()

	// Create timer struct
	now := time.Now()
	timer := &GroupTimer{
		GroupKey:  groupKey,
		TimerType: timerType,
		Duration:  duration,
		StartedAt: now,
		ExpiresAt: now.Add(duration),
		State:     TimerStateActive,
		Metadata: &TimerMetadata{
			Version:    1,
			CreatedBy:  tm.instanceID,
			ResetCount: 0,
		},
	}

	// Save to storage (Redis)
	if err := tm.storage.SaveTimer(ctx, timer); err != nil {
		tm.logger.Error("Failed to save timer to storage",
			"group_key", groupKey,
			"timer_type", timerType,
			"error", err)
		return nil, err
	}

	// Start Go timer
	timerCtx, cancelFunc := context.WithCancel(tm.ctx)
	goTimer := time.NewTimer(duration)

	handle := &timerHandle{
		timer:     goTimer,
		ctx:       timerCtx,
		cancel:    cancelFunc,
		groupKey:  groupKey,
		timerType: timerType,
		expiresAt: timer.ExpiresAt,
	}

	// Register handle
	tm.timersMu.Lock()
	tm.timers[groupKey] = handle
	tm.timersMu.Unlock()

	// Launch goroutine for expiration handling
	tm.wg.Add(1)
	go tm.handleTimerExpiration(handle, timer)

	// Update statistics
	tm.statsMu.Lock()
	tm.stats.totalStarted++
	tm.stats.durationSum[timerType] += duration
	tm.stats.durationCount[timerType]++
	tm.statsMu.Unlock()

	// Update metrics
	if tm.metrics != nil {
		tm.metrics.RecordTimerStarted(timerType.String())
		tm.metrics.IncActiveTimers()
		tm.metrics.RecordTimerDuration(timerType.String(), float64(duration))
		tm.metrics.RecordTimerOperationDuration("start", float64(time.Since(startTime)))
	}

	tm.logger.Info("Started timer",
		"group_key", groupKey,
		"timer_type", timerType,
		"duration", duration,
		"expires_at", timer.ExpiresAt,
		"latency", float64(time.Since(startTime)))

	return timer, nil
}

// CancelTimer stops and removes an active timer.
//
// If no timer exists, returns (false, nil).
//
// Algorithm:
//  1. Lock timers map
//  2. Find timer handle
//  3. Cancel context
//  4. Stop Go timer
//  5. Remove from map
//  6. Delete from Redis
//  7. Update metrics
//
// Performance target: <500µs (150%)
func (tm *DefaultTimerManager) CancelTimer(ctx context.Context, groupKey GroupKey) (bool, error) {
	startTime := time.Now()

	// Find and remove timer
	tm.timersMu.Lock()
	handle, exists := tm.timers[groupKey]
	if !exists {
		tm.timersMu.Unlock()
		return false, nil
	}

	// Cancel context and stop timer
	handle.cancel()
	handle.timer.Stop()
	delete(tm.timers, groupKey)
	tm.timersMu.Unlock()

	// Delete from storage
	if err := tm.storage.DeleteTimer(ctx, groupKey); err != nil {
		tm.logger.Warn("Failed to delete timer from storage",
			"group_key", groupKey,
			"error", err)
		// Continue - in-memory timer already cancelled
	}

	// Update statistics
	tm.statsMu.Lock()
	tm.stats.totalCancelled++
	tm.statsMu.Unlock()

	// Update metrics
	if tm.metrics != nil {
		tm.metrics.RecordTimerCancelled(handle.timerType.String())
		tm.metrics.DecActiveTimers()
		tm.metrics.RecordTimerOperationDuration("cancel", float64(time.Since(startTime)))
	}

	tm.logger.Info("Cancelled timer",
		"group_key", groupKey,
		"timer_type", handle.timerType,
		"latency", float64(time.Since(startTime)))

	return true, nil
}

// ResetTimer cancels the existing timer and starts a new one atomically.
//
// Algorithm:
//  1. CancelTimer (if exists)
//  2. StartTimer with new parameters
//  3. Increment reset count in metadata
//
// Performance target: <2ms (150%)
func (tm *DefaultTimerManager) ResetTimer(
	ctx context.Context,
	groupKey GroupKey,
	timerType TimerType,
	duration time.Duration,
) (*GroupTimer, error) {
	startTime := time.Now()

	// Load existing timer for reset count
	var resetCount int
	existingTimer, err := tm.storage.LoadTimer(ctx, groupKey)
	if err == nil && existingTimer != nil && existingTimer.Metadata != nil {
		resetCount = existingTimer.Metadata.ResetCount + 1
	}

	// Cancel existing timer
	cancelled, err := tm.CancelTimer(ctx, groupKey)
	if err != nil {
		return nil, err
	}

	if !cancelled {
		return nil, &TimerNotFoundError{GroupKey: groupKey}
	}

	// Start new timer
	timer, err := tm.StartTimer(ctx, groupKey, timerType, duration)
	if err != nil {
		return nil, err
	}

	// Update metadata with reset count
	if timer.Metadata != nil {
		timer.Metadata.ResetCount = resetCount
		now := time.Now()
		timer.Metadata.LastResetAt = &now

		// Save updated metadata
		if err := tm.storage.SaveTimer(ctx, timer); err != nil {
			tm.logger.Warn("Failed to update timer metadata after reset",
				"group_key", groupKey,
				"error", err)
		}
	}

	// Update statistics
	tm.statsMu.Lock()
	tm.stats.totalReset++
	tm.statsMu.Unlock()

	// Update metrics
	if tm.metrics != nil {
		tm.metrics.RecordTimerReset(timerType.String())
		tm.metrics.RecordTimerOperationDuration("reset", float64(time.Since(startTime)))
	}

	tm.logger.Info("Reset timer",
		"group_key", groupKey,
		"timer_type", timerType,
		"reset_count", resetCount,
		"latency", float64(time.Since(startTime)))

	return timer, nil
}

// GetTimer retrieves information about a timer.
//
// Returns a copy to prevent external mutation.
//
// Performance target: <1ms (150%)
func (tm *DefaultTimerManager) GetTimer(ctx context.Context, groupKey GroupKey) (*GroupTimer, error) {
	// Try in-memory first (fast path)
	tm.timersMu.RLock()
	_, exists := tm.timers[groupKey]
	tm.timersMu.RUnlock()

	if !exists {
		return nil, ErrTimerNotFound
	}

	// Load from storage for full data
	timer, err := tm.storage.LoadTimer(ctx, groupKey)
	if err != nil {
		return nil, err
	}

	return timer, nil
}

// ListActiveTimers returns all active timers matching filters.
//
// Performance target: <10ms for 1000 timers (150%)
func (tm *DefaultTimerManager) ListActiveTimers(ctx context.Context, filters *TimerFilters) ([]*GroupTimer, error) {
	// Load all timers from storage
	timers, err := tm.storage.ListTimers(ctx)
	if err != nil {
		return nil, err
	}

	// Apply filters if provided
	if filters == nil {
		return timers, nil
	}

	filtered := make([]*GroupTimer, 0, len(timers))
	for _, timer := range timers {
		if filters.Matches(timer) {
			filtered = append(filtered, timer)
		}
	}

	// Apply pagination
	start := filters.Offset
	end := len(filtered)

	if start >= end {
		return []*GroupTimer{}, nil
	}

	if filters.Limit > 0 && start+filters.Limit < end {
		end = start + filters.Limit
	}

	return filtered[start:end], nil
}

// OnTimerExpired registers a callback for timer expiration.
//
// Thread-safe: Can be called from multiple goroutines.
func (tm *DefaultTimerManager) OnTimerExpired(callback TimerCallback) {
	tm.callbacksMu.Lock()
	defer tm.callbacksMu.Unlock()

	tm.callbacks = append(tm.callbacks, callback)

	tm.logger.Debug("Registered timer expiration callback",
		"callback_count", len(tm.callbacks))
}

// handleTimerExpiration waits for timer to expire and invokes callbacks.
//
// Runs in a separate goroutine per timer.
func (tm *DefaultTimerManager) handleTimerExpiration(handle *timerHandle, timer *GroupTimer) {
	defer tm.wg.Done()

	select {
	case <-handle.timer.C:
		// Timer expired naturally. Deliberately NOT passing handle.ctx into
		// onTimerExpired (P0 fix, task 6.2 fix round 2) — see that method's
		// doc comment for why: onTimerExpired's own work must outlive THIS
		// handle, which StartTimer is about to cancel as part of the very
		// continuation this fire triggers. handle itself IS passed, purely
		// as an identity token — see onTimerExpired's doc comment for the
		// second half of this same fix round.
		tm.onTimerExpired(handle, handle.groupKey, handle.timerType)

	case <-handle.ctx.Done():
		// Timer cancelled (manual cancel or shutdown)
		tm.logger.Debug("Timer goroutine cancelled",
			"group_key", handle.groupKey,
			"timer_type", handle.timerType,
			"reason", handle.ctx.Err())
	}
}

// onTimerExpired handles timer expiration with distributed lock.
//
// P0 fix (task 6.2 fix round 2): every internal operation here is bounded
// by a context derived from tm.ctx (the manager's own lifetime context,
// only cancelled by Shutdown), never by a caller-supplied context tied to
// the specific timerHandle that triggered this fire. This method used to
// take a ctx parameter — handleTimerExpiration passed handle.ctx, the
// context of the JUST-FIRED timer — which broke every group_wait->
// group_interval and group_interval->repeat_interval continuation:
//
//  1. onTimerExpired(handle.ctx, ...) invokes the registered TimerCallback
//     (e.g. onGroupWaitExpired) with a callbackCtx derived from handle.ctx.
//  2. That callback calls startGroupIntervalTimer -> StartTimer for the
//     SAME groupKey, still carrying callbackCtx.
//  3. StartTimer finds the existing (still-registered — it isn't removed
//     from tm.timers until AFTER the callback loop below returns) handle
//     for this group and calls existing.cancel() — which is exactly the
//     handle whose ctx is callbackCtx's ancestor. That cancels callbackCtx
//     out from under the very call using it.
//  4. StartTimer's SaveTimer(callbackCtx, ...) receives an already-
//     cancelled context and fails with "context canceled" — StartTimer
//     returns an error BEFORE creating the new Go timer or registering its
//     handle. The continuation timer is silently never created.
//
// Result: the very first notification for a group always went out (it
// only depends on group_wait firing, no continuation involved), but no
// group ever got a second one — group_interval/repeat_interval timers
// were never scheduled. Rooting everything here in tm.ctx instead breaks
// that self-cancellation: a StartTimer call cancelling some OTHER
// (expiring) handle can never cancel tm.ctx itself, so this fire's own
// work — lock, group load, callbacks (including any StartTimer they
// trigger), final delete — proceeds independently of whichever handle
// happened to trigger it. Shutdown still stops everything, because every
// timerHandle's ctx AND every bounded context created below are both
// ultimately children of tm.ctx.
//
// firedHandle is the timerHandle whose Go timer just fired (nil when
// there is no such handle: RestoreTimers' startup "missed timer" branch
// and the reconciliation loop's orphan adoption both call this for a
// timer with no LOCAL handle at all — that absence is exactly what makes
// it "missed"/"orphaned"). It exists purely as an identity token for the
// second half of this same fix round: fixing the context-cancellation
// self-cancel above (see above) surfaced a SECOND bug that it had been
// silently masking. Once StartTimer for a continuation (e.g.
// group_interval, started by the TimerCallback invoked below) started
// actually succeeding, the unconditional cleanup this method runs AFTER
// the callback loop —
//
//	delete(tm.timers, groupKey)
//	tm.storage.DeleteTimer(ctx, groupKey)
//
// — deleted that BRAND NEW continuation from both tm.timers and storage
// milliseconds after StartTimer created it, because TimerStorage keys
// entries by GroupKey alone (not GroupKey+TimerType): the continuation's
// SaveTimer and this cleanup's DeleteTimer target the exact same storage
// key. Before the context-cancellation fix this was unreachable (the
// continuation's StartTimer always failed first, so there was nothing yet
// to accidentally delete); fixing that alone would have replaced a hard
// failure with an equally silent one. The cleanup below now compares
// tm.timers[groupKey] against firedHandle by pointer identity: if a
// callback installed a DIFFERENT handle for this groupKey while it ran
// (the continuation case), that handle owns this groupKey now and the
// cleanup skips it entirely — both the delete and the DeleteTimer call.
// If nothing replaced it (no callback restarted a timer — e.g. the group
// was empty, or this is the terminal branch of some future timer type),
// the old entry is still stale and must be deleted exactly as before.
// dropLocalHandle removes groupKey's entry from tm.timers, but only if it is
// still the handle that just fired. Used by onTimerExpired's early-return
// paths (final review finding 3).
//
// WHY: a timerHandle in tm.timers means "this replica has a live Go timer for
// this group". Once onTimerExpired has been entered for a handle, that Go
// timer has already fired and will never fire again — so any early return that
// leaves the handle behind creates a permanently dead entry. That dead entry is
// not merely garbage: reconcileOrphanedTimers treats tm.timers membership
// (trackedLocally) as proof that this replica will handle the group itself, so
// it skips it forever, and AddAlertToGroup only arms group_wait for BRAND NEW
// groups. Net effect before this fix: three distinct early returns (lock held
// elsewhere, lock-store error, transient group-load error) each silently
// wedged a group into never notifying again.
//
// The shared storage entry is deliberately NOT touched: it is exactly what
// lets this replica's reconciliation loop (or another replica's) pick the
// group up again on a later tick.
//
// The identity guard mirrors the continuation-takeover logic at the end of
// onTimerExpired: if some callback installed a DIFFERENT handle for this
// groupKey (a continuation timer), that handle is live and must survive.
// firedHandle == nil means there was no local handle to begin with (the
// RestoreTimers "missed timer" branch and reconciliation's orphan adoption
// both call onTimerExpired with nil), so there is nothing to drop.
func (tm *DefaultTimerManager) dropLocalHandle(firedHandle *timerHandle, groupKey GroupKey) {
	if firedHandle == nil {
		return
	}

	tm.timersMu.Lock()
	defer tm.timersMu.Unlock()

	if current, ok := tm.timers[groupKey]; ok && current == firedHandle {
		delete(tm.timers, groupKey)
	}
}

func (tm *DefaultTimerManager) onTimerExpired(firedHandle *timerHandle, groupKey GroupKey, timerType TimerType) {
	tm.logger.Info("Timer expired",
		"group_key", groupKey,
		"timer_type", timerType)

	// Acquire distributed lock for exactly-once delivery
	lockCtx, lockCancel := context.WithTimeout(tm.ctx, 5*time.Second)
	defer lockCancel()

	lockID, release, err := tm.storage.AcquireLock(lockCtx, groupKey, lockTTL)
	if err != nil {
		if err == ErrLockAlreadyAcquired {
			tm.logger.Debug("Lock already acquired by another instance",
				"group_key", groupKey)
			// Another instance owns this fire. Drop OUR dead handle (final
			// review finding 3) — see dropLocalHandle. Keeping it would make
			// reconcileOrphanedTimers skip this group forever if the other
			// replica then dies mid-fire.
			tm.dropLocalHandle(firedHandle, groupKey)
			return
		}
		tm.logger.Error("Failed to acquire lock",
			"group_key", groupKey,
			"error", err)
		// Transient lock-store failure: this fire is lost, so the local
		// handle is dead too. Drop it so reconciliation can retry from
		// shared storage (finding 3).
		tm.dropLocalHandle(firedHandle, groupKey)
		return
	}
	defer func() {
		if err := release(); err != nil {
			tm.logger.Warn("Failed to release lock",
				"group_key", groupKey,
				"lock_id", lockID,
				"error", err)
		}
	}()

	// Get group snapshot
	groupCtx, groupCancel := context.WithTimeout(tm.ctx, 5*time.Second)
	defer groupCancel()

	tm.groupManagerMu.RLock()
	gm := tm.groupManager
	tm.groupManagerMu.RUnlock()

	if gm == nil {
		// SetGroupManager (Task 2.2) was never called — the manager was
		// either constructed with a nil GroupManager and never wired, or a
		// timer fired before initialization completed. Log and skip rather
		// than panic; the timer is still removed from active state below.
		tm.logger.Error("Timer expired but no group manager is configured, skipping callback dispatch",
			"group_key", groupKey,
			"timer_type", timerType)
		tm.timersMu.Lock()
		delete(tm.timers, groupKey)
		tm.timersMu.Unlock()
		return
	}

	group, err := gm.GetGroup(groupCtx, groupKey)
	if err != nil {
		var notFoundErr *GroupNotFoundError
		if errors.As(err, &notFoundErr) {
			// fix round 1, Finding 2: the group is CONFIRMED gone (not a
			// transient storage error), most likely deleted by
			// RemoveAlertFromGroup/CleanupExpiredGroups after this timer
			// was scheduled but before it fired. Previously this returned
			// here without deleting the timer from storage or tm.timers,
			// which was harmless for a normal one-shot fire (the timer's
			// own Redis TTL eventually reaps it) but became a repeating
			// Error log every reconciliation tick — reconcileOrphanedTimers
			// keeps re-adopting the same still-overdue-in-storage entry
			// until that TTL expires. Clean it up now, once, at Warn
			// (expected condition, not a failure) instead.
			tm.logger.Warn("group no longer exists for timer expiration, removing leftover timer",
				"group_key", groupKey,
				"timer_type", timerType)

			tm.timersMu.Lock()
			delete(tm.timers, groupKey)
			tm.timersMu.Unlock()

			deleteCtx, deleteCancel := context.WithTimeout(tm.ctx, 5*time.Second)
			if delErr := tm.storage.DeleteTimer(deleteCtx, groupKey); delErr != nil {
				tm.logger.Warn("failed to delete leftover timer for a confirmed-deleted group",
					"group_key", groupKey,
					"error", delErr)
			}
			deleteCancel()
			return
		}

		// Any other error (Redis timeout, network blip, etc.) is transient
		// — the group may well still exist, just unreachable right now.
		// Deliberately NOT deleting the timer from STORAGE here: doing so on
		// a transient error would drop a live group's timer entirely.
		//
		// The LOCAL handle is a different matter (final review finding 3):
		// this fire is over and its Go timer will never fire again, so
		// leaving the handle in tm.timers only makes trackedLocally() lie to
		// reconcileOrphanedTimers and wedge the group permanently. Drop it
		// and let reconciliation retry against the surviving storage entry.
		tm.logger.Error("Failed to get group for timer expiration",
			"group_key", groupKey,
			"error", err)
		tm.dropLocalHandle(firedHandle, groupKey)
		return
	}

	// Invoke callbacks
	tm.callbacksMu.RLock()
	callbacks := make([]TimerCallback, len(tm.callbacks))
	copy(callbacks, tm.callbacks)
	tm.callbacksMu.RUnlock()

	for i, callback := range callbacks {
		callbackCtx, callbackCancel := context.WithTimeout(tm.ctx, 30*time.Second)
		tm.invokeCallbackSafely(callbackCtx, callback, i, groupKey, timerType, group)
		callbackCancel()
	}

	// Remove from active timers — but ONLY if nothing replaced firedHandle
	// for this groupKey while the callbacks above ran (P0 fix, task 6.2 fix
	// round 2 — see this method's doc comment). A callback that started a
	// continuation (e.g. onGroupWaitExpired -> startGroupIntervalTimer)
	// already installed a NEW handle at tm.timers[groupKey] and a NEW
	// storage entry under the same key; deleting either here would erase
	// that continuation seconds after creating it.
	tm.timersMu.Lock()
	currentHandle, stillPresent := tm.timers[groupKey]
	continuationTookOver := stillPresent && currentHandle != firedHandle
	if !continuationTookOver {
		delete(tm.timers, groupKey)
	}
	tm.timersMu.Unlock()

	if continuationTookOver {
		tm.logger.Debug("timer continuation replaced the fired handle during callback dispatch, skipping stale cleanup",
			"group_key", groupKey,
			"timer_type", timerType)
	} else {
		// Delete from storage
		deleteCtx, deleteCancel := context.WithTimeout(tm.ctx, 5*time.Second)
		if err := tm.storage.DeleteTimer(deleteCtx, groupKey); err != nil {
			tm.logger.Warn("Failed to delete expired timer from storage",
				"group_key", groupKey,
				"error", err)
		}
		deleteCancel()
	}

	// Update statistics
	tm.statsMu.Lock()
	tm.stats.totalExpired++
	tm.statsMu.Unlock()

	// Update metrics
	if tm.metrics != nil {
		tm.metrics.RecordTimerExpired(timerType.String())
		tm.metrics.DecActiveTimers()
	}

	tm.logger.Info("Timer expiration processed",
		"group_key", groupKey,
		"timer_type", timerType,
		"lock_id", lockID)
}

// invokeCallbackSafely runs a single timer-expiration callback with panic
// recovery (task 2.4 fix round 1, Finding 3).
//
// handleTimerExpiration/onTimerExpired run in their own unsupervised
// background goroutine (one per timer) — there is no request-scoped
// recover() anywhere above this call. Before task 2.4, registered
// callbacks only ever logged; the notify-stage chain wired in by task 2.4
// (Inhibit/Silence/Dedup + publish) does real work — network-adjacent
// queue submission, a user-injected InhibitionChecker/SilenceChecker — and
// a panic anywhere in that chain would otherwise crash the whole process.
// One recover() here covers every registered TimerCallback, not just the
// grouping package's own.
//
// A panicking callback is logged with its stack and otherwise treated like
// a callback that returned an error: the loop in onTimerExpired continues
// to the next callback, and this timer is still removed from active state
// below as usual (so a permanently-panicking callback can't wedge a group
// forever — timer_wait/interval/repeat_interval get rescheduled from
// scratch next time an alert lands in this group, same as after a normal
// error return).
func (tm *DefaultTimerManager) invokeCallbackSafely(
	ctx context.Context,
	callback TimerCallback,
	index int,
	groupKey GroupKey,
	timerType TimerType,
	group *AlertGroup,
) {
	defer func() {
		if r := recover(); r != nil {
			tm.logger.Error("Timer callback panicked",
				"group_key", groupKey,
				"timer_type", timerType,
				"callback_index", index,
				"panic", r,
				"stack", string(debug.Stack()))
		}
	}()

	if err := callback(ctx, groupKey, timerType, group); err != nil {
		tm.logger.Error("Timer callback failed",
			"group_key", groupKey,
			"timer_type", timerType,
			"callback_index", index,
			"error", err)
	}
}

// RestoreTimers recovers timers from storage after restart.
//
// Algorithm:
//  1. Load all timers from storage
//  2. Separate into expired and active
//  3. Trigger callbacks for expired timers
//  4. Restore active timers with remaining duration
//
// Performance target: <100ms for 1000 timers (150%)
func (tm *DefaultTimerManager) RestoreTimers(ctx context.Context) (restored int, missed int, err error) {
	tm.logger.Info("Starting timer restoration from storage")
	startTime := time.Now()

	// Load all timers from storage
	timers, err := tm.storage.ListTimers(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list timers: %w", err)
	}

	now := time.Now()

	for _, timer := range timers {
		if timer.ExpiresAt.Before(now) {
			// Timer expired during downtime - trigger immediately
			tm.logger.Warn("Found missed timer",
				"group_key", timer.GroupKey,
				"timer_type", timer.TimerType,
				"should_have_expired_at", timer.ExpiresAt,
				"delay", now.Sub(timer.ExpiresAt))

			timer.State = TimerStateMissed
			// Not passing the RestoreTimers ctx parameter here (P0 fix, task
			// 6.2 fix round 2): this goroutine is dispatched via `go` and can
			// still be running after RestoreTimers itself returns, at which
			// point ServiceRegistry.initializeGrouping calls the matching
			// cancel() for its bounded restoreCtx — which would otherwise
			// cancel this fire's own work out from under it, same failure
			// shape onTimerExpired's own doc comment describes. onTimerExpired
			// roots its internal work in tm.ctx unconditionally now, so there
			// is nothing caller-scoped left to pass.
			// firedHandle is nil: there is no local timerHandle for a timer
			// found already-expired at restore time — that absence is what
			// makes it "missed" in the first place.
			//
			// tm.wg.Add(1)/defer tm.wg.Done() (fix round 3): mirrors the
			// sibling "still valid" branch below (tm.wg.Add(1) before its
			// own `go handleTimerExpiration`) — without this, Shutdown's
			// tm.wg.Wait() does not track this goroutine at all, so a
			// Shutdown racing a missed-timer restore could return "done"
			// while this onTimerExpired call is still running, contradicting
			// the shutdown-safety claim in onTimerExpired's own doc comment.
			tm.wg.Add(1)
			go func(groupKey GroupKey, timerType TimerType) {
				defer tm.wg.Done()
				tm.onTimerExpired(nil, groupKey, timerType)
			}(timer.GroupKey, timer.TimerType)
			missed++
		} else {
			// Timer still valid - restore it
			remaining := time.Until(timer.ExpiresAt)

			tm.logger.Info("Restoring timer",
				"group_key", timer.GroupKey,
				"timer_type", timer.TimerType,
				"remaining", remaining)

			// Start timer with remaining duration
			timerCtx, cancelFunc := context.WithCancel(tm.ctx)
			goTimer := time.NewTimer(remaining)

			handle := &timerHandle{
				timer:     goTimer,
				ctx:       timerCtx,
				cancel:    cancelFunc,
				groupKey:  timer.GroupKey,
				timerType: timer.TimerType,
				expiresAt: timer.ExpiresAt,
			}

			tm.timersMu.Lock()
			tm.timers[timer.GroupKey] = handle
			tm.timersMu.Unlock()

			tm.wg.Add(1)
			go tm.handleTimerExpiration(handle, timer)

			restored++

			// Update metrics
			if tm.metrics != nil {
				tm.metrics.IncActiveTimers()
			}
		}
	}

	// Update statistics
	tm.statsMu.Lock()
	tm.stats.totalMissed += int64(missed)
	tm.statsMu.Unlock()

	// Update metrics
	if tm.metrics != nil {
		tm.metrics.RecordTimersRestored(restored)
		tm.metrics.RecordTimersMissed(missed)
	}

	tm.logger.Info("Timer restoration completed",
		"restored", restored,
		"missed", missed,
		"total", len(timers),
		"duration", float64(time.Since(startTime)))

	return restored, missed, nil
}

// reconciliationLoop periodically adopts orphaned group timers (task 6.2,
// distributed timer liveness). Runs for the lifetime of the manager: exits
// when tm.ctx is cancelled (Shutdown), same as every other background
// goroutine here. Only started by NewDefaultTimerManager when
// reconciliationInterval > 0.
func (tm *DefaultTimerManager) reconciliationLoop() {
	defer tm.wg.Done()

	ticker := time.NewTicker(tm.reconciliationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-tm.ctx.Done():
			return
		case <-ticker.C:
			tm.reconcileOrphanedTimers()
		}
	}
}

// reconcileOrphanedTimers scans shared storage for group timers that are
// overdue by more than reconciliationGrace and NOT tracked by this
// replica's own in-memory timers map, then adopts each one via the exact
// same onTimerExpired path RestoreTimers uses for timers found already
// expired at startup (see that method's "missed timer" branch).
//
// "Adopting" here does not skip the exactly-once mechanism: onTimerExpired
// still calls Storage.AcquireLock before doing anything else, so a timer
// that LOOKS orphaned from this replica's point of view (no local Go timer
// for it) but is actually still being processed by its rightful owner —
// lock held, in flight — is left alone; onTimerExpired logs the skip and
// returns, same as the concurrent-fire race case. This loop's only job is
// liveness: without it, a group whose owning replica crashed mid-interval
// would never fire again until that replica restarts and runs its own
// RestoreTimers, which may never happen (e.g. the pod was rescheduled
// elsewhere and a fresh replica took its place with an empty local timers
// map, or simply comes up in a different order relative to other
// replicas). Correctness (no double publish) was already guaranteed by
// task 6.1's nflog claim plus this same AcquireLock; this loop only closes
// the "nobody fires it at all" gap.
func (tm *DefaultTimerManager) reconcileOrphanedTimers() {
	ctx, cancel := context.WithTimeout(tm.ctx, 10*time.Second)
	defer cancel()

	// fix round 1, Finding 1: ListOverdueTimers pushes the "ExpiresAt <=
	// cutoff" filter into storage (a ZRANGEBYSCORE against the
	// ExpiresAt-scored index for RedisTimerStorage), so this tick costs
	// O(overdue timers) against Redis, not O(all timers up to
	// MaxConcurrentTimers) on every replica, every tick. Previously this
	// called ListTimers (full ZRANGE(0,-1) + MGET of everything) and
	// filtered the grace check below in Go.
	now := time.Now()
	cutoff := now.Add(-tm.reconciliationGrace)
	timers, err := tm.storage.ListOverdueTimers(ctx, cutoff)
	if err != nil {
		tm.logger.Warn("reconciliation: failed to list overdue timers from storage", "error", err)
		return
	}

	for _, timer := range timers {
		tm.timersMu.RLock()
		_, trackedLocally := tm.timers[timer.GroupKey]
		tm.timersMu.RUnlock()

		if trackedLocally {
			// This replica already has its own local Go timer for this
			// group (pending, or about to fire on its own) — reconciling it
			// here too would just race itself. handleTimerExpiration will
			// process it through the normal path.
			continue
		}

		tm.logger.Warn("reconciliation: adopting orphaned group timer",
			"group_key", timer.GroupKey,
			"timer_type", timer.TimerType,
			"expires_at", timer.ExpiresAt,
			"overdue_by", now.Sub(timer.ExpiresAt))

		tm.statsMu.Lock()
		tm.stats.totalReconciled++
		tm.statsMu.Unlock()

		// onTimerExpired acquires the distributed lock itself and quietly
		// skips if another replica already holds it (e.g. that replica's
		// own reconciliation loop won the race, or — despite the
		// overdue-by-more-than-grace check above — it was in fact still
		// mid-flight). Runs synchronously: reconciliation ticks are
		// infrequent (ReconciliationInterval) and timer counts are small
		// relative to that interval in practice; RestoreTimers' "missed"
		// branch dispatches via `go` instead because it runs once at
		// startup against a potentially large backlog and must not block
		// the rest of restoration on it. No ctx to pass here either (P0
		// fix, task 6.2 fix round 2) — onTimerExpired roots its own work in
		// tm.ctx, not this tick's bounded reconciliation ctx. firedHandle is
		// nil for the same reason as RestoreTimers' missed branch: an
		// orphan has no local timerHandle by definition.
		tm.onTimerExpired(nil, timer.GroupKey, timer.TimerType)
	}
}

// GetStats returns current timer statistics.
func (tm *DefaultTimerManager) GetStats(ctx context.Context) (*TimerStats, error) {
	tm.statsMu.RLock()
	defer tm.statsMu.RUnlock()

	// Count active timers by type
	tm.timersMu.RLock()
	activeTimers := make(map[TimerType]int)
	for _, handle := range tm.timers {
		activeTimers[handle.timerType]++
	}
	tm.timersMu.RUnlock()

	// Calculate average durations
	avgDuration := make(map[TimerType]time.Duration)
	for timerType, sum := range tm.stats.durationSum {
		count := tm.stats.durationCount[timerType]
		if count > 0 {
			avgDuration[timerType] = time.Duration(int64(sum) / count)
		}
	}

	return &TimerStats{
		ActiveTimers:     activeTimers,
		ExpiredTimers:    tm.stats.totalExpired,
		CancelledTimers:  tm.stats.totalCancelled,
		ResetCount:       tm.stats.totalReset,
		MissedTimers:     tm.stats.totalMissed,
		ReconciledTimers: tm.stats.totalReconciled,
		AverageDuration:  avgDuration,
		Timestamp:        time.Now(),
	}, nil
}

// Shutdown gracefully stops the timer manager.
//
// Algorithm:
//  1. Set shutdown flag
//  2. Cancel all active timers
//  3. Wait for goroutines with timeout
//  4. Force stop remaining goroutines
//
// Performance target: <30s for graceful completion
func (tm *DefaultTimerManager) Shutdown(ctx context.Context) error {
	tm.logger.Info("Shutting down timer manager")
	startTime := time.Now()

	// Set shutdown flag
	tm.shutdownMu.Lock()
	tm.shutdown = true
	tm.shutdownMu.Unlock()

	// Cancel all timers
	tm.timersMu.Lock()
	for groupKey := range tm.timers {
		tm.timers[groupKey].cancel()
		tm.timers[groupKey].timer.Stop()
	}
	timerCount := len(tm.timers)
	tm.timers = make(map[GroupKey]*timerHandle) // Clear map
	tm.timersMu.Unlock()

	// Cancel main context
	tm.cancel()

	// Wait for goroutines with timeout
	done := make(chan struct{})
	go func() {
		tm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		tm.logger.Info("Timer manager shutdown completed",
			"cancelled_timers", timerCount,
			"duration", float64(time.Since(startTime)))
		return nil

	case <-ctx.Done():
		tm.logger.Warn("Timer manager shutdown timed out",
			"cancelled_timers", timerCount,
			"duration", float64(time.Since(startTime)))
		return fmt.Errorf("shutdown timeout: %w", ctx.Err())
	}
}

// Helper function to get hostname
func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return hostname
}
