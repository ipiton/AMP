package publishing

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// FU-RECEIVERS-INTEGRATION slice 2: `send_resolved: false` on an integration
// must suppress RESOLVED notifications for that target only, and suppression is
// "send nothing, record nothing, not an error" — upstream's DedupStage outcome.
//
// Filtering lives in target RESOLUTION, not in a publisher: the publisher layer
// is this epic's fixed boundary, and filtering here also keeps the suppressed
// notification out of the queue entirely (no job, no retry, no breaker effect).

func noResolvedTarget(name, receiver string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:         name,
		Type:         "webhook",
		URL:          "http://example.com",
		Enabled:      true,
		Receivers:    []string{receiver},
		FilterConfig: map[string]any{FilterConfigSendResolved: false},
	}
}

func resolvedAlert() *core.EnrichedAlert {
	endsAt := time.Now().UTC()
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: "fp-resolved",
			AlertName:   "TestAlert",
			Status:      core.StatusResolved,
			StartsAt:    time.Now().UTC().Add(-time.Minute),
			EndsAt:      &endsAt,
		},
	}
}

func TestTargetAcceptsAlertStatus(t *testing.T) {
	plain := &core.PublishingTarget{Name: "plain"}
	assert.True(t, targetAcceptsAlertStatus(plain, core.StatusResolved),
		"no filter_config means upstream's default (send_resolved: true)")
	assert.True(t, targetAcceptsAlertStatus(nil, core.StatusResolved))

	off := noResolvedTarget("off", "team-x")
	assert.False(t, targetAcceptsAlertStatus(off, core.StatusResolved))
	assert.True(t, targetAcceptsAlertStatus(off, core.StatusFiring),
		"send_resolved never affects firing notifications")

	// Shapes a K8s Secret's JSON filter_config can round-trip into.
	for _, value := range []any{"false", "FALSE", float64(0)} {
		target := &core.PublishingTarget{FilterConfig: map[string]any{FilterConfigSendResolved: value}}
		assert.False(t, targetAcceptsAlertStatus(target, core.StatusResolved), "value %v", value)
	}
	for _, value := range []any{"true", float64(1), "unexpected"} {
		target := &core.PublishingTarget{FilterConfig: map[string]any{FilterConfigSendResolved: value}}
		assert.True(t, targetAcceptsAlertStatus(target, core.StatusResolved), "value %v", value)
	}
}

func TestPublishToTargets_SendResolvedFalseSuppressesResolvedOnly(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(noResolvedTarget("cfg:team-x/webhook0", "team-x"))
	discovery.AddTarget(&core.PublishingTarget{
		Name: "cfg:team-x/webhook1", Type: "webhook", URL: "http://example.com",
		Enabled: true, Receivers: []string{"team-x"},
	})

	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"team-x"})

	// Firing: both targets.
	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "team-x")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"cfg:team-x/webhook0", "cfg:team-x/webhook1"}, resultNames(results))

	// Resolved: only the target that wants them.
	results, err = coordinator.PublishToTargets(context.Background(), resolvedAlert(), nil, "team-x")
	require.NoError(t, err)
	assert.Equal(t, []string{"cfg:team-x/webhook1"}, resultNames(results))
}

// Every target of the receiver declines → no error, nothing published, and NOT
// counted as a blackhole (the receiver does have targets).
func TestPublishToTargets_AllTargetsDeclineResolvedIsNotAnError(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(noResolvedTarget("cfg:team-x/webhook0", "team-x"))

	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"team-x"})

	results, err := coordinator.PublishToTargets(context.Background(), resolvedAlert(), nil, "team-x")
	require.NoError(t, err, "suppression is not a delivery failure")
	assert.Empty(t, results)
}

func TestPublishGroupToTargets_SendResolvedNarrowsTheGroup(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(noResolvedTarget("cfg:team-x/webhook0", "team-x"))

	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"team-x"})

	endsAt := time.Now().UTC()
	firing := &core.Alert{Fingerprint: "fp-1", AlertName: "TestAlert", Status: core.StatusFiring, StartsAt: time.Now().UTC()}
	resolved := &core.Alert{Fingerprint: "fp-2", AlertName: "TestAlert", Status: core.StatusResolved, StartsAt: time.Now().UTC(), EndsAt: &endsAt}

	var owedSeen []*core.Alert
	_, err := coordinator.PublishGroupToTargets(
		context.Background(),
		[]*core.Alert{firing, resolved},
		"team-x",
		`{}:{alertname="TestAlert"}`,
		map[string]string{"alertname": "TestAlert"},
		func(target string, alerts []*core.Alert) []*core.Alert {
			owedSeen = alerts
			return alerts
		},
	)
	require.NoError(t, err)

	require.Len(t, owedSeen, 1, "the resolved alert must be filtered out before the dedup callback")
	assert.Equal(t, "fp-1", owedSeen[0].Fingerprint)
}

// A resolved-only group for a target that declines them: nothing to send, no
// error, no outcomes — so the notify chain records nothing (upstream's
// DedupStage stops the pipeline the same way).
func TestPublishGroupToTargets_ResolvedOnlyGroupWithSendResolvedFalse(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(noResolvedTarget("cfg:team-x/webhook0", "team-x"))

	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"team-x"})

	endsAt := time.Now().UTC()
	resolved := &core.Alert{Fingerprint: "fp-2", AlertName: "TestAlert", Status: core.StatusResolved, StartsAt: time.Now().UTC(), EndsAt: &endsAt}

	results, err := coordinator.PublishGroupToTargets(
		context.Background(), []*core.Alert{resolved}, "team-x",
		`{}:{alertname="TestAlert"}`, nil, nil,
	)
	require.NoError(t, err, "must not be reported as a publish failure")
	assert.Empty(t, results, "and must not fabricate a delivered outcome either")
}

// The fully-deduped steady state (every target already covered this cycle) is
// also a no-error, no-outcome result — it used to return
// `no targets found for receiver`, which the notify chain logged as a publish
// error on every fire.
func TestPublishGroupToTargets_FullyDedupedIsNotAnError(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{
		Name: "cfg:team-x/webhook0", Type: "webhook", URL: "http://example.com",
		Enabled: true, Receivers: []string{"team-x"},
	})

	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"team-x"})

	results, err := coordinator.PublishGroupToTargets(
		context.Background(), []*core.Alert{testAlert().Alert}, "team-x",
		`{}:{alertname="TestAlert"}`, nil,
		func(string, []*core.Alert) []*core.Alert { return nil }, // everything already sent
	)
	require.NoError(t, err)
	assert.Empty(t, results)
}
