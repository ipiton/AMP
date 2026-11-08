package upstreamtargets

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/core"
)

// fakeDiscovery serves a fixed target list; err fails DiscoverTargets.
type fakeDiscovery struct {
	mu      sync.Mutex
	targets []*core.PublishingTarget
	invalid int
	err     error
}

func (f *fakeDiscovery) set(targets ...*core.PublishingTarget) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets = targets
}

func (f *fakeDiscovery) DiscoverTargets(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeDiscovery) ListTargets() []*core.PublishingTarget {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.targets
}

func (f *fakeDiscovery) GetStats() publishing.DiscoveryStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return publishing.DiscoveryStats{InvalidTargets: f.invalid}
}

func (f *fakeDiscovery) GetTarget(string) (*core.PublishingTarget, error) { return nil, nil }
func (f *fakeDiscovery) GetTargetsByType(string) []*core.PublishingTarget { return nil }
func (f *fakeDiscovery) Health(context.Context) error                     { return nil }
func (f *fakeDiscovery) SetConfigTargets([]*core.PublishingTarget)        {}

const fileConfig = `route:
  receiver: team-a
receivers:
  - name: team-a
    webhook_configs:
      - url: https://file.example/a
  - name: team-b
`

func loadConfig(t *testing.T) *config.Config {
	t.Helper()
	conf, err := config.Load(fileConfig)
	require.NoError(t, err)
	return conf
}

type testSource struct {
	*Source
	discovery *fakeDiscovery
	reg       *prometheus.Registry
	logs      *bytes.Buffer
}

func newSource(t *testing.T, targets ...*core.PublishingTarget) *testSource {
	t.Helper()
	d := &fakeDiscovery{}
	d.set(targets...)
	logs := &bytes.Buffer{}
	reg := prometheus.NewRegistry()
	s, err := New(d, slog.New(slog.NewTextHandler(&syncWriter{w: logs}, nil)), reg)
	require.NoError(t, err)
	_, err = s.Refresh(context.Background())
	require.NoError(t, err)
	return &testSource{Source: s, discovery: d, reg: reg, logs: logs}
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func (ts *testSource) gauge(state string) float64 {
	return testutil.ToFloat64(ts.targets.WithLabelValues(state))
}

func hook(name, url string, receivers ...string) *core.PublishingTarget {
	return &core.PublishingTarget{Name: name, Type: "webhook", URL: url, Enabled: true, Receivers: receivers}
}

func webhookURLs(rc config.Receiver) []string {
	var out []string
	for _, wc := range rc.WebhookConfigs {
		out = append(out, string(wc.URL))
	}
	return out
}

func TestNewRequiresDiscoveryAndLogger(t *testing.T) {
	_, err := New(nil, slog.Default(), prometheus.NewRegistry())
	assert.Error(t, err)
	_, err = New(&fakeDiscovery{}, nil, prometheus.NewRegistry())
	assert.Error(t, err)
}

func TestExtendConfigMembership(t *testing.T) {
	ts := newSource(t,
		hook("b-all", "https://k8s.example/all"),
		hook("a-team-b", "https://k8s.example/b", "team-b"),
		hook("team-a", "https://k8s.example/by-name", "elsewhere"),
		&core.PublishingTarget{Name: "off", Type: "webhook", URL: "https://k8s.example/off", Enabled: false},
	)
	conf := loadConfig(t)
	out, err := ts.ExtendConfig(conf)
	require.NoError(t, err)

	// File integrations first, then K8s targets by name.
	assert.Equal(t, []string{"https://file.example/a", "https://k8s.example/all", "https://k8s.example/by-name"}, webhookURLs(out.Receivers[0]))
	assert.Equal(t, []string{"https://k8s.example/b", "https://k8s.example/all"}, webhookURLs(out.Receivers[1]))
	assert.Equal(t, 3.0, ts.gauge("attached"))
	assert.Equal(t, 0.0, ts.gauge("skipped"))
	assert.Contains(t, ts.logs.String(), "names a receiver that is not in the configuration")
}

func TestExtendConfigLeavesConfUnchanged(t *testing.T) {
	ts := newSource(t, hook("k", "https://k8s.example/k"))
	conf := loadConfig(t)
	before := conf.String()
	beforeLen := len(conf.Receivers[0].WebhookConfigs)

	out, err := ts.ExtendConfig(conf)
	require.NoError(t, err)
	assert.NotSame(t, conf, out)
	assert.Equal(t, before, conf.String())
	assert.Len(t, conf.Receivers[0].WebhookConfigs, beforeLen)
	assert.Len(t, out.Receivers[0].WebhookConfigs, beforeLen+1)
}

func TestExtendConfigSkips(t *testing.T) {
	ts := newSource(t,
		hook("ok", "https://k8s.example/ok"),
		hook("nowhere", "https://k8s.example/n", "missing"),
		&core.PublishingTarget{Name: "rest", Type: "rootly", URL: "https://api.rootly.com/v1/incidents", Enabled: true},
		&core.PublishingTarget{Name: "tg", Type: "telegram", URL: "https://t.example", Enabled: true},
		&core.PublishingTarget{Name: "pd", Type: "pagerduty", URL: "https://events.pagerduty.com", Enabled: true},
	)
	out, err := ts.ExtendConfig(loadConfig(t))
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, 1.0, ts.gauge("attached"))
	assert.Equal(t, 4.0, ts.gauge("skipped"))
	logs := ts.logs.String()
	for _, reason := range []string{reasonNoReceiver, reasonRootlyREST, reasonUnsupportedType, reasonInvalid} {
		assert.Contains(t, logs, "reason="+reason)
	}
}

func TestExtendConfigWithoutTargets(t *testing.T) {
	ts := newSource(t)
	conf := loadConfig(t)
	out, err := ts.ExtendConfig(conf)
	require.NoError(t, err)
	assert.Same(t, conf, out)
	assert.Equal(t, 0.0, ts.gauge("attached"))
}

func TestSecretsStayOutOfConfigTextAndLogs(t *testing.T) {
	ts := newSource(t,
		&core.PublishingTarget{
			Name: "hook", Type: "webhook", Enabled: true,
			URL:     "https://k8s.example/hook-token-u1",
			Headers: map[string]string{"Authorization": "Bearer bearer-t2", "X-Key": "header-v3"},
		},
		&core.PublishingTarget{
			Name: "pd", Type: "pagerduty", Enabled: true, URL: "https://events.pagerduty.com",
			Headers: map[string]string{"routing_key": "routing-k4"},
		},
		&core.PublishingTarget{
			Name: "slack", Type: "slack", Enabled: true, URL: "https://hooks.slack.com/services/slack-s5",
		},
		// Skipped: its error must not quote the credentials either.
		&core.PublishingTarget{
			Name: "bad", Type: "webhook", Enabled: true, URL: "https://k8s.example/bad-u6",
			Headers: map[string]string{"Authorization": "raw-a7"},
		},
	)
	out, err := ts.ExtendConfig(loadConfig(t))
	require.NoError(t, err)
	text := out.String() + ts.logs.String()
	for _, secret := range []string{"hook-token-u1", "bearer-t2", "header-v3", "routing-k4", "slack-s5", "bad-u6", "raw-a7"} {
		assert.NotContains(t, text, secret)
	}
	assert.Contains(t, out.String(), "<secret>")
}

func TestRefreshChanged(t *testing.T) {
	ts := newSource(t, hook("k", "https://k8s.example/1"))
	ctx := context.Background()

	changed, err := ts.Refresh(ctx)
	require.NoError(t, err)
	assert.False(t, changed, "same targets")

	ts.discovery.set(hook("k", "https://k8s.example/2"))
	changed, err = ts.Refresh(ctx)
	require.NoError(t, err)
	assert.True(t, changed, "URL changed")

	// Disabled targets are not part of the snapshot.
	ts.discovery.set(hook("k", "https://k8s.example/2"), &core.PublishingTarget{Name: "off", Type: "webhook", URL: "https://x.example"})
	changed, err = ts.Refresh(ctx)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestRefreshSnapshotIsACopy(t *testing.T) {
	target := hook("k", "https://k8s.example/1")
	ts := newSource(t, target)
	target.URL = "https://k8s.example/mutated"
	out, err := ts.ExtendConfig(loadConfig(t))
	require.NoError(t, err)
	assert.Contains(t, webhookURLs(out.Receivers[0]), "https://k8s.example/1")
}

func TestRefreshErrorKeepsSnapshot(t *testing.T) {
	ts := newSource(t, hook("k", "https://k8s.example/1"))
	ts.discovery.err = errors.New("api down")
	ts.discovery.set()
	changed, err := ts.Refresh(context.Background())
	require.Error(t, err)
	assert.False(t, changed)
	out, err := ts.ExtendConfig(loadConfig(t))
	require.NoError(t, err)
	assert.Contains(t, webhookURLs(out.Receivers[0]), "https://k8s.example/1")
}

func TestRefreshInvalidGauge(t *testing.T) {
	ts := newSource(t)
	ts.discovery.invalid = 2
	_, err := ts.Refresh(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2.0, ts.gauge("invalid"))
}

func TestDroppedHeadersWarn(t *testing.T) {
	ts := newSource(t, &core.PublishingTarget{
		Name: "hook", Type: "webhook", Enabled: true, URL: "https://k8s.example/h",
		Headers: map[string]string{"Content-Type": "application/json"},
	})
	_, err := ts.ExtendConfig(loadConfig(t))
	require.NoError(t, err)
	assert.Equal(t, 1.0, ts.gauge("attached"))
	assert.Contains(t, ts.logs.String(), "headers dropped")
}

func TestRunReloadsOnChange(t *testing.T) {
	ts := newSource(t, hook("k", "https://k8s.example/1"))
	var reloads atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ts.Run(ctx, 10*time.Millisecond, func() error { reloads.Add(1); return nil })
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, reloads.Load(), "no change, no reload")

	ts.discovery.set(hook("k", "https://k8s.example/2"))
	require.Eventually(t, func() bool { return reloads.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	assert.Equal(t, int32(1), reloads.Load())
}
