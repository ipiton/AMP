package publishing

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/cache"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// PUBLISH-QUICK-FIX-BUNDLE: resolve without local state, per-target state,
// half-open breaker, graceful queue stop, secrets kept out of errors and logs.

const quickFixSecret = "sup3rSecretToken"

func quickFixLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func quickFixAlert(fingerprint string, status core.AlertStatus) *core.EnrichedAlert {
	return &core.EnrichedAlert{
		Alert: &core.Alert{
			Fingerprint: fingerprint,
			AlertName:   "HighCPUUsage",
			Status:      status,
			StartsAt:    time.Now(),
			Labels:      map[string]string{"severity": "warning"},
			Annotations: map[string]string{"summary": "quick fix"},
		},
	}
}

// --- PagerDuty ---

type recordingPagerDutyClient struct {
	PagerDutyEventsClient
	resolves []*ResolveEventRequest
}

func (c *recordingPagerDutyClient) ResolveEvent(_ context.Context, req *ResolveEventRequest) (*EventResponse, error) {
	c.resolves = append(c.resolves, req)
	return &EventResponse{Status: "success", DedupKey: req.DedupKey}, nil
}

// A resolve reaching a publisher that never saw the trigger (restart, another
// replica) must still close the incident: the dedup key is the fingerprint.
func TestPagerDutyResolve_WithoutTrigger_UsesFingerprint(t *testing.T) {
	client := &recordingPagerDutyClient{}
	publisher := NewEnhancedPagerDutyPublisher(client, nil, NewAlertFormatter(""), quickFixLogger())
	target := &core.PublishingTarget{Name: "pd", Type: "pagerduty", Headers: map[string]string{"routing_key": "rk-1"}}

	err := publisher.Publish(context.Background(), quickFixAlert("fp-pd-1", core.StatusResolved), target)
	require.NoError(t, err)

	require.Len(t, client.resolves, 1)
	assert.Equal(t, EventActionResolve, client.resolves[0].EventAction)
	assert.Equal(t, "fp-pd-1", client.resolves[0].DedupKey)
	assert.Equal(t, "rk-1", client.resolves[0].RoutingKey)
}

// --- Rootly ---

type rootlyPayloadFormatter struct{}

func (rootlyPayloadFormatter) FormatAlert(_ context.Context, _ *core.EnrichedAlert, _ core.PublishingFormat) (map[string]any, error) {
	return map[string]any{"title": "t", "description": "d", "severity": "major"}, nil
}

type fakeRootlyClient struct {
	mu       sync.Mutex
	created  int
	updated  []string
	resolved []string
}

func (c *fakeRootlyClient) CreateIncident(_ context.Context, _ *CreateIncidentRequest) (*IncidentResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.created++
	resp := &IncidentResponse{}
	resp.Data.ID = "inc-" + string(rune('0'+c.created))
	return resp, nil
}

func (c *fakeRootlyClient) UpdateIncident(_ context.Context, id string, _ *UpdateIncidentRequest) (*IncidentResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updated = append(c.updated, id)
	return &IncidentResponse{}, nil
}

func (c *fakeRootlyClient) ResolveIncident(_ context.Context, id string, _ *ResolveIncidentRequest) (*IncidentResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolved = append(c.resolved, id)
	return &IncidentResponse{}, nil
}

// failingStore is a cache.Cache whose reads fail (Redis down).
type failingStore struct {
	cache.Cache
}

func (failingStore) Get(context.Context, string, interface{}) error {
	return errors.New("dial tcp: connection refused")
}

func newRootlyForTest(client RootlyIncidentsClient, ids IncidentIDCache) AlertPublisher {
	return NewEnhancedRootlyPublisher(client, ids, nil, rootlyPayloadFormatter{}, quickFixLogger())
}

// The incident ID lives in the shared store, so a fresh publisher (restart,
// another replica) resolves the incident the first one opened.
func TestRootlyIncidentStore_ResolveFromNewPublisherInstance(t *testing.T) {
	store := cache.NewMemoryCache(quickFixLogger())
	client := &fakeRootlyClient{}
	target := &core.PublishingTarget{Name: "rootly-a", Type: "rootly"}

	first := newRootlyForTest(client, newCacheIncidentIDCache(store, quickFixLogger()))
	require.NoError(t, first.Publish(context.Background(), quickFixAlert("fp-r-1", core.StatusFiring), target))

	second := newRootlyForTest(client, newCacheIncidentIDCache(store, quickFixLogger()))
	require.NoError(t, second.Publish(context.Background(), quickFixAlert("fp-r-1", core.StatusResolved), target))

	assert.Equal(t, 1, client.created)
	assert.Equal(t, []string{"inc-1"}, client.resolved)
}

// Two Rootly targets with one fingerprint keep separate incidents.
func TestRootlyPerTarget_IncidentsNotShared(t *testing.T) {
	store := cache.NewMemoryCache(quickFixLogger())
	client := &fakeRootlyClient{}
	publisher := newRootlyForTest(client, newCacheIncidentIDCache(store, quickFixLogger()))
	a := &core.PublishingTarget{Name: "rootly-a", Type: "rootly"}
	b := &core.PublishingTarget{Name: "rootly-b", Type: "rootly"}

	require.NoError(t, publisher.Publish(context.Background(), quickFixAlert("fp-shared", core.StatusFiring), a))
	require.NoError(t, publisher.Publish(context.Background(), quickFixAlert("fp-shared", core.StatusFiring), b))

	assert.Equal(t, 2, client.created, "the second target must open its own incident, not update the first one's")
	assert.Empty(t, client.updated)
}

// A miss on resolve is terminal: no error (no retry, no breaker), no API call.
func TestRootlyResolve_NotTracked_IsTerminal(t *testing.T) {
	client := &fakeRootlyClient{}
	publisher := newRootlyForTest(client, newCacheIncidentIDCache(cache.NewMemoryCache(quickFixLogger()), quickFixLogger()))

	err := publisher.Publish(context.Background(), quickFixAlert("fp-unknown", core.StatusResolved), &core.PublishingTarget{Name: "rootly-a"})
	require.NoError(t, err)
	assert.Empty(t, client.resolved)
}

// A store that cannot answer fails the delivery instead of opening a second
// incident (which would orphan the first) or dropping the resolve.
func TestRootlyStoreError_FailsDeliveryWithoutNewIncident(t *testing.T) {
	client := &fakeRootlyClient{}
	publisher := newRootlyForTest(client, newCacheIncidentIDCache(failingStore{}, quickFixLogger()))
	target := &core.PublishingTarget{Name: "rootly-a"}

	require.Error(t, publisher.Publish(context.Background(), quickFixAlert("fp-1", core.StatusFiring), target))
	require.Error(t, publisher.Publish(context.Background(), quickFixAlert("fp-1", core.StatusResolved), target))
	assert.Zero(t, client.created)
	assert.Empty(t, client.resolved)
}

// --- Slack / SMTP ---

func TestSlackPerTarget_CorrelationKeysDiffer(t *testing.T) {
	a := &core.PublishingTarget{Name: "slack-a"}
	b := &core.PublishingTarget{Name: "slack-b"}
	assert.NotEqual(t, correlationKey(a, "fp"), correlationKey(b, "fp"))
}

func TestSMTPClientKey_CoversEveryFieldWithoutPassword(t *testing.T) {
	base := SMTPConfig{Host: "smtp.example.com", Port: 587, Username: "u", Password: quickFixSecret, RequireTLS: true, From: "amp@example.com"}
	key := smtpClientKey(base)
	assert.NotContains(t, key, quickFixSecret)

	variants := map[string]func(c *SMTPConfig){
		"password":    func(c *SMTPConfig) { c.Password = "other-password" },
		"username":    func(c *SMTPConfig) { c.Username = "other" },
		"require_tls": func(c *SMTPConfig) { c.RequireTLS = false },
		"from":        func(c *SMTPConfig) { c.From = "other@example.com" },
		"port":        func(c *SMTPConfig) { c.Port = 465 },
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			assert.NotEqual(t, key, smtpClientKey(cfg))
		})
	}
}

// --- Circuit breaker ---

func openBreaker(t *testing.T, timeout time.Duration) *CircuitBreaker {
	t.Helper()
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, SuccessThreshold: 1, Timeout: timeout})
	cb.RecordFailure()
	require.Equal(t, StateOpen, cb.State())
	return cb
}

func TestCircuitBreaker_HalfOpenAdmitsExactlyOneProbe(t *testing.T) {
	cb := openBreaker(t, 20*time.Millisecond)
	time.Sleep(30 * time.Millisecond)

	const callers = 50
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if cb.CanAttempt() {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), admitted.Load())
	assert.Equal(t, StateHalfOpen, cb.State())
}

func TestCircuitBreaker_ProbeOutcomeTransitions(t *testing.T) {
	t.Run("failed probe reopens", func(t *testing.T) {
		cb := openBreaker(t, 10*time.Millisecond)
		time.Sleep(15 * time.Millisecond)
		require.True(t, cb.CanAttempt())
		cb.RecordFailure()
		assert.Equal(t, StateOpen, cb.State())
		assert.False(t, cb.CanAttempt(), "a fresh Open must wait out Timeout again")
	})
	t.Run("successful probe closes", func(t *testing.T) {
		cb := openBreaker(t, 10*time.Millisecond)
		time.Sleep(15 * time.Millisecond)
		require.True(t, cb.CanAttempt())
		cb.RecordSuccess()
		assert.Equal(t, StateClosed, cb.State())
		assert.True(t, cb.CanAttempt())
	})
	t.Run("success while open does not close", func(t *testing.T) {
		cb := openBreaker(t, time.Hour)
		cb.RecordSuccess()
		assert.Equal(t, StateOpen, cb.State())
	})
}

// A probe that never reports back (abandoned job) frees its slot after Timeout.
func TestCircuitBreaker_StaleProbeIsReplaced(t *testing.T) {
	cb := openBreaker(t, 20*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	require.True(t, cb.CanAttempt())
	assert.False(t, cb.CanAttempt(), "slot is taken while the probe is in flight")

	time.Sleep(30 * time.Millisecond)
	assert.True(t, cb.CanAttempt(), "a probe older than Timeout must not hold the slot forever")
}

// --- Queue ---

type panickingFormatter struct{}

func (panickingFormatter) FormatAlert(context.Context, *core.EnrichedAlert, core.PublishingFormat) (map[string]any, error) {
	panic("formatter blew up")
}

func newQuickFixQueue(t *testing.T, formatter AlertFormatter) *PublishingQueue {
	t.Helper()
	f := NewPublisherFactory(formatter, quickFixLogger(), nil, "https://amp.example.com")
	t.Cleanup(f.Shutdown)
	// One worker, so a job submitted before Start waits in its channel; own
	// metrics registry for the same reason as newQueueForFactory.
	return NewPublishingQueue(f, nil, nil, PublishingQueueConfig{
		WorkerCount:             1,
		HighPriorityQueueSize:   4,
		MediumPriorityQueueSize: 4,
		LowPriorityQueueSize:    4,
		MaxRetries:              0,
		RetryInterval:           time.Millisecond,
		CircuitTimeout:          time.Minute,
		Metrics:                 v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing,
	}, nil, quickFixLogger())
}

func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// Stop drains every priority, not just high, before the workers exit.
func TestQueueStop_DrainsAllPriorities(t *testing.T) {
	srv, calls := countingServer(t)
	q := newQuickFixQueue(t, NewAlertFormatter(""))
	target := &core.PublishingTarget{Name: "hook", Type: "webhook", URL: srv.URL, Format: core.FormatWebhook}

	high := quickFixAlert("fp-high", core.StatusFiring)
	high.Alert.Labels["severity"] = "critical"
	medium := quickFixAlert("fp-medium", core.StatusFiring)
	low := quickFixAlert("fp-low", core.StatusResolved)
	for _, a := range []*core.EnrichedAlert{high, medium, low} {
		require.NoError(t, q.Submit(a, target))
	}
	require.Equal(t, 1, q.GetQueueSizeByPriority(PriorityHigh))
	require.Equal(t, 1, q.GetQueueSizeByPriority(PriorityMedium))
	require.Equal(t, 1, q.GetQueueSizeByPriority(PriorityLow))

	q.Start()
	require.NoError(t, q.Stop(10*time.Second))
	assert.Equal(t, int32(3), calls.Load())
}

func TestQueueStop_SubmitAfterStopAndDoubleStop(t *testing.T) {
	q := newQuickFixQueue(t, NewAlertFormatter(""))
	q.Start()
	require.NoError(t, q.Stop(5*time.Second))

	assert.NotPanics(t, func() {
		err := q.Submit(quickFixAlert("fp-late", core.StatusFiring), &core.PublishingTarget{Name: "hook", Type: "webhook", URL: "https://hooks.example.com"})
		assert.Error(t, err)
	})
	assert.NotPanics(t, func() {
		assert.NoError(t, q.Stop(time.Second))
	})
}

// A panicking publisher fails its job; the worker survives and keeps going.
func TestQueue_PublisherPanicFailsJob(t *testing.T) {
	srv, _ := countingServer(t)
	q := newQuickFixQueue(t, panickingFormatter{})
	target := &core.PublishingTarget{Name: "hook", Type: "webhook", URL: srv.URL}

	q.Start()
	require.NoError(t, q.Submit(quickFixAlert("fp-1", core.StatusFiring), target))
	require.NoError(t, q.Submit(quickFixAlert("fp-2", core.StatusFiring), target))
	require.NoError(t, q.Stop(5*time.Second))

	assert.Equal(t, int64(2), q.GetStats().TotalFailed)
}

// --- Secrets in errors and logs ---

type staticFormatter struct{}

func (staticFormatter) FormatAlert(context.Context, *core.EnrichedAlert, core.PublishingFormat) (map[string]any, error) {
	return map[string]any{"text": "x"}, nil
}

func closedServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

func TestHTTPPublisher_SecretRedactedFromTransportError(t *testing.T) {
	publisher := NewWebhookPublisher(staticFormatter{}, quickFixLogger())
	target := &core.PublishingTarget{Name: "teams", URL: closedServerURL(t) + "/webhookb2/" + quickFixSecret}

	err := publisher.Publish(context.Background(), quickFixAlert("fp", core.StatusFiring), target)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), quickFixSecret)
}

func TestHTTPPublisher_SecretErrorBodyCapped(t *testing.T) {
	body := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	err := NewWebhookPublisher(staticFormatter{}, quickFixLogger()).Publish(context.Background(), quickFixAlert("fp", core.StatusFiring), &core.PublishingTarget{Name: "hook", URL: srv.URL})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), strings.Repeat("x", maxErrorBodyBytes+1))
}

func TestWebhookClient_SecretRedactedAndBodyCapped(t *testing.T) {
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	client := NewWebhookHTTPClient(WebhookRetryConfig{MaxRetries: 0, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond, Multiplier: 1}, logger)

	_, err := client.Post(context.Background(), closedServerURL(t)+"/api/webhooks/"+quickFixSecret, map[string]interface{}{"a": 1}, nil, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), quickFixSecret)
	assert.NotContains(t, logBuf.String(), quickFixSecret)

	body := strings.Repeat("y", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	_, err = client.Post(context.Background(), srv.URL, map[string]interface{}{"a": 1}, nil, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), strings.Repeat("y", maxErrorBodyBytes+1))
}

func TestPublisherFactory_SecretNotLoggedForWebhookTarget(t *testing.T) {
	var logBuf strings.Builder
	f := NewPublisherFactory(NewAlertFormatter(""), slog.New(slog.NewTextHandler(&logBuf, nil)), nil, "https://amp.example.com")
	t.Cleanup(f.Shutdown)

	_, err := f.CreatePublisherForTarget(&core.PublishingTarget{Name: "teams", Type: "webhook", URL: "https://outlook.office.com/webhookb2/" + quickFixSecret})
	require.NoError(t, err)
	require.Contains(t, logBuf.String(), "Creating enhanced webhook publisher")
	assert.NotContains(t, logBuf.String(), quickFixSecret)
}
