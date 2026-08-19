package publishing

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// Config-provisioned targets (FU-RECEIVERS-INTEGRATION) enter this package
// through the same discovery view as Secret-sourced ones, so the receiver
// scoping the coordinator already implements is what enforces R2 at publish
// time. These tests pin that end of the contract: a group routed to receiver X
// must reach X's OWN config targets plus the legacy unscoped K8s targets, and
// nothing belonging to another receiver.

func cfgTarget(receiver, kind string, index int, targetType string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:      "cfg:" + receiver + "/" + kind + string(rune('0'+index)),
		Type:      targetType,
		URL:       "http://example.com",
		Enabled:   true,
		Receivers: []string{receiver},
	}
}

func TestPublishToTargets_ConfigTargetsAreReceiverScoped(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	// Config-provisioned, scoped by construction (R2).
	discovery.AddTarget(cfgTarget("team-x", "webhook", 0, "webhook"))
	discovery.AddTarget(cfgTarget("team-x", "slack", 0, "webhook"))
	discovery.AddTarget(cfgTarget("team-y", "webhook", 0, "webhook"))
	// K8s-sourced: one scoped to another receiver, one legacy/unscoped.
	discovery.AddTarget(&core.PublishingTarget{Name: "slack-prod", Type: "webhook", URL: "http://example.com", Enabled: true, Receivers: []string{"team-y"}})
	discovery.AddTarget(&core.PublishingTarget{Name: "legacy-catchall", Type: "webhook", URL: "http://example.com", Enabled: true})

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "team-x")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"cfg:team-x/webhook0",
		"cfg:team-x/slack0",
		"legacy-catchall",
	}, resultNames(results))
}

func TestPublishGroupToTargets_ConfigTargetsAreReceiverScoped(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(cfgTarget("team-x", "webhook", 0, "webhook"))
	discovery.AddTarget(cfgTarget("team-y", "webhook", 0, "webhook"))
	discovery.AddTarget(&core.PublishingTarget{Name: "legacy-catchall", Type: "webhook", URL: "http://example.com", Enabled: true})

	coordinator := newTestCoordinator(t, discovery)

	alerts := []*core.Alert{testAlert().Alert}
	results, err := coordinator.PublishGroupToTargets(
		context.Background(),
		alerts,
		"team-x",
		`{}:{alertname="TestAlert"}`,
		map[string]string{"alertname": "TestAlert"},
		nil,
	)
	require.NoError(t, err)

	// Delivery itself cannot succeed here (the test queue has no workers — see
	// newTestCoordinator); what matters is WHICH targets were resolved.
	assert.ElementsMatch(t, []string{"cfg:team-x/webhook0", "legacy-catchall"}, resultNames(results))
}

// A cfg: target is never treated as unscoped: even a receiver name that
// happens to match nothing must not fall back to config targets of other
// receivers.
func TestPublishToTargets_ConfigTargetsNeverUnscoped(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(cfgTarget("team-x", "webhook", 0, "webhook"))

	coordinator := newTestCoordinator(t, discovery)

	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "team-z")
	require.Error(t, err, "no target belongs to team-z: a config gap must surface, not fan out")
	assert.Empty(t, results)
}

// ============================================================================
// Re-review finding R2: a receiver DECLARED in the config that provisions zero
// targets is upstream Alertmanager's blackhole receiver — it accepts the
// notification and drops it. It used to produce `no targets found for receiver`,
// which surfaced as a 207/500 on ingest (grouping off) or an error + a re-fire
// every group_interval forever (grouping on).
// ============================================================================

func TestPublishToTargets_BlackholeReceiverSucceedsWithNoResults(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(cfgTarget("team-x", "webhook", 0, "webhook"))

	coordinator := newTestCoordinator(t, discovery)
	// "null" is declared in the config but has no integrations.
	coordinator.SetKnownReceivers([]string{"team-x", "null"})

	results, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "null")
	require.NoError(t, err, "a declared receiver with no targets is an intentional drop, not a failure")
	assert.Empty(t, results)
}

func TestPublishToTargets_UnknownReceiverStillErrors(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(cfgTarget("team-x", "webhook", 0, "webhook"))

	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"team-x", "null"})

	_, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "typo-receiver")
	require.Error(t, err, "a receiver the config never declared is a real configuration gap")
}

// Without a known-receiver set (wiring never called SetKnownReceivers) the
// pre-R2 error behaviour must be preserved — no accidental silent drops.
func TestPublishToTargets_NoKnownReceiverSetKeepsErrorBehaviour(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(cfgTarget("team-x", "webhook", 0, "webhook"))

	coordinator := newTestCoordinator(t, discovery)

	_, err := coordinator.PublishToTargets(context.Background(), testAlert(), nil, "null")
	require.Error(t, err)
}

// Grouping ON: upstream treats a blackhole notification as SENT, so the group
// must get one successful outcome — that is what makes the notify chain write an
// nflog entry and settle the group instead of re-firing every group_interval.
func TestPublishGroupToTargets_BlackholeReportsSuccessfulOutcome(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"null"})

	alerts := []*core.Alert{testAlert().Alert}
	results, err := coordinator.PublishGroupToTargets(
		context.Background(), alerts, "null", `{}:{alertname="TestAlert"}`,
		map[string]string{"alertname": "TestAlert"}, nil,
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "blackhole:null", results[0].Target.Name)
	assert.True(t, results[0].Success, "upstream records a blackhole notification as sent")
}

// The synthetic target participates in the chain's own per-target dedup: once
// recorded, the next fire reports nothing new rather than re-recording.
func TestPublishGroupToTargets_BlackholeHonoursPerTargetDedup(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"null"})

	var asked []string
	alreadySent := func(target string, alerts []*core.Alert) []*core.Alert {
		asked = append(asked, target)
		return nil // nothing still owed
	}

	results, err := coordinator.PublishGroupToTargets(
		context.Background(), []*core.Alert{testAlert().Alert}, "null",
		`{}:{alertname="TestAlert"}`, nil, alreadySent,
	)
	require.NoError(t, err)
	assert.Empty(t, results, "already recorded this cycle: no new outcome to report")
	assert.Equal(t, []string{"blackhole:null"}, asked)
}

func TestPublishGroupToTargets_UnknownReceiverStillErrors(t *testing.T) {
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	coordinator := newTestCoordinator(t, discovery)
	coordinator.SetKnownReceivers([]string{"null"})

	_, err := coordinator.PublishGroupToTargets(
		context.Background(), []*core.Alert{testAlert().Alert}, "typo-receiver",
		`{}:{alertname="TestAlert"}`, nil, nil,
	)
	require.Error(t, err)
}
