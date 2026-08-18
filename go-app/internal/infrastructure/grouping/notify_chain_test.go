package grouping

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/inhibition"
	memorystore "github.com/ipiton/AMP/internal/infrastructure/storage/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeInhibitionChecker implements GroupInhibitionChecker (task 2.4) for
// tests: reports the target alert as inhibited iff its fingerprint is in
// inhibited.
type fakeInhibitionChecker struct {
	inhibited map[string]bool
	err       error
}

func (f *fakeInhibitionChecker) ShouldInhibit(_ context.Context, target *core.Alert) (*inhibition.MatchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.inhibited[target.Fingerprint] {
		return &inhibition.MatchResult{Matched: true, Rule: &inhibition.InhibitionRule{Name: "test-rule"}}, nil
	}
	return &inhibition.MatchResult{Matched: false}, nil
}

// createTestManagerWithChain builds a manager wired with a publisher and,
// optionally, an inhibition/silence checker (task 2.4 notify-stage chain).
// Mirrors createTestManagerWithPublisher but exposes the extra chain hooks.
func createTestManagerWithChain(t *testing.T, pub GroupNotificationPublisher, inhibitionChecker GroupInhibitionChecker, silenceChecker GroupSilenceChecker) *DefaultGroupManager {
	t.Helper()
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      &Duration{time.Hour}, // chain tests call publishGroupAlerts directly — no timer firing needed
			GroupInterval:  &Duration{time.Hour},
			RepeatInterval: &Duration{50 * time.Millisecond},
		},
	}

	storage := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})

	manager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator:      keyGen,
		Config:            config,
		Logger:            slog.Default(),
		Storage:           storage,
		Publisher:         pub,
		InhibitionChecker: inhibitionChecker,
		SilenceChecker:    silenceChecker,
	})
	require.NoError(t, err)
	return manager
}

// === Step 1: Inhibit (send-time) ===

func TestPublishGroupAlerts_DropsInhibitedAlertsAtSendTime(t *testing.T) {
	pub := &mockPublisher{}
	checker := &fakeInhibitionChecker{inhibited: map[string]bool{"fp_A2": true}}
	manager := createTestManagerWithChain(t, pub, checker, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert1 := createTestAlert("A1", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	alert2 := createTestAlert("A2", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert1, groupKey)
	_, _ = manager.AddAlertToGroup(ctx, alert2, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	calls := pub.calls()
	require.Len(t, calls, 1)
	require.Len(t, calls[0], 1, "the inhibited alert must be dropped, only A1 remains")
	assert.Equal(t, "fp_A1", calls[0][0].Fingerprint)
}

func TestPublishGroupAlerts_InhibitionCheckErrorFailsOpen(t *testing.T) {
	pub := &mockPublisher{}
	checker := &fakeInhibitionChecker{err: assertErr("inhibition backend down")}
	manager := createTestManagerWithChain(t, pub, checker, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	// Fail-open: an inhibition-check error must not drop the notification.
	calls := pub.calls()
	require.Len(t, calls, 1)
	require.Len(t, calls[0], 1)
}

// === Step 2: Silence (send-time, created AFTER ingest) ===

func TestPublishGroupAlerts_SilenceCreatedAfterIngestSuppressesNotify(t *testing.T) {
	pub := &mockPublisher{}
	silenceStore := memorystore.NewSilenceStore()
	manager := createTestManagerWithChain(t, pub, nil, silenceStore)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert", "team": "sre"})

	// Ingest happens BEFORE any silence exists.
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	// A silence is created AFTER the alert was already grouped.
	now := time.Now().UTC()
	_, err = silenceStore.CreateOrUpdate(&core.SilenceInput{
		Matchers:  []core.SilenceMatcherInput{{Name: "team", Value: "sre"}},
		EndsAt:    now.Add(time.Hour).Format(time.RFC3339),
		CreatedBy: "tester",
		Comment:   "created after ingest",
	}, now)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	// The silence created after ingest must still suppress the notification.
	assert.Empty(t, pub.calls(), "silence created after ingest must suppress the group notification")
}

func TestPublishGroupAlerts_NoActiveSilenceStillPublishes(t *testing.T) {
	pub := &mockPublisher{}
	silenceStore := memorystore.NewSilenceStore()
	manager := createTestManagerWithChain(t, pub, nil, silenceStore)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	assert.Len(t, pub.calls(), 1)
}

// === Empty after filtering => no publish ===

func TestPublishGroupAlerts_EmptyAfterFilteringNoPublish(t *testing.T) {
	pub := &mockPublisher{}
	checker := &fakeInhibitionChecker{inhibited: map[string]bool{"fp_A1": true, "fp_A2": true}}
	manager := createTestManagerWithChain(t, pub, checker, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert1 := createTestAlert("A1", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	alert2 := createTestAlert("A2", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert1, groupKey)
	_, _ = manager.AddAlertToGroup(ctx, alert2, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group)

	assert.Empty(t, pub.calls(), "every alert inhibited -> nothing published")
}

// === Step 3: Dedup ===

func TestPublishGroupAlerts_Dedup_SecondFireWithinRepeatIntervalSkipped(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil) // repeat_interval = 50ms
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group) // first send
	manager.publishGroupAlerts(ctx, group) // immediate re-fire, same unchanged alert set

	assert.Len(t, pub.calls(), 1, "second fire within repeat_interval, unchanged alert set, must be deduped")
}

func TestPublishGroupAlerts_Dedup_AfterRepeatIntervalElapsedPublishes(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil) // repeat_interval = 50ms
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group) // first send
	time.Sleep(60 * time.Millisecond)      // past repeat_interval (50ms)
	manager.publishGroupAlerts(ctx, group) // re-fire after TTL elapsed

	assert.Len(t, pub.calls(), 2, "re-fire after repeat_interval elapsed must publish again")
}

func TestPublishGroupAlerts_Dedup_ChangedAlertSetPublishesImmediately(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil) // repeat_interval = 50ms
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert1 := createTestAlert("A1", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert1, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group) // first send: [A1]

	// A new alert joins the SAME group immediately (well within repeat_interval).
	alert2 := createTestAlert("A2", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert2, groupKey)
	group, err = manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group) // alert set changed: [A1, A2]

	calls := pub.calls()
	require.Len(t, calls, 2, "a changed alert set must never be deduped, regardless of elapsed time")
	assert.Len(t, calls[1], 2)
}

// assertErr is a tiny error helper (avoids pulling in "errors" just for one
// sentinel-style test error).
type assertErr string

func (e assertErr) Error() string { return string(e) }

// === Fix round 1, Findings 1+2: Dedup must not record a send that wasn't
// confirmed (metrics-only mode / partial target failure) ===

// TestPublishGroupAlerts_FailedPublishDoesNotPoisonDedup covers both ruled
// scenarios generically at the point that matters to publishGroupAlerts:
// whatever the reason (metrics-only mode returning empty results, or N-of-M
// targets failing — both fixed in ApplicationPublishingAdapter.PublishGroup
// to surface as a non-nil error instead of a silent "success"), a
// PublishGroup call that returns an error must NOT be recorded in the
// Dedup log. The very next fire, still well within repeat_interval, must
// attempt delivery again rather than being silently suppressed.
func TestPublishGroupAlerts_FailedPublishDoesNotPoisonDedup(t *testing.T) {
	pub := &mockPublisher{err: assertErr("simulated failure: metrics-only mode or partial target failure")}
	manager := createTestManagerWithChain(t, pub, nil, nil) // repeat_interval = 50ms
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group) // fails — must NOT RecordSent
	pub.err = nil                          // e.g. metrics-only mode ends / the failed target recovers
	manager.publishGroupAlerts(ctx, group) // immediate re-fire, well within repeat_interval

	calls := pub.calls()
	require.Len(t, calls, 2,
		"a failed/unconfirmed publish must not be recorded as sent — the next fire must still attempt delivery, not be deduped")
}

// === Task fwb: per-target nflog granularity — partial failure retries
// only the failed target ===

// twoTargetPublisher simulates a GroupNotificationPublisher fanning out to
// TWO named targets (task fwb) — unlike mockPublisher's single synthetic
// target, this lets tests exercise per-target dedup/retry semantics
// end-to-end through DefaultGroupManager.publishGroupAlerts without pulling
// in the full infrastructure/publishing stack.
type twoTargetPublisher struct {
	mu         sync.Mutex
	failTarget string            // target name that fails this call; "" means none fail
	calls      []map[string]bool // one entry per PublishGroup call: target -> attempted (not skipped)
}

func (p *twoTargetPublisher) PublishGroup(_ context.Context, _ string, _ []*core.Alert, _ string, _ map[string]string, skipTarget func(string) bool) ([]TargetPublishOutcome, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	attempted := map[string]bool{}
	outcomes := make([]TargetPublishOutcome, 0, 2)
	for _, target := range []string{"t1", "t2"} {
		if skipTarget != nil && skipTarget(target) {
			continue
		}
		attempted[target] = true
		outcomes = append(outcomes, TargetPublishOutcome{Target: target, Success: target != p.failTarget})
	}
	p.calls = append(p.calls, attempted)
	return outcomes, nil
}

func (p *twoTargetPublisher) setFailTarget(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failTarget = name
}

func (p *twoTargetPublisher) callAttempts() []map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]bool, len(p.calls))
	copy(out, p.calls)
	return out
}

// TestPublishGroupAlerts_PartialTargetFailure_RetryOnlyResendsFailedTarget
// is the deliverable's central claim: t1 succeeds and t2 fails on the first
// fire; the immediate re-fire (same unchanged alert set, well within
// repeat_interval) must skip t1 (already recorded) and retry ONLY t2.
func TestPublishGroupAlerts_PartialTargetFailure_RetryOnlyResendsFailedTarget(t *testing.T) {
	pub := &twoTargetPublisher{failTarget: "t2"}
	manager := createTestManagerWithChain(t, pub, nil, nil) // repeat_interval = 50ms
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group) // t1 succeeds, t2 fails
	pub.setFailTarget("")                  // t2 recovers before the next fire
	manager.publishGroupAlerts(ctx, group) // immediate re-fire, same unchanged alert set

	calls := pub.callAttempts()
	require.Len(t, calls, 2)
	assert.True(t, calls[0]["t1"], "first fire: t1 must be attempted")
	assert.True(t, calls[0]["t2"], "first fire: t2 must be attempted")
	assert.False(t, calls[1]["t1"], "second fire: t1 already succeeded last cycle — must be skipped, not resent")
	assert.True(t, calls[1]["t2"], "second fire: t2 failed last cycle — must be retried")
}

// TestPublishGroupAlerts_PartialTargetFailure_ResolvedAlertsNotPrunedUntilAllTargetsConfirm
// guards a correctness property the redesign must preserve: pruning
// resolved alerts (final review finding 8) must wait until EVERY target has
// confirmed delivery, or a still-failing target would never get to see the
// resolved alert on its retry.
func TestPublishGroupAlerts_PartialTargetFailure_ResolvedAlertsNotPrunedUntilAllTargetsConfirm(t *testing.T) {
	pub := &twoTargetPublisher{failTarget: "t2"}
	manager := createTestManagerWithChain(t, pub, nil, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=ResolvingAlert")
	resolved := createTestAlert("R1", core.StatusResolved, map[string]string{"alertname": "ResolvingAlert"})
	_, err := manager.AddAlertToGroup(ctx, resolved, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	manager.publishGroupAlerts(ctx, group) // t1 confirms, t2 fails: must NOT prune yet

	stillThere, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err, "the group must survive a partial failure so the retry can still deliver to t2")
	_, found := stillThere.Alerts["fp_R1"]
	assert.True(t, found, "the resolved alert must not be pruned until every target confirms delivery")

	pub.setFailTarget("") // t2 recovers
	manager.publishGroupAlerts(ctx, stillThere)

	_, err = manager.GetGroup(ctx, groupKey)
	assert.Error(t, err, "once t2 also confirms, the fully-delivered resolved alert must be pruned and the group removed")
}

// === Review finding 1 (fwb fix round 1): groupLabels resolved from
// GroupMetadata.GroupBy, not hardcoded empty ===

// createTestManagerWithChainAndGroupBy is createTestManagerWithChain with a
// caller-supplied root Route.GroupBy, so tests can exercise
// groupLabelsFor's resolution against something other than the fixed
// ["alertname"] every other chain test uses.
func createTestManagerWithChainAndGroupBy(t *testing.T, pub GroupNotificationPublisher, groupBy []string) *DefaultGroupManager {
	t.Helper()
	keyGen := NewGroupKeyGenerator()
	config := &GroupingConfig{
		Route: &Route{
			Receiver:       "default",
			GroupBy:        groupBy,
			GroupWait:      &Duration{time.Hour},
			GroupInterval:  &Duration{time.Hour},
			RepeatInterval: &Duration{time.Hour},
		},
	}

	storage := NewMemoryGroupStorage(&MemoryGroupStorageConfig{Logger: slog.Default()})

	manager, err := NewDefaultGroupManager(context.Background(), DefaultGroupManagerConfig{
		KeyGenerator: keyGen,
		Config:       config,
		Logger:       slog.Default(),
		Storage:      storage,
		Publisher:    pub,
	})
	require.NoError(t, err)
	return manager
}

// TestPublishGroupAlerts_GroupLabels_ResolvedFromGroupBy is review finding
// 1's headline test: for group_by: [alertname, cluster], the batched
// notification must carry groupLabels resolved to this group's actual
// values for exactly those two names — not the hardcoded empty map the
// pre-fix code passed down.
func TestPublishGroupAlerts_GroupLabels_ResolvedFromGroupBy(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChainAndGroupBy(t, pub, []string{"alertname", "cluster"})
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=HighCPU/cluster=prod")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "HighCPU", "cluster": "prod", "instance": "host-1"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group)

	require.Len(t, pub.calls(), 1)
	assert.Equal(t, map[string]string{"alertname": "HighCPU", "cluster": "prod"}, pub.lastGroupLabels(),
		"groupLabels must resolve exactly the group_by names to this group's values, excluding non-group_by labels like instance")
}

// TestPublishGroupAlerts_GroupLabels_EmptyGroupByYieldsEmptyMap covers the
// other half: a route with no group_by at all must resolve to an empty,
// non-nil groupLabels map, matching the wire formatter's "never emit null"
// contract.
func TestPublishGroupAlerts_GroupLabels_EmptyGroupByYieldsEmptyMap(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChainAndGroupBy(t, pub, nil)
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/no-group-by")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "HighCPU"})
	_, err := manager.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	manager.publishGroupAlerts(ctx, group)

	require.Len(t, pub.calls(), 1)
	assert.Equal(t, map[string]string{}, pub.lastGroupLabels(), "empty group_by must resolve to an empty, non-nil map")
}

// === Fix round 1, Finding 4: check-then-publish-then-record must be
// atomic per group, not just the dedup log's own internal locking ===

// TestPublishGroupAlerts_ConcurrentFiringsForSameGroupAreSerialized proves
// the per-GroupKey publish lock actually prevents the double-send this
// finding described: without it, N concurrent publishGroupAlerts calls for
// the SAME unchanged group could all observe "not a duplicate" before any
// of them records success. With the lock, they run one at a time, so only
// the first can ever reach the publisher — every later one dedups against
// the first's now-recorded entry.
func TestPublishGroupAlerts_ConcurrentFiringsForSameGroupAreSerialized(t *testing.T) {
	pub := &mockPublisher{}
	manager := createTestManagerWithChain(t, pub, nil, nil) // repeat_interval = 50ms
	ctx := context.Background()

	groupKey := GroupKey("receiver=default/alertname=TestAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "TestAlert"})
	_, _ = manager.AddAlertToGroup(ctx, alert, groupKey)

	group, err := manager.GetGroup(ctx, groupKey)
	require.NoError(t, err)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			manager.publishGroupAlerts(ctx, group)
		}()
	}
	wg.Wait()

	assert.Len(t, pub.calls(), 1,
		"concurrent firings for the same unchanged group must serialize and dedup down to exactly one publish")
}
