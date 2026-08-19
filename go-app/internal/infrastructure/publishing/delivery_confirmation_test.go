package publishing

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// === Task rec (alertmanager-parity wave 3): RecordSent on CONFIRMED
// delivery ===
//
// PublishingResult.Success used to mean "a job was enqueued for this
// target"; it now means "this target accepted the notification". The suite
// below pins that contract at the coordinator boundary (against real
// httptest endpoints and a real worker pool) and at the queue boundary (for
// the paths that never reach an HTTP call at all).

// newConfirmingCoordinator builds a coordinator over a queue with a LIVE
// worker pool, so submitted jobs really are published and really do report
// their delivery outcome back.
//
// maxRetries is passed through deliberately: 0 keeps a failing target to one
// HTTP attempt so the "500 is not confirmed" tests finish in milliseconds
// instead of waiting out an exponential backoff ladder.
func newConfirmingCoordinator(t *testing.T, discovery TargetDiscoveryManager, confirmTimeout time.Duration, maxRetries int) *PublishingCoordinator {
	t.Helper()

	logger := slog.Default()
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing

	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), logger, metrics, ""),
		nil,
		NewLRUJobTrackingStore(16),
		PublishingQueueConfig{
			WorkerCount:             4,
			HighPriorityQueueSize:   16,
			MediumPriorityQueueSize: 16,
			LowPriorityQueueSize:    16,
			MaxRetries:              maxRetries,
			RetryInterval:           time.Millisecond,
			Metrics:                 metrics,
		},
		nil,
		logger,
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	config := DefaultCoordinatorConfig()
	config.DeliveryConfirmationTimeout = confirmTimeout

	return NewPublishingCoordinator(queue, discovery, nil, config, logger)
}

// webhookStub is an httptest webhook counting the requests it received and
// answering every one of them with status.
type webhookStub struct {
	server *httptest.Server
	hits   atomic.Int64
}

func newWebhookStub(t *testing.T, status int) *webhookStub {
	t.Helper()
	stub := &webhookStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		stub.hits.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *webhookStub) target(name string) *core.PublishingTarget {
	return &core.PublishingTarget{
		Name:    name,
		Type:    "webhook",
		URL:     s.server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	}
}

// TestPublishGroupToTargets_SuccessIsAConfirmedHTTPDelivery is the positive
// half of the contract: the endpoint really was called, and only then is the
// outcome Success (which is what lets the notify chain write its nflog
// entry).
func TestPublishGroupToTargets_SuccessIsAConfirmedHTTPDelivery(t *testing.T) {
	stub := newWebhookStub(t, http.StatusOK)

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(stub.target("webhook-ok"))

	coordinator := newConfirmingCoordinator(t, discovery, 5*time.Second, 0)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(3), "", "gk-ok", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Success, "a 200 from the target must be reported as confirmed delivery")
	assert.NoError(t, results[0].Error)
	// One wire-level POST for the whole group (task fwb batching), already
	// completed by the time PublishGroupToTargets returned — the point of
	// task rec is that the call no longer returns before delivery.
	assert.Equal(t, int64(1), stub.hits.Load())
}

// TestPublishGroupToTargets_HTTP500IsNotConfirmed is the regression this
// whole task exists for: a webhook that answers 500 AFTER its job was
// enqueued used to be reported as Success (enqueue confirmation) and got an
// nflog entry, suppressing the group for a whole repeat_interval.
func TestPublishGroupToTargets_HTTP500IsNotConfirmed(t *testing.T) {
	stub := newWebhookStub(t, http.StatusInternalServerError)

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(stub.target("webhook-500"))

	coordinator := newConfirmingCoordinator(t, discovery, 5*time.Second, 0)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(2), "", "gk-500", nil, nil)
	require.NoError(t, err, "a failed delivery is a per-target outcome, not a whole-call error")
	require.Len(t, results, 1)
	assert.False(t, results[0].Success, "HTTP 500 must never be reported as a confirmed delivery")
	require.Error(t, results[0].Error)
	assert.Positive(t, stub.hits.Load(), "delivery must actually have been attempted")
}

// TestPublishGroupToTargets_MixedBatchReportsPerTargetOutcomes covers the
// partial-failure shape: the notify chain records an nflog entry only for
// the confirmed target, so the failed one is retried on the next fire.
func TestPublishGroupToTargets_MixedBatchReportsPerTargetOutcomes(t *testing.T) {
	ok := newWebhookStub(t, http.StatusOK)
	broken := newWebhookStub(t, http.StatusInternalServerError)

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(ok.target("target-ok"))
	discovery.AddTarget(broken.target("target-500"))

	coordinator := newConfirmingCoordinator(t, discovery, 5*time.Second, 0)

	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(2), "", "gk-mixed", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)

	byName := map[string]*PublishingResult{}
	for _, r := range results {
		byName[r.Target.Name] = r
	}

	require.Contains(t, byName, "target-ok")
	require.Contains(t, byName, "target-500")
	assert.True(t, byName["target-ok"].Success)
	assert.False(t, byName["target-500"].Success)
	assert.Positive(t, ok.hits.Load())
	assert.Positive(t, broken.hits.Load())
}

// TestPublishGroupToTargets_ConfirmationTimeoutIsNotSuccess pins the timeout
// direction: a target that has not reported an outcome by the deadline is
// unconfirmed, never "probably fine". The job keeps running on the worker
// pool — see ErrDeliveryWaitTimeout.
func TestPublishGroupToTargets_ConfirmationTimeoutIsNotSuccess(t *testing.T) {
	release := make(chan struct{})
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release) // let the parked handler finish before the server closes
		server.Close()
	})

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{
		Name:    "webhook-slow",
		Type:    "webhook",
		URL:     server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	})

	coordinator := newConfirmingCoordinator(t, discovery, 150*time.Millisecond, 0)

	start := time.Now()
	results, err := coordinator.PublishGroupToTargets(context.Background(), testGroupAlerts(1), "", "gk-slow", nil, nil)
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Success, "an unfinished delivery must not be recorded as sent")
	assert.ErrorIs(t, results[0].Error, ErrDeliveryWaitTimeout)
	assert.Less(t, elapsed, 3*time.Second, "the wait must be bounded by DeliveryConfirmationTimeout")
}

// TestPublishGroupToTargets_ContextCancellationIsNotSuccess: the caller's
// context dying mid-delivery is also an unconfirmed target.
func TestPublishGroupToTargets_ContextCancellationIsNotSuccess(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(&core.PublishingTarget{
		Name:    "webhook-slow",
		Type:    "webhook",
		URL:     server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	})

	coordinator := newConfirmingCoordinator(t, discovery, 10*time.Second, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	results, err := coordinator.PublishGroupToTargets(ctx, testGroupAlerts(1), "", "gk-ctx", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Success)
	require.Error(t, results[0].Error)
}

// === Queue-level: outcomes for jobs that never reach an HTTP attempt ===

// metricsOnlyModeManager is a ModeManager stuck in metrics-only mode.
type metricsOnlyModeManager struct{}

func (m *metricsOnlyModeManager) GetCurrentMode() Mode { return ModeMetricsOnly }
func (m *metricsOnlyModeManager) IsMetricsOnly() bool  { return true }
func (m *metricsOnlyModeManager) CheckModeTransition() (Mode, bool, error) {
	return ModeMetricsOnly, false, nil
}
func (m *metricsOnlyModeManager) OnTargetsChanged() error { return nil }
func (m *metricsOnlyModeManager) Subscribe(_ ModeChangeCallback) UnsubscribeFunc {
	return func() {}
}
func (m *metricsOnlyModeManager) GetModeMetrics() ModeMetrics   { return ModeMetrics{} }
func (m *metricsOnlyModeManager) Start(_ context.Context) error { return nil }
func (m *metricsOnlyModeManager) Stop() error                   { return nil }

// TestSubmitGroupWithConfirmation_MetricsOnlyModeReportsNotAttempted: the
// worker drops the job without publishing, and must still report an
// outcome — a non-nil one, so no nflog entry is written (the same reasoning
// as grouping.ErrDeliveryNotConfirmed for MetricsOnlyPublisher).
func TestSubmitGroupWithConfirmation_MetricsOnlyModeReportsNotAttempted(t *testing.T) {
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), metrics, ""),
		nil,
		NewLRUJobTrackingStore(16),
		PublishingQueueConfig{WorkerCount: 1, HighPriorityQueueSize: 4, MediumPriorityQueueSize: 4, LowPriorityQueueSize: 4, Metrics: metrics},
		&metricsOnlyModeManager{},
		slog.Default(),
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	target := &core.PublishingTarget{Name: "webhook-1", Type: "webhook", URL: "http://127.0.0.1:1", Enabled: true, Format: core.FormatWebhook}
	confirm, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
	require.NoError(t, err)
	require.NotNil(t, confirm)

	select {
	case outcome := <-confirm:
		require.Error(t, outcome, "metrics-only mode delivers nothing, so it must not confirm delivery")
		assert.ErrorIs(t, outcome, ErrDeliveryNotAttempted)
	case <-time.After(3 * time.Second):
		t.Fatal("a skipped job must report an outcome instead of leaving the caller waiting")
	}
}

// TestProcessJob_OpenCircuitBreakerReportsNotAttempted: the breaker short-
// circuits before any HTTP call, which is a non-delivery like any other.
func TestProcessJob_OpenCircuitBreakerReportsNotAttempted(t *testing.T) {
	queue := newTestPublishingQueue()

	target := &core.PublishingTarget{Name: "tripped", Type: "webhook", URL: "http://127.0.0.1:1", Enabled: true, Format: core.FormatWebhook}
	cb := queue.getCircuitBreaker(target.Name)
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}
	require.False(t, cb.CanAttempt(), "breaker must be open for this test to mean anything")

	confirm, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
	require.NoError(t, err)

	// No workers in newTestPublishingQueue: drive the job by hand.
	queue.processJob(<-queue.mediumPriorityJobs)

	select {
	case outcome := <-confirm:
		require.Error(t, outcome)
		assert.ErrorIs(t, outcome, ErrDeliveryNotAttempted)
	case <-time.After(time.Second):
		t.Fatal("processJob must report an outcome on every exit path")
	}
}

// TestSubmitGroupWithConfirmation_EnqueueFailureReturnsNoChannel: a job that
// was never queued has no outcome to wait for, so the caller must get the
// error immediately rather than a channel that blocks until its timeout.
func TestSubmitGroupWithConfirmation_EnqueueFailureReturnsNoChannel(t *testing.T) {
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), metrics, ""),
		nil,
		NewLRUJobTrackingStore(16),
		// Capacity 1 everywhere, no workers: the second submission of the
		// same priority cannot be enqueued.
		PublishingQueueConfig{WorkerCount: 0, HighPriorityQueueSize: 1, MediumPriorityQueueSize: 1, LowPriorityQueueSize: 1, Metrics: metrics},
		nil,
		slog.Default(),
	)

	target := &core.PublishingTarget{Name: "webhook-1", Type: "webhook", URL: "http://127.0.0.1:1", Enabled: true, Format: core.FormatWebhook}

	_, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk-1", "recv", nil)
	require.NoError(t, err)

	confirm, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk-2", "recv", nil)
	require.Error(t, err, "queue is full")
	assert.Nil(t, confirm)
}

// TestSubmitGroup_StaysFireAndForget: the pre-rec entry point must keep
// working without a completion channel — a job with no waiter must not make
// the worker block or panic.
func TestSubmitGroup_StaysFireAndForget(t *testing.T) {
	stub := newWebhookStub(t, http.StatusOK)
	queue := newTestPublishingQueue()

	require.NoError(t, queue.SubmitGroup(testGroupAlerts(1), stub.target("webhook-ok"), "gk", "recv", nil))

	job := <-queue.mediumPriorityJobs
	require.Nil(t, job.completion)
	queue.processJob(job) // must not panic on the nil completion
	assert.Equal(t, int64(1), stub.hits.Load())
}
