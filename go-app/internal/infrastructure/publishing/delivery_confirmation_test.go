package publishing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

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
	// Pin awaitDelivery's ctx-branch wrapping contract (fix round 1, review
	// finding M5): the ctx path must be recognizable BOTH as an unconfirmed
	// delivery and as the underlying context failure.
	assert.ErrorIs(t, results[0].Error, ErrDeliveryWaitTimeout)
	assert.ErrorIs(t, results[0].Error, context.DeadlineExceeded)
}

// TestPublishGroupToTargets_ShutdownContextDoesNotCountAgainstBreaker is the
// regression test for review finding M-b: on a real SIGTERM the grouping
// context is explicitly cancelled (context.Canceled), not merely allowed to
// time out (context.DeadlineExceeded) — awaitDelivery's ctx.Done() branch
// must tell the two apart and abandon with AbandonReasonShutdown for the
// former, so an in-flight target's breaker sees nothing from a shutdown it
// had no part in.
func TestPublishGroupToTargets_ShutdownContextDoesNotCountAgainstBreaker(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	target := &core.PublishingTarget{
		Name:    "webhook-shutdown",
		Type:    "webhook",
		URL:     server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	}
	discovery := NewStubTargetDiscoveryManager(slog.Default())
	discovery.AddTarget(target)

	coordinator := newConfirmingCoordinator(t, discovery, 10*time.Second, 0)

	ctx, cancel := context.WithCancel(context.Background())
	// Simulate SIGTERM: cancel the grouping context explicitly, mid-delivery,
	// rather than letting a deadline expire.
	go func() {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			return
		}
		cancel()
	}()

	results, err := coordinator.PublishGroupToTargets(ctx, testGroupAlerts(1), "", "gk-shutdown", nil, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Success)
	require.Error(t, results[0].Error)
	assert.ErrorIs(t, results[0].Error, ErrDeliveryWaitTimeout)
	assert.ErrorIs(t, results[0].Error, context.Canceled)

	assert.Zero(t, coordinator.queue.getCircuitBreaker(target.Name).GetFailureCount(),
		"a shutdown-driven cancellation must not count against the target's circuit breaker")
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
	handle, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
	require.NoError(t, err)
	require.NotNil(t, handle)
	defer handle.Abandon(AbandonReasonSettled)

	select {
	case outcome := <-handle.Done():
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

	handle, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
	require.NoError(t, err)
	defer handle.Abandon(AbandonReasonSettled)

	// No workers in newTestPublishingQueue: drive the job by hand.
	queue.processJob(<-queue.mediumPriorityJobs)

	select {
	case outcome := <-handle.Done():
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

	first, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk-1", "recv", nil)
	require.NoError(t, err)
	defer first.Abandon(AbandonReasonSettled)

	handle, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk-2", "recv", nil)
	require.Error(t, err, "queue is full")
	assert.Nil(t, handle, "a job that was never enqueued has no outcome to wait for")
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

// === Fix round 1 (review finding I2): abandoning an unawaited job ===

// newHangingWebhook serves requests that never answer until the test tears
// down, and signals on the returned channel when a request arrives.
//
// The handler parks on an explicit release channel rather than on
// r.Context(): net/http only cancels a server-side request context once it
// notices the peer closed the connection, which is not prompt for a handler
// that has not written anything yet — so client-side cancellation (what this
// suite actually asserts, via the job's own outcome) is invisible here. The
// release channel is closed BEFORE Server.Close so teardown does not block on
// a parked handler.
func newHangingWebhook(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		rw.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	return server, started
}

// TestGroupPublishHandle_AbandonCancelsInFlightPublish pins review finding
// I2's fix: when the waiter gives up, the job's context is cancelled, so the
// in-flight HTTP request unwinds at once instead of holding a worker for the
// queue's whole retry budget (~2min with production defaults) while later
// ticks submit fresh jobs for the same (group, target).
func TestGroupPublishHandle_AbandonCancelsInFlightPublish(t *testing.T) {
	server, started := newHangingWebhook(t)

	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), metrics, ""),
		nil,
		NewLRUJobTrackingStore(8),
		// MaxRetries 3: without cancellation this job would keep a worker for
		// four attempts plus back-off, which is exactly the starvation the
		// finding describes.
		PublishingQueueConfig{WorkerCount: 2, HighPriorityQueueSize: 8, MediumPriorityQueueSize: 8, LowPriorityQueueSize: 8, MaxRetries: 3, RetryInterval: time.Second, Metrics: metrics},
		nil,
		slog.Default(),
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	target := &core.PublishingTarget{Name: "hanging", Type: "webhook", URL: server.URL, Enabled: true, Format: core.FormatWebhook}
	handle, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("publish never reached the target")
	}

	// The waiter gives up (what the coordinator does on
	// ErrDeliveryWaitTimeout) — an unanswered target, so this must count
	// against its circuit breaker (fix round 2, review finding R3).
	handle.Abandon(AbandonReasonUnconfirmed)

	select {
	case outcome := <-handle.Done():
		require.Error(t, outcome, "an abandoned publish must never report confirmed delivery")
		assert.ErrorIs(t, outcome, context.Canceled,
			"the job's context must be what ended the attempt")
	case <-time.After(5 * time.Second):
		t.Fatal("abandoning a job must unwind its in-flight publish, not wait out the retry budget")
	}

	// A target that was handed a notification and never answered IS unhealthy
	// (fix round 2, review finding R3): the abandonment must count against its
	// breaker. Round 1 skipped this, so a hanging target — which never produces
	// a completed, failed job — could never open its breaker at all.
	assert.Equal(t, 1, queue.getCircuitBreaker(target.Name).GetFailureCount(),
		"a waiter-timeout abandonment must be recorded as a circuit-breaker failure")
}

// TestGroupPublishHandle_RepeatedUnconfirmedAbandonmentsOpenTheBreaker is the
// consequence that matters operationally: a target that hangs on every fire
// must eventually be short-circuited instead of costing a worker and a full
// confirmation wait forever.
func TestGroupPublishHandle_RepeatedUnconfirmedAbandonmentsOpenTheBreaker(t *testing.T) {
	server, started := newHangingWebhook(t)

	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), metrics, ""),
		nil,
		NewLRUJobTrackingStore(16),
		PublishingQueueConfig{WorkerCount: 4, HighPriorityQueueSize: 16, MediumPriorityQueueSize: 16, LowPriorityQueueSize: 16, MaxRetries: 0, RetryInterval: time.Millisecond, Metrics: metrics},
		nil,
		slog.Default(),
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	target := &core.PublishingTarget{Name: "hanging", Type: "webhook", URL: server.URL, Enabled: true, Format: core.FormatWebhook}

	// FailureThreshold is 5 (getCircuitBreaker), i.e. five consecutive fires.
	for i := 0; i < 5; i++ {
		handle, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
		require.NoError(t, err)

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("publish %d never reached the target", i)
		}

		handle.Abandon(AbandonReasonUnconfirmed)

		select {
		case <-handle.Done():
		case <-time.After(5 * time.Second):
			t.Fatalf("abandoned job %d never reported an outcome", i)
		}
	}

	assert.False(t, queue.getCircuitBreaker(target.Name).CanAttempt(),
		"a target that never answers must eventually have its circuit breaker opened")
}

// TestGroupPublishHandle_ShutdownAbandonmentDoesNotBlameTheTarget: the other
// side of R3 — cancellation that says nothing about the endpoint must leave the
// breaker alone (and, as before, write no DLQ entry).
func TestGroupPublishHandle_ShutdownAbandonmentDoesNotBlameTheTarget(t *testing.T) {
	server, started := newHangingWebhook(t)

	dlq := &recordingDLQ{}
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), metrics, ""),
		dlq,
		NewLRUJobTrackingStore(8),
		PublishingQueueConfig{WorkerCount: 2, HighPriorityQueueSize: 8, MediumPriorityQueueSize: 8, LowPriorityQueueSize: 8, MaxRetries: 0, RetryInterval: time.Millisecond, Metrics: metrics},
		nil,
		slog.Default(),
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	target := &core.PublishingTarget{Name: "hanging", Type: "webhook", URL: server.URL, Enabled: true, Format: core.FormatWebhook}
	handle, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("publish never reached the target")
	}

	handle.Abandon(AbandonReasonShutdown)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("abandoned job never reported an outcome")
	}

	assert.Zero(t, queue.getCircuitBreaker(target.Name).GetFailureCount(),
		"shutdown-driven cancellation is not evidence about the target")
	assert.Zero(t, dlq.count(),
		"a shutdown-abandoned job must not be written to the DLQ (the notify chain retries after restart)")
}

// TestAbandonedJob_IsNotWrittenToDLQ: the notify chain re-publishes an
// unconfirmed target on the group's next fire, so a DLQ entry for the
// abandoned attempt would be a duplicate waiting to be replayed.
func TestAbandonedJob_IsNotWrittenToDLQ(t *testing.T) {
	server, started := newHangingWebhook(t)

	dlq := &recordingDLQ{}
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
	queue := NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), metrics, ""),
		dlq,
		NewLRUJobTrackingStore(8),
		PublishingQueueConfig{WorkerCount: 1, HighPriorityQueueSize: 8, MediumPriorityQueueSize: 8, LowPriorityQueueSize: 8, MaxRetries: 0, RetryInterval: time.Millisecond, Metrics: metrics},
		nil,
		slog.Default(),
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	target := &core.PublishingTarget{Name: "hanging", Type: "webhook", URL: server.URL, Enabled: true, Format: core.FormatWebhook}
	handle, err := queue.SubmitGroupWithConfirmation(testGroupAlerts(1), target, "gk", "recv", nil)
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("publish never reached the target")
	}
	handle.Abandon(AbandonReasonUnconfirmed)

	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("abandoned job never reported an outcome")
	}

	assert.Zero(t, dlq.count(), "an abandoned job must not be written to the DLQ")
}

// === Wave-4 hygiene item 2 (review finding M-c): a genuine failure racing
// the waiter's timeout must not lose its DLQ entry ===
//
// The real race is a single scheduling instant inside processJob (between
// retryPublish returning a REAL, already-decided outcome and the abandon-
// branch check reading job.ctx.Err()) that a concurrent handle.Abandon call
// can, in principle, land inside of. That window has no synchronization
// point to hook a test onto, so this pins the decision function processJob
// actually calls instead of trying to win a race that is inherently
// unrepeatable — see jobWasAbandoned's doc comment for why job.ctx.Err() !=
// nil alone is not enough.
func TestJobWasAbandoned_SettledFailureRacingTimeoutKeepsNormalPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate: the waiter gave up and Abandon()'d the job AFTER its attempt had already settled
	job := &PublishingJob{ctx: ctx}

	genuineFailure := errors.New("500 Internal Server Error")
	assert.False(t, jobWasAbandoned(job, genuineFailure),
		"a real, completed failure must not be reclassified as abandonment just because ctx is now cancelled — it must keep its DLQ entry")

	trueAbandonment := fmt.Errorf("request aborted: %w", context.Canceled)
	assert.True(t, jobWasAbandoned(job, trueAbandonment),
		"a job whose OWN attempt was actually aborted by cancellation must still take the abandon branch")

	assert.False(t, jobWasAbandoned(job, nil), "a nil error (success) is never abandonment")

	unstartedJob := &PublishingJob{}
	assert.False(t, jobWasAbandoned(unstartedJob, genuineFailure), "a job with no ctx (Submit/SubmitGroup) can never be abandoned")

	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	liveJob := &PublishingJob{ctx: liveCtx}
	assert.False(t, jobWasAbandoned(liveJob, genuineFailure), "ctx still live: a failure here is just a normal failure")
}

// recordingDLQ counts DLQ writes.
type recordingDLQ struct {
	mu     sync.Mutex
	writes int
}

func (d *recordingDLQ) Write(_ context.Context, _ *PublishingJob) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writes++
	return nil
}

func (d *recordingDLQ) Read(_ context.Context, _ DLQFilters) ([]*DLQEntry, error) { return nil, nil }

func (d *recordingDLQ) Replay(_ context.Context, _ uuid.UUID) error { return nil }

func (d *recordingDLQ) Purge(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

func (d *recordingDLQ) GetStats(_ context.Context) (*DLQStats, error) { return &DLQStats{}, nil }

var _ DLQRepository = (*recordingDLQ)(nil)

func (d *recordingDLQ) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writes
}
