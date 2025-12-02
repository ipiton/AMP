package grouping

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimerType_Validate tests timer type validation
func TestTimerType_Validate(t *testing.T) {
	tests := []struct {
		name      string
		timerType TimerType
		wantErr   bool
	}{
		{
			name:      "valid_group_wait",
			timerType: GroupWaitTimer,
			wantErr:   false,
		},
		{
			name:      "valid_group_interval",
			timerType: GroupIntervalTimer,
			wantErr:   false,
		},
		{
			name:      "valid_repeat_interval",
			timerType: RepeatIntervalTimer,
			wantErr:   false,
		},
		{
			name:      "invalid_empty",
			timerType: TimerType(""),
			wantErr:   true,
		},
		{
			name:      "invalid_unknown",
			timerType: TimerType("unknown_type"),
			wantErr:   true,
		},
		{
			name:      "invalid_typo",
			timerType: TimerType("group_waitt"),
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.timerType.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.IsType(t, &InvalidTimerTypeError{}, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestTimerType_IsValid tests IsValid convenience method
func TestTimerType_IsValid(t *testing.T) {
	assert.True(t, GroupWaitTimer.IsValid())
	assert.True(t, GroupIntervalTimer.IsValid())
	assert.True(t, RepeatIntervalTimer.IsValid())
	assert.False(t, TimerType("invalid").IsValid())
}

// TestTimerType_String tests string representation
func TestTimerType_String(t *testing.T) {
	assert.Equal(t, "group_wait", GroupWaitTimer.String())
	assert.Equal(t, "group_interval", GroupIntervalTimer.String())
	assert.Equal(t, "repeat_interval", RepeatIntervalTimer.String())
}

// TestTimerState_String tests timer state string representation
func TestTimerState_String(t *testing.T) {
	assert.Equal(t, "active", TimerStateActive.String())
	assert.Equal(t, "expired", TimerStateExpired.String())
	assert.Equal(t, "cancelled", TimerStateCancelled.String())
	assert.Equal(t, "missed", TimerStateMissed.String())
}

// TestGroupTimer_IsExpired tests expiration checking
func TestGroupTimer_IsExpired(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "expired_1_hour_ago",
			expiresAt: now.Add(-1 * time.Hour),
			want:      true,
		},
		{
			name:      "expired_1_second_ago",
			expiresAt: now.Add(-1 * time.Second),
			want:      true,
		},
		{
			name:      "expires_in_1_second",
			expiresAt: now.Add(1 * time.Second),
			want:      false,
		},
		{
			name:      "expires_in_1_hour",
			expiresAt: now.Add(1 * time.Hour),
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timer := &GroupTimer{
				ExpiresAt: tt.expiresAt,
			}
			assert.Equal(t, tt.want, timer.IsExpired())
		})
	}
}

// TestGroupTimer_RemainingDuration tests remaining duration calculation
func TestGroupTimer_RemainingDuration(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		expiresAt time.Time
		wantZero  bool
	}{
		{
			name:      "expired_returns_zero",
			expiresAt: now.Add(-1 * time.Hour),
			wantZero:  true,
		},
		{
			name:      "future_returns_positive",
			expiresAt: now.Add(1 * time.Hour),
			wantZero:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timer := &GroupTimer{
				ExpiresAt: tt.expiresAt,
			}
			remaining := timer.RemainingDuration()
			if tt.wantZero {
				assert.Equal(t, time.Duration(0), remaining)
			} else {
				assert.Greater(t, remaining, time.Duration(0))
			}
		})
	}
}

// TestGroupTimer_Clone tests deep copying
func TestGroupTimer_Clone(t *testing.T) {
	now := time.Now()
	lastResetAt := now.Add(-10 * time.Minute)

	original := &GroupTimer{
		GroupKey:  "test-group",
		TimerType: GroupWaitTimer,
		Duration:  30 * time.Second,
		StartedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
		Receiver:  "slack",
		State:     TimerStateActive,
		Metadata: &TimerMetadata{
			Version:     1,
			CreatedBy:   "instance-1",
			ResetCount:  2,
			LastResetAt: &lastResetAt,
			LockID:      "lock-123",
		},
	}

	// Clone
	cloned := original.Clone()

	// Verify deep copy
	assert.Equal(t, original.GroupKey, cloned.GroupKey)
	assert.Equal(t, original.TimerType, cloned.TimerType)
	assert.Equal(t, original.Duration, cloned.Duration)
	assert.Equal(t, original.State, cloned.State)

	// Verify metadata is deep copied
	require.NotNil(t, cloned.Metadata)
	assert.Equal(t, original.Metadata.Version, cloned.Metadata.Version)
	assert.Equal(t, original.Metadata.ResetCount, cloned.Metadata.ResetCount)

	// Verify LastResetAt is deep copied
	require.NotNil(t, cloned.Metadata.LastResetAt)
	assert.Equal(t, *original.Metadata.LastResetAt, *cloned.Metadata.LastResetAt)

	// Mutate original - should not affect clone
	original.Metadata.ResetCount = 5
	original.Metadata.LastResetAt = &now
	assert.Equal(t, 2, cloned.Metadata.ResetCount)
	assert.NotEqual(t, now, *cloned.Metadata.LastResetAt)
}

// TestGroupTimer_Clone_NilSafe tests cloning nil timer
func TestGroupTimer_Clone_NilSafe(t *testing.T) {
	var timer *GroupTimer
	cloned := timer.Clone()
	assert.Nil(t, cloned)
}

// TestGroupTimer_Clone_NilMetadata tests cloning timer without metadata
func TestGroupTimer_Clone_NilMetadata(t *testing.T) {
	original := &GroupTimer{
		GroupKey:  "test-group",
		TimerType: GroupWaitTimer,
		Metadata:  nil,
	}

	cloned := original.Clone()
	assert.NotNil(t, cloned)
	assert.Nil(t, cloned.Metadata)
}

// TestGroupTimer_String tests string representation
func TestGroupTimer_String(t *testing.T) {
	now := time.Now()
	timer := &GroupTimer{
		GroupKey:  "alertname=HighCPU",
		TimerType: GroupWaitTimer,
		State:     TimerStateActive,
		ExpiresAt: now.Add(30 * time.Second),
	}

	str := timer.String()
	assert.Contains(t, str, "alertname=HighCPU")
	assert.Contains(t, str, "group_wait")
	assert.Contains(t, str, "active")
}

// TestGroupTimer_Validate tests timer validation
func TestGroupTimer_Validate(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		timer   *GroupTimer
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid_timer",
			timer: &GroupTimer{
				GroupKey:  "test-group",
				TimerType: GroupWaitTimer,
				Duration:  30 * time.Second,
				StartedAt: now,
				ExpiresAt: now.Add(30 * time.Second),
				State:     TimerStateActive,
			},
			wantErr: false,
		},
		{
			name: "empty_group_key",
			timer: &GroupTimer{
				GroupKey:  "",
				TimerType: GroupWaitTimer,
				Duration:  30 * time.Second,
				StartedAt: now,
				ExpiresAt: now.Add(30 * time.Second),
			},
			wantErr: true,
			errMsg:  "group key cannot be empty",
		},
		{
			name: "invalid_timer_type",
			timer: &GroupTimer{
				GroupKey:  "test-group",
				TimerType: TimerType("invalid"),
				Duration:  30 * time.Second,
				StartedAt: now,
				ExpiresAt: now.Add(30 * time.Second),
			},
			wantErr: true,
		},
		{
			name: "zero_duration",
			timer: &GroupTimer{
				GroupKey:  "test-group",
				TimerType: GroupWaitTimer,
				Duration:  0,
				StartedAt: now,
				ExpiresAt: now,
			},
			wantErr: true,
		},
		{
			name: "negative_duration",
			timer: &GroupTimer{
				GroupKey:  "test-group",
				TimerType: GroupWaitTimer,
				Duration:  -30 * time.Second,
				StartedAt: now,
				ExpiresAt: now.Add(-30 * time.Second),
			},
			wantErr: true,
		},
		{
			name: "zero_started_at",
			timer: &GroupTimer{
				GroupKey:  "test-group",
				TimerType: GroupWaitTimer,
				Duration:  30 * time.Second,
				StartedAt: time.Time{},
				ExpiresAt: now.Add(30 * time.Second),
			},
			wantErr: true,
			errMsg:  "started_at cannot be zero",
		},
		{
			name: "zero_expires_at",
			timer: &GroupTimer{
				GroupKey:  "test-group",
				TimerType: GroupWaitTimer,
				Duration:  30 * time.Second,
				StartedAt: now,
				ExpiresAt: time.Time{},
			},
			wantErr: true,
			errMsg:  "expires_at cannot be zero",
		},
		{
			name: "inconsistent_expires_at",
			timer: &GroupTimer{
				GroupKey:  "test-group",
				TimerType: GroupWaitTimer,
				Duration:  30 * time.Second,
				StartedAt: now,
				ExpiresAt: now.Add(60 * time.Second), // Should be 30s, not 60s
			},
			wantErr: true,
			errMsg:  "expires_at inconsistent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.timer.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestTimerFilters_Matches tests filter matching
func TestTimerFilters_Matches(t *testing.T) {
	now := time.Now()

	timer := &GroupTimer{
		GroupKey:  "test-group",
		TimerType: GroupWaitTimer,
		Receiver:  "slack",
		ExpiresAt: now.Add(5 * time.Minute),
	}

	tests := []struct {
		name    string
		filters *TimerFilters
		want    bool
	}{
		{
			name:    "nil_filters_matches_all",
			filters: nil,
			want:    true,
		},
		{
			name: "matching_timer_type",
			filters: &TimerFilters{
				TimerType: ptrTimerType(GroupWaitTimer),
			},
			want: true,
		},
		{
			name: "non_matching_timer_type",
			filters: &TimerFilters{
				TimerType: ptrTimerType(GroupIntervalTimer),
			},
			want: false,
		},
		{
			name: "matching_receiver",
			filters: &TimerFilters{
				Receiver: ptrString("slack"),
			},
			want: true,
		},
		{
			name: "non_matching_receiver",
			filters: &TimerFilters{
				Receiver: ptrString("pagerduty"),
			},
			want: false,
		},
		{
			name: "expires_within_matches",
			filters: &TimerFilters{
				ExpiresWithin: ptrDuration(10 * time.Minute),
			},
			want: true,
		},
		{
			name: "expires_within_not_matches",
			filters: &TimerFilters{
				ExpiresWithin: ptrDuration(1 * time.Minute),
			},
			want: false,
		},
		{
			name: "multiple_filters_all_match",
			filters: &TimerFilters{
				TimerType: ptrTimerType(GroupWaitTimer),
				Receiver:  ptrString("slack"),
			},
			want: true,
		},
		{
			name: "multiple_filters_one_not_matches",
			filters: &TimerFilters{
				TimerType: ptrTimerType(GroupWaitTimer),
				Receiver:  ptrString("pagerduty"),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.filters.Matches(timer)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestTimerStats_Clone tests stats deep copying
func TestTimerStats_Clone(t *testing.T) {
	now := time.Now()
	original := &TimerStats{
		ActiveTimers: map[TimerType]int{
			GroupWaitTimer:     5,
			GroupIntervalTimer: 10,
		},
		ExpiredTimers:   100,
		CancelledTimers: 50,
		ResetCount:      20,
		MissedTimers:    2,
		AverageDuration: map[TimerType]time.Duration{
			GroupWaitTimer:     30 * time.Second,
			GroupIntervalTimer: 5 * time.Minute,
		},
		Timestamp: now,
	}

	cloned := original.Clone()

	// Verify deep copy
	assert.Equal(t, original.ExpiredTimers, cloned.ExpiredTimers)
	assert.Equal(t, original.ActiveTimers[GroupWaitTimer], cloned.ActiveTimers[GroupWaitTimer])

	// Mutate original - should not affect clone
	original.ActiveTimers[GroupWaitTimer] = 100
	assert.Equal(t, 5, cloned.ActiveTimers[GroupWaitTimer])

	original.AverageDuration[GroupWaitTimer] = 1 * time.Hour
	assert.Equal(t, 30*time.Second, cloned.AverageDuration[GroupWaitTimer])
}

// TestTimerStats_Clone_NilSafe tests cloning nil stats
func TestTimerStats_Clone_NilSafe(t *testing.T) {
	var stats *TimerStats
	cloned := stats.Clone()
	assert.Nil(t, cloned)
}

// TestTimerStats_TotalActiveTimers tests total count calculation
func TestTimerStats_TotalActiveTimers(t *testing.T) {
	stats := &TimerStats{
		ActiveTimers: map[TimerType]int{
			GroupWaitTimer:      5,
			GroupIntervalTimer:  10,
			RepeatIntervalTimer: 15,
		},
	}

	total := stats.TotalActiveTimers()
	assert.Equal(t, 30, total)
}

// TestTimerStats_TotalActiveTimers_Empty tests total with no active timers
func TestTimerStats_TotalActiveTimers_Empty(t *testing.T) {
	stats := &TimerStats{
		ActiveTimers: map[TimerType]int{},
	}

	total := stats.TotalActiveTimers()
	assert.Equal(t, 0, total)
}

// TestTimerStats_TotalOperations tests total operations calculation
func TestTimerStats_TotalOperations(t *testing.T) {
	stats := &TimerStats{
		ActiveTimers: map[TimerType]int{
			GroupWaitTimer: 10,
		},
		ExpiredTimers:   50,
		CancelledTimers: 20,
	}

	// Total = expired + cancelled + active
	total := stats.TotalOperations()
	assert.Equal(t, int64(80), total)
}

// Helper functions

func ptrTimerType(t TimerType) *TimerType {
	return &t
}

func ptrString(s string) *string {
	return &s
}

func ptrDuration(d time.Duration) *time.Duration {
	return &d
}
