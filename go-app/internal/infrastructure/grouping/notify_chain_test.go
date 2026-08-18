package grouping

import (
	"context"
	"log/slog"
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
