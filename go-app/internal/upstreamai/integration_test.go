package upstreamai

import (
	"bytes"
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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ipiton/AMP/internal/core"
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

	reg := prometheus.NewRegistry()
	c := New(Config{Service: svc, Logger: slog.New(slog.DiscardHandler), Registerer: reg})
	coreSrv, err := upstreamcore.New(upstreamcore.Options{
		ConfigFile:       configFile,
		DataDir:          filepath.Join(dir, "data"),
		ExternalURL:      &url.URL{Scheme: "http", Host: "am.example"},
		Logger:           slog.New(slog.DiscardHandler),
		Registerer:       reg,
		NewAlertCallback: c.AlertCallback,
		NotifyStage:      c.NotifyStage(wait, dropNoise),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(coreSrv.Handler())
	t.Cleanup(func() {
		srv.Close()
		coreSrv.Stop()
		c.Stop()
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
