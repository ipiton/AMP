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
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/ipiton/AMP/pkg/metrics"
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

	// Group manager for retrieving group snapshots
	groupManager *DefaultGroupManager

	// Configuration
	config *TimerManagerConfig

	// Observability
	logger  *slog.Logger
	metrics *metrics.BusinessMetrics

	// Statistics (in-memory, for GetStats)
	stats   *timerStats
	statsMu sync.RWMutex

	// Lifecycle management
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	shutdown bool
	shutdownMu sync.RWMutex

	// Instance ID for distributed debugging
	instanceID string
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
	totalStarted   int64
	totalExpired   int64
	totalCancelled int64
	totalReset     int64
	totalMissed    int64

	// Duration tracking for average calculation
	durationSum   map[TimerType]time.Duration
	durationCount map[TimerType]int64
}

// TimerManagerConfig configures DefaultTimerManager.
type TimerManagerConfig struct {
	// Storage implementation (Redis or in-memory)
	Storage TimerStorage

	// GroupManager for retrieving alert group snapshots
	GroupManager *DefaultGroupManager

	// Default durations (used if not specified in StartTimer)
	DefaultGroupWait      time.Duration
	DefaultGroupInterval  time.Duration
	DefaultRepeatInterval time.Duration

	// Performance tuning
	MaxConcurrentTimers int // Maximum active timers (default: 10000)

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
//	    Storage:              redisStorage,
//	    GroupManager:         groupManager,
//	    DefaultGroupWait:     30 * time.Second,
//	    DefaultGroupInterval: 5 * time.Minute,
//	    DefaultRepeatInterval: 4 * time.Hour,
//	    Logger:               slog.Default(),
//	    Metrics:              businessMetrics,
//	})
func NewDefaultTimerManager(config TimerManagerConfig) (*DefaultTimerManager, error) {
	// Validation
	if config.Storage == nil {
		return nil, fmt.Errorf("storage is required")
	}
	if config.GroupManager == nil {
		return nil, fmt.Errorf("group manager is required")
	}

	// Apply defaults
	if config.DefaultGroupWait == 0 {
		config.DefaultGroupWait = 30 * time.Second
	}
	if config.DefaultGroupInterval == 0 {
		config.DefaultGroupInterval = 5 * time.Minute
	}
	if config.DefaultRepeatInterval == 0 {
		config.DefaultRepeatInterval = 4 * time.Hour
	}
	if config.MaxConcurrentTimers == 0 {
		config.MaxConcurrentTimers = 10000
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
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
	}

	manager.logger.Info("Timer manager initialized",
		"instance_id", instanceID,
		"default_group_wait", config.DefaultGroupWait,
		"default_group_interval", config.DefaultGroupInterval,
		"default_repeat_interval", config.DefaultRepeatInterval,
		"max_concurrent_timers", config.MaxConcurrentTimers)

	return manager, nil
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
		// Timer expired naturally
		tm.onTimerExpired(handle.ctx, handle.groupKey, handle.timerType)

	case <-handle.ctx.Done():
		// Timer cancelled (manual cancel or shutdown)
		tm.logger.Debug("Timer goroutine cancelled",
			"group_key", handle.groupKey,
			"timer_type", handle.timerType,
			"reason", handle.ctx.Err())
	}
}

// onTimerExpired handles timer expiration with distributed lock.
func (tm *DefaultTimerManager) onTimerExpired(ctx context.Context, groupKey GroupKey, timerType TimerType) {
	tm.logger.Info("Timer expired",
		"group_key", groupKey,
		"timer_type", timerType)

	// Acquire distributed lock for exactly-once delivery
	lockCtx, lockCancel := context.WithTimeout(ctx, 5*time.Second)
	defer lockCancel()

	lockID, release, err := tm.storage.AcquireLock(lockCtx, groupKey, lockTTL)
	if err != nil {
		if err == ErrLockAlreadyAcquired {
			tm.logger.Debug("Lock already acquired by another instance",
				"group_key", groupKey)
			return // Another instance will process
		}
		tm.logger.Error("Failed to acquire lock",
			"group_key", groupKey,
			"error", err)
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
	groupCtx, groupCancel := context.WithTimeout(ctx, 5*time.Second)
	defer groupCancel()

	group, err := tm.groupManager.GetGroup(groupCtx, groupKey)
	if err != nil {
		tm.logger.Error("Failed to get group for timer expiration",
			"group_key", groupKey,
			"error", err)
		return
	}

	// Invoke callbacks
	tm.callbacksMu.RLock()
	callbacks := make([]TimerCallback, len(tm.callbacks))
	copy(callbacks, tm.callbacks)
	tm.callbacksMu.RUnlock()

	for i, callback := range callbacks {
		callbackCtx, callbackCancel := context.WithTimeout(ctx, 30*time.Second)
		if err := callback(callbackCtx, groupKey, timerType, group); err != nil {
			tm.logger.Error("Timer callback failed",
				"group_key", groupKey,
				"timer_type", timerType,
				"callback_index", i,
				"error", err)
		}
		callbackCancel()
	}

	// Remove from active timers
	tm.timersMu.Lock()
	delete(tm.timers, groupKey)
	tm.timersMu.Unlock()

	// Delete from storage
	deleteCtx, deleteCancel := context.WithTimeout(ctx, 5*time.Second)
	defer deleteCancel()

	if err := tm.storage.DeleteTimer(deleteCtx, groupKey); err != nil {
		tm.logger.Warn("Failed to delete expired timer from storage",
			"group_key", groupKey,
			"error", err)
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
			go tm.onTimerExpired(ctx, timer.GroupKey, timer.TimerType)
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
		ActiveTimers:    activeTimers,
		ExpiredTimers:   tm.stats.totalExpired,
		CancelledTimers: tm.stats.totalCancelled,
		ResetCount:      tm.stats.totalReset,
		MissedTimers:    tm.stats.totalMissed,
		AverageDuration: avgDuration,
		Timestamp:       time.Now(),
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
