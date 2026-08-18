package grouping

import (
	"context"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 12: AlertGroup.Metadata is a POINTER, and while every
// constructor in this package populates it, groups also arrive from paths that
// do not — a record rehydrated from a pre-metadata or hand-written JSON blob
// deserializes with Metadata == nil, and tests build groups literally.
// effectiveRepeatInterval already guarded against that; the notify chain
// (isTimeMuted), the three timer callbacks (startGroupIntervalTimer /
// startRepeatIntervalTimer arguments) and RedisGroupStorage's index score did
// not. A nil-metadata group therefore panicked inside a timer callback, which
// both lost that notification AND left the group with no further timer — the
// group went permanently silent.

// storeRawGroup writes group straight into storage, bypassing
// AddAlertToGroup's constructor (which would fill Metadata in).
func storeRawGroup(t *testing.T, m *DefaultGroupManager, group *AlertGroup) {
	t.Helper()
	require.NoError(t, m.storage.Store(context.Background(), group))
}

func nilMetadataGroup(key GroupKey) *AlertGroup {
	return &AlertGroup{
		Key: key,
		Alerts: map[string]*core.Alert{
			"fp_A1": createTestAlert("A1", core.StatusFiring, map[string]string{"alertname": "NilMetaAlert"}),
		},
		Metadata: nil, // the whole point of this test
	}
}

func TestNotifyChain_NilMetadataGroup_DoesNotPanic(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=NilMetaAlert")
	group := nilMetadataGroup(groupKey)

	require.NotPanics(t, func() {
		manager.publishGroupAlerts(ctx, group)
	})

	require.Len(t, pub.calls(), 1, "a metadata-less group must still be published, not dropped")
}

func TestTimerCallbacks_NilMetadataGroup_DoNotPanicAndKeepChainAlive(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name      string
		timerType TimerType
		invoke    func(m *DefaultGroupManager, key GroupKey, g *AlertGroup) error
	}{
		{
			name:      "group_wait",
			timerType: GroupWaitTimer,
			invoke: func(m *DefaultGroupManager, key GroupKey, g *AlertGroup) error {
				return m.onGroupWaitExpired(ctx, key, GroupWaitTimer, g)
			},
		},
		{
			name:      "group_interval",
			timerType: GroupIntervalTimer,
			invoke: func(m *DefaultGroupManager, key GroupKey, g *AlertGroup) error {
				return m.onGroupIntervalExpired(ctx, key, GroupIntervalTimer, g)
			},
		},
		{
			name:      "repeat_interval",
			timerType: RepeatIntervalTimer,
			invoke: func(m *DefaultGroupManager, key GroupKey, g *AlertGroup) error {
				return m.onRepeatIntervalExpired(ctx, key, RepeatIntervalTimer, g)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub := &mockPublisher{}
			manager := createTestManagerWithChain(t, pub, nil, nil)
			groupKey := GroupKey("receiver=default/alertname=NilMetaAlert")
			group := nilMetadataGroup(groupKey)
			storeRawGroup(t, manager, group)

			var err error
			require.NotPanics(t, func() {
				err = tc.invoke(manager, groupKey, group)
			})
			require.NoError(t, err)
			assert.Len(t, pub.calls(), 1, "the notification must go out despite missing metadata")
		})
	}
}

func TestGroupIndexScore_NilMetadataFallsBackToNow(t *testing.T) {
	before := float64(time.Now().Unix())

	score := groupIndexScore(&AlertGroup{Key: "k"})
	assert.GreaterOrEqual(t, score, before,
		"a metadata-less group must score as fresh, not be prematurely reaped from the index")

	assert.Equal(t, float64(0), groupIndexScore(&AlertGroup{
		Key:      "k",
		Metadata: &GroupMetadata{UpdatedAt: time.Unix(0, 0)},
	}), "a group WITH metadata must still score from its own UpdatedAt")
}

func TestGroupTimings_NilSafety(t *testing.T) {
	assert.Nil(t, groupTimings(nil))
	assert.Nil(t, groupTimings(&AlertGroup{Key: "k"}))
	assert.Nil(t, groupTimeIntervalNames(nil))
	assert.Nil(t, groupTimeIntervalNames(&AlertGroup{Key: "k"}))

	timings := &GroupTimings{RepeatInterval: 7 * time.Minute}
	assert.Same(t, timings, groupTimings(&AlertGroup{
		Key:      "k",
		Metadata: &GroupMetadata{Timings: timings},
	}))
}
