// Package grouping provides alert group management for Alertmanager++ compatibility.
//
// The Alert Group Manager manages the lifecycle of alert groups, tracking which alerts
// belong to which groups based on grouping configuration (from TN-121) and group keys
// (from TN-122).
//
// Key Features:
//   - Thread-safe concurrent access (sync.RWMutex)
//   - In-memory storage with fingerprint index
//   - Automatic state management (firing/resolved/mixed)
//   - Prometheus metrics integration
//   - Graceful degradation on errors
//
// Example Usage:
//
//	config := &GroupingConfig{...}  // from TN-121
//	keyGen := NewGroupKeyGenerator() // from TN-122
//
//	manager, err := NewDefaultGroupManager(DefaultGroupManagerConfig{
//	    KeyGenerator: keyGen,
//	    Config:       config,
//	    Logger:       slog.Default(),
//	    Metrics:      businessMetrics,
//	})
//
//	// Add alert to group
//	groupKey, _ := keyGen.GenerateKey(alert.Labels, []string{"alertname"})
//	group, err := manager.AddAlertToGroup(ctx, alert, groupKey)
//
//	// List all groups
//	groups, err := manager.ListGroups(ctx, nil)
//
//	// Cleanup expired groups
//	deleted, err := manager.CleanupExpiredGroups(ctx, 24*time.Hour)
//
// TN-123: Alert Group Manager
// Target Quality: 150%
// Date: 2025-11-03
package grouping

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/pkg/metrics"
)

// GroupState represents the state of an alert group.
type GroupState string

const (
	// GroupStateFiring - all alerts in the group are firing
	GroupStateFiring GroupState = "firing"

	// GroupStateResolved - all alerts in the group are resolved
	GroupStateResolved GroupState = "resolved"

	// GroupStateMixed - the group contains both firing and resolved alerts
	GroupStateMixed GroupState = "mixed"

	// GroupStateSilenced - the group is silenced (future: TN-133+)
	GroupStateSilenced GroupState = "silenced"
)

// AlertGroup represents a group of related alerts.
//
// Groups are identified by a GroupKey (from TN-122) which is derived from alert labels
// and grouping configuration. All alerts with the same group key belong to the same group.
//
// Thread-safety: AlertGroup is thread-safe via internal sync.RWMutex.
// Multiple goroutines can safely add/remove alerts concurrently.
type AlertGroup struct {
	// Key is the unique identifier for this group (from GroupKeyGenerator)
	Key GroupKey `json:"key"`

	// Alerts contains all alerts in this group, keyed by fingerprint
	// Using map for O(1) lookup and removal
	Alerts map[string]*core.Alert `json:"alerts"`

	// Metadata contains group state and statistics
	Metadata *GroupMetadata `json:"metadata"`

	// Version is used for optimistic locking in distributed storage (TN-125)
	// Incremented on every Store operation to detect concurrent modifications
	// Redis storage will reject Store if version mismatch detected
	Version int64 `json:"version"`

	// mu protects concurrent access to Alerts and Metadata
	// 150% Enhancement: Thread-safe by design
	mu sync.RWMutex `json:"-"`
}

// GroupMetadata contains metadata about an alert group's state and history.
type GroupMetadata struct {
	// State is the current state of the group (firing/resolved/mixed/silenced)
	State GroupState `json:"state"`

	// CreatedAt is when the group was first created
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the group was last modified (alert added/removed)
	UpdatedAt time.Time `json:"updated_at"`

	// FirstFiringAt is when the first firing alert was added to the group
	// nil if no firing alerts exist
	FirstFiringAt *time.Time `json:"first_firing_at,omitempty"`

	// ResolvedAt is when all alerts in the group became resolved
	// nil if there are still firing alerts
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`

	// FiringCount is the number of firing alerts in the group
	FiringCount int `json:"firing_count"`

	// ResolvedCount is the number of resolved alerts in the group
	ResolvedCount int `json:"resolved_count"`

	// GroupBy contains the label names used for grouping (from configuration)
	// e.g., ["alertname", "namespace"]
	GroupBy []string `json:"group_by"`

	// GroupWaitTimer contains state for group_wait timer (TN-124, TN-125)
	GroupWaitTimer *TimerMetadata `json:"group_wait_timer,omitempty"`

	// GroupIntervalTimer contains state for group_interval timer (TN-124, TN-125)
	GroupIntervalTimer *TimerMetadata `json:"group_interval_timer,omitempty"`

	// RepeatIntervalTimer contains state for repeat_interval timer (TN-124, TN-125)
	RepeatIntervalTimer *TimerMetadata `json:"repeat_interval_timer,omitempty"`

	// Version is used for optimistic locking (future: Redis storage in TN-125)
	Version int64 `json:"version"`
}

// Size returns the total number of alerts in the group (firing + resolved).
func (g *AlertGroup) Size() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.Alerts)
}

// GetFiringCount returns the number of firing alerts in the group.
func (g *AlertGroup) GetFiringCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.Metadata.FiringCount
}

// GetResolvedCount returns the number of resolved alerts in the group.
func (g *AlertGroup) GetResolvedCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.Metadata.ResolvedCount
}

// IsExpired checks if the group should be considered expired based on maxAge.
//
// A group is expired if:
//  1. All alerts are resolved AND resolved_at is older than maxAge, OR
//  2. updated_at is older than maxAge (no activity)
func (g *AlertGroup) IsExpired(maxAge time.Duration) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	cutoffTime := time.Now().Add(-maxAge)

	// Check if all alerts resolved and resolved_at exceeded maxAge
	if g.Metadata.State == GroupStateResolved {
		if g.Metadata.ResolvedAt != nil && g.Metadata.ResolvedAt.Before(cutoffTime) {
			return true
		}
	}

	// Check if group has no activity for maxAge
	if g.Metadata.UpdatedAt.Before(cutoffTime) {
		return true
	}

	return false
}

// Clone creates a shallow copy of the AlertGroup.
//
// 150% Enhancement: Returns a copy to prevent external mutation of internal state.
// The Alerts map is copied, but Alert pointers are shared (shallow copy).
func (g *AlertGroup) Clone() *AlertGroup {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Copy Alerts map
	alertsCopy := make(map[string]*core.Alert, len(g.Alerts))
	for k, v := range g.Alerts {
		alertsCopy[k] = v // Shallow copy (shared Alert pointer)
	}

	// Copy Metadata
	metadataCopy := *g.Metadata
	if g.Metadata.FirstFiringAt != nil {
		t := *g.Metadata.FirstFiringAt
		metadataCopy.FirstFiringAt = &t
	}
	if g.Metadata.ResolvedAt != nil {
		t := *g.Metadata.ResolvedAt
		metadataCopy.ResolvedAt = &t
	}

	// Copy GroupBy slice
	if g.Metadata.GroupBy != nil {
		metadataCopy.GroupBy = make([]string, len(g.Metadata.GroupBy))
		copy(metadataCopy.GroupBy, g.Metadata.GroupBy)
	}

	return &AlertGroup{
		Key:      g.Key,
		Alerts:   alertsCopy,
		Metadata: &metadataCopy,
	}
}

// Touch updates the UpdatedAt timestamp to current time.
//
// Caller must hold write lock (mu.Lock).
func (m *GroupMetadata) Touch() {
	m.UpdatedAt = time.Now()
}

// UpdateState recalculates the group state based on alert statuses.
//
// Caller must hold write lock on parent AlertGroup.
func (m *GroupMetadata) UpdateState(alerts map[string]*core.Alert) {
	firingCount := 0
	resolvedCount := 0

	for _, alert := range alerts {
		if alert.Status == core.StatusFiring {
			firingCount++
		} else if alert.Status == core.StatusResolved {
			resolvedCount++
		}
	}

	m.FiringCount = firingCount
	m.ResolvedCount = resolvedCount

	// Determine state
	if firingCount > 0 && resolvedCount == 0 {
		m.State = GroupStateFiring
		// Update FirstFiringAt if not set
		if m.FirstFiringAt == nil {
			now := time.Now()
			m.FirstFiringAt = &now
		}
		m.ResolvedAt = nil
	} else if firingCount == 0 && resolvedCount > 0 {
		m.State = GroupStateResolved
		// Update ResolvedAt if not set
		if m.ResolvedAt == nil {
			now := time.Now()
			m.ResolvedAt = &now
		}
	} else if firingCount > 0 && resolvedCount > 0 {
		m.State = GroupStateMixed
		m.ResolvedAt = nil
	}

	m.Touch()
}

// MarkResolved marks the group as fully resolved.
//
// Sets State to GroupStateResolved and updates ResolvedAt timestamp.
// Caller must hold write lock on parent AlertGroup.
func (m *GroupMetadata) MarkResolved() {
	m.State = GroupStateResolved
	now := time.Now()
	m.ResolvedAt = &now
	m.Touch()
}

// GroupFilters defines filters for ListGroups query.
//
// 150% Enhancement: Advanced filtering and pagination support.
type GroupFilters struct {
	// State filters groups by state (firing/resolved/mixed)
	// nil means no filtering by state
	State *GroupState `json:"state,omitempty"`

	// MinSize filters groups with at least this many alerts
	// nil means no minimum size
	MinSize *int `json:"min_size,omitempty"`

	// MaxAge filters groups younger than this duration
	// nil means no age filtering
	MaxAge *time.Duration `json:"max_age,omitempty"`

	// Limit limits the number of results (pagination)
	// 0 means no limit
	Limit int `json:"limit,omitempty"`

	// Offset skips this many results (pagination)
	// 0 means no offset
	Offset int `json:"offset,omitempty"`
}

// Matches checks if a group matches the filters.
func (f *GroupFilters) Matches(group *AlertGroup) bool {
	if f == nil {
		return true // No filters, match all
	}

	// Filter by state
	if f.State != nil && *f.State != group.Metadata.State {
		return false
	}

	// Filter by min size
	if f.MinSize != nil && group.Size() < *f.MinSize {
		return false
	}

	// Filter by max age
	if f.MaxAge != nil {
		cutoff := time.Now().Add(-*f.MaxAge)
		if group.Metadata.CreatedAt.Before(cutoff) {
			return false
		}
	}

	return true
}

// GroupMetrics contains snapshot metrics about alert groups.
//
// Used for monitoring and Prometheus scraping.
type GroupMetrics struct {
	// ActiveGroups is the total number of active groups
	ActiveGroups int `json:"active_groups"`

	// AlertsPerGroup maps group key to number of alerts
	AlertsPerGroup map[string]int `json:"alerts_per_group"`

	// SizeDistribution shows distribution of group sizes
	// Keys: "1-10", "11-50", "51-100", "101-500", "501-1000", "1000+"
	SizeDistribution map[string]int `json:"size_distribution"`

	// Operations contains operation counters
	// Keys: "add", "remove", "cleanup"
	Operations map[string]int64 `json:"operations"`

	// Timestamp when metrics were collected
	Timestamp time.Time `json:"timestamp"`
}

// GroupStats contains detailed statistics about group management.
//
// 150% Enhancement: Extended statistics for advanced monitoring.
type GroupStats struct {
	// Total operations
	TotalAdds    int64 `json:"total_adds"`
	TotalRemoves int64 `json:"total_removes"`
	TotalCleanups int64 `json:"total_cleanups"`
	TotalUpdates  int64 `json:"total_updates"`

	// Last cleanup time
	LastCleanupTime time.Time `json:"last_cleanup_time"`

	// Current state
	ActiveGroups    int `json:"active_groups"`
	TotalAlerts     int `json:"total_alerts"`
	FiringAlerts    int `json:"firing_alerts"`
	ResolvedAlerts  int `json:"resolved_alerts"`

	// Memory estimate (approximate)
	EstimatedMemoryBytes int64 `json:"estimated_memory_bytes"`

	// Snapshot timestamp
	Timestamp time.Time `json:"timestamp"`
}

// AlertGroupManager manages the lifecycle of alert groups.
//
// This interface defines operations for creating, updating, and querying alert groups.
// Implementations must be thread-safe and support concurrent access.
//
// Thread-safety: All methods are safe for concurrent use from multiple goroutines.
type AlertGroupManager interface {
	// === Lifecycle Management ===

	// AddAlertToGroup adds an alert to a group identified by groupKey.
	// If the group doesn't exist, it creates a new group.
	// If the alert is already in the group, it updates it.
	//
	// Parameters:
	//   - ctx: context for cancellation and timeouts
	//   - alert: the alert to add (must have fingerprint)
	//   - groupKey: the group key (from GroupKeyGenerator)
	//
	// Returns:
	//   - *AlertGroup: the updated group
	//   - error: InvalidAlertError, StorageError
	//
	// Thread-safe: Yes
	AddAlertToGroup(ctx context.Context, alert *core.Alert, groupKey GroupKey) (*AlertGroup, error)

	// RemoveAlertFromGroup removes an alert from a group.
	// If the group becomes empty, it automatically deletes the group.
	//
	// Parameters:
	//   - ctx: context
	//   - fingerprint: fingerprint of the alert to remove
	//   - groupKey: the group key
	//
	// Returns:
	//   - bool: true if alert was removed, false if not found
	//   - error: GroupNotFoundError, StorageError
	//
	// Thread-safe: Yes
	RemoveAlertFromGroup(ctx context.Context, fingerprint string, groupKey GroupKey) (bool, error)

	// UpdateGroupState recalculates and updates the state of a group.
	// Called automatically by AddAlertToGroup and RemoveAlertFromGroup.
	//
	// Parameters:
	//   - ctx: context
	//   - groupKey: the group key
	//
	// Returns:
	//   - *AlertGroup: the updated group with new state
	//   - error: GroupNotFoundError, StorageError
	//
	// Thread-safe: Yes
	UpdateGroupState(ctx context.Context, groupKey GroupKey) (*AlertGroup, error)

	// CleanupExpiredGroups deletes groups that are inactive for more than maxAge.
	//
	// A group is considered expired if:
	//  1. All alerts are resolved AND resolved_at > maxAge ago, OR
	//  2. updated_at > maxAge ago (no activity)
	//
	// Parameters:
	//   - ctx: context with timeout
	//   - maxAge: maximum age for inactive groups (e.g., 24h)
	//
	// Returns:
	//   - int: number of groups deleted
	//   - error: StorageError
	//
	// Thread-safe: Yes
	CleanupExpiredGroups(ctx context.Context, maxAge time.Duration) (int, error)

	// === Query Operations ===

	// GetGroup retrieves a group by its key.
	//
	// Returns:
	//   - *AlertGroup: the group (shallow copy to prevent external mutation)
	//   - error: GroupNotFoundError, StorageError
	//
	// Thread-safe: Yes
	GetGroup(ctx context.Context, groupKey GroupKey) (*AlertGroup, error)

	// ListGroups returns a list of all groups matching the filters.
	//
	// Parameters:
	//   - ctx: context
	//   - filters: optional filters (state, minSize, maxAge, limit, offset)
	//
	// Returns:
	//   - []*AlertGroup: list of groups (shallow copies)
	//   - error: StorageError
	//
	// Thread-safe: Yes
	ListGroups(ctx context.Context, filters *GroupFilters) ([]*AlertGroup, error)

	// GetGroupByFingerprint finds the group containing an alert with the given fingerprint.
	//
	// 150% Enhancement: Reverse lookup using fingerprint index.
	//
	// Returns:
	//   - GroupKey: the group key
	//   - *AlertGroup: the group
	//   - error: GroupNotFoundError (if alert not in any group)
	//
	// Thread-safe: Yes
	GetGroupByFingerprint(ctx context.Context, fingerprint string) (GroupKey, *AlertGroup, error)

	// === Metrics & Observability ===

	// GetMetrics returns current snapshot metrics about alert groups.
	// Used for Prometheus scraping and monitoring dashboards.
	//
	// Returns:
	//   - *GroupMetrics: snapshot of group metrics
	//   - error: StorageError
	//
	// Thread-safe: Yes
	GetMetrics(ctx context.Context) (*GroupMetrics, error)

	// GetStats returns detailed statistics about group operations.
	//
	// 150% Enhancement: Extended statistics for advanced monitoring.
	//
	// Returns:
	//   - *GroupStats: detailed statistics
	//   - error: StorageError
	//
	// Thread-safe: Yes
	GetStats(ctx context.Context) (*GroupStats, error)
}

// DefaultGroupManagerConfig holds configuration for DefaultGroupManager.
type DefaultGroupManagerConfig struct {
	// KeyGenerator generates group keys from alert labels (required, from TN-122)
	KeyGenerator *GroupKeyGenerator

	// Config is the grouping configuration (required, from TN-121)
	Config *GroupingConfig

	// Storage persists alert groups with distributed state (required, from TN-125)
	// Typically StorageManager with Redis primary + Memory fallback
	Storage GroupStorage

	// TimerManager manages group timers (optional, from TN-124)
	// If nil, timer functionality is disabled (backwards compatible)
	TimerManager GroupTimerManager

	// Logger for structured logging (optional, defaults to slog.Default())
	Logger *slog.Logger

	// Metrics for Prometheus integration (optional, recommended for production)
	Metrics *metrics.BusinessMetrics
}

// Validate checks if the configuration is valid.
func (c *DefaultGroupManagerConfig) Validate() error {
	if c.KeyGenerator == nil {
		return fmt.Errorf("key generator is required")
	}
	if c.Config == nil {
		return fmt.Errorf("grouping config is required")
	}
	if c.Storage == nil {
		return fmt.Errorf("storage is required (TN-125)")
	}
	return nil
}
