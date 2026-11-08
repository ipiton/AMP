package upstreamai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/featurecontrol"
	"github.com/prometheus/alertmanager/matcher/compat"
	"github.com/prometheus/alertmanager/provider/mem"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/core/alertconv"
	"github.com/ipiton/AMP/internal/upstreamcore"
)

// These tests pin the upstream behaviour the slice relies on, so a bump of
// github.com/prometheus/alertmanager that changes it fails here: the
// classification reaches group templates, the store keeps the alert as
// sent, dedup ignores annotations, and a resolve still goes out with
// drop_noise.

func TestMain(m *testing.M) {
	compat.InitFromFlags(slog.New(slog.DiscardHandler), featurecontrol.NoopFlags{})
	os.Exit(m.Run())
}

const amConfig = `route:
  receiver: hook
  group_by: [alertname]
  group_wait: 100ms
  group_interval: 200ms
  repeat_interval: 1h
receivers:
  - name: hook
    webhook_configs:
      - url: %s
`

type webhookMsg struct {
	Status string `json:"status"`
	Alerts []struct {
		Status      string            `json:"status"`
		Annotations map[string]string `json:"annotations"`
	} `json:"alerts"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
}

type harness struct {
	srv  *httptest.Server
	hook <-chan webhookMsg
}

func startCore(t *testing.T, svc Service, wait time.Duration, dropNoise bool) *harness {
	t.Helper()
	c := New(Config{Service: svc, Logger: slog.New(slog.DiscardHandler), Registerer: prometheus.NewRegistry()})
	return startCoreWith(t, c, nil, wait, dropNoise)
}

// startCoreWith runs the core with c and, when set, history chained before
// it, as amp-upstream does; it stops both after the core.
func startCoreWith(t *testing.T, c *Classifier, history *History, wait time.Duration, dropNoise bool) *harness {
	t.Helper()
	got := make(chan webhookMsg, 10)
	hook := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var msg webhookMsg
		if err := json.NewDecoder(r.Body).Decode(&msg); err == nil {
			got <- msg
		}
	}))
	t.Cleanup(hook.Close)

	dir := t.TempDir()
	configFile := filepath.Join(dir, "alertmanager.yml")
	require.NoError(t, os.WriteFile(configFile, fmt.Appendf(nil, amConfig, hook.URL), 0o600))

	newCallback := c.AlertCallback
	if history != nil {
		newCallback = func(mutes func(context.Context, model.LabelSet) bool) mem.AlertStoreCallback {
			return Chain(history, c.AlertCallback(mutes))
		}
	}
	coreSrv, err := upstreamcore.New(upstreamcore.Options{
		ConfigFile:       configFile,
		DataDir:          filepath.Join(dir, "data"),
		ExternalURL:      &url.URL{Scheme: "http", Host: "am.example"},
		Logger:           slog.New(slog.DiscardHandler),
		Registerer:       prometheus.NewRegistry(),
		NewAlertCallback: newCallback,
		NotifyStage:      c.NotifyStage(wait, dropNoise),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(coreSrv.Handler())
	t.Cleanup(func() {
		srv.Close()
		coreSrv.Stop()
		c.Stop()
		if history != nil {
			history.Stop()
		}
	})
	return &harness{srv: srv, hook: got}
}

func (h *harness) post(t *testing.T, alerts ...map[string]any) {
	t.Helper()
	b, err := json.Marshal(alerts)
	require.NoError(t, err)
	resp, err := http.Post(h.srv.URL+"/api/v2/alerts", "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func (h *harness) next(t *testing.T) webhookMsg {
	t.Helper()
	select {
	case msg := <-h.hook:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("no notification")
		return webhookMsg{}
	}
}

func (h *harness) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case msg := <-h.hook:
		t.Fatalf("unexpected notification: %+v", msg)
	case <-time.After(d):
	}
}

func diskFull() map[string]any {
	return map[string]any{
		"labels":      map[string]string{"alertname": "DiskFull", "instance": "db-1"},
		"annotations": map[string]string{"summary": "disk"},
	}
}

func TestCoreGroupNotificationCarriesClassification(t *testing.T) {
	h := startCore(t, &fakeService{res: warning()}, 2*time.Second, false)

	h.post(t, diskFull())
	msg := h.next(t)

	require.Len(t, msg.Alerts, 1)
	assert.Equal(t, "warning", msg.Alerts[0].Annotations["llm_severity"])
	assert.Equal(t, "disk", msg.Alerts[0].Annotations["summary"])
	assert.Equal(t, "warning", msg.CommonAnnotations["llm_severity"], "visible to group templates")

	// The store keeps the alert as received.
	resp, err := http.Get(h.srv.URL + "/api/v2/alerts")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var stored []struct {
		Annotations map[string]string `json:"annotations"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&stored))
	require.Len(t, stored, 1)
	assert.Equal(t, map[string]string{"summary": "disk"}, stored[0].Annotations)
}

func TestCoreLateClassificationDoesNotResend(t *testing.T) {
	svc := &fakeService{res: warning(), gate: make(chan struct{})}
	h := startCore(t, svc, 0, false)

	h.post(t, diskFull())
	msg := h.next(t)
	assert.NotContains(t, msg.Alerts[0].Annotations, "llm_severity", "sent before the classification")

	close(svc.gate)
	// Several group_intervals: dedup keys on labels, so the added
	// annotations alone do not trigger a new notification.
	h.quiet(t, time.Second)
}

func TestCoreDropNoiseStillSendsResolve(t *testing.T) {
	noise := &core.ClassificationResult{Severity: core.SeverityNoise, Confidence: 0.9, Reasoning: "flapping"}
	svc := &fakeService{res: noise, gate: make(chan struct{})}
	h := startCore(t, svc, 0, true)

	h.post(t, diskFull())
	assert.Equal(t, "firing", h.next(t).Status, "sent before it was classified as noise")
	close(svc.gate)
	h.quiet(t, 600*time.Millisecond) // later flushes drop the firing noise alert

	resolved := diskFull()
	resolved["endsAt"] = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	h.post(t, resolved)
	msg := h.next(t)
	assert.Equal(t, "resolved", msg.Status, "the receiver must see the incident close")
	require.Len(t, msg.Alerts, 1)
	assert.Equal(t, "noise", msg.Alerts[0].Annotations["llm_severity"])
}

// An alert pushed to the core lands in the history and the investigation
// queue under one key, cmd/server's: its row and its investigation are found
// together, and from the FNV fingerprint upstream's API shows via labels.
func TestCoreHistoryAndInvestigationShareTheKey(t *testing.T) {
	rec := &recorder{}
	history := NewHistory(HistoryConfig{Record: rec.record, Logger: slog.New(slog.DiscardHandler), Registerer: prometheus.NewRegistry()})
	inv := &submitter{}
	c := New(Config{Service: &fakeService{res: warning()}, Investigations: inv, Logger: slog.New(slog.DiscardHandler), Registerer: prometheus.NewRegistry()})
	h := startCoreWith(t, c, history, 0, false)

	h.post(t, diskFull())
	h.next(t)

	require.Eventually(t, func() bool { return len(rec.got()) >= 1 && len(inv.got()) >= 1 }, 5*time.Second, 10*time.Millisecond)
	want := alertconv.Fingerprint(map[string]string{"alertname": "DiskFull", "instance": "db-1"})
	assert.Equal(t, want, rec.got()[0].Fingerprint)
	assert.Equal(t, want, inv.got()[0].alert.Fingerprint)
}
