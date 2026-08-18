package publishing

import (
	"context"
	"encoding/json"
	"io"
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

// newTestPublishingQueue builds a PublishingQueue with worker pool disabled
// (WorkerCount: 0) and its own isolated metrics registry, so tests can call
// publishJob directly without racing the global Prometheus default registry
// across parallel test binaries.
func newTestPublishingQueue() *PublishingQueue {
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing
	return NewPublishingQueue(
		NewPublisherFactory(NewAlertFormatter(""), slog.Default(), metrics, ""),
		nil,
		NewLRUJobTrackingStore(16),
		PublishingQueueConfig{WorkerCount: 0, HighPriorityQueueSize: 4, MediumPriorityQueueSize: 4, LowPriorityQueueSize: 4, Metrics: metrics},
		nil,
		slog.Default(),
	)
}

// === Task fwb: wire-level group batching ===

// testGroupBatchAlerts builds n alerts sharing a common label (so
// commonLabels/commonAnnotations stay non-empty) but each with its own
// fingerprint/annotation (so the intersection narrows exactly as expected).
func testGroupBatchAlerts(n int) []*core.Alert {
	alerts := make([]*core.Alert, 0, n)
	for i := 0; i < n; i++ {
		alerts = append(alerts, &core.Alert{
			Fingerprint: "fp-" + string(rune('1'+i)),
			AlertName:   "HighCPU",
			Status:      core.StatusFiring,
			Labels: map[string]string{
				"alertname": "HighCPU",
				"instance":  "host-" + string(rune('1'+i)),
			},
			Annotations: map[string]string{
				"summary": "shared summary",
				"detail":  "detail-" + string(rune('1'+i)),
			},
			StartsAt: time.Now().UTC(),
		})
	}
	return alerts
}

// TestWebhookPublisher_PublishBatch_SendsOnePOSTWithAlertsArray is the
// headline test for task fwb's deliverable 1: a webhook target must receive
// exactly ONE HTTP POST for the whole group, carrying every alert in an
// "alerts" array — the upstream Alertmanager v4 webhook shape — instead of
// one POST per alert.
func TestWebhookPublisher_PublishBatch_SendsOnePOSTWithAlertsArray(t *testing.T) {
	var requestCount atomic.Int64
	var capturedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	formatter := NewAlertFormatter("https://amp.example.com")
	publisher := NewWebhookPublisher(formatter, slog.Default())

	target := &core.PublishingTarget{
		Name:    "ops-webhook",
		Type:    string(TargetTypeWebhook),
		URL:     server.URL,
		Enabled: true,
		Format:  core.FormatWebhook,
	}

	alerts := testGroupBatchAlerts(3)
	batchPublisher, ok := publisher.(BatchAlertPublisher)
	require.True(t, ok, "WebhookPublisher must implement BatchAlertPublisher")

	groupLabels := map[string]string{"alertname": "HighCPU", "cluster": "prod"}
	err := batchPublisher.PublishBatch(context.Background(), alerts, "receiver=ops/alertname=HighCPU", "ops", groupLabels, target)
	require.NoError(t, err)

	assert.Equal(t, int64(1), requestCount.Load(), "exactly ONE POST must be sent for the whole group, not one per alert")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &payload))

	// Upstream Alertmanager v4 webhook shape.
	assert.Equal(t, "4", payload["version"])
	assert.Equal(t, "receiver=ops/alertname=HighCPU", payload["groupKey"])
	assert.Equal(t, "firing", payload["status"])
	assert.Equal(t, "ops", payload["receiver"])
	assert.Equal(t, "https://amp.example.com", payload["externalURL"])
	assert.Equal(t, map[string]any{"alertname": "HighCPU", "cluster": "prod"}, payload["groupLabels"],
		"groupLabels must carry the group_by=[alertname,cluster] values the caller resolved (review finding 1)")

	rawAlerts, ok := payload["alerts"].([]any)
	require.True(t, ok, "payload must carry an \"alerts\" array")
	assert.Len(t, rawAlerts, 3, "the array must carry all 3 alerts from the group")

	commonLabels, ok := payload["commonLabels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "HighCPU", commonLabels["alertname"], "alertname is common to every alert in the group")
	_, hasInstance := commonLabels["instance"]
	assert.False(t, hasInstance, "instance differs per alert and must NOT be in commonLabels")

	commonAnnotations, ok := payload["commonAnnotations"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "shared summary", commonAnnotations["summary"])
}

// TestWebhookPublisher_PublishBatch_NilGroupLabelsBecomesEmptyMap covers the
// other half of review finding 1's requested coverage: a group whose route
// has no group_by (or the caller passes nil) must still emit
// "groupLabels": {} — never a JSON null — so downstream webhook receivers
// don't have to special-case a missing field.
func TestWebhookPublisher_PublishBatch_NilGroupLabelsBecomesEmptyMap(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	publisher := NewWebhookPublisher(NewAlertFormatter(""), slog.Default())
	target := &core.PublishingTarget{Name: "ops-webhook", Type: string(TargetTypeWebhook), URL: server.URL, Enabled: true, Format: core.FormatWebhook}

	batchPublisher := publisher.(BatchAlertPublisher)
	require.NoError(t, batchPublisher.PublishBatch(context.Background(), testGroupBatchAlerts(1), "gk", "ops", nil, target))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &payload))
	assert.Equal(t, map[string]any{}, payload["groupLabels"], "nil groupLabels must serialize as an empty object, not null")
}

// TestWebhookPublisher_PublishBatch_ResolvedGroupReportsResolvedStatus
// pins the group status derivation: "resolved" only once EVERY alert has
// resolved, matching upstream aggrGroup status semantics.
func TestWebhookPublisher_PublishBatch_ResolvedGroupReportsResolvedStatus(t *testing.T) {
	var capturedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	publisher := NewWebhookPublisher(NewAlertFormatter(""), slog.Default())
	target := &core.PublishingTarget{Name: "ops-webhook", Type: string(TargetTypeWebhook), URL: server.URL, Enabled: true, Format: core.FormatWebhook}

	alerts := []*core.Alert{
		{Fingerprint: "fp-1", AlertName: "HighCPU", Status: core.StatusResolved, Labels: map[string]string{"alertname": "HighCPU"}, StartsAt: time.Now().UTC()},
		{Fingerprint: "fp-2", AlertName: "HighCPU", Status: core.StatusResolved, Labels: map[string]string{"alertname": "HighCPU"}, StartsAt: time.Now().UTC()},
	}

	batchPublisher := publisher.(BatchAlertPublisher)
	require.NoError(t, batchPublisher.PublishBatch(context.Background(), alerts, "gk", "ops", nil, target))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(capturedBody, &payload))
	assert.Equal(t, "resolved", payload["status"])
}

// TestWebhookPublisher_PublishBatch_HTTPErrorPropagates proves a failed
// batch POST surfaces as a single error for the whole job — there is no
// per-alert partial success at the wire level for one HTTP request.
func TestWebhookPublisher_PublishBatch_HTTPErrorPropagates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	publisher := NewWebhookPublisher(NewAlertFormatter(""), slog.Default())
	target := &core.PublishingTarget{Name: "ops-webhook", Type: string(TargetTypeWebhook), URL: server.URL, Enabled: true, Format: core.FormatWebhook}

	batchPublisher := publisher.(BatchAlertPublisher)
	err := batchPublisher.PublishBatch(context.Background(), testGroupBatchAlerts(2), "gk", "ops", nil, target)
	assert.Error(t, err)
}

// === Task fwb: per-message publishers iterate within one job ===

// countingAlertPublisher is a minimal AlertPublisher that does NOT implement
// BatchAlertPublisher — standing in for Slack/Telegram/PagerDuty/Email,
// which have no array-payload wire shape to batch into. It records every
// Publish call's alert fingerprint.
type countingAlertPublisher struct {
	fingerprints []string
	failOn       string // fingerprint that returns an error; "" means none fail
}

func (p *countingAlertPublisher) Publish(_ context.Context, enrichedAlert *core.EnrichedAlert, _ *core.PublishingTarget) error {
	p.fingerprints = append(p.fingerprints, enrichedAlert.Alert.Fingerprint)
	if p.failOn != "" && enrichedAlert.Alert.Fingerprint == p.failOn {
		return assertGroupBatchErr("simulated per-alert failure")
	}
	return nil
}

func (p *countingAlertPublisher) Name() string { return "counting" }

type assertGroupBatchErr string

func (e assertGroupBatchErr) Error() string { return string(e) }

var _ AlertPublisher = (*countingAlertPublisher)(nil)

// TestPublishingQueue_PublishJob_NonBatchPublisherIteratesAlertsWithinOneJob
// is the deliverable's other half: a group job for a publisher that does
// NOT implement BatchAlertPublisher must still be ONE job (one retry unit,
// one circuit-breaker/rate-limit scope) that internally calls Publish once
// per alert, rather than the coordinator fragmenting back into one job per
// alert.
func TestPublishingQueue_PublishJob_NonBatchPublisherIteratesAlertsWithinOneJob(t *testing.T) {
	queue := newTestPublishingQueue()

	publisher := &countingAlertPublisher{}
	alerts := testGroupBatchAlerts(3)
	job := &PublishingJob{
		EnrichedAlert: &core.EnrichedAlert{Alert: alerts[0]},
		Target:        &core.PublishingTarget{Name: "telegram-1", Type: "telegram"},
		Alerts:        alerts,
		GroupKey:      "gk",
		Receiver:      "ops",
	}

	err := queue.publishJob(publisher, job)
	require.NoError(t, err)

	require.Len(t, publisher.fingerprints, 3, "one Publish call per alert, all within the same job")
	assert.ElementsMatch(t, []string{"fp-1", "fp-2", "fp-3"}, publisher.fingerprints)
}

// TestPublishingQueue_PublishJob_NonBatchPublisher_BestEffortAttemptsAll
// proves one alert failing mid-iteration does not stop the rest from being
// attempted (best-effort), while the job still reports failure overall so
// the retry strategy above it treats the job as needing another attempt.
func TestPublishingQueue_PublishJob_NonBatchPublisher_BestEffortAttemptsAll(t *testing.T) {
	queue := newTestPublishingQueue()

	publisher := &countingAlertPublisher{failOn: "fp-2"}
	alerts := testGroupBatchAlerts(3)
	job := &PublishingJob{
		EnrichedAlert: &core.EnrichedAlert{Alert: alerts[0]},
		Target:        &core.PublishingTarget{Name: "telegram-1", Type: "telegram"},
		Alerts:        alerts,
		GroupKey:      "gk",
		Receiver:      "ops",
	}

	err := queue.publishJob(publisher, job)
	assert.Error(t, err, "the job as a whole must report failure when any alert failed")
	assert.Len(t, publisher.fingerprints, 3, "every alert must still be attempted despite the middle one failing")
}

// TestPublishingQueue_PublishJob_BatchPublisherCalledOnceNotIterated proves
// the OTHER branch: when the publisher DOES implement BatchAlertPublisher,
// publishJob must call PublishBatch exactly once and never fall back to
// per-alert iteration.
func TestPublishingQueue_PublishJob_BatchPublisherCalledOnceNotIterated(t *testing.T) {
	queue := newTestPublishingQueue()

	publisher := &countingBatchPublisher{}
	alerts := testGroupBatchAlerts(3)
	groupLabels := map[string]string{"alertname": "HighCPU"}
	job := &PublishingJob{
		EnrichedAlert: &core.EnrichedAlert{Alert: alerts[0]},
		Target:        &core.PublishingTarget{Name: "ops-webhook", Type: string(TargetTypeWebhook)},
		Alerts:        alerts,
		GroupKey:      "gk",
		Receiver:      "ops",
		GroupLabels:   groupLabels,
	}

	require.NoError(t, queue.publishJob(publisher, job))
	assert.Equal(t, 1, publisher.batchCalls, "PublishBatch must be called exactly once for a batch-capable publisher")
	assert.Equal(t, 0, publisher.publishCalls, "Publish must never be called when PublishBatch is available")
	assert.Len(t, publisher.lastAlerts, 3)
	assert.Equal(t, groupLabels, publisher.lastGroupLabels, "job.GroupLabels must be forwarded to PublishBatch")
}

// countingBatchPublisher implements both AlertPublisher and
// BatchAlertPublisher to prove publishJob prefers the batch path.
type countingBatchPublisher struct {
	publishCalls    int
	batchCalls      int
	lastAlerts      []*core.Alert
	lastGroupLabels map[string]string
}

func (p *countingBatchPublisher) Publish(_ context.Context, _ *core.EnrichedAlert, _ *core.PublishingTarget) error {
	p.publishCalls++
	return nil
}

func (p *countingBatchPublisher) Name() string { return "counting-batch" }

func (p *countingBatchPublisher) PublishBatch(_ context.Context, alerts []*core.Alert, _ string, _ string, groupLabels map[string]string, _ *core.PublishingTarget) error {
	p.batchCalls++
	p.lastAlerts = alerts
	p.lastGroupLabels = groupLabels
	return nil
}

var (
	_ AlertPublisher      = (*countingBatchPublisher)(nil)
	_ BatchAlertPublisher = (*countingBatchPublisher)(nil)
)
