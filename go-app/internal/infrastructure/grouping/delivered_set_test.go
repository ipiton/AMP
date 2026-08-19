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

// TestDeliveredSet_OneStatusPerFingerprint is the C1 regression (review round
// 1, Critical): the delivered set must hold AT MOST ONE status per fingerprint.
//
// Recording `fp-1:resolved` has to invalidate a previously recorded
// `fp-1:firing`, because an alert has exactly one current status and the two
// keys describe the same alert. Accumulating both meant that an alert flapping
// firing→resolved→firing while the target stayed partially delivered had its
// re-fire filtered out as "already delivered" — a LOST notification, and the
// one place the design degraded to a drop instead of a duplicate.
func TestDeliveredSet_OneStatusPerFingerprint(t *testing.T) {
	for name, log := range deliveredSetLogs(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:firing", "fp-2:firing"}, time.Hour))
			// fp-1 resolved and that resolved notification landed too.
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:resolved"}, time.Hour))

			delivered, err := log.DeliveredAlerts(ctx, "gk", "target-a")
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"fp-1:resolved", "fp-2:firing"}, delivered,
				"the superseded status must be gone: an alert has one current status, and keeping the old key would suppress a re-fire")
			assert.NotContains(t, delivered, "fp-1:firing",
				"fp-1:firing must not survive, or fp-1 firing AGAIN would be filtered out as already delivered")
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
			require.ErrorIs(t, err, ErrDeliveredStateCapped,
				"the cap must be distinguishable from a backend failure — the caller counts the two separately")

			// Re-recording an alert ALREADY present consumes no capacity, so a
			// full state can still track status changes (exact accounting, both
			// implementations).
			require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-0:resolved"}, time.Hour),
				"overwriting an alert already in the state must not count against the cap")

			delivered, dErr := log.DeliveredAlerts(ctx, "gk", "target-a")
			require.NoError(t, dErr)
			assert.LessOrEqual(t, len(delivered), maxDeliveredAlertsPerTarget, "the set must stay bounded")
			assert.NotContains(t, delivered, "one-too-many:firing")
		})
	}
}

// TestDeliveredSet_TTLIsBoundedByRepeatInterval proves the Redis state cannot
// outlive the dedup window it belongs to: a stale partial state must age out
// into a full resend rather than suppress alerts indefinitely.
//
// Asserts the TTL is set at all (review round 1, finding I2: it used to be a
// separate EXPIRE round-trip that could fail and leave a TTL-LESS key behind,
// i.e. permanent suppression) AND that expiry really empties the state.
func TestDeliveredSet_TTLIsBoundedByRepeatInterval(t *testing.T) {
	log, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:firing"}, 10*time.Minute))

	ttl := mr.TTL(notifyLogDeliveredKey("gk", "target-a"))
	assert.Equal(t, 10*time.Minute+notifyLogEntryTTLGracePeriod, ttl,
		"the delivered state must expire on the same schedule an nflog entry would")

	mr.FastForward(ttl + time.Second)

	delivered, err := log.DeliveredAlerts(ctx, "gk", "target-a")
	require.NoError(t, err)
	assert.Empty(t, delivered, "past its TTL the state must read as absent, so the whole set is re-sent")
}

// TestDeliveredSet_MemoryStateExpires is the I1 regression (review round 1,
// Important): the IN-MEMORY delivered state had no expiry at all, so in the lite
// profile — or in the standard profile whenever Redis was unavailable at
// grouping-init — the alerts that landed were filtered out of every subsequent
// fire FOREVER while one alert of the group kept failing. Their repeat
// notifications were lost indefinitely, with no path back (the Redis
// implementation recovers via TTL; this one had none).
//
// recordedAt is back-dated rather than slept on: the real expiry bound is
// repeat_interval + 60s grace, and this exercises the same expired() check and
// the same read/write paths the production clock would.
func TestDeliveredSet_MemoryStateExpires(t *testing.T) {
	log := newNotifyDedupLog()
	ctx := context.Background()

	require.NoError(t, log.RecordPartialDelivery(ctx, "gk", "target-a", []string{"fp-1:firing"}, 10*time.Minute))

	delivered, err := log.DeliveredAlerts(ctx, "gk", "target-a")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"fp-1:firing"}, delivered, "fresh state must be visible")

	backdateDeliveredState(t, log, "gk", "target-a", deliveredStateTTL(10*time.Minute)+time.Second)

	delivered, err = log.DeliveredAlerts(ctx, "gk", "target-a")
	require.NoError(t, err)
	assert.Empty(t, delivered,
		"state older than repeat_interval + grace must read as absent, so the alerts are re-sent instead of suppressed forever")

	log.mu.Lock()
	_, stillThere := log.delivered[dedupKey{groupKey: "gk", target: "target-a"}]
	log.mu.Unlock()
	assert.False(t, stillThere, "expiring on read must also reclaim the memory")
}

// TestNotifyChain_ExpiredMemoryDeliveredStateResendsEverything is I1 at chain
// level: once the in-memory state has aged past repeat_interval, the next fire
// carries the WHOLE set again (one duplicate round — at-least-once — instead of
// permanently suppressed alerts).
func TestNotifyChain_ExpiredMemoryDeliveredStateResendsEverything(t *testing.T) {
	notifyLog := newNotifyDedupLog()
	pub := newPerAlertRecordingPublisher("target-a", "fp-3")
	manager := createTestManagerWithRedisNotifyLog(t, pub, notifyLog)
	group := addAlertsForDeliveredSetTest(t, manager, 3)
	ctx := context.Background()

	manager.publishGroupAlerts(ctx, group)

	// Fire 2 while the state is fresh: only the alert still owed.
	manager.publishGroupAlerts(ctx, group)
	calls := pub.callsHanded()
	require.Len(t, calls, 2)
	require.Equal(t, []string{"fp-3"}, calls[1], "fresh state narrows the fire")

	// Age the state past its window; the next fire must resend everything.
	backdateDeliveredState(t, notifyLog, group.Key, "target-a", deliveredStateTTL(time.Hour)+time.Second)
	manager.publishGroupAlerts(ctx, group)

	calls = pub.callsHanded()
	require.Len(t, calls, 3)
	assert.ElementsMatch(t, []string{"fp-1", "fp-2", "fp-3"}, calls[2],
		"an expired delivered state must age out into a full resend, never into indefinite suppression")
}

// backdateDeliveredState ages one (group, target) delivered state by age, which
// is how the in-memory TTL is exercised without sleeping through a
// repeat_interval.
func backdateDeliveredState(t *testing.T, log *notifyDedupLog, groupKey GroupKey, target string, age time.Duration) {
	t.Helper()

	log.mu.Lock()
	defer log.mu.Unlock()

	state, ok := log.delivered[dedupKey{groupKey: groupKey, target: target}]
	require.True(t, ok, "expected a delivered state for %s/%s", groupKey, target)
	state.recordedAt = state.recordedAt.Add(-age)
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

// TestNotifyChain_FlappingAlertIsResentWhenItFiresAgain is the C1 regression at
// chain level (review round 1, Critical): with one alert of the group
// persistently failing — so no full entry is ever written and the delivered set
// is never cleared — an alert that goes firing → resolved → firing MUST reach
// the target on that third fire.
//
// Before the fix the set accumulated both `fp-1:firing` and `fp-1:resolved`, so
// alertsStillOwed filtered the re-fire out and the notification was lost until
// the set's TTL (repeat_interval scale) or a full success.
func TestNotifyChain_FlappingAlertIsResentWhenItFiresAgain(t *testing.T) {
	notifyLog, _, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	// fp-3 fails on every fire: the target can never be fully confirmed, which
	// is exactly the state the delivered set exists for.
	pub := newPerAlertRecordingPublisher("target-a", "fp-3")
	manager := createTestManagerWithRedisNotifyLog(t, pub, notifyLog)
	group := addAlertsForDeliveredSetTest(t, manager, 3)
	ctx := context.Background()

	// Fire 1: fp-1, fp-2 land as firing; fp-3 fails.
	manager.publishGroupAlerts(ctx, group)

	// fp-1 resolves. Fire 2: its resolved notification lands.
	group = flipAlertStatus(t, manager, group.Key, "fp-1", core.StatusResolved)
	manager.publishGroupAlerts(ctx, group)

	// fp-1 fires again. Fire 3: this is a NEW notification by every upstream
	// rule and must go on the wire.
	group = flipAlertStatus(t, manager, group.Key, "fp-1", core.StatusFiring)
	manager.publishGroupAlerts(ctx, group)

	calls := pub.callsHanded()
	require.Len(t, calls, 3, "every fire must reach the publisher (fp-3 keeps failing, so nothing is ever fully deduped)")

	assert.Contains(t, calls[1], "fp-1", "fire 2 must carry fp-1's resolved notification")
	assert.Contains(t, calls[2], "fp-1",
		"fire 3 must carry fp-1 firing AGAIN — a flap must never be suppressed by a stale same-fingerprint key")
	assert.NotContains(t, calls[2], "fp-2", "fp-2 has not changed and already landed: still must not be re-sent")
}

// flipAlertStatus re-adds an alert under the same fingerprint with a new status,
// which is how a real flap reaches a group (AddAlertToGroup replaces in place).
//
// Returns the group instance AddAlertToGroup resolved: the manager reloads the
// group through its storage, so the caller must publish through that instance
// rather than a pointer captured earlier.
func flipAlertStatus(t *testing.T, manager *DefaultGroupManager, groupKey GroupKey, fingerprint string, status core.AlertStatus) *AlertGroup {
	t.Helper()

	alert := &core.Alert{
		Fingerprint: fingerprint,
		AlertName:   "HighCPU",
		Status:      status,
		Labels:      map[string]string{"alertname": "HighCPU"},
		StartsAt:    time.Now().UTC(),
	}
	if status == core.StatusResolved {
		endsAt := time.Now().UTC()
		alert.EndsAt = &endsAt
	}

	group, err := manager.AddAlertToGroup(context.Background(), alert, groupKey)
	require.NoError(t, err)
	return group
}

// TestDeliveredSet_ScriptRejectsMalformedArgsWithoutWriting pins the r3 guard
// (re-review): the Lua script must validate its argument shape BEFORE touching
// the key. An odd fingerprint/status tail used to reach the HSET loop, error out
// part-way through it, and leave the hash created but with NO TTL — the
// permanent-suppression failure mode the script exists to prevent.
//
// RecordPartialDelivery always builds even pairs, so this drives the script
// directly: the invariant under test is the script's, and it is what keeps "an
// error means nothing was written" true even for a malformed call.
func TestDeliveredSet_ScriptRejectsMalformedArgsWithoutWriting(t *testing.T) {
	log, mr, cleanup := setupTestRedisNotifyLog(t)
	defer cleanup()

	ctx := context.Background()
	key := notifyLogDeliveredKey("gk", "target-a")

	for name, argv := range map[string][]any{
		"odd fingerprint/status tail": {maxDeliveredAlertsPerTarget, int64(60000), "fp-1", "firing", "fp-2"},
		"no pairs at all":             {maxDeliveredAlertsPerTarget, int64(60000)},
		"non-numeric ttl":             {maxDeliveredAlertsPerTarget, "soon", "fp-1", "firing"},
		"zero ttl":                    {maxDeliveredAlertsPerTarget, int64(0), "fp-1", "firing"},
	} {
		t.Run(name, func(t *testing.T) {
			err := recordPartialDeliveryScript.Run(ctx, log.client, []string{key}, argv...).Err()
			require.Error(t, err, "a malformed call must be refused")
			assert.False(t, mr.Exists(key),
				"nothing may be written when the arguments are refused — a key without a TTL suppresses alerts forever")
		})
	}
}
