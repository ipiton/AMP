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

	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), logger, nil, ""),
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

	return NewPublishingCoordinator(queue, discovery, nil, DefaultCoordinatorConfig(), logger)
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

	results, err := coordinator.PublishGroupToTargets(context.Background(), nil, "critical")
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestPublishGroupToTargets_ReceiverFiltering_ResolvesTargetsOnce(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "slack-critical", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})
	discovery.AddTarget(&core.PublishingTarget{Name: "pagerduty-oncall", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"oncall"}})

	coordinator := newTestCoordinator(t, discovery)

	// One group notification of 3 alerts, one matching target: expect one
	// PublishingResult PER (target, alert) pair — 3 results, all against
	// "slack-critical" — not against "pagerduty-oncall".
	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(3), "critical")
	require.NoError(t, err)
	require.Len(t, results, 3)
	for _, r := range results {
		assert.Equal(t, "slack-critical", r.Target.Name)
		assert.True(t, r.Success)
	}
}

func TestPublishGroupToTargets_NoMatchingTargetsReturnsError(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"critical"}})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(2), "no-such-receiver")
	require.Error(t, err)
	assert.Empty(t, results)
}

func TestPublishGroupToTargets_EmptyReceiverMeansAllEnabled(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{Name: "a", Type: "webhook", URL: "http://example.com", Enabled: true})
	discovery.AddTarget(&core.PublishingTarget{Name: "b", Type: "webhook", URL: "http://example.com", Enabled: true})
	discovery.AddTarget(&core.PublishingTarget{Name: "disabled", Type: "webhook", URL: "http://example.com", Enabled: false})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(1), "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a", "b"}, resultNames(results))
}
