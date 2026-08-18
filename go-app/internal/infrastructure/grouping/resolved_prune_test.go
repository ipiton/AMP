package grouping

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 8: nothing ever pruned resolved alerts from a group.
// RemoveAlertFromGroup had no non-test caller, so a fully-resolved group kept
// re-publishing the SAME resolved notification on every repeat_interval —
// forever, until CleanupExpiredGroups eventually reaped it. Upstream
// Alertmanager's aggrGroup.flush deletes resolved alerts from the aggregation
// group right after a successful notify, so the resolved notification goes out
// exactly once.

func TestPublishGroupAlerts_FullyResolvedGroup_NotifiesOnceThenStops(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=ResolvingAlert")
	labels := map[string]string{"alertname": "ResolvingAlert"}

	// --- firing notification ---
	firing := createTestAlert("R1", core.StatusFiring, labels)
	_, err := manager.AddAlertToGroup(ctx, firing, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group)
	require.Len(t, pub.calls(), 1, "the firing notification must go out")

	// --- the alert resolves ---
	resolved := createTestAlert("R1", core.StatusResolved, labels)
	_, err = manager.AddAlertToGroup(ctx, resolved, groupKey)
	require.NoError(t, err)

	group, err = manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group)
	calls := pub.calls()
	require.Len(t, calls, 2, "the resolved notification must go out once")
	require.Len(t, calls[1], 1)
	assert.Equal(t, core.StatusResolved, calls[1][0].Status)

	// --- the group must now be gone, so no repeat is even possible ---
	_, err = manager.GetGroup(ctx, groupKey)
	require.Error(t, err, "a fully-resolved, already-notified group must be deleted (upstream aggrGroup.flush semantics)")
	assert.False(t, manager.groupStillExists(ctx, groupKey))

	// Firing again after the group was reaped must start a NEW group cleanly
	// (i.e. pruning must not have corrupted the fingerprint index).
	refiring := createTestAlert("R1", core.StatusFiring, labels)
	_, err = manager.AddAlertToGroup(ctx, refiring, groupKey)
	require.NoError(t, err)
	group, err = manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	require.Len(t, group.Alerts, 1)
}

// TestPublishGroupAlerts_PartiallyResolvedGroup_KeepsFiringAlerts pins the
// other half of upstream's semantics: only the RESOLVED alerts are dropped; the
// still-firing ones stay so the group keeps reminding about them.
func TestPublishGroupAlerts_PartiallyResolvedGroup_KeepsFiringAlerts(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=MixedAlert")
	labels := map[string]string{"alertname": "MixedAlert"}

	stillFiring := createTestAlert("M1", core.StatusFiring, labels)
	nowResolved := createTestAlert("M2", core.StatusResolved, labels)
	_, err := manager.AddAlertToGroup(ctx, stillFiring, groupKey)
	require.NoError(t, err)
	_, err = manager.AddAlertToGroup(ctx, nowResolved, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	require.Len(t, group.Alerts, 2)

	manager.publishGroupAlerts(ctx, group)
	require.Len(t, pub.calls(), 1)

	group, err = manager.GetGroup(ctx, groupKey)
	require.NoError(t, err, "the group must survive: one of its alerts is still firing")
	require.Len(t, group.Alerts, 1)
	_, firingKept := group.Alerts["fp_M1"]
	assert.True(t, firingKept, "the still-firing alert must be kept")
	_, resolvedDropped := group.Alerts["fp_M2"]
	assert.False(t, resolvedDropped, "the resolved alert must be pruned after being announced")
}

// labelSilenceChecker suppresses any alert carrying the configured label pair,
// standing in for an active silence at send time.
type labelSilenceChecker struct {
	label string
	value string
}

func (c *labelSilenceChecker) HasActiveMatch(labels map[string]string, _ time.Time) bool {
	return labels[c.label] == c.value
}

// TestPublishGroupAlerts_SuppressedResolvedAlertIsNotPruned guards the
// precondition: pruning only applies to alerts actually SENT. An alert dropped
// by inhibition/silence was never announced as resolved, so forgetting it would
// mean nobody is ever told.
func TestPublishGroupAlerts_SuppressedResolvedAlertIsNotPruned(t *testing.T) {
	pub := &mockPublisher{}
	// The silenced alert never reaches the publisher. A SILENCE (not
	// inhibition) is used deliberately: filterInhibited only considers FIRING
	// alerts, so a resolved alert can never be inhibited.
	manager := createTestManagerWithChain(t, pub, nil, &labelSilenceChecker{label: "quiet", value: "yes"})
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=SuppressedAlert")
	labels := map[string]string{"alertname": "SuppressedAlert"}
	silencedLabels := map[string]string{"alertname": "SuppressedAlert", "quiet": "yes"}

	_, err := manager.AddAlertToGroup(ctx, createTestAlert("S1", core.StatusFiring, labels), groupKey)
	require.NoError(t, err)
	_, err = manager.AddAlertToGroup(ctx, createTestAlert("S2", core.StatusResolved, silencedLabels), groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group)

	calls := pub.calls()
	require.Len(t, calls, 1)
	require.Len(t, calls[0], 1, "sanity: the silenced alert was not sent")

	group, err = manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	_, stillThere := group.Alerts["fp_S2"]
	assert.True(t, stillThere,
		"a resolved alert that was SUPPRESSED (never announced) must not be pruned")
}

// TestOnRepeatIntervalExpired_FullyResolvedGroup_DoesNotRescheduleTimer is the
// callback-level consequence: after pruning empties the group, the timer chain
// must stop rather than arm another repeat_interval for a deleted group.
func TestOnRepeatIntervalExpired_FullyResolvedGroup_DoesNotRescheduleTimer(t *testing.T) {
	pub := &mockPublisher{}
	manager, timerStorage := createTestManagerWithPublisher(t, pub)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TerminalAlert")
	labels := map[string]string{"alertname": "TerminalAlert"}

	_, err := manager.AddAlertToGroup(ctx, createTestAlert("T1", core.StatusResolved, labels), groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	require.NoError(t, manager.onRepeatIntervalExpired(ctx, groupKey, RepeatIntervalTimer, group))
	require.Len(t, pub.calls(), 1, "the resolved notification must still be delivered")

	_, err = timerStorage.LoadTimer(ctx, groupKey)
	require.ErrorIs(t, err, ErrTimerNotFound,
		"no follow-up timer may be armed for a group that was just fully resolved and removed")
}

// Wave re-review, Important 1: groupStillExists treated EVERY storage error as
// "the group is gone". That recreated finding 3's wedge from the other end — on
// a transient Redis error the timer callback returned early, so onTimerExpired's
// tail then ran its normal cleanup and deleted the SHARED storage entry along
// with the local handle, leaving nothing for reconcileOrphanedTimers to adopt
// and the group permanently silent. Only a CONFIRMED absence may return false.
func TestGroupStillExists_FailsOpenOnTransientError(t *testing.T) {
	ctx := context.Background()
	manager := createTestManagerWithChain(t, &mockPublisher{}, nil, nil)
	groupKey := GroupKey("receiver=default/alertname=TransientProbe")

	// Confirmed absence -> false (this is the case pruning relies on).
	assert.False(t, manager.groupStillExists(ctx, groupKey),
		"a confirmed GroupNotFoundError must report the group as gone")

	// Present with alerts -> true.
	_, err := manager.AddAlertToGroup(ctx, createTestAlert("P1", core.StatusFiring,
		map[string]string{"alertname": "TransientProbe"}), groupKey)
	require.NoError(t, err)
	assert.True(t, manager.groupStillExists(ctx, groupKey))

	// Transient error -> true (fail open).
	failing := &loadFailingGroupStorage{GroupStorage: manager.storage}
	loadErr := errors.New("redis: i/o timeout")
	failing.loadErr.Store(&loadErr)
	manager.storage = failing

	assert.True(t, manager.groupStillExists(ctx, groupKey),
		"a transient storage error must NOT be read as 'group gone' — that deletes the shared timer entry and wedges the group")
}

// TestTimerCallback_TransientLoadErrorAfterPublish_KeepsTimerChain is the
// behavioural half: a transient error at the post-publish existence probe must
// still leave the group's next timer armed, so the chain continues.
func TestTimerCallback_TransientLoadErrorAfterPublish_KeepsTimerChain(t *testing.T) {
	ctx := context.Background()
	pub := &mockPublisher{}
	manager, timerStorage := createTestManagerWithPublisher(t, pub)

	groupKey := GroupKey("receiver=default/alertname=ChainSurvives")
	labels := map[string]string{"alertname": "ChainSurvives"}
	_, err := manager.AddAlertToGroup(ctx, createTestAlert("C1", core.StatusFiring, labels), groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	// The group is loaded fine for the publish itself; the probe afterwards is
	// what fails. Simulate the worst case: Load broken for everything after the
	// callback's own initial load, by wrapping storage in a counter that starts
	// failing on the second call.
	failing := &loadFailingAfterNGroupStorage{GroupStorage: manager.storage, failAfter: 1}
	manager.storage = failing

	require.NoError(t, manager.onGroupIntervalExpired(ctx, groupKey, GroupIntervalTimer, group))
	require.Len(t, pub.calls(), 1, "the notification must still go out")

	timer, err := timerStorage.LoadTimer(ctx, groupKey)
	require.NoError(t, err, "a transient probe error must not stop the timer chain")
	require.NotNil(t, timer)
	// Must be the NEXT timer in the chain, not the leftover group_wait entry
	// AddAlertToGroup created — that distinction is what makes this assertion
	// prove the callback continued rather than returned early.
	assert.Equal(t, RepeatIntervalTimer, timer.TimerType,
		"onGroupIntervalExpired must have armed repeat_interval despite the transient probe error")
}

// loadFailingAfterNGroupStorage passes the first failAfter Load calls through,
// then fails every later one with a transient error.
type loadFailingAfterNGroupStorage struct {
	GroupStorage
	failAfter int
	calls     atomic.Int64
}

func (s *loadFailingAfterNGroupStorage) Load(ctx context.Context, key GroupKey) (*AlertGroup, error) {
	if s.calls.Add(1) > int64(s.failAfter) {
		return nil, errors.New("redis: i/o timeout")
	}
	return s.GroupStorage.Load(ctx, key)
}
