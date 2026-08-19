package publishing

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
)

// === Task fu4 (alertmanager-parity wave 4): per-alert outcome tracking for
// non-batch publishers ===
//
// A Slack/Telegram/PagerDuty/Email group job sends one wire message PER ALERT.
// Before this task the job's outcome was all-or-nothing, so alert 3 of 5
// failing re-sent all five on the next fire. The suite below pins the two
// halves of the fix at the queue boundary (which alerts a job re-attempts, and
// what progress it reports back) and at the coordinator boundary (the progress
// reaching PublishingResult), plus the wave-3 batch contract staying untouched.

// groupJobWithConfirmation builds a non-enqueued group job carrying a
// completion channel, i.e. the shape SubmitGroupWithConfirmation produces —
// per-alert progress is only published for jobs someone is waiting on.
func groupJobWithConfirmation(alerts []*core.Alert, targetType string) *PublishingJob {
	job := &PublishingJob{
		EnrichedAlert: &core.EnrichedAlert{Alert: alerts[0]},
		Target:        &core.PublishingTarget{Name: "telegram-1", Type: targetType},
		Alerts:        alerts,
		GroupKey:      "gk",
		Receiver:      "ops",
	}
	job.completion = newJobCompletion()
	return job
}

// TestPublishJob_NonBatch_PartialFailureRecordsTheAlertsThatLanded is the
// headline queue-level assertion: the four alerts that were accepted are
// reported even though the job as a whole failed.
func TestPublishJob_NonBatch_PartialFailureRecordsTheAlertsThatLanded(t *testing.T) {
	queue := newTestPublishingQueue(t)

	alerts := testGroupBatchAlerts(5)
	publisher := &countingAlertPublisher{failOn: "fp-3"}
	job := groupJobWithConfirmation(alerts, "telegram")

	err := queue.publishJob(publisher, job)
	require.Error(t, err, "the job as a whole must still report failure")

	assert.ElementsMatch(t,
		[]string{"fp-1:firing", "fp-2:firing", "fp-4:firing", "fp-5:firing"},
		job.deliveredSnapshot(),
		"every alert the target accepted must be reported; the failing one must not be")
}

// TestPublishJob_NonBatch_RetryResendsOnlyTheFailedAlert is the within-job
// half: retryPublish calls publishJob again for the same job, and that attempt
// must not repeat the wire messages that already landed.
func TestPublishJob_NonBatch_RetryResendsOnlyTheFailedAlert(t *testing.T) {
	queue := newTestPublishingQueue(t)

	alerts := testGroupBatchAlerts(5)
	publisher := &countingAlertPublisher{failOn: "fp-3"}
	job := groupJobWithConfirmation(alerts, "telegram")

	require.Error(t, queue.publishJob(publisher, job))
	require.Len(t, publisher.fingerprints, 5, "first attempt sends the whole set")

	// Second attempt of the SAME job, with the endpoint now healthy.
	publisher.failOn = ""
	publisher.fingerprints = nil
	require.NoError(t, queue.publishJob(publisher, job))

	assert.Equal(t, []string{"fp-3"}, publisher.fingerprints,
		"a retry must resend ONLY the alert that failed, not the four that already landed")
	assert.Len(t, job.deliveredSnapshot(), 5, "the retry completes the delivered set")
}

// TestPublishJob_NonBatch_ProgressIsCumulativeAcrossAttempts pins that a
// second partial failure adds to the delivered set instead of replacing it.
func TestPublishJob_NonBatch_ProgressIsCumulativeAcrossAttempts(t *testing.T) {
	queue := newTestPublishingQueue(t)

	alerts := testGroupBatchAlerts(4)
	publisher := &countingAlertPublisher{failOn: "fp-2"}
	job := groupJobWithConfirmation(alerts, "telegram")

	require.Error(t, queue.publishJob(publisher, job))
	require.ElementsMatch(t, []string{"fp-1:firing", "fp-3:firing", "fp-4:firing"}, job.deliveredSnapshot())

	publisher.failOn = "fp-2" // still failing
	require.Error(t, queue.publishJob(publisher, job))
	assert.ElementsMatch(t, []string{"fp-1:firing", "fp-3:firing", "fp-4:firing"}, job.deliveredSnapshot(),
		"a second failed attempt must neither lose nor duplicate earlier progress")
}

// TestPublishJob_Batch_RecordsNoPerAlertProgress is the wave-3 regression
// guard: a batch-capable target delivers one POST for the whole group, so
// there is no partial state to report and none must be invented.
func TestPublishJob_Batch_RecordsNoPerAlertProgress(t *testing.T) {
	queue := newTestPublishingQueue(t)

	alerts := testGroupBatchAlerts(3)
	publisher := &countingBatchPublisher{}
	job := groupJobWithConfirmation(alerts, string(TargetTypeWebhook))

	require.NoError(t, queue.publishJob(publisher, job))
	assert.Nil(t, job.deliveredSnapshot(), "a batch publisher must not produce per-alert progress")
	assert.Nil(t, job.progress, "the per-alert tracker must not even be allocated on the batch path")
}

// TestPublishJob_SingleAlertJob_RecordsNoPerAlertProgress covers the
// fire-and-forget Submit path (no group set at all).
func TestPublishJob_SingleAlertJob_RecordsNoPerAlertProgress(t *testing.T) {
	queue := newTestPublishingQueue(t)

	alerts := testGroupBatchAlerts(1)
	publisher := &countingAlertPublisher{}
	job := &PublishingJob{
		EnrichedAlert: &core.EnrichedAlert{Alert: alerts[0]},
		Target:        &core.PublishingTarget{Name: "telegram-1", Type: "telegram"},
	}

	require.NoError(t, queue.publishJob(publisher, job))
	assert.Nil(t, job.deliveredSnapshot())
}

// TestPublishJob_NoWaiter_KeepsProgressUnpublished proves the snapshot is only
// maintained for jobs someone can read it from: a fire-and-forget SubmitGroup
// job still skips already-delivered alerts on retry (progress is tracked) but
// pays no allocation for a snapshot nobody will load.
func TestPublishJob_NoWaiter_KeepsProgressUnpublished(t *testing.T) {
	queue := newTestPublishingQueue(t)

	alerts := testGroupBatchAlerts(3)
	publisher := &countingAlertPublisher{failOn: "fp-2"}
	job := &PublishingJob{
		EnrichedAlert: &core.EnrichedAlert{Alert: alerts[0]},
		Target:        &core.PublishingTarget{Name: "telegram-1", Type: "telegram"},
		Alerts:        alerts,
		GroupKey:      "gk",
		Receiver:      "ops",
	}

	require.Error(t, queue.publishJob(publisher, job))
	assert.Nil(t, job.deliveredSnapshot(), "no completion channel ⇒ no published snapshot")
	require.NotNil(t, job.progress)
	assert.True(t, job.progress.has("fp-1:firing"), "progress is still tracked for within-job retries")
}

// TestGroupPublishHandle_DeliveredAlerts_NilSafe covers the defensive paths
// (nil handle from a failed submit, handle with no job).
func TestGroupPublishHandle_DeliveredAlerts_NilSafe(t *testing.T) {
	var nilHandle *GroupPublishHandle
	assert.Nil(t, nilHandle.DeliveredAlerts())
	assert.Nil(t, (&GroupPublishHandle{}).DeliveredAlerts())
}

// perAlertStub is an httptest endpoint standing in for a non-batch
// integration: it answers every request 200 except the failAt-th one, which
// gets a 500.
//
// Request order equals the job's alert order — publishJob iterates one job's
// alerts sequentially on a single worker — and the target type (telegram with
// no bot token, i.e. the plain HTTP publisher) performs no client-side retry
// of its own, so "the failAt-th request" is exactly "the failAt-th alert" as
// long as the queue's own MaxRetries is 0.
type perAlertStub struct {
	server *httptest.Server
	hits   atomic.Int64
	failAt int64
}

func newPerAlertStub(t *testing.T, failAt int64) *perAlertStub {
	t.Helper()
	stub := &perAlertStub{failAt: failAt}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if stub.hits.Add(1) == stub.failAt {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *perAlertStub) target(name string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:    name,
		Type:    string(TargetTypeTelegram),
		URL:     s.server.URL,
		Enabled: true,
		Format:  core.FormatTelegram,
	}
}

// TestPublishGroupToTargets_NonBatchPartialFailureReportsDeliveredAlerts is
// the coordinator-boundary assertion, against a real worker pool and a real
// HTTP endpoint: the unconfirmed target's result carries exactly the alerts
// that did land, which is what lets the notify chain retry only the rest.
func TestPublishGroupToTargets_NonBatchPartialFailureReportsDeliveredAlerts(t *testing.T) {
	stub := newPerAlertStub(t, 3) // third wire message (alert fp-3) fails

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(stub.target("telegram-partial"))

	coordinator := newConfirmingCoordinator(t, discovery, 10*time.Second, 0)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupBatchAlerts(5), "", "gk-partial", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.False(t, results[0].Success, "a per-alert failure leaves the target unconfirmed (wave-3 semantics)")
	assert.ElementsMatch(t,
		[]string{"fp-1:firing", "fp-2:firing", "fp-4:firing", "fp-5:firing"},
		results[0].DeliveredAlerts,
		"the four alerts the target accepted must be reported back")
}

// TestPublishGroupToTargets_FullSuccessReportsNoDeliveredAlerts pins the
// happy-path contract: a confirmed target gets a full nflog entry, so no
// per-alert state is produced (no extra Redis key on the common path).
func TestPublishGroupToTargets_FullSuccessReportsNoDeliveredAlerts(t *testing.T) {
	stub := newPerAlertStub(t, 0) // never fails

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(stub.target("telegram-ok"))

	coordinator := newConfirmingCoordinator(t, discovery, 10*time.Second, 0)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupBatchAlerts(3), "", "gk-ok-nonbatch", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.True(t, results[0].Success)
	assert.Nil(t, results[0].DeliveredAlerts, "full success must not carry per-alert state")
	assert.Equal(t, int64(3), stub.hits.Load(), "one wire message per alert for a non-batch target")
}

// TestPublishGroupToTargets_BatchTargetReportsNoDeliveredAlerts is the
// wave-3 batch regression guard at the coordinator boundary: a failing
// webhook target stays a plain unconfirmed outcome with no per-alert state.
func TestPublishGroupToTargets_BatchTargetReportsNoDeliveredAlerts(t *testing.T) {
	stub := newWebhookStub(t, http.StatusInternalServerError)

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(stub.target("webhook-500"))

	coordinator := newConfirmingCoordinator(t, discovery, 10*time.Second, 0)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupBatchAlerts(4), "", "gk-batch-500", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.False(t, results[0].Success)
	assert.Nil(t, results[0].DeliveredAlerts, "a batch target has no per-alert outcome to report")
	assert.Equal(t, int64(1), stub.hits.Load(), "still exactly ONE POST for the whole group")
}
