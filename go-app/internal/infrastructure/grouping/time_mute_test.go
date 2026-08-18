package grouping

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/routing/timeinterval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alwaysMatchInterval matches every time.Time (Interval{} has every field
// empty — see timeinterval.Interval's doc comment).
func alwaysMatchInterval() timeinterval.TimeInterval {
	return timeinterval.TimeInterval{
		Name:          "always",
		TimeIntervals: []timeinterval.Interval{{}},
	}
}

// neverMatchInterval matches no time.Time reachable by this test suite: a
// years constraint pinned to 1900 is safely in the past regardless of when
// the test actually runs.
func neverMatchInterval() timeinterval.TimeInterval {
	return timeinterval.TimeInterval{
		Name: "never",
		TimeIntervals: []timeinterval.Interval{
			{Years: []timeinterval.YearRange{{Begin: 1900, End: 1900}}},
		},
	}
}

// fakeTimeIntervalLookup implements GroupTimeIntervalLookup (task 3.2) for
// tests: a mutable name->definition map, so tests can simulate a hot config
// reload by mutating it between two publishGroupAlerts calls.
type fakeTimeIntervalLookup struct {
	mu        sync.Mutex
	intervals map[string]timeinterval.TimeInterval
}

func newFakeTimeIntervalLookup(entries map[string]timeinterval.TimeInterval) *fakeTimeIntervalLookup {
	f := &fakeTimeIntervalLookup{intervals: make(map[string]timeinterval.TimeInterval)}
	for name, ti := range entries {
		f.intervals[name] = ti
	}
	return f
}

func (f *fakeTimeIntervalLookup) GetTimeInterval(name string) (timeinterval.TimeInterval, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ti, ok := f.intervals[name]
	return ti, ok
}

func (f *fakeTimeIntervalLookup) set(name string, ti timeinterval.TimeInterval) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.intervals[name] = ti
}

// createTestManagerWithTimeMute builds a manager wired with a publisher and
// a GroupTimeIntervalLookup (task 3.2 notify-stage TimeMute step). Mirrors
// createTestManagerWithChain (notify_chain_test.go) but exposes the
// TimeMute-specific hook.
func createTestManagerWithTimeMute(t *testing.T, pub GroupNotificationPublisher, lookup GroupTimeIntervalLookup) *DefaultGroupManager {
	t.Helper()
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{time.Hour}, // chain tests call publishGroupAlerts directly — no timer firing needed
			GroupInterval:  &Duration{time.Hour},
			RepeatInterval: &Duration{time.Hour},
		},
	}

	storage := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})

	manager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator:       keyGen,
		Config:             config,
		Logger:             slog.Default(),
		Storage:            storage,
		Publisher:          pub,
		TimeIntervalLookup: lookup,
	})
	require.NoError(t, err)
	return manager
}

// === isTimeMuted unit tests (direct, no timer/publish plumbing needed) ===

func TestIsTimeMuted_NoLookupWired_NeverMutes(t *testing.T) {
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, nil)
	names := &TimeIntervalNames{Mute: []string{"always"}}
	assert.False(t, manager.isTimeMuted("gk", names, time.Now()))
}

func TestIsTimeMuted_EmptyNames_NeverMutes(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"always": alwaysMatchInterval()})
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	assert.False(t, manager.isTimeMuted("gk", nil, time.Now()))
	assert.False(t, manager.isTimeMuted("gk", &TimeIntervalNames{}, time.Now()))
}

func TestIsTimeMuted_MuteMatches_Muted(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"w": alwaysMatchInterval()})
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	names := &TimeIntervalNames{Mute: []string{"w"}}
	assert.True(t, manager.isTimeMuted("gk", names, time.Now()))
}

func TestIsTimeMuted_MuteDoesNotMatch_NotMuted(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"w": neverMatchInterval()})
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	names := &TimeIntervalNames{Mute: []string{"w"}}
	assert.False(t, manager.isTimeMuted("gk", names, time.Now()))
}

func TestIsTimeMuted_ActiveOutsideWindow_Muted(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"w": neverMatchInterval()})
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	names := &TimeIntervalNames{Active: []string{"w"}}
	assert.True(t, manager.isTimeMuted("gk", names, time.Now()), "outside every active window must mute")
}

func TestIsTimeMuted_ActiveInsideWindow_NotMuted(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"w": alwaysMatchInterval()})
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	names := &TimeIntervalNames{Active: []string{"w"}}
	assert.False(t, manager.isTimeMuted("gk", names, time.Now()), "inside an active window must not mute")
}

func TestIsTimeMuted_BothSet_MuteWins(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{
		"active-window": alwaysMatchInterval(),
		"mute-window":   alwaysMatchInterval(),
	})
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	names := &TimeIntervalNames{Active: []string{"active-window"}, Mute: []string{"mute-window"}}
	assert.True(t, manager.isTimeMuted("gk", names, time.Now()),
		"currently inside the active window AND the mute window: mute must win")
}

func TestIsTimeMuted_UndefinedMuteName_FailOpenTreatedAsNotMatched(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(nil) // "gone" is not defined
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	names := &TimeIntervalNames{Mute: []string{"gone"}}
	assert.False(t, manager.isTimeMuted("gk", names, time.Now()),
		"an undefined mute interval name must fail open (not matched), never abort delivery")
}

func TestIsTimeMuted_UndefinedActiveName_FailOpenCountsAsNoMatch(t *testing.T) {
	lookup := newFakeTimeIntervalLookup(nil) // "gone" is not defined
	manager := createTestManagerWithTimeMute(t, &mockPublisher{}, lookup)

	names := &TimeIntervalNames{Active: []string{"gone"}}
	// An undefined active_time_intervals name contributes no match, so
	// "no active window matched" holds -> muted (same as outside window).
	assert.True(t, manager.isTimeMuted("gk", names, time.Now()))
}

// === publishGroupAlerts integration: TimeMute step (Inhibit -> Silence ->
// TimeMute -> Dedup) ===

func TestPublishGroupAlerts_TimeMuted_NoPublishNoRecordSent(t *testing.T) {
	pub := &mockPublisher{}
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"maintenance": alwaysMatchInterval()})
	manager := createTestManagerWithTimeMute(t, pub, lookup)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey, WithMuteTimeIntervals([]string{"maintenance"}, nil))
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	assert.Empty(t, pub.calls(), "a currently-active mute window must suppress the whole group notification")
}

func TestPublishGroupAlerts_TimeMuteWindowEnds_NextFirePublishes(t *testing.T) {
	pub := &mockPublisher{}
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"maintenance": alwaysMatchInterval()})
	manager := createTestManagerWithTimeMute(t, pub, lookup)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey, WithMuteTimeIntervals([]string{"maintenance"}, nil))
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group) // muted: no publish, no RecordSent
	require.Empty(t, pub.calls())

	// The mute window ends (the interval no longer matches "now" — same
	// name, redefinition simulates upstream's group_interval tick landing
	// after the window closed).
	lookup.set("maintenance", neverMatchInterval())

	manager.publishGroupAlerts(ctx, group) // next tick: must deliver

	assert.Len(t, pub.calls(), 1, "once the mute window ends, the next scheduled fire must publish")
}

func TestPublishGroupAlerts_HotReloadSwapsIntervalDefinition_NextFireUsesIt(t *testing.T) {
	pub := &mockPublisher{}
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"biz-hours": neverMatchInterval()})
	manager := createTestManagerWithTimeMute(t, pub, lookup)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey, WithMuteTimeIntervals([]string{"biz-hours"}, nil))
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group) // not muted yet: publishes
	require.Len(t, pub.calls(), 1)

	// Simulate a config hot-reload that redefines "biz-hours" to now cover
	// the current moment. DefaultGroupManager never caches the lookup's
	// answer — the very next fire must observe the new definition.
	lookup.set("biz-hours", alwaysMatchInterval())

	manager.publishGroupAlerts(ctx, group) // group unchanged, but now muted

	assert.Len(t, pub.calls(), 1, "hot-reloaded interval definition must apply on the very next fire")
}

func TestPublishGroupAlerts_ActiveTimeIntervals_OutsideWindowSuppressed(t *testing.T) {
	pub := &mockPublisher{}
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"biz-hours": neverMatchInterval()})
	manager := createTestManagerWithTimeMute(t, pub, lookup)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey, WithMuteTimeIntervals(nil, []string{"biz-hours"}))
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	assert.Empty(t, pub.calls(), "outside every active_time_intervals window must suppress")
}

func TestPublishGroupAlerts_ActiveTimeIntervals_InsideWindowPublishes(t *testing.T) {
	pub := &mockPublisher{}
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{"biz-hours": alwaysMatchInterval()})
	manager := createTestManagerWithTimeMute(t, pub, lookup)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey, WithMuteTimeIntervals(nil, []string{"biz-hours"}))
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	assert.Len(t, pub.calls(), 1, "inside an active_time_intervals window must publish")
}

func TestPublishGroupAlerts_BothMuteAndActiveSet_MuteWinsSuppressed(t *testing.T) {
	pub := &mockPublisher{}
	lookup := newFakeTimeIntervalLookup(map[string]timeinterval.TimeInterval{
		"biz-hours":   alwaysMatchInterval(),
		"maintenance": alwaysMatchInterval(),
	})
	manager := createTestManagerWithTimeMute(t, pub, lookup)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey,
		WithMuteTimeIntervals([]string{"maintenance"}, []string{"biz-hours"}))
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	assert.Empty(t, pub.calls(), "inside the active window but also inside a mute window: mute must win")
}

func TestPublishGroupAlerts_NoTimeIntervalNames_StillPublishes(t *testing.T) {
	pub := &mockPublisher{}
	lookup := newFakeTimeIntervalLookup(nil)
	manager := createTestManagerWithTimeMute(t, pub, lookup)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	// No WithMuteTimeIntervals option at all — the common case (route
	// references no time_intervals).
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	assert.Len(t, pub.calls(), 1)
}
