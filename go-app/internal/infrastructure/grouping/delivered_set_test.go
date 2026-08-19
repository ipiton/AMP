package grouping

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// === Task fu4 (alertmanager-parity wave 4): the per-alert delivered set ===
//
// Both GroupNotifyLog implementations must agree on the contract the notify
// chain relies on: additive recording, superseded by a full entry, cleared with
// the group, and defensively capped. The tests below run the SAME assertions
// against notifyDedupLog (in-memory) and RedisNotifyLog (miniredis).

// deliveredSetLogs returns each implementation under test, named.
func deliveredSetLogs(t *testing.T) map[string]GroupNotifyLog {
	t.Helper()

	redisLog, _, cleanup := setupTestRedisNotifyLog(t)
	t.Cleanup(cleanup)

	return map[string]GroupNotifyLog{
		"memory": newNotifyDedupLog(),
		"redis":  redisLog,
	}
}

func TestDeliveredSet_AbsentByDefault(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			delivered, err := log.DeliveredAlerts(context.Background(), "gk", "target-a")
			require.NoError(t, err)
			assert.Empty(t, delivered, "no partial failure has happened, so there must be no delivered set")
		})
	}
}

func TestDeliveredSet_RecordIsAdditiveAndDeduplicated(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:firing", "fp-2:firing"}, time.Hour))
			// Second partial fire: one new alert plus one already recorded.
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-2:firing", "fp-4:firing"}, time.Hour))

			delivered, err := log.DeliveredAlerts(ctx, "gk", "target-a")
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"fp-1:firing", "fp-2:firing", "fp-4:firing"}, delivered,
				"each fire reports only the alerts it attempted, so recording must extend the set, not replace it")
		})
	}
}

func TestDeliveredSet_IsPerTargetAndPerGroup(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk-1", "target-a", []string{"fp-1:firing"}, time.Hour))

			for _, probe := range []struct{ groupKey, target string }{
				{"gk-1", "target-b"},
				{"gk-2", "target-a"},
			} {
				delivered, err := log.DeliveredAlerts(ctx, GroupKey(probe.groupKey), probe.target)
				require.NoError(t, err)
				assert.Empty(t, delivered, "%s/%s must not see another pair's partial state", probe.groupKey, probe.target)
			}
		})
	}
}

func TestDeliveredSet_RecordSentSupersedesIt(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:firing"}, time.Hour))
			require.NoError(t, log.RecordSent(ctx, "gk", "target-a", "fp-1:firing|fp-2:firing", time.Now(), time.Hour))

			delivered, err := log.DeliveredAlerts(ctx, "gk", "target-a")
			require.NoError(t, err)
			assert.Empty(t, delivered,
				"a full entry states the whole set was delivered, which supersedes per-alert progress toward it")
		})
	}
}

func TestDeliveredSet_ForgetClearsIt(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:firing"}, time.Hour))
			require.NoError(t, log.Forget(ctx, "gk"))

			delivered, err := log.DeliveredAlerts(ctx, "gk", "target-a")
			require.NoError(t, err)
			assert.Empty(t, delivered, "the group is gone; none of its state may outlive it")
		})
	}
}

// TestDeliveredSet_ForgetClearsAPartialOnlyTarget is the specific regression
// for a target that NEVER had a full entry: Forget enumerates targets through
// the group's target-set, so RecordPartialDelivery has to register the target
// there too or the delivered set is orphaned until its TTL.
func TestDeliveredSet_ForgetClearsAPartialOnlyTarget(t *testing.T) {
	log, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "partial-only", []string{"fp-1:firing"}, time.Hour))
	require.NoError(t, log.Forget(ctx, "gk"))

	assert.False(t, mr.Exists(notifyLogDeliveredKey("gk", "partial-only")),
		"Forget must reach a delivered set whose target never got a full entry")
}

func TestDeliveredSet_EmptyRecordIsANoOp(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", nil, time.Hour))

			delivered, err := log.DeliveredAlerts(ctx, "gk", "target-a")
			require.NoError(t, err)
			assert.Empty(t, delivered)
		})
	}
}

// TestDeliveredSet_CapStopsRecording pins the defensive bound: past the cap the
// implementation refuses to record rather than growing without limit, which
// degrades to the pre-fu4 resend-everything behaviour (at-least-once) instead of
// leaking Redis memory driven by a remote endpoint's failure pattern.
func TestDeliveredSet_CapStopsRecording(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()

			keys := make([]string, 0, maxDeliveredAlertsPerTarget)
			for i := 0; i < maxDeliveredAlertsPerTarget; i++ {
				keys = append(keys, fmt.Sprintf("fp-%d:firing", i))
			}
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", keys, time.Hour))

			err := log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"one-too-many:firing"}, time.Hour)
			require.Error(t, err, "exceeding the cap must be reported, not silently absorbed")

			delivered, dErr := log.DeliveredAlerts(ctx, "gk", "target-a")
			require.NoError(t, dErr)
			assert.LessOrEqual(t, len(delivered), maxDeliveredAlertsPerTarget, "the set must stay bounded")
			assert.NotContains(t, delivered, "one-too-many:firing")
		})
	}
}

// TestDeliveredSet_TTLIsBoundedByRepeatInterval proves the Redis set cannot
// outlive the dedup window it belongs to: a stale partial state must age out
// into a full resend rather than suppress alerts indefinitely.
func TestDeliveredSet_TTLIsBoundedByRepeatInterval(t *testing.T) {
	log, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	require.NoError(t, log.RecordPartialDelivery(context.Background(), "gk", "target-a", []string{"fp-1:firing"}, 10*time.Minute))

	ttl := mr.TTL(notifyLogDeliveredKey("gk", "target-a"))
	assert.Equal(t, 10*time.Minute+notifyLogEntryTTLGracePeriod, ttl,
		"the delivered set must expire on the same schedule an nflog entry would")
}

// TestDeliveredSet_CrossReplicaVisibility is the HA assertion: a second replica
// process, sharing nothing but Redis, must see the partial progress and
// therefore re-send the same remainder.
func TestDeliveredSet_CrossReplicaVisibility(t *testing.T) {
	first, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	second, cleanupSecond := newSecondReplicaNotifyLog(t, mr)
	defer cleanupSecond()

	ctx := context.Background()
	require.NoError(t, first.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:firing", "fp-2:firing"}, time.Hour))

	delivered, err := second.DeliveredAlerts(ctx, "gk", "target-a")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"fp-1:firing", "fp-2:firing"}, delivered,
		"a replica adopting the group must see what the previous one already delivered")
}

// === The notify chain's use of the delivered set (task fu4) ===

// perAlertRecordingPublisher captures the alert subset it is handed for its one
// synthetic target, and reports a configurable per-alert outcome: every alert
// whose fingerprint is in failFingerprints "fails", the rest are reported as
// delivered through TargetPublishOutcome.DeliveredAlerts.
type perAlertRecordingPublisher struct {
	mu               sync.Mutex
	target           string
	failFingerprints map[string]bool
	handed           [][]string // per call: the fingerprints it was asked to deliver
	skipped          int        // calls where the target was excluded entirely
}

func newPerAlertRecordingPublisher(target string, failFingerprints ...string) *perAlertRecordingPublisher {
	p := &perAlertRecordingPublisher{target: target, failFingerprints: map[string]bool{}}
	for _, fp := range failFingerprints {
		p.failFingerprints[fp] = true
	}
	return p
}

func (p *perAlertRecordingPublisher) PublishGroup(_ context.Context, _ string, alerts []*core.Alert, _ string, _ map[string]string, targetAlerts func(string, []*core.Alert) []*core.Alert) ([]TargetPublishOutcome, error) {
	owed := alerts
	if targetAlerts != nil {
		owed = targetAlerts(p.target, alerts)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(owed) == 0 {
		p.skipped++
		return nil, nil
	}

	fingerprints := make([]string, 0, len(owed))
	delivered := make([]string, 0, len(owed))
	failed := false
	for _, alert := range owed {
		fingerprints = append(fingerprints, alert.Fingerprint)
		if p.failFingerprints[alert.Fingerprint] {
			failed = true
			continue
		}
		delivered = append(delivered, alert.DeliveryKey())
	}
	p.handed = append(p.handed, fingerprints)

	if failed {
		return []TargetPublishOutcome{{Target: p.target, Success: false, DeliveredAlerts: delivered}}, nil
	}
	return []TargetPublishOutcome{{Target: p.target, Success: true}}, nil
}

func (p *perAlertRecordingPublisher) callsHanded() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]string, len(p.handed))
	copy(out, p.handed)
	return out
}

func (p *perAlertRecordingPublisher) heal() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failFingerprints = map[string]bool{}
}

// addAlertsForDeliveredSetTest puts n firing alerts (fp-1..fp-n) into one group
// and returns it.
func addAlertsForDeliveredSetTest(t *testing.T, manager *DefaultGroupManager, n int) *AlertGroup {
	t.Helper()

	var group *AlertGroup
	for i := 1; i <= n; i++ {
		g, err := manager.AddAlertToGroup(context.Background(), &core.Alert{
			Fingerprint: fmt.Sprintf("fp-%d", i),
			AlertName:   "HighCPU",
			Status:      core.StatusFiring,
			Labels:      map[string]string{"alertname": "HighCPU"},
			StartsAt:    time.Now().UTC(),
		}, "receiver=default/alertname=HighCPU")
		require.NoError(t, err)
		group = g
	}
	require.NotNil(t, group)
	return group
}

// TestNotifyChain_PartialDeliveryNarrowsTheNextFire is the chain-level unit
// test for the whole loop: record the delivered alerts on a partial failure,
// hand only the remainder to the publisher next fire, then write the full entry
// and clear the partial state once the remainder lands.
func TestNotifyChain_PartialDeliveryNarrowsTheNextFire(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	pub := newPerAlertRecordingPublisher("target-a", "fp-3")
	manager := createTestManagerWithRedisNotifyLog(t, pub, notifyLog)
	group := addAlertsForDeliveredSetTest(t, manager, 5)
	ctx := context.Background()

	manager.publishGroupAlerts(ctx, group)

	delivered, err := notifyLog.DeliveredAlerts(ctx, group.Key, "target-a")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"fp-1:firing", "fp-2:firing", "fp-4:firing", "fp-5:firing"}, delivered)

	dup, err := notifyLog.IsDuplicate(ctx, group.Key, "target-a", alertSetSignature(groupAlertSlice(group)), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.False(t, dup, "a partially delivered target must have no full entry")

	// Second fire, endpoint healthy.
	pub.heal()
	manager.publishGroupAlerts(ctx, group)

	calls := pub.callsHanded()
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"fp-3"}, calls[1],
		"the retry fire must carry ONLY the alert still owed")

	delivered, err = notifyLog.DeliveredAlerts(ctx, group.Key, "target-a")
	require.NoError(t, err)
	assert.Empty(t, delivered, "the full entry supersedes the delivered set")

	dup, err = notifyLog.IsDuplicate(ctx, group.Key, "target-a", alertSetSignature(groupAlertSlice(group)), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.True(t, dup, "the target has now received the whole set, so a full entry must exist")
}

// TestNotifyChain_DeliveredSetCoveringEverythingSkipsTheTarget covers the
// degenerate case: the delivered set already accounts for every alert (a job
// that delivered everything but whose waiter timed out). Nothing may be sent.
func TestNotifyChain_DeliveredSetCoveringEverythingSkipsTheTarget(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	pub := newPerAlertRecordingPublisher("target-a")
	manager := createTestManagerWithRedisNotifyLog(t, pub, notifyLog)
	group := addAlertsForDeliveredSetTest(t, manager, 2)
	ctx := context.Background()

	require.NoError(t, notifyLog.RecordPartialDelivery(ctx, group.Key, "target-a",
		[]string{"fp-1:firing", "fp-2:firing"}, time.Hour))

	manager.publishGroupAlerts(ctx, group)

	assert.Empty(t, pub.callsHanded(), "every alert is already delivered, so nothing may go on the wire")
	assert.Equal(t, 1, pub.skipped, "the target must be excluded, not published to with an empty set")
}

// TestNotifyChain_StaleDeliveredSetDoesNotSuppressCurrentAlerts guards against
// a delivered set that only holds keys for alerts no longer in the group (all
// pruned or status-flipped): the current set must be sent in full.
func TestNotifyChain_StaleDeliveredSetDoesNotSuppressCurrentAlerts(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	pub := newPerAlertRecordingPublisher("target-a")
	manager := createTestManagerWithRedisNotifyLog(t, pub, notifyLog)
	group := addAlertsForDeliveredSetTest(t, manager, 2)
	ctx := context.Background()

	// Keys for a previous incarnation of the group: same fingerprints, but the
	// alerts were RESOLVED then and are FIRING now, so no key matches.
	require.NoError(t, notifyLog.RecordPartialDelivery(ctx, group.Key, "target-a",
		[]string{"fp-1:resolved", "fp-2:resolved"}, time.Hour))

	manager.publishGroupAlerts(ctx, group)

	calls := pub.callsHanded()
	require.Len(t, calls, 1)
	assert.ElementsMatch(t, []string{"fp-1", "fp-2"}, calls[0],
		"a status flip is a new notification: nothing in the stale set may suppress it")
}

// groupAlertSlice snapshots a group's alerts, mirroring what publishGroupAlerts
// hands the publisher.
func groupAlertSlice(group *AlertGroup) []*core.Alert {
	group.mu.RLock()
	defer group.mu.RUnlock()
	out := make([]*core.Alert, 0, len(group.Alerts))
	for _, a := range group.Alerts {
		out = append(out, a)
	}
	return out
}
