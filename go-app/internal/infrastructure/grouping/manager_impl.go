package grouping

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: deprecated pkg/metrics kept until v2 migration (v2 lacks BusinessMetrics)
)

// notifyLogClaimTTL bounds how long a cross-replica publish claim
// (GroupNotifyLog.TryClaim, task 6.1) is held before it expires
// automatically. Deliberately short — seconds, NOT repeat_interval — so a
// replica that crashes mid-publish (before its deferred release runs)
// self-heals quickly instead of blocking every replica's retries for that
// group. Reuses lockTTL (redis_timer_storage.go, 30s), the same
// distributed-lock duration RedisTimerStorage.AcquireLock already uses, per
// storage.go's note that lockTTL is meant to be shared across timer/group/
// notify-log storage for consistency.
const notifyLogClaimTTL = lockTTL

// DefaultGroupManager is an in-memory implementation of AlertGroupManager.
//
// Thread-safety: All methods are thread-safe via sync.RWMutex.
// Concurrent reads and writes are properly synchronized.
//
// Performance:
//   - AddAlertToGroup: O(1) map lookup + lock
//   - GetGroup: O(1) map lookup + RLock
//   - ListGroups: O(n) iteration with filtering
//   - RemoveAlertFromGroup: O(1) map deletion + lock
//   - CleanupExpiredGroups: O(n) iteration
//
// Memory: ~5KB per group (target: <10KB baseline, <5KB at 150%)
type DefaultGroupManager struct {
	// storage persists alert groups (Redis primary + Memory fallback) (TN-125)
	// Replaces in-memory groups map for distributed state management
	storage GroupStorage

	// fingerprintIndex is a reverse index for fast lookup: map[fingerprint]GroupKey
	// 150% Enhancement: Enables O(1) lookup of group by alert fingerprint
	// NOTE: This remains in-memory for performance. Groups are in storage.
	fingerprintIndex map[string]GroupKey

	// mu protects concurrent access to fingerprintIndex
	// NOTE: Groups are protected by storage's internal locking
	mu sync.RWMutex

	// keyGenerator generates group keys from alert labels (from TN-122)
	keyGenerator *GroupKeyGenerator

	// config is the grouping configuration (from TN-121)
	config *GroupingConfig

	// timerManager manages group timers (group_wait, group_interval) (TN-124)
	// Optional: can be nil for backwards compatibility
	timerManager GroupTimerManager

	// publisher sends alert notifications when group timers fire (TN-124).
	// Optional: can be nil for backwards compatibility (only logs). Read
	// under mu (task 2.4, SetPublisher can update it after construction).
	publisher GroupNotificationPublisher

	// inhibitionChecker is the notify-chain's Inhibit step (task 2.4).
	// Optional: nil skips inhibition filtering.
	inhibitionChecker GroupInhibitionChecker

	// silenceChecker is the notify-chain's Silence step (task 2.4).
	// Optional: nil skips silence filtering.
	silenceChecker GroupSilenceChecker

	// timeIntervalLookup is the notify-chain's TimeMute step (task 3.2).
	// Optional: nil skips time-interval mute filtering.
	timeIntervalLookup GroupTimeIntervalLookup

	// notifyLog is the notify-chain's Dedup step + cross-replica publish
	// claim (task 2.4 Step 4; Redis-backed variant task 6.1). Always
	// non-nil: defaults to an in-memory notifyDedupLog when
	// DefaultGroupManagerConfig.NotifyLog is nil — see GroupNotifyLog's doc
	// comment (manager.go) for the interface contract and
	// RedisNotifyLog's (redis_notify_log.go) for the cross-replica
	// protocol.
	notifyLog GroupNotifyLog

	// publishLocks serializes the whole notify-stage chain per GroupKey
	// (task 2.4 fix round 1, Finding 4 — see publish_lock.go). Always
	// non-nil.
	publishLocks *groupPublishLocks

	// logger for structured logging
	logger *slog.Logger

	// metrics for Prometheus integration
	metrics *metrics.BusinessMetrics

	// stats tracks operation statistics
	stats *groupStats
}

// groupStats stores internal statistics for operations.
//
// Thread-safety: Protected by its own mutex for lock-free access from methods.
type groupStats struct {
	totalAdds       int64
	totalRemoves    int64
	totalCleanups   int64
	totalUpdates    int64
	lastCleanupTime time.Time
	mu              sync.RWMutex
}

// NewDefaultGroupManager creates a new in-memory group manager.
//
// Example:
//
//	manager, err := NewDefaultGroupManager(DefaultGroupManagerConfig{
//	    KeyGenerator: keyGen,
//	    Config:       config,
//	    Logger:       slog.Default(),
//	    Metrics:      businessMetrics,
//	})
func NewDefaultGroupManager(ctx context.Context, cfg DefaultGroupManagerConfig) (*DefaultGroupManager, error) {
	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	// Storage is required for distributed state (TN-125)
	if cfg.Storage == nil {
		return nil, fmt.Errorf("storage cannot be nil (TN-125 requirement)")
	}

	// NotifyLog defaults to the in-memory implementation (task 2.4 behavior)
	// when the caller doesn't inject one — always non-nil either way (task
	// 6.1: standard profile injects a *RedisNotifyLog here via
	// ServiceRegistry.newNotifyLog).
	notifyLog := cfg.NotifyLog
	if notifyLog == nil {
		notifyLog = newNotifyDedupLog()
	}

	mgr := &DefaultGroupManager{
		storage:            cfg.Storage,
		fingerprintIndex:   make(map[string]GroupKey),
		keyGenerator:       cfg.KeyGenerator,
		config:             cfg.Config,
		timerManager:       cfg.TimerManager,       // Optional (TN-124)
		publisher:          cfg.Publisher,          // Optional (TN-124), may also arrive later via SetPublisher (task 2.4)
		inhibitionChecker:  cfg.InhibitionChecker,  // Optional (task 2.4)
		silenceChecker:     cfg.SilenceChecker,     // Optional (task 2.4)
		timeIntervalLookup: cfg.TimeIntervalLookup, // Optional (task 3.2)
		notifyLog:          notifyLog,              // task 2.4/6.1: always-on dedup + cross-replica claim
		publishLocks:       &groupPublishLocks{},   // task 2.4 fix round 1: serialize per group key
		logger:             cfg.Logger,
		metrics:            cfg.Metrics,
		stats:              &groupStats{},
	}

	// Register timer callbacks if timer manager is configured (TN-124)
	if err := mgr.registerTimerCallbacks(); err != nil {
		return nil, fmt.Errorf("register timer callbacks: %w", err)
	}

	// Restore groups from storage on startup (TN-125)
	if err := mgr.restoreGroupsFromStorage(ctx); err != nil {
		return nil, fmt.Errorf("restore groups from storage: %w", err)
	}

	return mgr, nil
}

// === Lifecycle Management Implementation ===

// AddAlertToGroup implements AlertGroupManager.AddAlertToGroup.
func (m *DefaultGroupManager) AddAlertToGroup(
	ctx context.Context,
	alert *core.Alert,
	groupKey GroupKey,
	opts ...AddAlertOption,
) (*AlertGroup, error) {
	startTime := time.Now()

	options := &addAlertOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// Validation
	if alert == nil {
		return nil, &InvalidAlertError{Reason: "alert is nil"}
	}
	if alert.Fingerprint == "" {
		return nil, &InvalidAlertError{Reason: "alert fingerprint is empty"}
	}

	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Load or create group (TN-125: storage-backed)
	group, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		// Check if group not found
		var notFoundErr *GroupNotFoundError
		if !errors.As(err, &notFoundErr) {
			// Unexpected error
			return nil, fmt.Errorf("load group: %w", err)
		}

		// Create new group
		group = m.createNewGroupUnsafe(groupKey)

		// task 2.4: apply per-route timing overrides (RoutingDecision), if
		// the caller supplied any — see WithGroupTimings and
		// GroupMetadata.Timings.
		group.Metadata.Timings = options.timings.Clone()

		// task 3.2: capture the matched route's own mute/active
		// time_interval NAMES onto this group at creation time — see
		// WithMuteTimeIntervals and GroupMetadata.TimeIntervalNames.
		group.Metadata.TimeIntervalNames = options.timeIntervalNames.Clone()

		m.logger.Info("created new alert group",
			"group_key", groupKey,
			"alert", alert.AlertName,
			"fingerprint", alert.Fingerprint,
			"timings", group.Metadata.Timings,
			"time_interval_names", group.Metadata.TimeIntervalNames)

		// Metric: new group created
		if m.metrics != nil {
			m.metrics.IncActiveGroups()
		}

		// Start group_wait timer for new group (TN-124), honoring this
		// group's own timing override if one was supplied (task 2.4).
		if startErr := m.startGroupWaitTimer(ctx, groupKey, group.Metadata.Timings); startErr != nil {
			// Log error but don't fail the operation (timer is optional)
			m.logger.Warn("failed to start group_wait timer for new group",
				"group_key", groupKey,
				"error", startErr)
		}
	}

	// Add alert to group (thread-safe)
	group.mu.Lock()
	isNewAlert := group.Alerts[alert.Fingerprint] == nil
	group.Alerts[alert.Fingerprint] = alert
	group.mu.Unlock()

	// Update fingerprint index
	m.mu.Lock()
	m.fingerprintIndex[alert.Fingerprint] = groupKey
	m.mu.Unlock()

	// Update group state
	m.updateGroupStateUnsafe(group)

	// Persist to storage (TN-125)
	if storeErr := m.storage.Store(ctx, group); storeErr != nil {
		m.logger.Error("failed to persist group to storage",
			"group_key", groupKey,
			"error", storeErr)
		// Don't fail the operation - group is in memory
		// TODO: Consider retry logic or fallback strategy
	}

	// Update stats
	m.stats.mu.Lock()
	m.stats.totalAdds++
	m.stats.mu.Unlock()

	// Metrics
	if m.metrics != nil {
		m.recordAddMetrics(groupKey, isNewAlert, time.Since(startTime))
	}

	m.logger.Debug("added alert to group",
		"group_key", groupKey,
		"alert", alert.AlertName,
		"fingerprint", alert.Fingerprint,
		"group_size", len(group.Alerts),
		"is_new", isNewAlert,
		"state", group.Metadata.State)

	// Return shallow copy (150% enhancement: prevent external mutation)
	return group.Clone(), nil
}

// RemoveAlertFromGroup implements AlertGroupManager.RemoveAlertFromGroup.
func (m *DefaultGroupManager) RemoveAlertFromGroup(
	ctx context.Context,
	fingerprint string,
	groupKey GroupKey,
) (bool, error) {
	startTime := time.Now()

	// Check context cancellation
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	// Load group from storage (TN-125)
	group, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		return false, err
	}

	// Remove alert from group
	group.mu.Lock()
	_, existed := group.Alerts[fingerprint]
	delete(group.Alerts, fingerprint)
	groupSize := len(group.Alerts)
	group.mu.Unlock()

	if !existed {
		return false, nil // Alert wasn't in the group
	}

	// Remove from fingerprint index (TN-125: single lock)
	m.mu.Lock()
	delete(m.fingerprintIndex, fingerprint)
	m.mu.Unlock()

	// If group is empty - delete group
	if groupSize == 0 {
		if delErr := m.storage.Delete(ctx, groupKey); delErr != nil {
			m.logger.Error("failed to delete empty group from storage",
				"group_key", groupKey,
				"error", delErr)
		}

		m.logger.Info("deleted empty alert group",
			"group_key", groupKey)

		// Metric: group deleted
		if m.metrics != nil {
			m.metrics.DecActiveGroups()
		}

		// Cancel all timers for this group (TN-124)
		m.cancelGroupTimers(ctx, groupKey)

		// Forget this group's dedup entry (task 2.4) — otherwise the
		// notify-log would grow independent of active groups. Best-effort:
		// a failure here (Redis down) just means the entry outlives the
		// group until its own TTL expires — not fatal.
		if forgetErr := m.notifyLog.Forget(ctx, groupKey); forgetErr != nil {
			m.logger.Warn("failed to forget nflog entry for deleted group",
				"group_key", groupKey,
				"error", forgetErr)
		}
	} else {
		// Update group state
		m.updateGroupStateUnsafe(group)

		// Persist updated group (TN-125)
		if storeErr := m.storage.Store(ctx, group); storeErr != nil {
			m.logger.Error("failed to persist group after alert removal",
				"group_key", groupKey,
				"error", storeErr)
		}
	}

	// Update stats
	m.stats.mu.Lock()
	m.stats.totalRemoves++
	m.stats.mu.Unlock()

	// Metrics
	if m.metrics != nil {
		m.recordRemoveMetrics(groupKey, time.Since(startTime))
	}

	m.logger.Debug("removed alert from group",
		"group_key", groupKey,
		"fingerprint", fingerprint,
		"group_size", groupSize,
		"group_deleted", groupSize == 0)

	return true, nil
}

// UpdateGroupState implements AlertGroupManager.UpdateGroupState.
func (m *DefaultGroupManager) UpdateGroupState(
	ctx context.Context,
	groupKey GroupKey,
) (*AlertGroup, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Load group from storage (TN-125)
	group, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		return nil, err
	}

	// Update state
	m.updateGroupStateUnsafe(group)

	// Persist updated group (TN-125)
	if storeErr := m.storage.Store(ctx, group); storeErr != nil {
		m.logger.Error("failed to persist group after state update",
			"group_key", groupKey,
			"error", storeErr)
		return nil, fmt.Errorf("store group: %w", storeErr)
	}

	// Update stats
	m.stats.mu.Lock()
	m.stats.totalUpdates++
	m.stats.mu.Unlock()

	m.logger.Debug("updated group state",
		"group_key", groupKey,
		"state", group.Metadata.State,
		"firing_count", group.Metadata.FiringCount,
		"resolved_count", group.Metadata.ResolvedCount)

	return group.Clone(), nil
}

// CleanupExpiredGroups implements AlertGroupManager.CleanupExpiredGroups.
func (m *DefaultGroupManager) CleanupExpiredGroups(
	ctx context.Context,
	maxAge time.Duration,
) (int, error) {
	startTime := time.Now()

	// Check context cancellation
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	// Load all groups from storage (TN-125)
	allGroups, err := m.storage.LoadAll(ctx)
	if err != nil {
		m.logger.Error("failed to load groups for cleanup",
			"error", err)
		return 0, fmt.Errorf("load groups: %w", err)
	}

	// Find expired groups and delete them
	m.mu.Lock()
	defer m.mu.Unlock()

	deletedCount := 0
	for _, group := range allGroups {
		if !group.IsExpired(maxAge) {
			continue
		}

		groupKey := group.Key

		// Remove all fingerprints from index
		group.mu.RLock()
		for fingerprint := range group.Alerts {
			delete(m.fingerprintIndex, fingerprint)
		}
		group.mu.RUnlock()

		// Delete from storage (TN-125)
		if delErr := m.storage.Delete(ctx, groupKey); delErr != nil {
			m.logger.Error("failed to delete expired group from storage",
				"group_key", groupKey,
				"error", delErr)
			continue // Skip this group if delete fails
		}

		// Forget this group's dedup entry (task 2.4). Best-effort — see the
		// same Forget call in RemoveAlertFromGroup for why a failure here
		// isn't fatal.
		if forgetErr := m.notifyLog.Forget(ctx, groupKey); forgetErr != nil {
			m.logger.Warn("failed to forget nflog entry for expired group",
				"group_key", groupKey,
				"error", forgetErr)
		}

		deletedCount++
	}

	// Update stats
	m.stats.mu.Lock()
	m.stats.totalCleanups += int64(deletedCount)
	m.stats.lastCleanupTime = startTime
	m.stats.mu.Unlock()

	// Metrics
	if m.metrics != nil {
		m.recordCleanupMetrics(deletedCount, time.Since(startTime))
	}

	m.logger.Info("cleaned up expired groups",
		"deleted_count", deletedCount,
		"max_age", maxAge,
		"duration", time.Since(startTime))

	return deletedCount, nil
}

// === Query Operations Implementation ===

// GetGroup implements AlertGroupManager.GetGroup.
func (m *DefaultGroupManager) GetGroup(
	ctx context.Context,
	groupKey GroupKey,
) (*AlertGroup, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Load from storage (TN-125)
	group, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		return nil, err
	}

	// Return shallow copy (150% enhancement: prevent external mutation)
	return group.Clone(), nil
}

// ListGroups implements AlertGroupManager.ListGroups.
func (m *DefaultGroupManager) ListGroups(
	ctx context.Context,
	filters *GroupFilters,
) ([]*AlertGroup, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Load all groups from storage (TN-125)
	allGroups, err := m.storage.LoadAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("load groups: %w", err)
	}

	// Pre-allocate result slice (150% optimization)
	result := make([]*AlertGroup, 0, len(allGroups))

	// Apply filters and collect matching groups
	offset := 0
	limit := 0
	if filters != nil {
		limit = filters.Limit
	}

	for _, group := range allGroups {
		// Check if group matches filters
		if filters != nil && !filters.Matches(group) {
			continue
		}

		// Apply offset (pagination)
		if filters != nil && filters.Offset > 0 && offset < filters.Offset {
			offset++
			continue
		}

		// Add group clone to result
		result = append(result, group.Clone())

		// Apply limit (pagination)
		if limit > 0 && len(result) >= limit {
			break
		}
	}

	return result, nil
}

// GetGroupByFingerprint implements AlertGroupManager.GetGroupByFingerprint.
//
// 150% Enhancement: Reverse lookup using fingerprint index.
func (m *DefaultGroupManager) GetGroupByFingerprint(
	ctx context.Context,
	fingerprint string,
) (GroupKey, *AlertGroup, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Lookup in fingerprint index
	groupKey, exists := m.fingerprintIndex[fingerprint]
	if !exists {
		return "", nil, &GroupNotFoundError{Key: GroupKey(fmt.Sprintf("fingerprint=%s", fingerprint))}
	}

	// Load group from storage (TN-125)
	group, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		// Index inconsistency (should not happen)
		m.logger.Error("fingerprint index inconsistency",
			"fingerprint", fingerprint,
			"group_key", groupKey,
			"error", err)
		return "", nil, err
	}

	return groupKey, group.Clone(), nil
}

// === Metrics & Observability Implementation ===

// GetMetrics implements AlertGroupManager.GetMetrics.
func (m *DefaultGroupManager) GetMetrics(ctx context.Context) (*GroupMetrics, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Load all groups from storage (TN-125)
	allGroups, err := m.storage.LoadAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("load groups: %w", err)
	}

	// Collect metrics
	alertsPerGroup := make(map[string]int, len(allGroups))
	sizeDistribution := map[string]int{
		"1-10":     0,
		"11-50":    0,
		"51-100":   0,
		"101-500":  0,
		"501-1000": 0,
		"1000+":    0,
	}

	for _, group := range allGroups {
		size := group.Size()
		alertsPerGroup[string(group.Key)] = size

		// Calculate size distribution
		switch {
		case size <= 10:
			sizeDistribution["1-10"]++
		case size <= 50:
			sizeDistribution["11-50"]++
		case size <= 100:
			sizeDistribution["51-100"]++
		case size <= 500:
			sizeDistribution["101-500"]++
		case size <= 1000:
			sizeDistribution["501-1000"]++
		default:
			sizeDistribution["1000+"]++
		}
	}

	// Get operation stats
	m.stats.mu.RLock()
	operations := map[string]int64{
		"add":     m.stats.totalAdds,
		"remove":  m.stats.totalRemoves,
		"cleanup": m.stats.totalCleanups,
	}
	m.stats.mu.RUnlock()

	return &GroupMetrics{
		ActiveGroups:     len(allGroups),
		AlertsPerGroup:   alertsPerGroup,
		SizeDistribution: sizeDistribution,
		Operations:       operations,
		Timestamp:        time.Now(),
	}, nil
}

// GetStats implements AlertGroupManager.GetStats.
//
// 150% Enhancement: Extended statistics for advanced monitoring.
func (m *DefaultGroupManager) GetStats(ctx context.Context) (*GroupStats, error) {
	// Check context cancellation
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Load all groups from storage (TN-125)
	allGroups, err := m.storage.LoadAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("load groups: %w", err)
	}

	// Calculate totals
	totalAlerts := 0
	firingAlerts := 0
	resolvedAlerts := 0

	for _, group := range allGroups {
		group.mu.RLock()
		totalAlerts += len(group.Alerts)
		firingAlerts += group.Metadata.FiringCount
		resolvedAlerts += group.Metadata.ResolvedCount
		group.mu.RUnlock()
	}

	// Estimate memory usage (approximate)
	// ~5KB per group: struct overhead + alerts map + metadata
	estimatedMemory := int64(len(allGroups) * 5 * 1024)

	// Get operation stats
	m.stats.mu.RLock()
	stats := &GroupStats{
		TotalAdds:            m.stats.totalAdds,
		TotalRemoves:         m.stats.totalRemoves,
		TotalCleanups:        m.stats.totalCleanups,
		TotalUpdates:         m.stats.totalUpdates,
		LastCleanupTime:      m.stats.lastCleanupTime,
		ActiveGroups:         len(allGroups),
		TotalAlerts:          totalAlerts,
		FiringAlerts:         firingAlerts,
		ResolvedAlerts:       resolvedAlerts,
		EstimatedMemoryBytes: estimatedMemory,
		Timestamp:            time.Now(),
	}
	m.stats.mu.RUnlock()

	return stats, nil
}

// === Internal Helper Methods ===

// createNewGroupUnsafe creates a new empty group.
//
// Caller must hold write lock (m.mu.Lock).
func (m *DefaultGroupManager) createNewGroupUnsafe(groupKey GroupKey) *AlertGroup {
	now := time.Now()

	return &AlertGroup{
		Key:    groupKey,
		Alerts: make(map[string]*core.Alert),
		Metadata: &GroupMetadata{
			State:     GroupStateFiring, // Initial state (will be updated)
			CreatedAt: now,
			UpdatedAt: now,
			GroupBy:   m.config.Route.GroupBy,
			Version:   1,
		},
	}
}

// updateGroupStateUnsafe updates the state of a group based on alert statuses.
//
// Caller must hold write lock (m.mu.Lock).
func (m *DefaultGroupManager) updateGroupStateUnsafe(group *AlertGroup) {
	group.mu.Lock()
	defer group.mu.Unlock()

	group.Metadata.UpdateState(group.Alerts)
}

// recordAddMetrics records Prometheus metrics for AddAlertToGroup operation.
func (m *DefaultGroupManager) recordAddMetrics(groupKey GroupKey, isNew bool, duration time.Duration) {
	m.metrics.RecordGroupOperation("add", "success")
	m.metrics.RecordGroupOperationDuration("add", float64(duration))

	// Record group size histogram (async to avoid lock contention)
	// Note: This is a simplified version. Real implementation would be in pkg/metrics/business.go
}

// recordRemoveMetrics records Prometheus metrics for RemoveAlertFromGroup operation.
func (m *DefaultGroupManager) recordRemoveMetrics(groupKey GroupKey, duration time.Duration) {
	m.metrics.RecordGroupOperation("remove", "success")
	m.metrics.RecordGroupOperationDuration("remove", float64(duration))
}

// recordCleanupMetrics records Prometheus metrics for CleanupExpiredGroups operation.
func (m *DefaultGroupManager) recordCleanupMetrics(deletedCount int, duration time.Duration) {
	m.metrics.RecordGroupOperation("cleanup", "success")
	m.metrics.RecordGroupOperationDuration("cleanup", float64(duration))
	m.metrics.RecordGroupsCleanedUp(deletedCount)
}

// === Timer Integration (TN-124) ===

// startGroupWaitTimer starts a group_wait timer for a newly created group.
// This timer delays the first notification until group_wait duration elapses.
//
// timings is the group's own per-route override (task 2.4, from
// AddAlertToGroup's WithGroupTimings), or nil to use the grouping config's
// root Route.group_wait.
//
// Called when a new group is created in AddAlertToGroup.
func (m *DefaultGroupManager) startGroupWaitTimer(ctx context.Context, groupKey GroupKey, timings *GroupTimings) error {
	if m.timerManager == nil {
		return nil // Timer functionality disabled (backwards compatible)
	}

	// Get group_wait duration: per-group override (task 2.4) takes
	// precedence over the root Route.* default (default: 30s).
	duration := 30 * time.Second
	if m.config != nil && m.config.Route != nil && m.config.Route.GroupWait != nil {
		duration = m.config.Route.GroupWait.Duration
	}
	if timings != nil && timings.GroupWait > 0 {
		duration = timings.GroupWait
	}

	// Start group_wait timer
	_, err := m.timerManager.StartTimer(ctx, groupKey, GroupWaitTimer, duration)
	if err != nil {
		m.logger.Error("failed to start group_wait timer",
			"group_key", groupKey,
			"duration", duration,
			"error", err)
		return fmt.Errorf("start group_wait timer: %w", err)
	}

	m.logger.Debug("started group_wait timer",
		"group_key", groupKey,
		"duration", duration)

	return nil
}

// startGroupIntervalTimer starts a group_interval timer for an existing group.
// This timer ensures minimum time between notifications for the same group.
//
// timings is the group's own per-route override (task 2.4), or nil to use
// the grouping config's root Route.group_interval.
//
// Called after a notification is sent for a group.
func (m *DefaultGroupManager) startGroupIntervalTimer(ctx context.Context, groupKey GroupKey, timings *GroupTimings) error {
	if m.timerManager == nil {
		return nil // Timer functionality disabled
	}

	// Get group_interval duration: per-group override (task 2.4) takes
	// precedence over the root Route.* default (default: 5m).
	duration := 5 * time.Minute
	if m.config != nil && m.config.Route != nil && m.config.Route.GroupInterval != nil {
		duration = m.config.Route.GroupInterval.Duration
	}
	if timings != nil && timings.GroupInterval > 0 {
		duration = timings.GroupInterval
	}

	// Start group_interval timer
	_, err := m.timerManager.StartTimer(ctx, groupKey, GroupIntervalTimer, duration)
	if err != nil {
		m.logger.Error("failed to start group_interval timer",
			"group_key", groupKey,
			"duration", duration,
			"error", err)
		return fmt.Errorf("start group_interval timer: %w", err)
	}

	m.logger.Debug("started group_interval timer",
		"group_key", groupKey,
		"duration", duration)

	return nil
}

// SetPublisher wires (or clears, if nil) the notify-stage publisher used by
// group timer callbacks (task 2.4).
//
// Exists because ServiceRegistry.initializeGrouping runs BEFORE
// initializePublishing (see that method's doc comment) — the publishing
// stack, and therefore a GroupNotificationPublisher, does not exist yet at
// GroupManager construction time. SetPublisher lets the registry wire it in
// afterward, once it does — mirrors DefaultTimerManager.SetGroupManager's
// same construction-order workaround. Safe to call at any time; publisher
// reads in publishGroupAlerts take the same lock.
//
// Thread-safe.
func (m *DefaultGroupManager) SetPublisher(pub GroupNotificationPublisher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publisher = pub
}

// receiverFromGroupKey recovers the receiver name from a GroupKey built by
// AlertProcessor.groupKeyFor (task 2.3): "receiver=<name>/<rest>". Returns
// "" if key doesn't have that prefix (e.g. a key built directly by a test,
// or by any future caller that doesn't go through groupKeyFor) — an empty
// receiver means "no receiver-scoped filtering" to PublishToTargets
// (publish to every enabled target), matching that function's pre-task-1.5
// fallback semantics.
func receiverFromGroupKey(key GroupKey) string {
	const prefix = "receiver="
	s := string(key)
	if !strings.HasPrefix(s, prefix) {
		return ""
	}
	rest := s[len(prefix):]
	if idx := strings.Index(rest, "/"); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

// effectiveRepeatInterval returns the dedup TTL for group: its own
// per-route override (task 2.4, GroupMetadata.Timings) if one was captured
// at group-creation time, else the grouping config's root
// Route.repeat_interval (default: 4h).
func (m *DefaultGroupManager) effectiveRepeatInterval(group *AlertGroup) time.Duration {
	duration := 4 * time.Hour
	if m.config != nil && m.config.Route != nil {
		duration = m.config.Route.GetEffectiveRepeatInterval()
	}
	if group != nil && group.Metadata != nil && group.Metadata.Timings != nil && group.Metadata.Timings.RepeatInterval > 0 {
		duration = group.Metadata.Timings.RepeatInterval
	}
	return duration
}

// filterInhibited drops alerts currently matched by an inhibition rule
// (notify-chain Step 1). Checked against CURRENT state (send time), not
// ingest time — see GroupInhibitionChecker's doc comment. No-op (returns
// alerts unchanged) if no inhibitionChecker is wired, or if alert.Status
// isn't firing (resolved alerts are never inhibited — mirrors the ingest-
// time check in AlertProcessor.ProcessAlert).
func (m *DefaultGroupManager) filterInhibited(ctx context.Context, groupKey GroupKey, alerts []*core.Alert) []*core.Alert {
	if m.inhibitionChecker == nil {
		return alerts
	}

	kept := make([]*core.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if alert.Status != core.StatusFiring {
			kept = append(kept, alert)
			continue
		}

		result, err := m.inhibitionChecker.ShouldInhibit(ctx, alert)
		if err != nil {
			// Fail-open: same posture as the ingest-time check in
			// AlertProcessor.ProcessAlert — an inhibition-check error must
			// not silently drop a notification.
			m.logger.Warn("inhibition check failed at send time, keeping alert",
				"group_key", groupKey,
				"fingerprint", alert.Fingerprint,
				"error", err)
			kept = append(kept, alert)
			continue
		}

		if result != nil && result.Matched {
			m.logger.Info("alert dropped from group notification: inhibited at send time",
				"group_key", groupKey,
				"fingerprint", alert.Fingerprint)
			continue
		}

		kept = append(kept, alert)
	}
	return kept
}

// filterSilenced drops alerts currently matching an active silence
// (notify-chain Step 2). Checked against CURRENT state (send time) — a
// silence created AFTER the alert entered its group still suppresses the
// notification. No-op if no silenceChecker is wired.
func (m *DefaultGroupManager) filterSilenced(groupKey GroupKey, alerts []*core.Alert) []*core.Alert {
	if m.silenceChecker == nil {
		return alerts
	}

	now := time.Now()
	kept := make([]*core.Alert, 0, len(alerts))
	for _, alert := range alerts {
		if m.silenceChecker.HasActiveMatch(alert.Labels, now) {
			m.logger.Info("alert dropped from group notification: silenced at send time",
				"group_key", groupKey,
				"fingerprint", alert.Fingerprint)
			continue
		}
		kept = append(kept, alert)
	}
	return kept
}

// isTimeMuted evaluates the notify-chain's TimeMute step (task 3.2, Step 3:
// Inhibit -> Silence -> TimeMute -> Dedup). Unlike filterInhibited/
// filterSilenced, which drop individual alerts, a time-interval mute
// suppresses the WHOLE group notification — upstream Alertmanager applies
// mute_time_intervals/active_time_intervals per ROUTE, not per alert.
//
// Mute semantics (mirrors upstream):
//   - muted if ActiveTimeIntervals is non-empty and NO name in it matches
//     now (outside every allowed window), OR
//   - muted if ANY name in MuteTimeIntervals matches now.
//
// Mute wins: if both lists are set and the current time both matches a
// mute interval AND falls inside an active interval, the group is still
// muted.
//
// No-op (never muted) when no timeIntervalLookup is wired (backwards
// compatible, same posture as inhibitionChecker/silenceChecker being
// optional), or when names is nil/empty (the common case — the matched
// route referenced no time_intervals at all).
//
// Fail-open per name (task 3.2 documented decision): if a referenced
// interval name is no longer defined in the CURRENT config — e.g. it was
// renamed or removed between alert ingest and this group-timer fire — that
// name is logged as an error and treated as "did not match" rather than
// aborting delivery. This matches filterInhibited's existing fail-open
// posture: an internal lookup gap must never silently drop a notification
// that would otherwise have gone out.
func (m *DefaultGroupManager) isTimeMuted(groupKey GroupKey, names *TimeIntervalNames, now time.Time) bool {
	if m.timeIntervalLookup == nil || names.IsEmpty() {
		return false
	}

	if len(names.Active) > 0 {
		anyActiveMatched := false
		for _, name := range names.Active {
			interval, ok := m.timeIntervalLookup.GetTimeInterval(name)
			if !ok {
				m.logger.Error("active_time_intervals: interval name undefined in current config at fire time, treating as not matched",
					"group_key", groupKey,
					"interval", name)
				continue
			}
			if interval.Matches(now) {
				anyActiveMatched = true
				break
			}
		}
		if !anyActiveMatched {
			return true // outside every active window: muted
		}
	}

	for _, name := range names.Mute {
		interval, ok := m.timeIntervalLookup.GetTimeInterval(name)
		if !ok {
			m.logger.Error("mute_time_intervals: interval name undefined in current config at fire time, treating as not matched",
				"group_key", groupKey,
				"interval", name)
			continue
		}
		if interval.Matches(now) {
			return true // mute wins, even if an active window also matched
		}
	}

	return false
}

// publishGroupAlerts runs the notify-stage chain (task 2.4-3.2,
// alertmanager-parity) for a group snapshot when a group timer fires:
//
//	Inhibit -> Silence -> TimeMute -> Dedup -> publish (ONE grouped notification)
//
// This order matches upstream Alertmanager's notification pipeline. Inhibit
// and Silence are evaluated against CURRENT state (send time), not ingest
// time — see filterInhibited/filterSilenced. TimeMute (task 3.2) is also
// send-time, but — unlike Inhibit/Silence — suppresses the WHOLE group at
// once rather than filtering individual alerts (see isTimeMuted). If
// filtering removes every alert, TimeMute applies, or Dedup finds this
// exact alert set was already sent within repeat_interval, nothing is
// published — that is the normal "suppressed" case, not a failure, and is
// only logged at Debug. Suppression at any step means RecordSent is never
// called, so the group's already-scheduled group_interval/repeat_interval
// timer keeps ticking and will retry with the group's then-current state
// (e.g. once a mute window ends).
//
// The publisher is read under m.mu (SetPublisher can update it after
// construction — see its doc comment for why).
//
// Concurrency (task 2.4 fix round 1, Finding 4): the whole
// check-then-publish-then-record sequence below is serialized per
// group.Key via m.publishLocks — see its doc comment for why the Dedup
// check and record alone aren't enough to prevent a double-send from two
// concurrent callers.
//
// Cross-replica concurrency (task 6.1): m.publishLocks only serializes
// callers within THIS process. In an HA deployment, another replica
// process can be running the exact same sequence for the exact same group
// at the exact same time (both fired the same group_interval timer, say).
// The Dedup step's IsDuplicate/RecordSent alone don't prevent that — two
// replicas can both observe "not a duplicate" before either records
// success, exactly the same race publishLocks fixes locally. m.notifyLog's
// TryClaim (GroupNotifyLog, see manager.go's doc comment) closes this gap
// for a Redis-backed notifyLog: only the replica that wins the claim
// proceeds past this point; the other returns immediately and lets its own
// next-scheduled timer retry later. The in-memory notifyDedupLog's TryClaim
// is a no-op (always wins) — there is no other replica to race against in
// that deployment shape, and publishLocks already covers same-process
// races.
func (m *DefaultGroupManager) publishGroupAlerts(ctx context.Context, group *AlertGroup) {
	m.mu.RLock()
	publisher := m.publisher
	m.mu.RUnlock()

	if publisher == nil {
		return
	}

	lock := m.publishLocks.lockFor(group.Key)
	lock.Lock()
	defer lock.Unlock()

	group.mu.RLock()
	alerts := make([]*core.Alert, 0, len(group.Alerts))
	for _, a := range group.Alerts {
		alerts = append(alerts, a)
	}
	group.mu.RUnlock()

	if len(alerts) == 0 {
		return
	}

	receiver := receiverFromGroupKey(group.Key)

	// Step 1: Inhibit (send-time)
	alerts = m.filterInhibited(ctx, group.Key, alerts)
	if len(alerts) == 0 {
		m.logger.Debug("group notification fully suppressed by inhibition", "group_key", group.Key)
		return
	}

	// Step 2: Silence (send-time)
	alerts = m.filterSilenced(group.Key, alerts)
	if len(alerts) == 0 {
		m.logger.Debug("group notification fully suppressed by silence", "group_key", group.Key)
		return
	}

	// Step 3: TimeMute (send-time, task 3.2) — whole-group suppression, not
	// per-alert (see isTimeMuted's doc comment for semantics). Checked
	// against the group's own MuteTimeIntervals/ActiveTimeIntervals NAMES,
	// captured from the matched route at group-creation time.
	if m.isTimeMuted(group.Key, group.Metadata.TimeIntervalNames, time.Now()) {
		m.logger.Debug("group notification suppressed by time-interval mute",
			"group_key", group.Key,
			"receiver", receiver)
		if m.metrics != nil {
			m.metrics.RecordGroupOperation("publish", "muted")
		}
		return
	}

	// Step 4a: cross-replica publish claim (task 6.1 — see this function's
	// doc comment above). A no-op ("always wins") for the in-memory
	// notifyLog. claimTTL is deliberately short (notifyLogClaimTTL, seconds)
	// so a replica that crashes before releasing self-heals quickly instead
	// of blocking every replica's retries for this group.
	claimed, releaseClaim, claimErr := m.notifyLog.TryClaim(ctx, group.Key, notifyLogClaimTTL)
	switch {
	case claimErr != nil:
		// Fail-open (Redis down): proceed without a claim, matching the
		// chain's Inhibit/Silence fail-open posture. Accepts a duplicate-
		// across-replicas risk while Redis is unavailable — documented
		// trade-off, see redis_notify_log.go's package doc comment.
		m.logger.Error("nflog publish-claim check failed, proceeding fail-open (duplicate-across-replicas risk accepted)",
			"group_key", group.Key,
			"receiver", receiver,
			"error", claimErr)
	case !claimed:
		m.logger.Debug("group notification claim held by another replica, skipping this fire",
			"group_key", group.Key,
			"receiver", receiver)
		return
	default:
		defer func() {
			if relErr := releaseClaim(); relErr != nil {
				m.logger.Warn("failed to release nflog publish-claim",
					"group_key", group.Key,
					"receiver", receiver,
					"error", relErr)
			}
		}()
	}

	// Step 4b: Dedup (notification-log semantics, task 2.4 Step 4 — see
	// dedup.go for the in-memory implementation and redis_notify_log.go for
	// the cross-replica one).
	signature := alertSetSignature(alerts)
	repeatInterval := m.effectiveRepeatInterval(group)
	ttl := time.Now().Add(-repeatInterval)
	dup, err := m.notifyLog.IsDuplicate(ctx, group.Key, signature, ttl)
	if err != nil {
		// Fail-open (Redis down): proceed as not-a-duplicate — same
		// documented trade-off as the claim check above.
		m.logger.Error("nflog duplicate check failed, proceeding fail-open (duplicate-across-replicas risk accepted)",
			"group_key", group.Key,
			"receiver", receiver,
			"error", err)
		dup = false
	}
	if dup {
		m.logger.Debug("group notification suppressed by dedup (already sent within repeat_interval)",
			"group_key", group.Key,
			"receiver", receiver,
			"repeat_interval", repeatInterval)
		return // claim (if any) is released by the deferred call above
	}

	// Step 5: publish ONE grouped notification (task 2.4's core change: a
	// single PublishGroup call carrying all of alerts, not one PublishToAll
	// call per alert).
	if err := publisher.PublishGroup(ctx, alerts, receiver); err != nil {
		// "No targets for receiver" and any other publish error: log +
		// metric, do NOT retry-loop here — the next scheduled timer
		// (group_interval/repeat_interval) will naturally retry with the
		// group's then-current state (task 2.4 dispatch decision, carried
		// from task 1.5's "no targets" semantics note).
		m.logger.Error("failed to publish group notification",
			"group_key", group.Key,
			"receiver", receiver,
			"alert_count", len(alerts),
			"error", err)
		if m.metrics != nil {
			m.metrics.RecordGroupOperation("publish", "error")
		}
		return
	}

	if err := m.notifyLog.RecordSent(ctx, group.Key, signature, time.Now(), repeatInterval); err != nil {
		// Confirmed delivery already happened — a failure here only means
		// the NEXT fire (this or another replica) might not see this send
		// recorded and could re-publish. Logged, not fatal: matches the
		// chain's overall fail-open posture.
		m.logger.Error("failed to record nflog sent entry (duplicate-across-replicas risk on next fire)",
			"group_key", group.Key,
			"receiver", receiver,
			"error", err)
	}

	if m.metrics != nil {
		m.metrics.RecordGroupOperation("publish", "success")
	}
}

// startRepeatIntervalTimer starts a repeat_interval timer for an existing group.
// This timer provides periodic reminders for ongoing alert groups with no new changes.
//
// timings is the group's own per-route override (task 2.4), or nil to use
// the grouping config's root Route.repeat_interval.
//
// Called after the group_interval notification is sent (when switching to "steady" mode).
func (m *DefaultGroupManager) startRepeatIntervalTimer(ctx context.Context, groupKey GroupKey, timings *GroupTimings) error {
	if m.timerManager == nil {
		return nil // Timer functionality disabled
	}

	// Get repeat_interval duration: per-group override (task 2.4) takes
	// precedence over the root Route.* default (default: 4h, via helper).
	duration := 4 * time.Hour
	if m.config != nil && m.config.Route != nil {
		duration = m.config.Route.GetEffectiveRepeatInterval()
	}
	if timings != nil && timings.RepeatInterval > 0 {
		duration = timings.RepeatInterval
	}

	// Start repeat_interval timer
	_, err := m.timerManager.StartTimer(ctx, groupKey, RepeatIntervalTimer, duration)
	if err != nil {
		m.logger.Error("failed to start repeat_interval timer",
			"group_key", groupKey,
			"duration", duration,
			"error", err)
		return fmt.Errorf("start repeat_interval timer: %w", err)
	}

	m.logger.Debug("started repeat_interval timer",
		"group_key", groupKey,
		"duration", duration)

	return nil
}

// cancelGroupTimers cancels all timers for a group.
// Called when a group is deleted (empty after alert removal).
func (m *DefaultGroupManager) cancelGroupTimers(ctx context.Context, groupKey GroupKey) {
	if m.timerManager == nil {
		return // Timer functionality disabled
	}

	// Cancel group_wait timer (if exists)
	if _, err := m.timerManager.CancelTimer(ctx, groupKey); err != nil {
		m.logger.Warn("failed to cancel group timer",
			"group_key", groupKey,
			"error", err)
	} else {
		m.logger.Debug("cancelled group timers",
			"group_key", groupKey)
	}
}

// onGroupWaitExpired is the callback for group_wait timer expiration.
// This sends the first notification for a group after the initial delay.
func (m *DefaultGroupManager) onGroupWaitExpired(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
	m.logger.Info("group_wait timer expired, sending first notification",
		"group_key", groupKey,
		"alert_count", len(group.Alerts))

	// Check if group still exists and has alerts (load from storage for freshness,
	// matching the pattern used in onGroupIntervalExpired/onRepeatIntervalExpired).
	currentGroup, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		m.logger.Debug("group no longer exists, not sending notification",
			"group_key", groupKey,
			"error", err)
		return nil
	}

	if len(currentGroup.Alerts) == 0 {
		m.logger.Debug("group is empty, not sending notification",
			"group_key", groupKey)
		return nil
	}

	// Publish all alerts in the current group snapshot as the first notification
	m.publishGroupAlerts(ctx, currentGroup)

	// Start group_interval timer for subsequent notifications, honoring this
	// group's own timing override if one was supplied (task 2.4).
	if err := m.startGroupIntervalTimer(ctx, groupKey, currentGroup.Metadata.Timings); err != nil {
		m.logger.Error("failed to start group_interval timer after group_wait",
			"group_key", groupKey,
			"error", err)
		return err
	}

	return nil
}

// onGroupIntervalExpired is the callback for group_interval timer expiration.
// This sends an update notification for the group and starts the repeat_interval timer
// for periodic reminders.
func (m *DefaultGroupManager) onGroupIntervalExpired(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
	m.logger.Info("group_interval timer expired, sending update notification",
		"group_key", groupKey,
		"alert_count", len(group.Alerts))

	// Check if group still exists and has alerts (TN-125: load from storage)
	currentGroup, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		m.logger.Debug("group no longer exists or is empty, not sending notification",
			"group_key", groupKey,
			"error", err)
		return nil
	}

	if len(currentGroup.Alerts) == 0 {
		m.logger.Debug("group is empty, not sending notification",
			"group_key", groupKey)
		return nil
	}

	// Publish update notification for all alerts in the current group snapshot
	m.publishGroupAlerts(ctx, currentGroup)

	// Switch to repeat_interval for periodic reminders.
	// group_interval fires once after a notification is sent; subsequent reminders
	// use repeat_interval (Alertmanager-compatible behaviour).
	if err := m.startRepeatIntervalTimer(ctx, groupKey, currentGroup.Metadata.Timings); err != nil {
		m.logger.Error("failed to start repeat_interval timer after group_interval",
			"group_key", groupKey,
			"error", err)
		return err
	}

	return nil
}

// onRepeatIntervalExpired is the callback for repeat_interval timer expiration.
// This sends a periodic reminder notification for an ongoing alert group and
// restarts the repeat_interval timer so reminders continue.
func (m *DefaultGroupManager) onRepeatIntervalExpired(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
	m.logger.Info("repeat_interval timer expired, sending reminder notification",
		"group_key", groupKey,
		"alert_count", len(group.Alerts))

	// Check if group still exists and has alerts (TN-125: load from storage)
	currentGroup, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		m.logger.Debug("group no longer exists or is empty, stopping repeat_interval",
			"group_key", groupKey,
			"error", err)
		return nil
	}

	if len(currentGroup.Alerts) == 0 {
		m.logger.Debug("group is empty, stopping repeat_interval",
			"group_key", groupKey)
		return nil
	}

	// Publish reminder notification for all alerts
	m.publishGroupAlerts(ctx, currentGroup)

	// Restart repeat_interval for the next reminder
	if err := m.startRepeatIntervalTimer(ctx, groupKey, currentGroup.Metadata.Timings); err != nil {
		m.logger.Error("failed to restart repeat_interval timer",
			"group_key", groupKey,
			"error", err)
		return err
	}

	return nil
}

// registerTimerCallbacks registers timer expiration callbacks with the timer manager.
// This should be called during manager initialization.
func (m *DefaultGroupManager) registerTimerCallbacks() error {
	if m.timerManager == nil {
		return nil // Timer functionality disabled
	}

	// Register callback for all timer types
	m.timerManager.OnTimerExpired(func(ctx context.Context, groupKey GroupKey, timerType TimerType, group *AlertGroup) error {
		switch timerType {
		case GroupWaitTimer:
			return m.onGroupWaitExpired(ctx, groupKey, timerType, group)
		case GroupIntervalTimer:
			return m.onGroupIntervalExpired(ctx, groupKey, timerType, group)
		case RepeatIntervalTimer:
			return m.onRepeatIntervalExpired(ctx, groupKey, timerType, group)
		default:
			m.logger.Warn("unknown timer type expired",
				"group_key", groupKey,
				"timer_type", timerType)
			return fmt.Errorf("unknown timer type: %s", timerType)
		}
	})

	m.logger.Info("registered timer expiration callbacks")
	return nil
}
