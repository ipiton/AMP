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

// defaultNotifyLogClaimTTL is the fallback cross-replica publish-claim TTL
// (GroupNotifyLog.TryClaim, task 6.1) for a manager built without an
// explicit DefaultGroupManagerConfig.NotifyLogClaimTTL.
//
// Deliberately short — seconds, NOT repeat_interval — so a replica that
// crashes mid-publish (before its deferred release runs) self-heals quickly
// instead of blocking every replica's retries for that group.
//
// SIZING (task rec, wave 3; reworked in fix round 1 after review finding C1)
// — the claim is held across the whole publisher.PublishGroup call and is NOT
// renewed while held (unlike RedisTimerStorage's lock, which has Extend;
// there is no Extend call anywhere in publishGroupAlerts). Since task rec
// that call blocks until delivery is CONFIRMED, so the claim must outlive a
// real HTTP publish AND the timer-callback deadline that bounds the fire.
// All three durations are now derived from one knob — see notify_budget.go
// (NotifyLogClaimTTLFor / TimerCallbackTimeoutFor) — and
// ServiceRegistry.validateNotifyTimingBudget re-checks the relationship at
// startup. This constant is just NotifyLogClaimTTLFor's value for the
// default 45s wait.
//
// Related (review finding M6): RedisTimerStorage's own distributed timer
// lock (lockTTL, 30s, no Extend) now routinely expires mid-fire, so a second
// replica's timer for the same group CAN fire while the first is still
// publishing. This claim is what prevents the double publish in that window
// — it went from a backstop to load-bearing, which is another reason it must
// cover the whole fire.
const defaultNotifyLogClaimTTL = defaultDeliveryConfirmationBudget + notifyChainOverheadBudget + notifyBookkeepingTimeout

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

	// notifyLogClaimTTL is the TTL passed to notifyLog.TryClaim (task rec fix
	// round 1: was a package constant). Always positive — see
	// defaultNotifyLogClaimTTL and notify_budget.go for the sizing chain it
	// belongs to.
	notifyLogClaimTTL time.Duration

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

	// Claim TTL (task rec fix round 1, review finding C1): derived from the
	// publishing stack's delivery-confirmation wait by wiring code
	// (NotifyLogClaimTTLFor), with a default for hand-built managers.
	claimTTL := cfg.NotifyLogClaimTTL
	if claimTTL <= 0 {
		claimTTL = defaultNotifyLogClaimTTL
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
		notifyLogClaimTTL:  claimTTL,               // task rec fix round 1: derived from the delivery-confirmation wait
		publishLocks:       newGroupPublishLocks(), // task 2.4 fix round 1: serialize per group key
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
		// Guard against nil Metadata (task fu2-d item 8): same
		// rehydrated-without-metadata/test-built gap groupTimings and
		// effectiveRepeatInterval already guard against elsewhere in this
		// package — count such a group's alerts but skip its firing/resolved
		// contribution rather than panic.
		if group.Metadata != nil {
			firingAlerts += group.Metadata.FiringCount
			resolvedAlerts += group.Metadata.ResolvedCount
		}
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

	// Guard against nil Metadata (task fu2-d item 8): lazily initialize
	// rather than skip, since the group needs metadata for every downstream
	// step (notify chain, timers, GetStats) to work at all.
	if group.Metadata == nil {
		group.Metadata = &GroupMetadata{CreatedAt: time.Now()}
	}

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
// bookkeepingContext returns a context for work that must still complete
// AFTER a notify fire's delivery wait: RecordSent per confirmed target and
// pruneResolvedAlerts (the claim release builds its own equivalent inside
// RedisNotifyLog.TryClaim, since it takes no context argument).
//
// WHY DETACHED: parent is the timer manager's per-callback context (bounded by
// TimerManagerConfig.CallbackTimeout) and the publish blocks for as long as
// delivery confirmation takes. If a slow target burns the whole callback
// budget, parent is already dead by the time we learn that a DIFFERENT target
// delivered — and then RecordSent fails for a target that really was notified
// (⇒ duplicate page next fire) and resolved-alert pruning silently no-ops (⇒
// the resolved re-notification loop it exists to stop comes back).
//
// WHY IT MUST BE CALLED AT THE POINT OF USE, NOT EARLIER (fix round 2, review
// finding R1): context.WithTimeout stamps an ABSOLUTE deadline when it is
// created. Fix round 1 built this once near the top of publishGroupAlerts, so
// the delivery wait (up to 45s) ran down the 5s bookkeeping budget and every
// fire longer than notifyBookkeepingTimeout still did its bookkeeping on an
// expired context — the exact bug the detaching was supposed to fix, just
// harder to see. Detaching from cancellation is only half of it; the deadline
// has to start when the work does. Every call site therefore calls this
// immediately before the work it covers, and each phase gets its own budget.
func (m *DefaultGroupManager) bookkeepingContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), notifyBookkeepingTimeout)
}

// NotifyLogClaimTTL reports the cross-replica publish-claim TTL this manager
// passes to GroupNotifyLog.TryClaim (task rec fix round 1). Exported so
// wiring code can assert it covers the publishing stack's
// delivery-confirmation wait at startup — see
// ServiceRegistry.validateNotifyTimingBudget and notify_budget.go.
func (m *DefaultGroupManager) NotifyLogClaimTTL() time.Duration {
	return m.notifyLogClaimTTL
}

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

// groupTimings returns group's per-route timing overrides, or nil when the
// group carries no metadata at all.
//
// AlertGroup.Metadata is a POINTER that every constructor in this package
// populates — but not every AlertGroup reaching the notify chain comes from a
// constructor: groups are also rehydrated from JSON by the storage layer (a
// pre-metadata or hand-written record deserializes with Metadata == nil), and
// built directly by tests. effectiveRepeatInterval already guarded against
// that; the notify chain and the three timer callbacks did not, so one such
// group panicked mid-chain and — because the panic unwound the timer
// callback — wedged the group (final review finding 12).
//
// nil is a valid return: startGroupIntervalTimer/startRepeatIntervalTimer both
// document nil timings as "use the root Route.* defaults".
func groupTimings(group *AlertGroup) *GroupTimings {
	if group == nil || group.Metadata == nil {
		return nil
	}
	return group.Metadata.Timings
}

// groupTimeIntervalNames returns group's captured mute/active time-interval
// names, or nil when the group carries no metadata (see groupTimings for why
// that is possible). nil means "no time-interval muting for this group", which
// is exactly how isTimeMuted already treats an empty value.
func groupTimeIntervalNames(group *AlertGroup) *TimeIntervalNames {
	if group == nil || group.Metadata == nil {
		return nil
	}
	return group.Metadata.TimeIntervalNames
}

// groupLabelsFor resolves group's GroupMetadata.GroupBy names to their
// actual values (review finding 1, fwb fix round 1: publishGroupAlerts
// passes this through to GroupNotificationPublisher.PublishGroup for the
// wire payload's "groupLabels" field, instead of the previously hardcoded
// empty map).
//
// Sourced from alerts[0] when available: GroupKeyGenerator guarantees every
// alert ever added to this group shares identical values for its own
// GroupBy names (that is the definition of belonging to the group), so any
// alert in the caller's already-filtered set is an equally valid source —
// there is no need to consult the group's original, unfiltered Alerts map.
//
// Always returns a non-nil map (empty when group/Metadata is nil, GroupBy
// is empty, alerts is empty, or a name isn't present on the source alert's
// Labels — the last case is defensive; it should not happen for alerts
// that legitimately belong to this group).
func groupLabelsFor(group *AlertGroup, alerts []*core.Alert) map[string]string {
	if group == nil || group.Metadata == nil || len(group.Metadata.GroupBy) == 0 || len(alerts) == 0 {
		return map[string]string{}
	}

	source := alerts[0].Labels
	labels := make(map[string]string, len(group.Metadata.GroupBy))
	for _, name := range group.Metadata.GroupBy {
		if v, ok := source[name]; ok {
			labels[name] = v
		}
	}
	return labels
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

	// Per-GroupKey lock, held across the whole chain including the blocking
	// delivery wait. Genuinely per-key since task rec fix round 1 (review
	// finding I1): the old striped implementation would have made two
	// unrelated groups that hash-collide serialize for a full delivery
	// timeout. See publish_lock.go.
	releaseLock := m.publishLocks.acquire(group.Key)
	defer releaseLock()

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
	if m.isTimeMuted(group.Key, groupTimeIntervalNames(group), time.Now()) {
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
	// notifyLog. claimTTL is deliberately short (m.notifyLogClaimTTL,
	// seconds) so a replica that crashes before releasing self-heals quickly
	// instead of blocking every replica's retries for this group.
	//
	// This claim is held across the publisher.PublishGroup call below with
	// NO renewal/extension. Since task rec that call blocks until delivery
	// is CONFIRMED, so the claim has to cover a real HTTP publish — see
	// notify_budget.go for the derived wait/callback/claim chain. Re-check
	// that relationship before changing any side of it.
	claimed, releaseClaim, claimErr := m.notifyLog.TryClaim(ctx, group.Key, m.notifyLogClaimTTL)
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

	// Step 4b: per-target Dedup (task fwb, alertmanager-parity wave 2 —
	// replaces the old whole-group Dedup check with upstream nflog's
	// group:receiver:integration granularity), refined to per-ALERT dedup for
	// non-batch targets (task fu4, wave 4). targetAlerts is handed to the
	// publisher below; the publisher's own receiver-scoped target resolution
	// (deep inside ApplicationPublishingAdapter/PublishingCoordinator) calls
	// it once per candidate target BEFORE attempting delivery and sends
	// exactly what it returns:
	//
	//   - nil       → this target already received this EXACT alert set within
	//                 repeat_interval: skipped, not resent, no outcome.
	//   - a subset  → this target is a non-batch integration (one wire message
	//                 per alert) that already accepted SOME of these alerts on
	//                 an earlier fire; only the remainder is sent, so the ones
	//                 that landed are not duplicated.
	//   - the input → nothing is known to have been delivered: send everything.
	signature := alertSetSignature(alerts)
	repeatInterval := m.effectiveRepeatInterval(group)
	ttl := time.Now().Add(-repeatInterval)
	targetAlerts := func(target string, candidates []*core.Alert) []*core.Alert {
		dup, dupErr := m.notifyLog.IsDuplicate(ctx, group.Key, target, signature, ttl)
		if dupErr != nil {
			// Fail-open (Redis down): proceed as not-a-duplicate — same
			// documented trade-off as the claim check above.
			m.logger.Error("nflog duplicate check failed for target, proceeding fail-open (duplicate-across-replicas risk accepted)",
				"group_key", group.Key,
				"receiver", receiver,
				"target", target,
				"error", dupErr)
			return candidates
		}
		if dup {
			m.logger.Debug("target notification suppressed by dedup (already sent within repeat_interval)",
				"group_key", group.Key,
				"receiver", receiver,
				"target", target,
				"repeat_interval", repeatInterval)
			return nil
		}
		return m.alertsStillOwed(ctx, group.Key, receiver, target, candidates)
	}

	// groupLabels (review finding 1, fwb fix round 1): the resolved
	// {label_name: value} map for this group's own GroupBy names, e.g.
	// {"alertname": "HighCPU", "cluster": "prod"} for
	// group_by: [alertname, cluster]. Read unguarded, same as
	// groupTimings/groupTimeIntervalNames above: GroupMetadata.GroupBy is
	// set once at group creation (createNewGroupUnsafe) and never mutated
	// afterward, so this is not a new race pattern. Sourced from alerts[0]
	// (the already-filtered set) rather than the original unfiltered
	// group.Alerts: GroupKeyGenerator guarantees every alert that was ever
	// added to this group shares identical values for these names, so any
	// alert still in scope is an equally valid source. Empty/nil GroupBy
	// (or an empty alerts slice, defensively) yields an empty, non-nil map.
	groupLabels := groupLabelsFor(group, alerts)

	// Step 5: publish ONE grouped notification (task 2.4's core change: a
	// single PublishGroup call carrying all of alerts, not one PublishToAll
	// call per alert). outcomes reports per-target results (task fwb) so
	// RecordSent below can be scoped to exactly the targets that confirmed
	// delivery this cycle.
	outcomes, err := publisher.PublishGroup(ctx, string(group.Key), alerts, receiver, groupLabels, targetAlerts)
	if err != nil {
		// ErrDeliveryNotConfirmed (final review finding 4): the publisher
		// deliberately delivered nothing — degraded/metrics-only mode. NOT a
		// failure, but crucially NOT a send either, so the RecordSent below
		// must be skipped: the notification log is SHARED across replicas
		// with TTL = repeat_interval, so recording it here would make every
		// healthy replica skip this group for a full repeat_interval.
		// Logged at Warn, not Error, because `publishing.enabled: false` is
		// a legitimate deliberate configuration, not an incident.
		if errors.Is(err, ErrDeliveryNotConfirmed) {
			m.logger.Warn("group notification not delivered (publisher confirmed no delivery); dedup log deliberately NOT updated",
				"group_key", group.Key,
				"receiver", receiver,
				"alert_count", len(alerts),
				"reason", err)
			if m.metrics != nil {
				m.metrics.RecordGroupOperation("publish", "not_delivered")
			}
			return
		}

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

	if len(outcomes) == 0 {
		// Nothing NEW happened this fire: either every candidate target for
		// this receiver was already covered by an earlier send this cycle
		// (skipTarget returned true for all of them — the common steady-
		// state case once every target has succeeded) or the publisher had
		// no targets to report on. Neither is an error worth logging loudly.
		m.logger.Debug("group notification produced no new target outcomes (fully deduped this cycle, or no targets)",
			"group_key", group.Key,
			"receiver", receiver)
		return
	}

	// The delivery wait is over: everything below is bookkeeping, and its
	// context budget starts HERE, not before the publish (fix round 2, review
	// finding R1 — see bookkeepingContext).
	recordCtx, cancelRecord := m.bookkeepingContext(ctx)
	defer cancelRecord()

	now := time.Now()
	allSucceeded := true
	anySucceeded := false
	for _, outcome := range outcomes {
		if !outcome.Success {
			allSucceeded = false

			// Task fu4 (alertmanager-parity wave 4): the target did not confirm
			// the notification as a whole, but a NON-BATCH target sends one
			// wire message per alert and may well have accepted some of them.
			// Recording those keys is what makes the retry fire send only the
			// alerts still owed instead of re-sending the whole set and
			// duplicating everything that already landed.
			//
			// Empty for every batch target (one atomic POST per group ⇒ no
			// partial state exists) and for a non-batch target that got
			// nowhere, so this is a no-op on both of those paths.
			if len(outcome.DeliveredAlerts) > 0 {
				if partErr := m.notifyLog.RecordPartialDelivery(recordCtx, group.Key, outcome.Target, outcome.DeliveredAlerts, repeatInterval); partErr != nil {
					// Advisory only: the consequence is the pre-fu4 behaviour
					// for this target — the alerts that landed are re-sent on
					// the next fire (duplicates), never dropped.
					m.logger.Warn("failed to record per-alert delivered set for an unconfirmed target (already-delivered alerts may be re-sent next fire)",
						"group_key", group.Key,
						"receiver", receiver,
						"target", outcome.Target,
						"delivered_alerts", len(outcome.DeliveredAlerts),
						"error", partErr)
				} else {
					m.logger.Info("target notification partially delivered; only the remaining alerts will be re-sent next fire",
						"group_key", group.Key,
						"receiver", receiver,
						"target", outcome.Target,
						"delivered_alerts", len(outcome.DeliveredAlerts),
						"alert_count", len(alerts))
				}
			}
			continue
		}
		anySucceeded = true
		// Task rec (alertmanager-parity wave 3 — CONFIRMED delivery):
		// outcome.Success == true now means this target actually accepted
		// the notification (the publisher's HTTP call succeeded, after any
		// in-queue retries), not merely that a job was enqueued for it —
		// PublishGroup blocks until each target's queued job reports its
		// final outcome. See TargetPublishOutcome.Success's doc comment
		// (manager.go) for the full contract.
		//
		// So the nflog entry written here records a delivery that provably
		// happened. A webhook 500, an exhausted retry budget, an open
		// circuit breaker, metrics-only mode, or a confirmation wait that
		// expired all arrive as Success == false, get NO entry, and are
		// therefore retried by skipTarget on this group's next scheduled
		// fire (group_interval) instead of being suppressed for a whole
		// repeat_interval. That closes the "RecordSent == enqueue
		// confirmation" gap task fwb narrowed but could not fix.
		//
		// recordCtx, NOT ctx (fix round 1 finding C1.2, corrected in round 2
		// by finding R1): a mixed batch whose slowest target burned the whole
		// callback budget arrives here with ctx already expired, and recording
		// nothing for the target that DID deliver would re-page it on the next
		// fire — the very failure this task removes, in the opposite
		// direction. recordCtx is created just above, AFTER the wait, so its
		// own deadline is not spent by the wait either.
		if recErr := m.notifyLog.RecordSent(recordCtx, group.Key, outcome.Target, signature, now, repeatInterval); recErr != nil {
			// Confirmed delivery already happened — a failure here only
			// means the NEXT fire (this or another replica) might not see
			// this target's send recorded and could re-publish to it.
			// Logged, not fatal: matches the chain's overall fail-open
			// posture.
			m.logger.Error("failed to record nflog sent entry for target (duplicate risk for this target on next fire)",
				"group_key", group.Key,
				"receiver", receiver,
				"target", outcome.Target,
				"error", recErr)
		}
	}

	if m.metrics != nil {
		switch {
		case allSucceeded:
			m.metrics.RecordGroupOperation("publish", "success")
		case anySucceeded:
			m.metrics.RecordGroupOperation("publish", "partial")
		default:
			m.metrics.RecordGroupOperation("publish", "error")
		}
	}

	if !anySucceeded {
		m.logger.Error("failed to publish group notification to any target",
			"group_key", group.Key,
			"receiver", receiver,
			"alert_count", len(alerts),
			"target_count", len(outcomes))
		return
	}

	if !allSucceeded {
		// Partial failure (task fwb): the targets that succeeded were
		// already recorded above, so the NEXT fire's skipTarget will skip
		// them and retry only the ones still missing an entry. Deliberately
		// do NOT prune resolved alerts yet — a target that hasn't confirmed
		// delivery still needs to see them in the next attempt.
		m.logger.Warn("group notification partially delivered; targets that failed will be retried on the next scheduled fire",
			"group_key", group.Key,
			"receiver", receiver,
			"alert_count", len(alerts),
			"target_count", len(outcomes))
		return
	}

	// Step 6: every target confirmed delivery — safe to drop the alerts
	// that were resolved in the notification we just delivered (final
	// review finding 8) — see pruneResolvedAlerts. On its own bookkeeping
	// context for the same reason RecordSent is (fix round 1 finding C1.4,
	// corrected in round 2 by finding R1) — and a SEPARATE one from
	// recordCtx, so a slow RecordSent loop cannot eat the pruning budget.
	pruneCtx, cancelPrune := m.bookkeepingContext(ctx)
	defer cancelPrune()
	m.pruneResolvedAlerts(pruneCtx, group.Key, alerts)
}

// alertsStillOwed narrows candidates to the alerts target has NOT already
// accepted (task fu4, alertmanager-parity wave 4 — per-alert outcome tracking
// for non-batch publishers).
//
// The delivered set it consults exists only after a PARTIAL failure: a
// non-batch target (Slack/Telegram/PagerDuty/Email) sends one wire message per
// alert, so alert 3 of 5 failing used to leave the whole (group, target) pair
// unrecorded and make the next fire re-send all five — duplicating the four
// that had landed. See GroupNotifyLog.DeliveredAlerts.
//
// Three results, matching the targetAlerts contract:
//
//   - candidates unchanged — no partial state (the common case, including
//     every batch target and every first fire), or the lookup failed. A lookup
//     failure is deliberately fail-open in the resend direction: at-least-once
//     is the floor, so a duplicate notification beats suppressing an alert
//     because Redis was unreachable.
//   - a shorter slice — send only these.
//   - nil — every candidate is already in the delivered set, so this target
//     has provably seen the whole current alert set and there is nothing to
//     send. Reported as "skip" rather than as a delivery, because no wire
//     message would be sent this fire; the delivered set's own TTL
//     (repeat_interval + grace) is what eventually re-opens the group for a
//     full resend, exactly as a full nflog entry's TTL would.
//
// Matching is by core.Alert.DeliveryKey, never by position: the group's alert
// set legitimately changes between fires (new alerts arrive, resolved ones are
// pruned) and an index-based comparison would silently pair up unrelated
// alerts. Because the key includes status, an alert that flipped
// firing<->resolved does not match its own earlier delivery and is correctly
// re-sent.
func (m *DefaultGroupManager) alertsStillOwed(ctx context.Context, groupKey GroupKey, receiver string, target string, candidates []*core.Alert) []*core.Alert {
	delivered, err := m.notifyLog.DeliveredAlerts(ctx, groupKey, target)
	if err != nil {
		m.logger.Error("nflog per-alert delivered-set lookup failed for target, proceeding fail-open (already-delivered alerts may be re-sent)",
			"group_key", groupKey,
			"receiver", receiver,
			"target", target,
			"error", err)
		return candidates
	}
	if len(delivered) == 0 {
		return candidates
	}

	deliveredSet := make(map[string]struct{}, len(delivered))
	for _, key := range delivered {
		deliveredSet[key] = struct{}{}
	}

	remaining := make([]*core.Alert, 0, len(candidates))
	for _, alert := range candidates {
		if alert == nil {
			continue
		}
		if _, ok := deliveredSet[alert.DeliveryKey()]; ok {
			continue
		}
		remaining = append(remaining, alert)
	}

	if len(remaining) == len(candidates) {
		// Stale set: every key in it belongs to alerts no longer in the group
		// (all pruned/changed). Nothing to filter — hand back the original
		// slice so the publisher sees the identical, unallocated input.
		return candidates
	}

	if len(remaining) == 0 {
		m.logger.Debug("target notification fully covered by the per-alert delivered set; nothing left to send",
			"group_key", groupKey,
			"receiver", receiver,
			"target", target,
			"alert_count", len(candidates))
		return nil
	}

	m.logger.Info("target notification narrowed to the alerts it has not yet accepted (per-alert retry)",
		"group_key", groupKey,
		"receiver", receiver,
		"target", target,
		"remaining", len(remaining),
		"alert_count", len(candidates))
	return remaining
}

// pruneResolvedAlerts removes from the group every alert that was RESOLVED in
// the notification just confirmed delivered, deleting the group entirely once
// that empties it.
//
// Upstream parity: this is exactly what Alertmanager's aggrGroup.flush does
// after a successful notify — resolved alerts are deleted from the aggregation
// group, so the resolved notification goes out ONCE.
//
// WHY IT WAS MISSING (final review finding 8): RemoveAlertFromGroup has no
// non-test caller, and nothing else pruned resolved alerts. A group whose
// alerts had all resolved therefore kept re-publishing the same resolved
// notification on every repeat_interval — forever, until CleanupExpiredGroups
// eventually reaped it. Operators saw a resolved alert paging them every
// repeat_interval.
//
// Only called after a CONFIRMED delivery (publisher returned nil and RecordSent
// was reached): pruning before that would drop the resolved state before anyone
// was told about it.
//
// alerts is the post-filter set actually sent, so alerts suppressed by
// inhibition/silence are deliberately left in place — they were not announced
// as resolved, so they must not be forgotten. Errors are logged, never fatal:
// the worst case is the pre-fix behaviour for one more interval.
func (m *DefaultGroupManager) pruneResolvedAlerts(ctx context.Context, groupKey GroupKey, alerts []*core.Alert) {
	pruned := 0
	for _, alert := range alerts {
		if alert == nil || alert.Status != core.StatusResolved {
			continue
		}

		// RemoveAlertFromGroup handles the whole teardown when this empties
		// the group: storage delete, DecActiveGroups, cancelGroupTimers and
		// notifyLog.Forget.
		removed, err := m.RemoveAlertFromGroup(ctx, alert.Fingerprint, groupKey)
		if err != nil {
			m.logger.Warn("failed to prune resolved alert after successful group notification",
				"group_key", groupKey,
				"fingerprint", alert.Fingerprint,
				"error", err)
			continue
		}
		if removed {
			pruned++
		}
	}

	if pruned > 0 {
		m.logger.Info("pruned resolved alerts after group notification (upstream aggrGroup.flush semantics)",
			"group_key", groupKey,
			"pruned", pruned)
	}
}

// groupStillExists reports whether groupKey is still present in storage, and
// therefore whether the caller should schedule the group's next timer.
//
// Used by the three timer callbacks after publishGroupAlerts: that call may
// have deleted the group (all its alerts resolved — see pruneResolvedAlerts),
// and scheduling the next group_interval/repeat_interval timer for a deleted
// group would resurrect the very notification loop finding 8 is about.
//
// FAIL-OPEN on transient errors (wave re-review, Important 1). Only a
// CONFIRMED absence (GroupNotFoundError) may return false. The first version
// treated every Load error as "gone", which recreated finding 3's wedge from
// the other end: on a Redis blip the callback returned early, so
// onTimerExpired's tail then ran its normal cleanup and deleted the SHARED
// storage entry as well as the local handle — leaving nothing for
// reconcileOrphanedTimers to adopt, and the group permanently silent. A
// transient error must instead behave exactly as it did before pruning
// existed: assume the group is alive and let the next timer be armed. Arming
// a timer for a group that turns out to be gone is self-correcting (the very
// next fire hits onTimerExpired's confirmed-GroupNotFound branch, which
// cleans up once, at Warn); losing the group is not.
func (m *DefaultGroupManager) groupStillExists(ctx context.Context, groupKey GroupKey) bool {
	group, err := m.storage.Load(ctx, groupKey)
	if err != nil {
		var notFound *GroupNotFoundError
		if errors.As(err, &notFound) {
			return false
		}
		m.logger.Warn("could not confirm group still exists after publishing; assuming it does and continuing the timer chain",
			"group_key", groupKey,
			"error", err)
		return true
	}
	return alertCount(group) > 0
}

// alertCount returns len(group.Alerts) under the group's own RWMutex.
//
// AlertGroup.mu exists precisely because Alerts is mutated concurrently
// (AddAlertToGroup writes it while a timer callback reads it), and
// MemoryGroupStorage.Load can hand back a group sharing that map with the live
// object — so an unlocked `len(group.Alerts)` is a genuine data race, caught by
// `go test -race` on TestTimerChain_GroupWaitToRepeatInterval.
func alertCount(group *AlertGroup) int {
	if group == nil {
		return 0
	}
	group.mu.RLock()
	defer group.mu.RUnlock()
	return len(group.Alerts)
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

	// The publish above may have deleted the group entirely: all its alerts
	// were resolved and got pruned (finding 8). Scheduling the next timer for
	// a deleted group would resurrect the endless resolved-notification loop
	// that pruning exists to stop.
	if !m.groupStillExists(ctx, groupKey) {
		m.logger.Debug("group fully resolved and removed after notification, not scheduling the next timer",
			"group_key", groupKey,
			"timer_type", timerType)
		return nil
	}

	// Start group_interval timer for subsequent notifications, honoring this
	// group's own timing override if one was supplied (task 2.4).
	if err := m.startGroupIntervalTimer(ctx, groupKey, groupTimings(currentGroup)); err != nil {
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

	// The publish above may have deleted the group entirely: all its alerts
	// were resolved and got pruned (finding 8). Scheduling the next timer for
	// a deleted group would resurrect the endless resolved-notification loop
	// that pruning exists to stop.
	if !m.groupStillExists(ctx, groupKey) {
		m.logger.Debug("group fully resolved and removed after notification, not scheduling the next timer",
			"group_key", groupKey,
			"timer_type", timerType)
		return nil
	}

	// Switch to repeat_interval for periodic reminders.
	// group_interval fires once after a notification is sent; subsequent reminders
	// use repeat_interval (Alertmanager-compatible behaviour).
	if err := m.startRepeatIntervalTimer(ctx, groupKey, groupTimings(currentGroup)); err != nil {
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

	// The publish above may have deleted the group entirely: all its alerts
	// were resolved and got pruned (finding 8). Scheduling the next timer for
	// a deleted group would resurrect the endless resolved-notification loop
	// that pruning exists to stop.
	if !m.groupStillExists(ctx, groupKey) {
		m.logger.Debug("group fully resolved and removed after notification, not scheduling the next timer",
			"group_key", groupKey,
			"timer_type", timerType)
		return nil
	}

	// Restart repeat_interval for the next reminder
	if err := m.startRepeatIntervalTimer(ctx, groupKey, groupTimings(currentGroup)); err != nil {
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
