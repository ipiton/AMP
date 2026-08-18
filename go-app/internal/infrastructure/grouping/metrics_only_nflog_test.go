package grouping

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ipiton/AMP/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final review finding 4: a replica whose publisher is in degraded /
// metrics-only mode (publishing.enabled: false, the lite profile, or a
// transient Kubernetes failure at startup) used to POISON the shared,
// cross-replica notification log. Its PublishGroup returned plain nil, so
// publishGroupAlerts called RecordSent — writing nflog:entry:<groupKey> with
// TTL = repeat_interval into the Redis every replica reads — and every HEALTHY
// replica then saw a delivery that never happened and skipped the group for a
// full repeat_interval.
//
// The contract is now explicit: a publisher that delivered nothing must return
// ErrDeliveryNotConfirmed (see manager.go), and publishGroupAlerts must skip
// RecordSent for it while treating it as a deliberate state, not a failure.

// nonDeliveringPublisher stands in for application.MetricsOnlyPublisher, which
// this package cannot import (application imports grouping). Its contract is
// asserted against the real type in
// internal/application/publishing_metrics_only_test.go.
type nonDeliveringPublisher struct {
	calls int
}

func (p *nonDeliveringPublisher) PublishGroup(_ context.Context, alerts []*core.Alert, _ string) error {
	if len(alerts) == 0 {
		return nil
	}
	p.calls++
	return fmt.Errorf("%w: publishing disabled", ErrDeliveryNotConfirmed)
}

func TestPublishGroupAlerts_MetricsOnlyPublisher_DoesNotRecordSent(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	notifyLog, cleanup := newReplicaRedisNotifyLog(t, mr)
	defer cleanup()

	degraded := &nonDeliveringPublisher{}
	replica := createTestManagerWithRedisNotifyLog(t, degraded, notifyLog)

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=DegradedAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "DegradedAlert"})
	_, err = replica.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	group, err := replica.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	replica.publishGroupAlerts(ctx, group)

	require.Equal(t, 1, degraded.calls, "the degraded publisher must still be called (metrics/log side effects)")

	// The observable that mattered: nothing recorded in shared Redis.
	keys := mr.Keys()
	for _, k := range keys {
		assert.NotContains(t, k, "nflog:entry",
			"a non-delivering publisher must not write a shared nflog entry (keys: %v)", keys)
	}

	// And the group must still read as un-notified for any replica.
	dup, err := notifyLog.IsDuplicate(ctx, groupKey, alertSetSignature([]*core.Alert{alert}), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.False(t, dup, "the group must not be deduped away for other replicas")
}

// TestPublishGroupAlerts_HealthyReplicaPublishesAfterDegradedOne is the
// end-to-end payoff: replica A is degraded, replica B is healthy, and they
// share only the Redis notify log. B must still deliver.
func TestPublishGroupAlerts_HealthyReplicaPublishesAfterDegradedOne(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	notifyLogA, cleanupA := newReplicaRedisNotifyLog(t, mr)
	defer cleanupA()
	notifyLogB, cleanupB := newReplicaRedisNotifyLog(t, mr)
	defer cleanupB()

	degraded := &nonDeliveringPublisher{}
	healthy := &mockPublisher{}
	replicaA := createTestManagerWithRedisNotifyLog(t, degraded, notifyLogA)
	replicaB := createTestManagerWithRedisNotifyLog(t, healthy, notifyLogB)

	ctx := context.Background()
	groupKey := GroupKey("receiver=default/alertname=FailoverAlert")
	alert := createTestAlert("A", core.StatusFiring, map[string]string{"alertname": "FailoverAlert"})

	_, err = replicaA.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)
	_, err = replicaB.AddAlertToGroup(ctx, alert, groupKey)
	require.NoError(t, err)

	groupA, err := replicaA.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	replicaA.publishGroupAlerts(ctx, groupA)
	require.Empty(t, healthy.calls(), "sanity: replica B has not published yet")

	groupB, err := replicaB.GetGroup(ctx, groupKey)
	require.NoError(t, err)
	replicaB.publishGroupAlerts(ctx, groupB)

	require.Len(t, healthy.calls(), 1,
		"the healthy replica must publish for real — the degraded one must not have claimed the send")

	// Now that a REAL delivery happened, the shared dedup entry must exist and
	// suppress the next fire.
	replicaB.publishGroupAlerts(ctx, groupB)
	assert.Len(t, healthy.calls(), 1, "a genuine send must still be deduped within repeat_interval")
}
