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
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
	"github.com/ipiton/AMP/pkg/metrics" //nolint:staticcheck // SA1019: deprecated pkg/metrics kept until v2 migration (v2 lacks BusinessMetrics)
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

	// Timings holds optional per-group route timing overrides (task 2.4,
	// alertmanager-parity), captured from the matched route's
	// RoutingDecision at group-CREATION time (AddAlertToGroup, via
	// WithGroupTimings). nil means "use the grouping config's root
	// Route.group_wait/group_interval/repeat_interval for every duration"
	// — the pre-2.4 behavior, and still what non-route-aware callers (e.g.
	// direct grouping-package tests) get by default.
	//
	// This closes a task 2.3 carry-over gap: AddAlertToGroup previously had
	// no way to honor a non-root route's own timings, so every group used
	// the root route's group_wait regardless of which route actually
	// matched the alert.
	Timings *GroupTimings `json:"timings,omitempty"`

	// TimeIntervalNames holds optional per-group mute_time_intervals/
	// active_time_intervals route references (task 3.2, alertmanager-parity),
	// captured from the matched route's RoutingDecision at group-CREATION
	// time (AddAlertToGroup, via WithMuteTimeIntervals) — same capture
	// timing and non-update-on-existing-group semantics as Timings above.
	// nil means "this route referenced no time_intervals," the common
	// case, in which the notify-chain's TimeMute step is a no-op for this
	// group regardless of whether a TimeIntervalLookup is wired.
	TimeIntervalNames *TimeIntervalNames `json:"time_interval_names,omitempty"`

	// Version is used for optimistic locking (future: Redis storage in TN-125)
	Version int64 `json:"version"`
}

// GroupTimings holds per-group group_wait/group_interval/repeat_interval
// overrides (task 2.4). See GroupMetadata.Timings for when it's set and why.
type GroupTimings struct {
	GroupWait      time.Duration `json:"group_wait,omitempty"`
	GroupInterval  time.Duration `json:"group_interval,omitempty"`
	RepeatInterval time.Duration `json:"repeat_interval,omitempty"`
}

// Clone returns a shallow copy of t, or nil if t is nil.
func (t *GroupTimings) Clone() *GroupTimings {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

// TimeIntervalNames holds a group's own mute_time_intervals/
// active_time_intervals NAME references (task 3.2) — not the resolved
// timeinterval.TimeInterval definitions themselves, which are looked up
// fresh at TimeMute time via GroupTimeIntervalLookup so a config hot
// reload is picked up without re-creating the group. See
// GroupMetadata.TimeIntervalNames for when this is captured.
type TimeIntervalNames struct {
	Mute   []string `json:"mute,omitempty"`
	Active []string `json:"active,omitempty"`
}

// Clone returns a deep copy of n, or nil if n is nil.
func (n *TimeIntervalNames) Clone() *TimeIntervalNames {
	if n == nil {
		return nil
	}
	return &TimeIntervalNames{
		Mute:   append([]string(nil), n.Mute...),
		Active: append([]string(nil), n.Active...),
	}
}

// IsEmpty reports whether n carries no interval names at all (nil n counts
// as empty). Used by the TimeMute step to skip evaluation entirely for the
// common case of a route that references no time_intervals.
func (n *TimeIntervalNames) IsEmpty() bool {
	return n == nil || (len(n.Mute) == 0 && len(n.Active) == 0)
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

	// Copy per-group timing overrides (task 2.4)
	metadataCopy.Timings = g.Metadata.Timings.Clone()

	// Copy per-group time-interval name references (task 3.2)
	metadataCopy.TimeIntervalNames = g.Metadata.TimeIntervalNames.Clone()

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
		switch alert.Status {
		case core.StatusFiring:
			firingCount++
		case core.StatusResolved:
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
	TotalAdds     int64 `json:"total_adds"`
	TotalRemoves  int64 `json:"total_removes"`
	TotalCleanups int64 `json:"total_cleanups"`
	TotalUpdates  int64 `json:"total_updates"`

	// Last cleanup time
	LastCleanupTime time.Time `json:"last_cleanup_time"`

	// Current state
	ActiveGroups   int `json:"active_groups"`
	TotalAlerts    int `json:"total_alerts"`
	FiringAlerts   int `json:"firing_alerts"`
	ResolvedAlerts int `json:"resolved_alerts"`

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
	//   - opts: optional per-call overrides (task 2.4) — currently only
	//     WithGroupTimings, applied ONLY when this call creates a brand-new
	//     group; ignored when adding to an existing group (that group's
	//     timers already run with whatever timings applied at its own
	//     creation).
	//
	// Returns:
	//   - *AlertGroup: the updated group
	//   - error: InvalidAlertError, StorageError
	//
	// Thread-safe: Yes
	AddAlertToGroup(ctx context.Context, alert *core.Alert, groupKey GroupKey, opts ...AddAlertOption) (*AlertGroup, error)

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

// TargetPublishOutcome reports one target's publish outcome for a single
// group notification (task fwb: wire-level group batching + per-target
// nflog). GroupNotificationPublisher.PublishGroup returns one of these per
// target it actually attempted delivery to — a target skipped via the
// skipTarget callback (already delivered this cycle) is omitted entirely,
// not reported as an outcome.
type TargetPublishOutcome struct {
	// Target is the publishing target's name (core.PublishingTarget.Name),
	// used as the nflog dedup key's target segment — see GroupNotifyLog.
	Target string

	// Success reports whether this target's job was successfully ENQUEUED
	// onto the publishing queue (infrastructure/publishing.PublishingQueue.
	// SubmitGroup returning nil) — NOT whether the target's HTTP endpoint
	// confirmed receipt. Actual delivery (the HTTP POST, retries, DLQ) runs
	// asynchronously on the queue's own worker pool after this call
	// returns; a webhook 500/timeout at that later stage is invisible here
	// and is NOT retried by publishGroupAlerts's skipTarget mechanism (see
	// its call site in manager_impl.go for the accepted-gap note and the
	// follow-up this should eventually close: recording on the queue job's
	// own completion callback instead of on enqueue). DefaultGroupManager.
	// publishGroupAlerts records an nflog entry only for Success == true
	// outcomes, so a target whose ENQUEUE failed (queue full, shutting
	// down) is retried on the group's next scheduled timer fire, while one
	// that enqueued successfully is skipped (via skipTarget) on that retry
	// even if the actual HTTP delivery later fails.
	Success bool
}

// GroupNotificationPublisher publishes a resolved batch of alerts belonging
// to ONE alert group as a single logical group notification (task 2.4,
// alertmanager-parity), when a group timer fires — one call per group
// notification, not one call per alert as the pre-2.4 PublishToAll loop did.
//
// alerts have already been through the notify-stage chain
// (Inhibit -> Silence -> Dedup, see publishGroupAlerts) by the time this is
// called; PublishGroup only needs to deliver them. receiver is the matched
// route's receiver name (parsed from the group key — see
// receiverFromGroupKey), passed through so the implementation can do
// receiver-scoped target selection (task 1.5's PublishToTargets).
//
// groupKey is the group's own key (group.Key) as a plain string — passed
// through (not as the grouping.GroupKey type) so implementations can forward
// it into the wire payload's "groupKey" field (upstream Alertmanager webhook
// shape) without this package depending on infrastructure/publishing, and
// vice versa.
//
// groupLabels is the resolved {label_name: value} map for this group's own
// GroupMetadata.GroupBy names (review finding 1, fwb fix round 1) — e.g.
// {"alertname": "HighCPU", "cluster": "prod"} for group_by:
// [alertname, cluster]. The caller (publishGroupAlerts) computes this from
// the group's own Metadata.GroupBy plus alerts[0].Labels: GroupKeyGenerator
// guarantees every alert in a group shares identical values for its
// GroupBy names, so any alert in the (already filtered) set is a valid
// source, and an empty/nil GroupBy yields an empty map. Passed through as a
// plain map (same non-coupling pattern as groupKey/receiver) so
// implementations can forward it into the wire payload's "groupLabels"
// field (upstream Alertmanager webhook shape) without a dependency in
// either direction between this package and infrastructure/publishing.
//
// skipTarget implements task fwb's per-target notification-log dedup:
// PublishGroup's implementation resolves its own receiver-scoped target list
// internally (as it always has) and MUST call skipTarget(target.Name) once
// per candidate target BEFORE attempting delivery to it. A true result means
// "this target already received this exact alert set within
// repeat_interval — do not send, and do not include it in the returned
// outcomes." This is what makes a retry after a partial failure resend ONLY
// the targets that failed last time: the ones that already succeeded report
// skipTarget == true on the next fire (their nflog entry was recorded) and
// are silently excluded.
//
// This interface is intentionally NOT a subset of services.Publisher (which
// is strictly per-alert) to avoid import cycles between infrastructure/
// grouping and core/services, and because the batch signature is the point.
// application.ApplicationPublishingAdapter and application.MetricsOnlyPublisher
// both implement it.
type GroupNotificationPublisher interface {
	PublishGroup(ctx context.Context, groupKey string, alerts []*core.Alert, receiver string, groupLabels map[string]string, skipTarget func(target string) bool) ([]TargetPublishOutcome, error)
}

// ErrDeliveryNotConfirmed is the sentinel a GroupNotificationPublisher must
// return (wrapped is fine) when it deliberately did NOT attempt real delivery
// — the degraded/metrics-only runtime modes. It says: "this call is not a
// failure, but nothing was delivered either."
//
// WHY IT EXISTS (final review finding 4 — silent notification loss):
// publishGroupAlerts records the notification in the SHARED, cross-replica
// notification log (RecordSent, TTL = repeat_interval) whenever PublishGroup
// returns nil. application.MetricsOnlyPublisher — the publisher installed when
// publishing.enabled is false, in the lite profile, or after a transient
// Kubernetes failure during startup — returned plain nil, so a replica running
// in that mode poisoned the shared nflog for every healthy replica: they saw a
// send that never happened and skipped the group for a full repeat_interval.
// This is the exact twin of the ApplicationPublishingAdapter empty-results bug
// fixed in task 2.4.
//
// A plain error would also stop RecordSent, but would log at Error and count as
// a publish failure on every group fire in what is a deliberate configuration
// (`publishing.enabled: false`). The sentinel lets publishGroupAlerts
// distinguish "not delivered on purpose" from "delivery broke".
var ErrDeliveryNotConfirmed = errors.New("group notification not delivered: publisher confirmed no delivery (metrics-only/degraded mode)")

// AddAlertOption customizes a single AddAlertToGroup call (task 2.4).
// Currently only used to carry a matched route's per-route timings
// (RoutingDecision) onto a group created by that call — see
// WithGroupTimings.
type AddAlertOption func(*addAlertOptions)

type addAlertOptions struct {
	timings           *GroupTimings
	timeIntervalNames *TimeIntervalNames
}

// WithGroupTimings overrides group_wait/group_interval/repeat_interval for
// a group CREATED by this AddAlertToGroup call, sourced from the matched
// route's RoutingDecision (task 2.4). Has no effect when the alert lands in
// an already-existing group (see AddAlertToGroup's doc comment on why).
func WithGroupTimings(groupWait, groupInterval, repeatInterval time.Duration) AddAlertOption {
	return func(o *addAlertOptions) {
		o.timings = &GroupTimings{
			GroupWait:      groupWait,
			GroupInterval:  groupInterval,
			RepeatInterval: repeatInterval,
		}
	}
}

// WithMuteTimeIntervals carries a matched route's own
// mute_time_intervals/active_time_intervals NAMES (task 3.2) onto a group
// CREATED by this AddAlertToGroup call, sourced from the matched route's
// RoutingDecision — same capture-at-creation-only semantics as
// WithGroupTimings (has no effect when the alert lands in an
// already-existing group). A nil/empty mute and active pair still records
// an explicit (non-nil) *TimeIntervalNames so the intent "this route has no
// time_intervals" is distinguishable from "no option was passed at all";
// either way TimeIntervalNames.IsEmpty() makes the TimeMute step a no-op.
func WithMuteTimeIntervals(mute, active []string) AddAlertOption {
	return func(o *addAlertOptions) {
		o.timeIntervalNames = &TimeIntervalNames{Mute: mute, Active: active}
	}
}

// GroupInhibitionChecker is the send-time inhibition read path used by the
// notify-stage chain (task 2.4, Step 1: Inhibit). Re-checked when a group
// timer fires (current state), not just at alert ingest — an alert can
// become inhibited by a newer alert while it sits inside an already-open
// group, and must be dropped from the notification even though it was
// already grouped. inhibition.InhibitionMatcher satisfies this
// automatically (subset interface, same pattern as GroupNotificationPublisher).
type GroupInhibitionChecker interface {
	ShouldInhibit(ctx context.Context, targetAlert *core.Alert) (*inhibition.MatchResult, error)
}

// GroupSilenceChecker is the send-time silence read path used by the
// notify-stage chain (task 2.4, Step 2: Silence). memory.SilenceStore
// satisfies this automatically. Checked at group-timer-fire time so a
// silence created AFTER an alert entered its group still suppresses the
// notification (upstream Alertmanager silences are inherently a
// notify-time-only concept — there is no ingest-time silence check to
// diverge from).
type GroupSilenceChecker interface {
	HasActiveMatch(labels map[string]string, now time.Time) bool
}

// GroupTimeIntervalLookup is the send-time named-time_intervals definition
// lookup used by the notify-stage chain (task 3.2, Step 3: TimeMute — order
// Inhibit -> Silence -> TimeMute -> Dedup, matching upstream Alertmanager).
// Resolves a name captured on GroupMetadata.TimeIntervalNames (from the
// matched route at group-creation time) to its current
// timeinterval.TimeInterval definition.
//
// Must read the CURRENT config's index on every call, not a construction-
// time snapshot: a hot config reload can rename/redefine/delete a
// time_intervals entry, and the very next group-timer fire must see that
// change (businessrouting.RouteTree.GetTimeInterval / RouteTreeManager.
// GetTree satisfies this automatically — see the application package's
// wiring for the concrete adapter).
//
// ok=false (name not found) is NOT treated as an error by the TimeMute
// step — see DefaultGroupManager.isTimeMuted's doc comment for the
// documented fail-open decision (log + treat as "not matched", never abort
// delivery).
type GroupTimeIntervalLookup interface {
	GetTimeInterval(name string) (timeinterval.TimeInterval, bool)
}

// GroupNotifyLog is the notify-stage chain's Dedup step (task 2.4, Step 4;
// Redis-backed cross-replica variant added by task 6.1). It answers the
// same question upstream Alertmanager's nflog answers: "did we already
// send a notification for this exact alert set, for this group+receiver,
// within repeat_interval?" — and, since task 6.1, additionally arbitrates
// which of several concurrently-firing replicas is allowed to publish.
//
// Implementations:
//   - notifyDedupLog (dedup.go): single-process, in-memory. Always used in
//     the lite profile and as the standard-profile fallback when Redis is
//     unavailable at grouping-init time.
//   - RedisNotifyLog (redis_notify_log.go): Redis-backed, shared across
//     replicas. Selected for the standard profile when a live Redis cache
//     is available (mirrors task 2.2's GroupStorage selection).
//
// IsDuplicate/RecordSent/Forget carry ctx and return an error so a
// Redis-backed implementation can surface backend failures; callers must
// treat a non-nil error as "assume not a duplicate" (fail-open — see
// DefaultGroupManager.publishGroupAlerts), matching the chain's existing
// Inhibit/Silence fail-open posture.
//
// TryClaim/release (the *_ release func() error, mirroring
// TimerStorage.AcquireLock's existing convention) implement the
// cross-replica publish-claim protocol task 6.1 adds on top of dedup:
// claim -> check dedup log -> publish -> record -> release (or let the
// claim expire on crash). claimTTL must be short (seconds, NOT
// repeat_interval) so a crashed replica's claim self-heals quickly — see
// RedisNotifyLog.TryClaim's doc comment for the exact protocol. The
// in-memory implementation's TryClaim is a no-op that always succeeds:
// DefaultGroupManager's own per-GroupKey publishLocks (publish_lock.go)
// already fully serialize same-process callers, so no additional claim is
// needed there — only cross-process/cross-replica callers need Redis's
// claim.
type GroupNotifyLog interface {
	// IsDuplicate reports whether a notification for groupKey carrying
	// exactly this alert set was already sent to target within ttl (a
	// cutoff time: "sent after ttl" counts as duplicate). Does not record
	// anything.
	//
	// target scopes the check to one publishing target (task fwb,
	// alertmanager-parity wave 2 — mirrors upstream nflog's
	// group:receiver:integration key). Before this, a single entry covered
	// the whole group+receiver, so a partial delivery failure (M of N
	// targets) had no way to record "N succeeded" without also recording
	// the M that didn't — the next tick re-sent to every target,
	// duplicating the N that already got it. Per-target keys let
	// DefaultGroupManager.publishGroupAlerts (via the skipTarget callback
	// passed to GroupNotificationPublisher.PublishGroup) retry ONLY the
	// targets that failed.
	IsDuplicate(ctx context.Context, groupKey GroupKey, target string, signature string, ttl time.Time) (bool, error)

	// RecordSent records that a notification carrying signature for
	// groupKey was just sent successfully to target, at now. repeatInterval
	// is used by Redis-backed implementations as the entry's TTL (plus a
	// grace period) so an abandoned group's entry doesn't outlive it
	// indefinitely; the in-memory implementation ignores it (IsDuplicate
	// recomputes freshness against a caller-supplied cutoff on every call
	// instead).
	RecordSent(ctx context.Context, groupKey GroupKey, target string, signature string, now time.Time, repeatInterval time.Duration) error

	// Forget removes every per-target dedup entry for groupKey (task fwb:
	// there can be more than one now — one per target that ever received
	// this group's notification within its current repeat_interval). Called
	// when a group is deleted so the log doesn't grow unbounded independent
	// of active groups. Must NOT clear any in-flight TryClaim claim for
	// groupKey (fix round 1, Finding 2): Forget's callers
	// (RemoveAlertFromGroup/CleanupExpiredGroups) run under a different
	// lock than the claim -> check -> publish -> record sequence in
	// publishGroupAlerts, so a group can be deleted while another replica
	// still holds a live claim for it — deleting that claim early would let
	// a third replica race in and publish concurrently, reopening the
	// double-publish window TryClaim exists to close. Implementations must
	// let any claim self-expire via its own claimTTL instead.
	Forget(ctx context.Context, groupKey GroupKey) error

	// TryClaim attempts to acquire a short-lived cross-replica publish
	// claim for groupKey, valid for at most claimTTL. acquired == false
	// means another replica currently holds the claim — the caller must
	// skip this fire (the group's own group_interval/repeat_interval timer
	// will retry later). release must be called exactly once after a
	// successful (acquired == true) claim, as soon as the check-publish-
	// record sequence finishes (success OR failure) — do not hold it for
	// claimTTL. release is always non-nil when err == nil, even if
	// acquired == false (a no-op in that case).
	TryClaim(ctx context.Context, groupKey GroupKey, claimTTL time.Duration) (acquired bool, release func() error, err error)
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

	// Publisher sends notifications when group timers fire (optional).
	// If nil, timer callbacks only log — no alerts are sent (backwards
	// compatible). Can also be wired later via SetPublisher (task 2.4) —
	// see its doc comment for why (registry construction-order gap).
	Publisher GroupNotificationPublisher

	// InhibitionChecker is the notify-chain's Inhibit step (task 2.4,
	// optional). If nil, the chain skips inhibition filtering entirely
	// (backwards compatible — same posture as Publisher/TimerManager being
	// optional).
	InhibitionChecker GroupInhibitionChecker

	// SilenceChecker is the notify-chain's Silence step (task 2.4,
	// optional). If nil, the chain skips silence filtering entirely.
	SilenceChecker GroupSilenceChecker

	// TimeIntervalLookup is the notify-chain's TimeMute step (task 3.2,
	// optional). If nil, the chain skips time-interval mute filtering
	// entirely (backwards compatible — same posture as InhibitionChecker/
	// SilenceChecker being optional).
	TimeIntervalLookup GroupTimeIntervalLookup

	// NotifyLog is the notify-chain's Dedup step + cross-replica publish
	// claim (task 2.4 Step 4, Redis-backed variant task 6.1). Optional: if
	// nil, defaults to a fresh in-memory notifyDedupLog (the task 2.4
	// behavior) — see NewDefaultGroupManager. Pass a *RedisNotifyLog in the
	// standard profile for cross-replica notification dedup; leave nil (or
	// pass a *notifyDedupLog) for a single-process/lite deployment.
	NotifyLog GroupNotifyLog

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
