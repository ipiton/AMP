package grouping

import (
	"context"
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
