// Package grouping provides timer management for alert group notifications.
//
// This file defines the data models for group timers, supporting Alertmanager-compatible
// timing for notifications (group_wait, group_interval, repeat_interval).
//
// TN-124: Group Wait/Interval Timers
// Target Quality: 150%
// Date: 2025-11-03
package grouping

import (
	"fmt"
	"time"
)

// TimerType defines the type of alert group timer.
//
// Three timer types are supported, matching Alertmanager behavior:
//   - GroupWaitTimer: delay before first notification (default: 30s)
//   - GroupIntervalTimer: interval between notifications when group changes (default: 5m)
//   - RepeatIntervalTimer: interval between notifications without changes (default: 4h)
type TimerType string

const (
	// GroupWaitTimer delays the first notification for a new group.
	// This allows batching of alerts that arrive in quick succession.
	//
	// Example: If 5 alerts for the same service arrive within 30 seconds,
	// they will all be included in a single notification instead of 5 separate ones.
	//
	// Default: 30 seconds
	GroupWaitTimer TimerType = "group_wait"

	// GroupIntervalTimer sets the interval between notifications when a group changes.
	// When a new alert is added to an existing group, this timer is reset.
	//
	// Example: After sending a notification, if a new alert arrives, wait 5 minutes
	// before sending another notification (allowing more alerts to batch).
	//
	// Default: 5 minutes
	GroupIntervalTimer TimerType = "group_interval"

	// RepeatIntervalTimer sets the interval between notifications when a group
	// hasn't changed. This provides periodic reminders for ongoing issues.
	//
	// Example: Send a reminder notification every 4 hours even if no new alerts arrived.
	//
	// Default: 4 hours
	RepeatIntervalTimer TimerType = "repeat_interval"
)

// String returns the string representation of TimerType.
func (t TimerType) String() string {
	return string(t)
}

// Validate checks if the timer type is valid.
//
// Returns InvalidTimerTypeError if the type is not one of the three supported values.
func (t TimerType) Validate() error {
	switch t {
	case GroupWaitTimer, GroupIntervalTimer, RepeatIntervalTimer:
		return nil
	default:
		return &InvalidTimerTypeError{Type: string(t)}
	}
}

// IsValid returns true if the timer type is valid.
// Convenience method for boolean checks without error handling.
func (t TimerType) IsValid() bool {
	return t.Validate() == nil
}

// TimerState represents the current state of a group timer.
type TimerState string

const (
	// TimerStateActive indicates the timer is currently running and waiting to expire.
	TimerStateActive TimerState = "active"

	// TimerStateExpired indicates the timer has expired and triggered its callback.
	TimerStateExpired TimerState = "expired"

	// TimerStateCancelled indicates the timer was manually cancelled before expiration.
	// This happens when a group is deleted or the timer is replaced.
	TimerStateCancelled TimerState = "cancelled"

	// TimerStateMissed indicates the timer expired while the service was down.
	// The notification was delayed beyond the intended time.
	//
	// 150% Enhancement: Tracking missed timers for observability.
	TimerStateMissed TimerState = "missed"
)

// String returns the string representation of TimerState.
func (s TimerState) String() string {
	return string(s)
}

// GroupTimer represents a timer for an alert group.
//
// Timers control when notifications are sent for alert groups, implementing
// Alertmanager-compatible timing behavior.
//
// Thread-safety: GroupTimer instances are immutable after creation (except Metadata).
// The TimerManager handles synchronization when modifying timers.
type GroupTimer struct {
	// GroupKey is the unique identifier for the alert group.
	// From TN-122 (Group Key Generator).
	GroupKey GroupKey `json:"group_key"`

	// TimerType indicates what kind of timer this is.
	// Must be one of: group_wait, group_interval, repeat_interval.
	TimerType TimerType `json:"timer_type"`

	// Duration is how long the timer should wait before firing.
	// Must be positive, typically ranges from 30s to 4h.
	Duration time.Duration `json:"duration"`

	// StartedAt is when the timer was created/started.
	// Used for metrics and debugging.
	StartedAt time.Time `json:"started_at"`

	// ExpiresAt is when the timer will fire (StartedAt + Duration).
	// Used for restoration after service restart.
	ExpiresAt time.Time `json:"expires_at"`

	// Receiver is the notification receiver name (optional).
	// From Alertmanager routing configuration.
	Receiver string `json:"receiver,omitempty"`

	// State is the current state of the timer.
	State TimerState `json:"state"`

	// Metadata contains additional information about the timer.
	// 150% Enhancement: Extended metadata for observability and HA.
	Metadata *TimerMetadata `json:"metadata,omitempty"`
}

// TimerMetadata contains additional information about a timer.
//
// 150% Enhancement: Metadata supports advanced features like version tracking,
// distributed locking, and reset counting.
type TimerMetadata struct {
	// Version is incremented each time the timer is modified.
	// Used for optimistic locking in Redis (future: TN-125).
	Version int64 `json:"version"`

	// CreatedBy identifies which service instance created the timer.
	// Format: "{hostname}:{pid}" or pod name in Kubernetes.
	// Used for debugging multi-instance deployments.
	CreatedBy string `json:"created_by,omitempty"`

	// ResetCount tracks how many times the timer was reset.
	// High reset counts indicate frequent group changes (may need longer group_interval).
	ResetCount int `json:"reset_count"`

	// LastResetAt is when the timer was last reset (if ResetCount > 0).
	LastResetAt *time.Time `json:"last_reset_at,omitempty"`

	// LockID is the distributed lock ID for exactly-once delivery.
	// When a timer expires, a lock is acquired to ensure only one instance
	// processes the expiration in multi-instance deployments.
	LockID string `json:"lock_id,omitempty"`
}

// IsExpired returns true if the timer has passed its expiration time.
//
// This is a convenience method for checking if a timer should have fired.
// Used during timer restoration to identify missed timers.
func (t *GroupTimer) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// RemainingDuration returns how long until the timer expires.
//
// Returns 0 if the timer has already expired.
// Useful for debugging and monitoring.
func (t *GroupTimer) RemainingDuration() time.Duration {
	remaining := time.Until(t.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Clone creates a deep copy of the GroupTimer.
//
// 150% Enhancement: Thread-safety enhancement.
// Returns a new instance to prevent external mutation of shared timers.
func (t *GroupTimer) Clone() *GroupTimer {
	if t == nil {
		return nil
	}

	clone := &GroupTimer{
		GroupKey:  t.GroupKey,
		TimerType: t.TimerType,
		Duration:  t.Duration,
		StartedAt: t.StartedAt,
		ExpiresAt: t.ExpiresAt,
		Receiver:  t.Receiver,
		State:     t.State,
	}

	// Deep copy metadata if present
	if t.Metadata != nil {
		clone.Metadata = &TimerMetadata{
			Version:     t.Metadata.Version,
			CreatedBy:   t.Metadata.CreatedBy,
			ResetCount:  t.Metadata.ResetCount,
			LockID:      t.Metadata.LockID,
		}
		if t.Metadata.LastResetAt != nil {
			resetTime := *t.Metadata.LastResetAt
			clone.Metadata.LastResetAt = &resetTime
		}
	}

	return clone
}

// String returns a human-readable representation of the timer.
//
// Format: "GroupTimer{key=..., type=..., state=..., expires_in=...}"
// Useful for logging and debugging.
func (t *GroupTimer) String() string {
	remaining := t.RemainingDuration()
	return fmt.Sprintf("GroupTimer{key=%s, type=%s, state=%s, expires_in=%s}",
		t.GroupKey, t.TimerType, t.State, remaining)
}

// Validate checks if the timer has valid field values.
//
// Returns error if any field is invalid:
//   - GroupKey cannot be empty
//   - TimerType must be valid
//   - Duration must be positive
//   - StartedAt and ExpiresAt must be consistent
func (t *GroupTimer) Validate() error {
	if t.GroupKey == "" {
		return fmt.Errorf("group key cannot be empty")
	}

	if err := t.TimerType.Validate(); err != nil {
		return err
	}

	if t.Duration <= 0 {
		return &InvalidDurationError{Duration: t.Duration}
	}

	if t.StartedAt.IsZero() {
		return fmt.Errorf("started_at cannot be zero")
	}

	if t.ExpiresAt.IsZero() {
		return fmt.Errorf("expires_at cannot be zero")
	}

	// ExpiresAt should equal StartedAt + Duration (with small tolerance for clock skew)
	expectedExpires := t.StartedAt.Add(t.Duration)
	diff := t.ExpiresAt.Sub(expectedExpires)
	if diff < -time.Second || diff > time.Second {
		return fmt.Errorf("expires_at inconsistent with started_at + duration (diff: %v)", diff)
	}

	return nil
}

// TimerFilters defines filtering options for listing timers.
//
// 150% Enhancement: Advanced filtering for operational queries.
type TimerFilters struct {
	// TimerType filters by specific timer type.
	// If nil, all types are included.
	TimerType *TimerType `json:"timer_type,omitempty"`

	// ExpiresWithin filters timers expiring within the specified duration.
	// Example: ExpiresWithin = 5*time.Minute returns only timers expiring in next 5 minutes.
	ExpiresWithin *time.Duration `json:"expires_within,omitempty"`

	// Receiver filters by notification receiver.
	// If nil, all receivers are included.
	Receiver *string `json:"receiver,omitempty"`

	// Limit restricts the number of results returned.
	// If 0, no limit is applied.
	Limit int `json:"limit,omitempty"`

	// Offset skips the first N results (for pagination).
	// Used together with Limit for paginated queries.
	Offset int `json:"offset,omitempty"`
}

// Matches returns true if the timer matches the filters.
//
// Used for in-memory filtering when database-level filtering isn't available.
func (f *TimerFilters) Matches(timer *GroupTimer) bool {
	if f == nil {
		return true
	}

	// Filter by timer type
	if f.TimerType != nil && timer.TimerType != *f.TimerType {
		return false
	}

	// Filter by expires within
	if f.ExpiresWithin != nil {
		expiresAt := time.Until(timer.ExpiresAt)
		if expiresAt < 0 || expiresAt > *f.ExpiresWithin {
			return false
		}
	}

	// Filter by receiver
	if f.Receiver != nil && timer.Receiver != *f.Receiver {
		return false
	}

	return true
}

// TimerStats contains statistics about timer operations.
//
// 150% Enhancement: Comprehensive statistics for monitoring and capacity planning.
type TimerStats struct {
	// ActiveTimers is the count of currently active timers by type.
	// Key: TimerType, Value: count of active timers.
	ActiveTimers map[TimerType]int `json:"active_timers"`

	// ExpiredTimers is the total count of timers that have expired.
	// Incremented each time a timer fires successfully.
	ExpiredTimers int64 `json:"expired_timers"`

	// CancelledTimers is the total count of timers that were cancelled.
	// Cancelled timers are those stopped before expiration.
	CancelledTimers int64 `json:"cancelled_timers"`

	// ResetCount is the total number of timer resets.
	// High reset counts may indicate frequent group changes.
	ResetCount int64 `json:"reset_count"`

	// MissedTimers is the count of timers that expired while service was down.
	// Tracked during RestoreTimers operation after service restart.
	MissedTimers int64 `json:"missed_timers"`

	// AverageDuration is the average duration of timers by type.
	// Useful for understanding typical timing patterns.
	AverageDuration map[TimerType]time.Duration `json:"average_duration"`

	// Timestamp is when these statistics were captured.
	Timestamp time.Time `json:"timestamp"`
}

// Clone creates a deep copy of TimerStats.
func (s *TimerStats) Clone() *TimerStats {
	if s == nil {
		return nil
	}

	clone := &TimerStats{
		ExpiredTimers:   s.ExpiredTimers,
		CancelledTimers: s.CancelledTimers,
		ResetCount:      s.ResetCount,
		MissedTimers:    s.MissedTimers,
		Timestamp:       s.Timestamp,
	}

	// Deep copy maps
	clone.ActiveTimers = make(map[TimerType]int, len(s.ActiveTimers))
	for k, v := range s.ActiveTimers {
		clone.ActiveTimers[k] = v
	}

	clone.AverageDuration = make(map[TimerType]time.Duration, len(s.AverageDuration))
	for k, v := range s.AverageDuration {
		clone.AverageDuration[k] = v
	}

	return clone
}

// TotalActiveTimers returns the sum of all active timers across types.
func (s *TimerStats) TotalActiveTimers() int {
	total := 0
	for _, count := range s.ActiveTimers {
		total += count
	}
	return total
}

// TotalOperations returns the total number of timer operations (started, expired, cancelled).
func (s *TimerStats) TotalOperations() int64 {
	// Note: started count = expired + cancelled + active
	active := int64(s.TotalActiveTimers())
	return s.ExpiredTimers + s.CancelledTimers + active
}
