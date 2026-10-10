package grouping

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/require"
)

// Tests for the group timer chain (PROD-GROUPING-DEFAULT): a group is flushed
// every group_interval, as upstream does, and repeat_interval is only the age
// after which an unchanged group is notified again.

// failingLoadTimerStorage makes LoadTimer fail on demand, as a Redis outage
// would, and can hide a timer record to simulate a lost key.
type failingLoadTimerStorage struct {
	*InMemoryTimerStorage
	failLoad atomic.Bool
	failSave atomic.Bool
}

func (s *failingLoadTimerStorage) LoadTimer(ctx context.Context, groupKey GroupKey) (*GroupTimer, error) {
	if s.failLoad.Load() {
		return nil, errors.New("redis: i/o timeout")
	}
	return s.InMemoryTimerStorage.LoadTimer(ctx, groupKey)
}

func (s *failingLoadTimerStorage) SaveTimer(ctx context.Context, timer *GroupTimer) error {
	if s.failSave.Load() {
		return errors.New("redis: i/o timeout")
	}
	return s.InMemoryTimerStorage.SaveTimer(ctx, timer)
}

type chainTimings struct {
	wait, interval, repeat time.Duration
}

type chainFixture struct {
	manager      *DefaultGroupManager
	timers       *DefaultTimerManager
	timerStorage *failingLoadTimerStorage
	publisher    *perTargetPublisher
	notifyLog    *notifyDedupLog
}

func newChainFixture(t *testing.T, timings chainTimings) *chainFixture {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &chainFixture{
		timerStorage: &failingLoadTimerStorage{InMemoryTimerStorage: NewInMemoryTimerStorage(logger)},
		publisher:    &perTargetPublisher{},
		notifyLog:    newNotifyDedupLog(),
	}

	var err error
	f.timers, err = NewDefaultTimerManager(TimerManagerConfig{Storage: f.timerStorage, Logger: logger})
	require.NoError(t, err)

	f.manager, err = NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: NewGroupKeyGenerator(),
		Config: &GroupingConfig{Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{Duration: timings.wait},
			GroupInterval:  &Duration{Duration: timings.interval},
			RepeatInterval: &Duration{Duration: timings.repeat},
		}},
		Logger:       logger,
		Storage:      NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: logger}),
		TimerManager: f.timers,
		Publisher:    f.publisher,
		NotifyLog:    f.notifyLog,
	})
	require.NoError(t, err)
	require.NoError(t, f.timers.SetGroupManager(f.manager))
	t.Cleanup(func() { _ = f.timers.Shutdown(context.Background()) })
	return f
}

func (f *chainFixture) add(t *testing.T, name string, status core.AlertStatus) {
	t.Helper()
	_, err := f.manager.AddAlertToGroup(context.Background(), createTestAlert(name, status, perTargetLabels), perTargetGroupKey)
	require.NoError(t, err)
}

func (f *chainFixture) expiredTimers(t *testing.T) int64 {
	t.Helper()
	stats, err := f.timers.GetStats(context.Background())
	require.NoError(t, err)
	return stats.ExpiredTimers
}

func (f *chainFixture) notifications() int { return len(f.publisher.sentTo("t")) }

func (f *chainFixture) timerType(t *testing.T) TimerType {
	t.Helper()
	timer, err := f.timers.GetTimer(context.Background(), perTargetGroupKey)
	require.NoError(t, err)
	return timer.TimerType
}

// fastTick flushes every 20ms and never reaches repeat_interval.
var fastTick = chainTimings{wait: 20 * time.Millisecond, interval: 20 * time.Millisecond, repeat: time.Hour}

// slowTimers never fire on their own: the test drives the callbacks.
var slowTimers = chainTimings{wait: time.Hour, interval: time.Hour, repeat: 4 * time.Hour}

const chainWait = 5 * time.Second

// The group keeps being flushed every group_interval. Before, the timer after
// the first group_interval fire was a repeat_interval one, and the chain went
// quiet for hours.
func TestGroupIntervalChain_KeepsFlushingEveryGroupInterval(t *testing.T) {
	f := newChainFixture(t, fastTick)
	f.add(t, "a", core.StatusFiring)

	require.Eventually(t, func() bool { return f.expiredTimers(t) >= 5 }, chainWait, 5*time.Millisecond,
		"with repeat_interval of 1h only a group_interval chain can fire five times")
	require.Equal(t, GroupIntervalTimer, f.timerType(t))
}

func TestGroupIntervalChain_UnchangedGroupIsNotifiedOnce(t *testing.T) {
	f := newChainFixture(t, fastTick)
	f.add(t, "a", core.StatusFiring)

	require.Eventually(t, func() bool { return f.expiredTimers(t) >= 5 }, chainWait, 5*time.Millisecond)
	require.Equal(t, 1, f.notifications(), "flushes within repeat_interval must not repeat the notification")
}

// The reason for the change: an alert joining an already-notified group used
// to wait for repeat_interval (4h by default).
func TestGroupIntervalChain_LateAlertGoesOutOnTheNextFlush(t *testing.T) {
	f := newChainFixture(t, fastTick)
	f.add(t, "a", core.StatusFiring)
	// Past the first group_interval fire, where the old chain switched to
	// repeat_interval.
	require.Eventually(t, func() bool { return f.expiredTimers(t) >= 2 }, chainWait, 5*time.Millisecond)
	require.Equal(t, 1, f.notifications())

	f.add(t, "b", core.StatusFiring)

	require.Eventually(t, func() bool { return f.notifications() == 2 }, chainWait, 5*time.Millisecond)
	both := alertSetSignature([]*core.Alert{
		createTestAlert("a", core.StatusFiring, perTargetLabels),
		createTestAlert("b", core.StatusFiring, perTargetLabels),
	})
	require.Equal(t, both, f.publisher.sentTo("t")[1])
}

func TestGroupIntervalChain_ResolveGoesOutAndTheGroupIsGone(t *testing.T) {
	f := newChainFixture(t, fastTick)
	f.add(t, "a", core.StatusFiring)
	require.Eventually(t, func() bool { return f.expiredTimers(t) >= 2 }, chainWait, 5*time.Millisecond)

	f.add(t, "a", core.StatusResolved)

	require.Eventually(t, func() bool { return f.notifications() == 2 }, chainWait, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		scheduled, err := f.timers.HasTimer(context.Background(), perTargetGroupKey)
		return err == nil && !scheduled
	}, chainWait, 5*time.Millisecond, "a group with nothing left must not keep a timer")

	_, err := f.manager.GetGroup(context.Background(), perTargetGroupKey)
	require.Error(t, err, "the fully resolved group is removed")

	fired := f.expiredTimers(t)
	time.Sleep(5 * fastTick.interval)
	require.Equal(t, fired, f.expiredTimers(t), "no further fires for the removed group")
	require.Equal(t, 2, f.notifications())
}

// An unchanged group is reminded at the first flush after repeat_interval.
func TestGroupIntervalChain_ReminderAfterRepeatInterval(t *testing.T) {
	f := newChainFixture(t, slowTimers)
	f.add(t, "a", core.StatusFiring)
	ctx := context.Background()
	group, err := f.manager.GetGroup(ctx, perTargetGroupKey)
	require.NoError(t, err)
	signature := currentSignature(t, f.manager)

	// Notified three hours ago: within repeat_interval (4h), nothing to send.
	require.NoError(t, f.notifyLog.RecordSent(ctx, perTargetGroupKey, "t", signature, time.Now().Add(-3*time.Hour), slowTimers.repeat))
	require.NoError(t, f.manager.onGroupIntervalExpired(ctx, perTargetGroupKey, GroupIntervalTimer, group))
	require.Zero(t, f.notifications())

	// Notified five hours ago: the reminder is due.
	require.NoError(t, f.notifyLog.RecordSent(ctx, perTargetGroupKey, "t", signature, time.Now().Add(-5*time.Hour), slowTimers.repeat))
	require.NoError(t, f.manager.onGroupIntervalExpired(ctx, perTargetGroupKey, GroupIntervalTimer, group))
	require.Equal(t, 1, f.notifications())
}

// A repeat_interval timer saved in Redis by a version from before the change
// still fires, and moves the group to the group_interval chain.
func TestGroupIntervalChain_LegacyRepeatTimerMovesToGroupInterval(t *testing.T) {
	f := newChainFixture(t, slowTimers)
	f.add(t, "a", core.StatusFiring)
	ctx := context.Background()
	group, err := f.manager.GetGroup(ctx, perTargetGroupKey)
	require.NoError(t, err)
	_, err = f.timers.CancelTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)

	require.NoError(t, f.manager.onRepeatIntervalExpired(ctx, perTargetGroupKey, RepeatIntervalTimer, group))

	require.Equal(t, 1, f.notifications())
	require.Equal(t, GroupIntervalTimer, f.timerType(t))
}

func TestHasTimer(t *testing.T) {
	ctx := context.Background()

	t.Run("no timer", func(t *testing.T) {
		f := newChainFixture(t, slowTimers)
		scheduled, err := f.timers.HasTimer(ctx, perTargetGroupKey)
		require.NoError(t, err)
		require.False(t, scheduled)
	})

	t.Run("timer held by this replica", func(t *testing.T) {
		f := newChainFixture(t, slowTimers)
		f.add(t, "a", core.StatusFiring)
		scheduled, err := f.timers.HasTimer(ctx, perTargetGroupKey)
		require.NoError(t, err)
		require.True(t, scheduled)
	})

	t.Run("timer held by another replica", func(t *testing.T) {
		f := newChainFixture(t, slowTimers)
		require.NoError(t, f.timerStorage.SaveTimer(ctx, &GroupTimer{
			GroupKey:  perTargetGroupKey,
			TimerType: GroupIntervalTimer,
			Duration:  time.Hour,
			StartedAt: time.Now(),
			ExpiresAt: time.Now().Add(time.Hour),
			State:     TimerStateActive,
		}))
		scheduled, err := f.timers.HasTimer(ctx, perTargetGroupKey)
		require.NoError(t, err)
		require.True(t, scheduled)
	})

	t.Run("storage error", func(t *testing.T) {
		f := newChainFixture(t, slowTimers)
		f.timerStorage.failLoad.Store(true)
		_, err := f.timers.HasTimer(ctx, perTargetGroupKey)
		require.Error(t, err)
	})
}

// A group left without a timer (a failed re-arm, a timer key lost from Redis)
// used to stay silent for good. The next alert joining it arms group_wait.
func TestAddAlertToGroup_RearmsGroupWithoutTimer(t *testing.T) {
	f := newChainFixture(t, slowTimers)
	f.add(t, "a", core.StatusFiring)
	ctx := context.Background()
	_, err := f.timers.CancelTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)

	f.add(t, "b", core.StatusFiring)

	require.Equal(t, GroupWaitTimer, f.timerType(t))
}

// An alert joining a group that has a timer must not restart it: the group
// would never flush under a steady stream of alerts.
func TestAddAlertToGroup_DoesNotRestartExistingTimer(t *testing.T) {
	f := newChainFixture(t, slowTimers)
	f.add(t, "a", core.StatusFiring)
	ctx := context.Background()
	before, err := f.timers.GetTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)

	f.add(t, "b", core.StatusFiring)

	after, err := f.timers.GetTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)
	require.Equal(t, before.ExpiresAt, after.ExpiresAt)
}

// When it cannot be told whether another replica holds the timer, nothing is
// armed: a second chain for the same group would be worse than waiting.
func TestAddAlertToGroup_LeavesTimerAloneWhenCheckFails(t *testing.T) {
	f := newChainFixture(t, slowTimers)
	f.add(t, "a", core.StatusFiring)
	ctx := context.Background()
	_, err := f.timers.CancelTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)
	f.timerStorage.failLoad.Store(true)

	f.add(t, "b", core.StatusFiring)

	f.timerStorage.failLoad.Store(false)
	scheduled, err := f.timers.HasTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)
	require.False(t, scheduled)
}

// A failed re-arm does not fail the ingest; the next alert tries again.
func TestAddAlertToGroup_FailedRearmIsRetriedByTheNextAlert(t *testing.T) {
	f := newChainFixture(t, slowTimers)
	f.add(t, "a", core.StatusFiring)
	ctx := context.Background()
	_, err := f.timers.CancelTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)

	f.timerStorage.failSave.Store(true)
	f.add(t, "b", core.StatusFiring)
	scheduled, err := f.timers.HasTimer(ctx, perTargetGroupKey)
	require.NoError(t, err)
	require.False(t, scheduled)

	f.timerStorage.failSave.Store(false)
	f.add(t, "c", core.StatusFiring)
	require.Equal(t, GroupWaitTimer, f.timerType(t))
}
