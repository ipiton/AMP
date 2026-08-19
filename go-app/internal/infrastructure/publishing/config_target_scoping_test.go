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
