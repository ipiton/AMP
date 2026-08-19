package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	businesspublishing "github.com/ipiton/AMP/internal/business/publishing"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
	infrapublishing "github.com/ipiton/AMP/internal/infrastructure/publishing"
	infraroute "github.com/ipiton/AMP/internal/infrastructure/routing"
	v2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// ============================================================================
// FU-RECEIVERS-INTEGRATION slice 2, item 3: config-only delivery, end to end.
// ============================================================================
//
// The whole epic's claim in one test: an UNTOUCHED upstream-shaped config file
// with `route:` + `receivers:` and ZERO Kubernetes Secrets delivers
// notifications. Every layer is the real one — internal/config.LoadConfig,
// BuildConfigTargets, the config-only discovery manager, DefaultGroupManager's
// group_wait timer and notify chain, ApplicationPublishingAdapter,
// PublishingCoordinator, the PublishingQueue worker pool, and the real
// publishers — with only the HTTP endpoints replaced by httptest servers and the
// notify log/storage in memory (no Redis in a unit test).
//
// It asserts the WIRE payloads, not just that a request arrived: the webhook must
// receive upstream's v4 group shape, and the Slack target must receive a
// Slack-shaped message. A green "a POST happened" test is exactly what let two
// payload bugs through in wave 5.

type recordedRequest struct {
	path string
	body map[string]any
}

type recordingEndpoint struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

func newRecordingEndpoint(t *testing.T) *recordingEndpoint {
	t.Helper()

	e := &recordingEndpoint{}
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		e.mu.Lock()
		e.requests = append(e.requests, recordedRequest{path: r.URL.Path, body: body})
		e.mu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`ok`))
	}))
	t.Cleanup(e.server.Close)
	return e
}

func (e *recordingEndpoint) all() []recordedRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]recordedRequest(nil), e.requests...)
}

func (e *recordingEndpoint) waitForRequest(t *testing.T, timeout time.Duration) recordedRequest {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := e.all(); len(got) > 0 {
			return got[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no request received within %s", timeout)
	return recordedRequest{}
}

// configOnlyStack is the assembled runtime under test.
type configOnlyStack struct {
	groupManager *grouping.DefaultGroupManager
	groupKey     grouping.GroupKey
	discovery    businesspublishing.TargetDiscoveryManager
}

// newConfigOnlyStack loads configYAML from disk exactly as production does and
// assembles the delivery stack around it — no K8s client anywhere.
//
// ENVIRONMENT=development is required because the httptest endpoints are plain
// HTTP and both validators enforce HTTPS otherwise (routing's own
// `https_production` tag; configvalidator downgrades a webhook URL to a W111
// warning but hard-errors E117 on a plaintext Slack api_url — which is why the
// Slack wire shape is asserted from a programmatic RouteConfig instead, see
// TestConfigOnlyDelivery_SlackWireShape).
func newConfigOnlyStack(t *testing.T, configYAML string, receiver string) *configOnlyStack {
	t.Helper()

	t.Setenv("ENVIRONMENT", "development")

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(configYAML), 0o600))

	// LoadConfig reads through viper's global instance; each call sets its own
	// config file, so sequential loads in one process are fine.
	cfg, err := appconfig.LoadConfig(path)
	require.NoError(t, err, "the config must load with no K8s and no edits")
	require.NotNil(t, cfg.Routing, "sanity: route:/receivers: must have been parsed")

	return newStackFromRouting(t, cfg.Routing, receiver, mustBuildGroupingConfig(t, cfg))
}

// newStackFromRouting is the same assembly starting from an already-built
// RouteConfig, for the cases a config FILE cannot express in a test (see the
// Slack note above).
func newStackFromRouting(t *testing.T, routing *infraroute.RouteConfig, receiver string, groupingConfig *grouping.GroupingConfig) *configOnlyStack {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelWarn}))
	metrics := v2.NewRegistry(v2.WithPrometheusRegisterer(prometheus.NewRegistry())).Publishing

	// Config-only discovery: targets come from `receivers:`, nothing else.
	discovery := businesspublishing.NewConfigOnlyTargetDiscoveryManager(logger, nil)
	discovery.SetConfigTargets(businesspublishing.BuildConfigTargets(routing, logger))
	require.NotEmpty(t, discovery.ListTargets(), "the config must have provisioned targets")

	adapterDiscovery, err := NewDiscoveryAdapter(discovery)
	require.NoError(t, err)

	factory := infrapublishing.NewPublisherFactory(infrapublishing.NewAlertFormatter(""), logger, metrics, "")
	t.Cleanup(factory.Shutdown)

	queue := infrapublishing.NewPublishingQueue(
		factory,
		nil,
		infrapublishing.NewLRUJobTrackingStore(32),
		infrapublishing.PublishingQueueConfig{
			WorkerCount:             4,
			HighPriorityQueueSize:   32,
			MediumPriorityQueueSize: 32,
			LowPriorityQueueSize:    32,
			MaxRetries:              0,
			RetryInterval:           time.Millisecond,
			Metrics:                 metrics,
		},
		nil,
		logger,
	)
	queue.Start()
	t.Cleanup(func() { _ = queue.Stop(5 * time.Second) })

	confirmationTimeout := 2 * time.Second
	coordinatorConfig := infrapublishing.DefaultCoordinatorConfig()
	coordinatorConfig.DeliveryConfirmationTimeout = confirmationTimeout
	coordinatorConfig.Metrics = metrics
	coordinator := infrapublishing.NewPublishingCoordinator(queue, adapterDiscovery, nil, coordinatorConfig, logger)
	coordinator.SetKnownReceivers([]string{receiver})

	publisher, err := NewApplicationPublishingAdapter(coordinator, logger)
	require.NoError(t, err)

	timerManager, err := grouping.NewDefaultTimerManager(grouping.TimerManagerConfig{
		Storage:         grouping.NewInMemoryTimerStorage(logger),
		Logger:          logger,
		CallbackTimeout: grouping.TimerCallbackTimeoutFor(confirmationTimeout),
	})
	require.NoError(t, err)

	groupManager, err := grouping.NewDefaultGroupManager(context.Background(), grouping.DefaultGroupManagerConfig{
		KeyGenerator: grouping.NewGroupKeyGenerator(),
		Config:       groupingConfig,
		Storage:      grouping.NewMemoryGroupStorage(&grouping.MemoryGroupStorageConfig{Logger: logger}),
		TimerManager: timerManager,
		Publisher:    publisher,
		// NotifyLog left nil on purpose: NewDefaultGroupManager fills in the
		// in-memory notifyDedupLog, which is the lite-profile production path.
		NotifyLogClaimTTL: grouping.NotifyLogClaimTTLFor(confirmationTimeout),
		Logger:            logger,
	})
	require.NoError(t, err)
	require.NoError(t, timerManager.SetGroupManager(groupManager))
	t.Cleanup(func() { _ = timerManager.Shutdown(context.Background()) })

	return &configOnlyStack{
		groupManager: groupManager,
		groupKey:     grouping.GroupKey(fmt.Sprintf("receiver=%s/alertname=HighCPU", receiver)),
		discovery:    discovery,
	}
}

func (s *configOnlyStack) ingest(t *testing.T, fingerprint string) {
	t.Helper()
	_, err := s.groupManager.AddAlertToGroup(context.Background(), &core.Alert{
		Fingerprint: fingerprint,
		AlertName:   "HighCPU",
		Status:      core.StatusFiring,
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical", "cluster": "prod"},
		Annotations: map[string]string{"summary": "cpu is high", "description": "node-1 at 98%"},
		StartsAt:    time.Now().UTC(),
	}, s.groupKey)
	require.NoError(t, err)
}

// TestConfigOnlyDelivery_WebhookV4Payload: a config FILE with one webhook
// integration and no Secrets. Ingest → group_wait → the endpoint receives
// upstream's v4 group payload.
func TestConfigOnlyDelivery_WebhookV4Payload(t *testing.T) {
	webhook := newRecordingEndpoint(t)

	configYAML := fmt.Sprintf(`
server:
  port: 8080

route:
  receiver: team-x
  group_by: [alertname]
  group_wait: 40ms
  group_interval: 200ms
  repeat_interval: 1h

receivers:
  - name: team-x
    webhook_configs:
      - url: %s/hook
`, webhook.server.URL)

	stack := newConfigOnlyStack(t, configYAML, "team-x")

	targetNames := make([]string, 0)
	for _, target := range stack.discovery.ListTargets() {
		targetNames = append(targetNames, target.Name)
	}
	assert.Equal(t, []string{"cfg:team-x/webhook0"}, targetNames)

	stack.ingest(t, "fp-1")

	// --- webhook: upstream's v4 group payload -------------------------------
	got := webhook.waitForRequest(t, 5*time.Second)
	assert.Equal(t, "/hook", got.path)
	assert.Equal(t, "4", fmt.Sprint(got.body["version"]), "upstream webhook payload is version 4")
	assert.Equal(t, "firing", got.body["status"])
	assert.Equal(t, "team-x", got.body["receiver"], "the routed receiver travels in the payload")

	groupLabels, ok := got.body["groupLabels"].(map[string]any)
	require.True(t, ok, "groupLabels must be present: %v", got.body)
	assert.Equal(t, "HighCPU", groupLabels["alertname"])

	alerts, ok := got.body["alerts"].([]any)
	require.True(t, ok, "alerts array must be present: %v", got.body)
	require.Len(t, alerts, 1, "one alert in the group")
	first, ok := alerts[0].(map[string]any)
	require.True(t, ok)
	alertLabels, ok := first["labels"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "HighCPU", alertLabels["alertname"])
	assert.Equal(t, "critical", alertLabels["severity"])

}

// TestConfigOnlyDelivery_SendResolvedFalseSuppresses: the same stack, with
// `send_resolved: false` on the webhook — a resolved-only group must reach
// nothing, while the firing notification still lands.
func TestConfigOnlyDelivery_SendResolvedFalseSuppresses(t *testing.T) {
	webhook := newRecordingEndpoint(t)

	configYAML := fmt.Sprintf(`
server:
  port: 8080

route:
  receiver: team-x
  group_by: [alertname]
  group_wait: 40ms
  group_interval: 120ms
  repeat_interval: 1h

receivers:
  - name: team-x
    webhook_configs:
      - url: %s/hook
        send_resolved: false
`, webhook.server.URL)

	stack := newConfigOnlyStack(t, configYAML, "team-x")

	target, err := stack.discovery.GetTarget("cfg:team-x/webhook0")
	require.NoError(t, err)
	assert.Equal(t, false, target.FilterConfig["send_resolved"],
		"send_resolved: false must reach the target's filter config")

	stack.ingest(t, "fp-1")
	got := webhook.waitForRequest(t, 5*time.Second)
	assert.Equal(t, "firing", got.body["status"], "firing notifications are unaffected")

	// Flip the alert to resolved: the group re-fires on group_interval, and the
	// resolved notification must be suppressed for this target.
	endsAt := time.Now().UTC()
	_, err = stack.groupManager.AddAlertToGroup(context.Background(), &core.Alert{
		Fingerprint: "fp-1",
		AlertName:   "HighCPU",
		Status:      core.StatusResolved,
		Labels:      map[string]string{"alertname": "HighCPU", "severity": "critical", "cluster": "prod"},
		Annotations: map[string]string{"summary": "cpu is high"},
		StartsAt:    time.Now().UTC().Add(-time.Minute),
		EndsAt:      &endsAt,
	}, stack.groupKey)
	require.NoError(t, err)

	// Give the group two group_intervals to (not) fire.
	time.Sleep(400 * time.Millisecond)

	for _, request := range webhook.all() {
		assert.NotEqual(t, "resolved", request.body["status"],
			"send_resolved: false must suppress the resolved notification: %v", request.body)
	}

	// …and the group must still SETTLE (review finding S2-I1). Suppression used
	// to return zero outcomes, which made publishGroupAlerts skip both
	// RecordSent and pruneResolvedAlerts — the only caller of
	// RemoveAlertFromGroup — so the group kept its resolved alert and re-armed
	// its repeat_interval timer forever, one silent no-op fire per interval.
	// A pruned group whose last alert was resolved is deleted outright.
	deadline := time.Now().Add(3 * time.Second)
	settled := false
	for time.Now().Before(deadline) {
		if _, err := stack.groupManager.GetGroup(context.Background(), stack.groupKey); err != nil {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, settled,
		"the fully-resolved group must be pruned and torn down, not left re-firing forever")
}

// mustBuildGroupingConfig derives the grouping config (group_wait/
// group_interval/repeat_interval) from the loaded AMP config, the same call
// ServiceRegistry makes.
func mustBuildGroupingConfig(t *testing.T, cfg *appconfig.Config) *grouping.GroupingConfig {
	t.Helper()
	groupingConfig, err := cfg.BuildGroupingConfig()
	require.NoError(t, err)
	return groupingConfig
}

// TestConfigOnlyDelivery_SlackWireShape asserts the Slack half of slice 2 item 3.
//
// It starts from a programmatic RouteConfig rather than a config FILE because a
// plaintext Slack api_url is a hard load error (configvalidator E117, correctly —
// a Slack webhook URL is a credential and must never travel over HTTP) and no
// test in this repo can hold a real HTTPS Slack endpoint. Everything after the
// config layer is identical to the file-driven case: BuildConfigTargets → the
// config-only discovery view → coordinator → queue → EnhancedSlackPublisher →
// the wire.
func TestConfigOnlyDelivery_SlackWireShape(t *testing.T) {
	slack := newRecordingEndpoint(t)

	routing := &infraroute.RouteConfig{
		Receivers: []*infraroute.Receiver{{
			Name: "team-x",
			SlackConfigs: []*infraroute.SlackConfig{{
				APIURL:  slack.server.URL + "/services/T/B/C",
				Channel: "#ops",
			}},
		}},
	}

	groupingConfig := &grouping.GroupingConfig{
		Route: &grouping.Route{
			Receiver:       "team-x",
			GroupBy:        []string{"alertname"},
			GroupWait:      &grouping.Duration{Duration: 40 * time.Millisecond},
			GroupInterval:  &grouping.Duration{Duration: 200 * time.Millisecond},
			RepeatInterval: &grouping.Duration{Duration: time.Hour},
		},
	}

	stack := newStackFromRouting(t, routing, "team-x", groupingConfig)

	targets := stack.discovery.ListTargets()
	require.Len(t, targets, 1)
	assert.Equal(t, "cfg:team-x/slack0", targets[0].Name)

	stack.ingest(t, "fp-1")

	got := slack.waitForRequest(t, 5*time.Second)
	assert.Equal(t, "/services/T/B/C", got.path,
		"the slack target must post to the api_url from the config, not to a base")
	text, _ := got.body["text"].(string)
	assert.NotEmpty(t, text, "Slack requires a non-empty text fallback: %v", got.body)
	assert.Contains(t, text, "HighCPU")
	_, hasAttachments := got.body["attachments"]
	_, hasBlocks := got.body["blocks"]
	assert.True(t, hasAttachments || hasBlocks, "Slack payload must carry blocks or attachments: %v", got.body)
}
