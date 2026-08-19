package publishing

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// Test Suite: targetMatchesReceiver filtering matrix

func TestTargetMatchesReceiver_EmptyReceiverNameMatchesAll(t *testing.T) {
	target := &core.PublishingTarget{Name: "webhook-1", Receivers: []string{"slack-critical"}}
	assert.True(t, targetMatchesReceiver(target, ""))
}

func TestTargetMatchesReceiver_LabelSingleName(t *testing.T) {
	target := &core.PublishingTarget{Name: "webhook-1", Receivers: []string{"slack-critical"}}
	assert.True(t, targetMatchesReceiver(target, "slack-critical"))
	assert.False(t, targetMatchesReceiver(target, "pagerduty-oncall"))
}

func TestTargetMatchesReceiver_LabelMultipleNames(t *testing.T) {
	target := &core.PublishingTarget{Name: "webhook-1", Receivers: []string{"slack-critical", "pagerduty-oncall"}}
	assert.True(t, targetMatchesReceiver(target, "slack-critical"))
	assert.True(t, targetMatchesReceiver(target, "pagerduty-oncall"))
	assert.False(t, targetMatchesReceiver(target, "team-b"))
}

func TestTargetMatchesReceiver_FallbackByTargetName(t *testing.T) {
	// Label present but does not list the receiver; target's own Name
	// equals the receiver being queried -> fallback match applies.
	target := &core.PublishingTarget{Name: "team-b", Receivers: []string{"slack-critical"}}
	assert.True(t, targetMatchesReceiver(target, "team-b"))
}

func TestTargetMatchesReceiver_AbsentLabelMeansAll(t *testing.T) {
	target := &core.PublishingTarget{Name: "legacy-webhook", Receivers: nil}
	assert.True(t, targetMatchesReceiver(target, "slack-critical"))
	assert.True(t, targetMatchesReceiver(target, "anything-else"))
}

func TestTargetMatchesReceiver_LabelPresentNoMatchNoFallback(t *testing.T) {
	target := &core.PublishingTarget{Name: "webhook-1", Receivers: []string{"slack-critical"}}
	assert.False(t, targetMatchesReceiver(target, "team-b"))
}

func TestTargetMatchesReceiver_CaseSensitive(t *testing.T) {
	// Matching is exact/case-sensitive by design: "Slack-Critical" must NOT
	// match a Receivers list containing "slack-critical", and (Name being
	// unrelated to either casing) must not match via the name fallback.
	target := &core.PublishingTarget{Name: "webhook-1", Receivers: []string{"slack-critical"}}
	assert.False(t, targetMatchesReceiver(target, "Slack-Critical"))
}

// Test Suite: PublishingCoordinator.PublishToTargets receiver filtering (integration)

func newTestCoordinator(t *testing.T, discovery TargetDiscoveryManager) *PublishingCoordinator {
	t.Helper()

	logger := slog.Default()
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing

	factory := NewPublisherFactory(NewAlertFormatter(""), logger, nil, "")
	t.Cleanup(factory.Shutdown)

	queue := NewPublishingQueue(
		factory,
		nil,
		NewLRUJobTrackingStore(16),
		PublishingQueueConfig{
			WorkerCount:             0,
			HighPriorityQueueSize:   16,
			MediumPriorityQueueSize: 16,
			LowPriorityQueueSize:    16,
			MaxRetries:              0,
			RetryInterval:           time.Millisecond,
			Metrics:                 metrics,
		},
		nil,
		logger,
	)

	// DeliveryConfirmationTimeout (task rec): this coordinator's queue runs
	// with NO workers, so a submitted job can never report a delivery
	// outcome and PublishGroupToTargets waits out the full timeout for each
	// target. These tests only assert target RESOLUTION (which targets got a
	// job, which were filtered/skipped), so a millisecond-scale timeout
	// keeps them fast; every group result they produce is deliberately
	// Success == false / ErrDeliveryWaitTimeout. Delivery outcomes
	// themselves are covered in delivery_confirmation_test.go against real
	// workers and httptest servers.
	config := DefaultCoordinatorConfig()
	config.DeliveryConfirmationTimeout = 20 * time.Millisecond

	return NewPublishingCoordinator(queue, discovery, nil, config, logger)
}

func testAlert() *core.EnrichedAlert {
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp-1",
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			StartsAt:    time.Now().UTC(),
		},
	}
}

func resultNames(results []*PublishingResult) []string {
	names := make([]string, 0, len(results))
	for _, r := range results {
		names = append(names, r.Target.Name)
	}
	return names
}

func TestPublishToTargets_ReceiverFiltering_LabelScoped(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "slack-critical", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})
	discovery.AddTarget(&core.PublishingTarget{Name: "pagerduty-oncall", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"oncall"}})
	discovery.AddTarget(&core.PublishingTarget{Name: "legacy-catchall", Type: "webhook", URL: "http://example.com", Enabled: true})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "critical")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"slack-critical", "legacy-catchall"}, resultNames(results))
}

func TestPublishToTargets_ReceiverFiltering_FallbackByName(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "team-b", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"slack-critical"}})

	coordinator := newTestCoordinator(t, discovery)

	// "team-b" is not in the target's receiver list, but its Name matches
	// the receiver being queried -> fallback match.
	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "team-b")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"team-b"}, resultNames(results))
}

func TestPublishToTargets_ReceiverFiltering_EmptyReceiverMeansAllEnabled(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})
	discovery.AddTarget(&core.PublishingTarget{Name: "b", Type: "webhook", URL: "http://example.com", Enabled: true})
	discovery.AddTarget(&core.PublishingTarget{Name: "disabled", Type: "webhook", URL: "http://example.com", Enabled: false})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, resultNames(results))
}

func TestPublishToTargets_ReceiverFiltering_NoMatchPublishesToNone(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})
	discovery.AddTarget(&core.PublishingTarget{Name: "b", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"oncall"}})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "no-such-receiver")
	require.Error(t, err)
	assert.Empty(t, results)
}

func TestPublishToTargets_ExplicitTargetNames_UnaffectedByReceiverName(t *testing.T) {
	// Backward-compat path used by the manual "test target" HTTP handler:
	// explicit target-name resolution ignores receiver filtering entirely.
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), []string{"a"}, "some-other-receiver")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a"}, resultNames(results))
}

// Test Suite: PublishingCoordinator.PublishGroupToTargets (task 2.4,
// notify-stage chain batch publish)

func testGroupAlerts(n int) []*core.Alert {
	alerts := make([]*core.Alert, 0, n)
	for i := 0; i < n; i++ {
		alerts = append(alerts, &core.Alert{
			Fingerprint: "fp-" + string(rune('1'+i)),
			AlertName:   "TestAlert",
			Status:      core.StatusFiring,
			StartsAt:    time.Now().UTC(),
		})
	}
	return alerts
}

func TestPublishGroupToTargets_EmptyAlertsIsNoop(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishGroupToTargets(context.Background(), nil, "critical", "gk", nil, nil)
	require.NoError(t, err)
	assert.Nil(t, results)
}

// TestPublishGroupToTargets_ReceiverFiltering_ResolvesTargetsOnce covers
// task fwb's wire-level batching change: ONE PublishingResult PER matching
// TARGET now (one queue job carrying every alert), not one per (target,
// alert) pair as the pre-fwb one-job-per-alert shape produced.
func TestPublishGroupToTargets_ReceiverFiltering_ResolvesTargetsOnce(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "slack-critical", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})
	discovery.AddTarget(&core.PublishingTarget{Name: "pagerduty-oncall", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"oncall"}})

	coordinator := newTestCoordinator(t, discovery)

	// One group notification of 3 alerts, one matching target: expect
	// exactly ONE PublishingResult, for "slack-critical" — not one per
	// alert, and nothing for "pagerduty-oncall".
	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(3), "critical", "gk", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "slack-critical", results[0].Target.Name)
	// Success is NOT asserted here: newTestCoordinator's queue has no
	// workers, so no delivery outcome can arrive (task rec — see the helper's
	// comment). Confirmed-delivery assertions live in
	// delivery_confirmation_test.go.
	assert.False(t, results[0].Success)
	assert.ErrorIs(t, results[0].Error, ErrDeliveryWaitTimeout)
}

func TestPublishGroupToTargets_NoMatchingTargetsReturnsError(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(2), "no-such-receiver", "gk", nil, nil)
	require.Error(t, err)
	assert.Empty(t, results)
}

func TestPublishGroupToTargets_EmptyReceiverMeansAllEnabled(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true})
	discovery.AddTarget(&core.PublishingTarget{Name: "b", Type: "webhook", URL: "http://example.com", Enabled: true})
	discovery.AddTarget(&core.PublishingTarget{Name: "disabled", Type: "webhook", URL: "http://example.com", Enabled: false})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(1), "", "gk", nil, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, resultNames(results))
}

// TestPublishGroupToTargets_SkipTarget_ExcludesAlreadyDeliveredTargets
// covers task fwb's per-target nflog dedup hook: a target for which
// skipTarget returns true must be excluded entirely — no job submitted, no
// result reported — while a target it doesn't skip still gets its result.
func TestPublishGroupToTargets_SkipTarget_ExcludesAlreadyDeliveredTargets(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true})
	discovery.AddTarget(&core.PublishingTarget{Name: "b", Type: "webhook", URL: "http://example.com", Enabled: true})

	coordinator := newTestCoordinator(t, discovery)

	asked := map[string]bool{}
	skipTarget := func(target string) bool {
		asked[target] = true
		return target == "a"
	}

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(1), "", "gk", nil, skipTarget)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "b", results[0].Target.Name)
	assert.True(t, asked["a"], "skipTarget must be consulted for target a")
	assert.True(t, asked["b"], "skipTarget must be consulted for target b")
}
