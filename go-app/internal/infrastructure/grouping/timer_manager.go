// Package grouping provides timer management for alert group notifications.
//
// GroupTimerManager is the core interface for managing alert group timers,
// supporting Alertmanager-compatible timing (group_wait, group_interval, repeat_interval).
//
// Key Features:
//   - Redis persistence for High Availability
//   - Distributed locks for exactly-once notification delivery
//   - Callback mechanism for notification triggering
//   - Graceful degradation on Redis failure
//   - Context-aware cancellation and timeouts
//
// Example Usage:
//
//	// Create timer manager
//	timerManager, err := NewDefaultTimerManager(TimerManagerConfig{
//	    Storage:              redisStorage,
//	    GroupManager:         groupManager,
//	    DefaultGroupWait:     30 * time.Second,
//	    DefaultGroupInterval: 5 * time.Minute,
//	    DefaultRepeatInterval: 4 * time.Hour,
//	    Logger:               slog.Default(),
//	    Metrics:              businessMetrics,
//	})
//
//	// Register callback for notifications
//	timerManager.OnTimerExpired(func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
//	    log.Info("Timer expired, sending notification", "group_key", groupKey)
//	    return publisher.PublishGroupNotification(ctx, group)
//	})
//
//	// Start timer for new group
//	timer, err := timerManager.StartTimer(ctx, groupKey, GroupWaitTimer, 30*time.Second)
//
//	// Restore timers after restart
//	restored, missed, err := timerManager.RestoreTimers(ctx)
//
// TN-124: Group Wait/Interval Timers
// Target Quality: 150%
// Date: 2025-11-03
package grouping

import (
	"context"
	"time"
)

// GroupTimerManager manages lifecycle of timers for alert groups.
//
// Thread-safe implementation supports concurrent access from multiple goroutines.
// Redis-based persistence ensures timers survive service restarts.
//
// Timer Flow:
//  1. New group created → StartTimer(GroupWaitTimer, 30s)
//  2. Timer expires → callback triggered → notification sent
//  3. Alert added to group → ResetTimer(GroupIntervalTimer, 5m)
//  4. Timer expires → callback triggered → notification sent
//  5. No changes → RepeatIntervalTimer (4h) → periodic notifications
type GroupTimerManager interface {
	// === Timer Lifecycle ===

	// StartTimer creates and starts a new timer for an alert group.
	// If a timer already exists for the group, it is cancelled and replaced.
	//
	// The timer will fire after the specified duration, triggering all registered
	// callbacks with the group snapshot at expiration time.
	//
	// Parameters:
	//   - ctx: Context for timeout and cancellation
	//   - groupKey: Unique identifier for the alert group (from TN-122)
	//   - timerType: Type of timer (group_wait, group_interval, repeat_interval)
	//   - duration: How long to wait before timer expires (must be positive)
	//
	// Returns:
	//   - *GroupTimer: Created timer with metadata
	//   - error: InvalidTimerTypeError, InvalidDurationError, TimerStorageError, ManagerShutdownError
	//
	// Performance target (150%): <1ms
	// Baseline: <5ms
	StartTimer(ctx context.Context, groupKey GroupKey, timerType TimerType, duration time.Duration) (*GroupTimer, error)

	// CancelTimer stops an active timer for a group.
	// If no timer exists, returns (false, nil).
	//
	// The timer is removed from both in-memory state and Redis storage.
	// Any waiting goroutine is cancelled gracefully.
	//
	// Parameters:
	//   - ctx: Context for timeout and cancellation
	//   - groupKey: Unique identifier for the alert group
	//
	// Returns:
	//   - bool: true if timer was cancelled, false if no timer existed
	//   - error: TimerStorageError
	//
	// Performance target (150%): <500µs
	// Baseline: <2ms
	CancelTimer(ctx context.Context, groupKey GroupKey) (bool, error)

	// ResetTimer cancels the existing timer and starts a new one.
	// This is an atomic operation: cancel old → start new.
	//
	// Used when alert group changes (new alert added) and we want to
	// reset the group_interval timer to wait for more changes before sending.
	//
	// Parameters:
	//   - ctx: Context for timeout and cancellation
	//   - groupKey: Unique identifier for the alert group
	//   - timerType: Type of new timer (usually same as old timer)
	//   - duration: New duration to wait
	//
	// Returns:
	//   - *GroupTimer: New timer with reset metadata
	//   - error: TimerNotFoundError, InvalidTimerTypeError, InvalidDurationError, TimerStorageError
	//
	// Performance target (150%): <2ms (cancel + start)
	// Baseline: <7ms
	ResetTimer(ctx context.Context, groupKey GroupKey, timerType TimerType, duration time.Duration) (*GroupTimer, error)

	// === Query Operations ===

	// GetTimer retrieves information about a timer for a group.
	//
	// Returns a copy of the timer to prevent external mutation.
	//
	// Parameters:
	//   - ctx: Context for timeout and cancellation
	//   - groupKey: Unique identifier for the alert group
	//
	// Returns:
	//   - *GroupTimer: Copy of the timer with current state
	//   - error: TimerNotFoundError, TimerStorageError
	//
	// Performance target (150%): <1ms
	// Baseline: <5ms
	GetTimer(ctx context.Context, groupKey GroupKey) (*GroupTimer, error)

	// ListActiveTimers returns all currently active timers.
	//
	// Supports filtering by timer type, expiration window, and receiver.
	// Results can be paginated using Limit and Offset in filters.
	//
	// Parameters:
	//   - ctx: Context for timeout and cancellation
	//   - filters: Optional filtering criteria (nil = no filtering)
	//
	// Returns:
	//   - []*GroupTimer: List of active timers matching filters
	//   - error: TimerStorageError
	//
	// Performance target (150%): <10ms for 1000 timers
	// Baseline: <50ms for 1000 timers
	ListActiveTimers(ctx context.Context, filters *TimerFilters) ([]*GroupTimer, error)

	// === Callback Management ===

	// OnTimerExpired registers a callback to be invoked when timers expire.
	//
	// Multiple callbacks can be registered; all will be invoked in order.
	// Callbacks are executed in separate goroutines with distributed lock
	// to ensure exactly-once delivery in multi-instance deployments.
	//
	// Callback signature:
	//   func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error
	//
	// Callback responsibilities:
	//   - Send notification (via Publisher)
	//   - Start next timer (group_interval → repeat_interval)
	//   - Update metrics
	//
	// Parameters:
	//   - callback: Function to call when timer expires
	//
	// Thread-safety: Safe to call from multiple goroutines
	OnTimerExpired(callback TimerCallback)

	// === High Availability ===

	// RestoreTimers recovers timers from Redis storage after service restart.
	//
	// Algorithm:
	//   1. Load all timers from Redis
	//   2. Identify expired timers (expires_at < now)
	//   3. Trigger callbacks for expired/missed timers immediately
	//   4. Restore active timers with remaining duration
	//   5. Clean up invalid/corrupted timer entries
	//
	// This should be called once during service initialization.
	//
	// Parameters:
	//   - ctx: Context for timeout (recommend 30s+ for large timer sets)
	//
	// Returns:
	//   - restored: Count of timers successfully restored
	//   - missed: Count of timers that expired during downtime
	//   - error: TimerStorageError
	//
	// Performance target (150%): <100ms for 1000 timers
	// Baseline: <500ms for 1000 timers
	RestoreTimers(ctx context.Context) (restored int, missed int, err error)

	// === Observability ===

	// GetStats returns current statistics about timer operations.
	//
	// Statistics include:
	//   - Count of active timers by type
	//   - Total expired/cancelled/reset counts
	//   - Missed timer count (from HA recovery)
	//   - Average duration by timer type
	//
	// 150% Enhancement: Detailed stats for capacity planning and debugging.
	//
	// Parameters:
	//   - ctx: Context for timeout
	//
	// Returns:
	//   - *TimerStats: Snapshot of current statistics
	//   - error: TimerStorageError
	GetStats(ctx context.Context) (*TimerStats, error)

	// === Lifecycle ===

	// Shutdown gracefully stops the timer manager.
	//
	// Steps:
	//   1. Set shutdown flag (reject new StartTimer calls)
	//   2. Cancel all active timers
	//   3. Wait for running callbacks to complete (with timeout)
	//   4. Close storage connections
	//
	// After Shutdown, the manager cannot be reused.
	//
	// Parameters:
	//   - ctx: Context with timeout (recommend 30s for graceful completion)
	//
	// Returns:
	//   - error: If shutdown didn't complete within timeout
	Shutdown(ctx context.Context) error
}

// TimerCallback is invoked when a timer expires.
//
// The callback receives:
//   - ctx: Context with timeout (typically 30s)
//   - groupKey: Identifier of the group whose timer expired
//   - timerType: Type of expired timer (determines next action)
//   - group: Snapshot of the alert group at expiration time
//
// Callback responsibilities:
//  1. Send notification via Publisher
//  2. Start next timer based on type:
//     - group_wait → group_interval (5m)
//     - group_interval → repeat_interval (4h)
//     - repeat_interval → repeat_interval (4h)
//  3. Log and record metrics
//
// Error handling:
//   - Return error if notification sending fails
//   - Manager logs error but continues processing other timers
//   - Failed notifications are NOT retried automatically
//
// Thread-safety:
//   - Called in separate goroutine per expiration
//   - Distributed lock ensures exactly-once execution
//   - Multiple callbacks are executed sequentially
type TimerCallback func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error

// TimerStorage abstracts persistence for group timers.
//
// Implementations:
//   - RedisTimerStorage: Production implementation with HA support
//   - InMemoryTimerStorage: Fallback when Redis unavailable
//
// Storage schema (Redis):
//
//	Key: "timer:{groupKey}"
//	Value: JSON-serialized GroupTimer
//	TTL: duration + 60s grace period
//
//	Index: "timers:index" (Sorted Set)
//	Score: expires_at Unix timestamp
//	Member: groupKey
//
//	Lock: "lock:timer:{groupKey}"
//	Value: lock ID (UUID)
//	TTL: 30s
type TimerStorage interface {
	// SaveTimer persists a timer to storage.
	//
	// Parameters:
	//   - ctx: Context for timeout
	//   - timer: Timer to save
	//
	// Returns:
	//   - error: TimerStorageError if save fails
	SaveTimer(ctx context.Context, timer *GroupTimer) error

	// LoadTimer retrieves a timer from storage.
	//
	// Parameters:
	//   - ctx: Context for timeout
	//   - groupKey: Identifier of the group
	//
	// Returns:
	//   - *GroupTimer: Loaded timer
	//   - error: ErrTimerNotFound or TimerStorageError
	LoadTimer(ctx context.Context, groupKey GroupKey) (*GroupTimer, error)

	// DeleteTimer removes a timer from storage.
	//
	// Parameters:
	//   - ctx: Context for timeout
	//   - groupKey: Identifier of the group
	//
	// Returns:
	//   - error: TimerStorageError if delete fails (not found is not an error)
	DeleteTimer(ctx context.Context, groupKey GroupKey) error

	// ListTimers returns all timers in storage.
	//
	// Parameters:
	//   - ctx: Context for timeout
	//
	// Returns:
	//   - []*GroupTimer: List of all stored timers
	//   - error: TimerStorageError
	ListTimers(ctx context.Context) ([]*GroupTimer, error)

	// AcquireLock attempts to acquire a distributed lock for a group.
	//
	// Used for exactly-once delivery in multi-instance deployments.
	// Only one instance can hold the lock at a time.
	//
	// Parameters:
	//   - ctx: Context for timeout
	//   - groupKey: Identifier of the group
	//   - ttl: How long the lock should be held (typically 30s)
	//
	// Returns:
	//   - lockID: Unique identifier for this lock acquisition
	//   - release: Function to release the lock (must be called!)
	//   - error: ErrLockAlreadyAcquired or TimerStorageError
	AcquireLock(ctx context.Context, groupKey GroupKey, ttl time.Duration) (lockID string, release func() error, err error)
}
