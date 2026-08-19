package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
)

// A route WITHOUT group_by aggregates everything it matches into ONE group with
// an empty label set (upstream's DefaultRouteOpts leaves GroupBy empty). AMP's
// TreeBuilder used to substitute ["alertname"] there, which produced one group —
// and therefore one notification — PER ALERTNAME. This is the notify-path half of
// that regression: the API half is
// TestUpstreamParity_AlertGroupsWithoutGroupByHaveEmptyLabels (cmd/server,
// futureparity tag).
func TestProcessAlert_NoGroupBy_AllAlertsShareOneGroupKey(t *testing.T) {
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{}

	// Exactly what the tree produces for `route: {receiver: default}` with no
	// group_by anywhere: an empty GroupBy.
	decision := &RoutingDecision{
		Receiver:       "default",
		GroupBy:        nil,
		GroupWait:      10 * time.Second,
		GroupInterval:  time.Minute,
		RepeatInterval: time.Hour,
	}

	cfg := newTestProcessorConfig(t, &fakeRouteEvaluator{decision: decision}, publisher)
	cfg.GroupingEnabled = true
	cfg.GroupManager = groupMgr
	cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	for _, name := range []string{"GroupNoByA", "GroupNoByB"} {
		require.NoError(t, processor.ProcessAlert(context.Background(), &core.Alert{
			Fingerprint: "fp-" + name,
			AlertName:   name,
			Status:      core.StatusFiring,
			Labels:      map[string]string{"alertname": name, "service": "api"},
			StartsAt:    time.Now(),
		}))
	}

	require.Len(t, groupMgr.calls, 2, "both alerts must be grouped, not published directly")
	assert.Equal(t, groupMgr.calls[0].key, groupMgr.calls[1].key,
		"without group_by, alerts with DIFFERENT alertnames must land in the SAME group")
	assert.Equal(t, grouping.GroupKey("receiver=default/{global}"), groupMgr.calls[0].key,
		"the key must be the receiver-level global group")
	assert.Zero(t, publisher.publishToAllCalls+publisher.publishWithClassificationCalls)
}

// Sanity counter-case: an explicit group_by still splits, so the fix did not
// disable grouping.
func TestProcessAlert_ExplicitGroupBy_StillSplitsPerLabel(t *testing.T) {
	publisher := &fakePublisher{}
	groupMgr := &fakeGroupManager{}

	decision := &RoutingDecision{
		Receiver:       "default",
		GroupBy:        []string{"alertname"},
		GroupWait:      10 * time.Second,
		GroupInterval:  time.Minute,
		RepeatInterval: time.Hour,
	}

	cfg := newTestProcessorConfig(t, &fakeRouteEvaluator{decision: decision}, publisher)
	cfg.GroupingEnabled = true
	cfg.GroupManager = groupMgr
	cfg.GroupKeyGenerator = grouping.NewGroupKeyGenerator()
	processor, err := NewAlertProcessor(cfg)
	require.NoError(t, err)

	for _, name := range []string{"SplitA", "SplitB"} {
		require.NoError(t, processor.ProcessAlert(context.Background(), &core.Alert{
			Fingerprint: "fp-" + name,
			AlertName:   name,
			Status:      core.StatusFiring,
			Labels:      map[string]string{"alertname": name},
			StartsAt:    time.Now(),
		}))
	}

	require.Len(t, groupMgr.calls, 2)
	assert.NotEqual(t, groupMgr.calls[0].key, groupMgr.calls[1].key,
		"group_by: [alertname] must still produce one group per alertname")
}
