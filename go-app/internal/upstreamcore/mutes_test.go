package upstreamcore

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/alertmanager/alert"
	"github.com/prometheus/alertmanager/provider/mem"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const inhibitConfig = testConfig + `inhibit_rules:
  - source_matchers: [severity="critical"]
    target_matchers: [severity="warning"]
    equal: [instance]
`

type nopCallback struct{}

func (nopCallback) PreStore(*alert.Alert, bool) error { return nil }
func (nopCallback) PostStore(*alert.Alert, bool)      {}
func (nopCallback) PostDelete(*alert.Alert)           {}
func (nopCallback) PostGC(model.Fingerprints)         {}

// The mutes func an AMP callback gets covers the active configuration's
// inhibition as well as silences: the classification skips both.
func TestAlertCallbackMutesCoverInhibitionAndSilences(t *testing.T) {
	hook, _ := webhookReceiver(t)
	dir := t.TempDir()
	configFile := filepath.Join(dir, "alertmanager.yml")
	require.NoError(t, os.WriteFile(configFile, fmt.Appendf(nil, inhibitConfig, hook.URL), 0o600))

	var mutes atomic.Pointer[func(context.Context, model.LabelSet) bool]
	core, err := New(Options{
		ConfigFile:  configFile,
		DataDir:     filepath.Join(dir, "data"),
		ExternalURL: &url.URL{Scheme: "http", Host: "am.example"},
		Logger:      slog.New(slog.DiscardHandler),
		Registerer:  prometheus.NewRegistry(),
		NewAlertCallback: func(m func(context.Context, model.LabelSet) bool) mem.AlertStoreCallback {
			mutes.Store(&m)
			return nopCallback{}
		},
	})
	require.NoError(t, err)
	srv := httptest.NewServer(core.Handler())
	t.Cleanup(func() {
		srv.Close()
		core.Stop()
	})
	tc := &testCore{core: core, srv: srv, configFile: configFile}
	require.NotNil(t, mutes.Load())
	muted := func(ls model.LabelSet) bool { return (*mutes.Load())(context.Background(), ls) }

	target := model.LabelSet{"alertname": "Latency", "instance": "db-1", "severity": "warning"}
	source := model.LabelSet{"alertname": "NodeDown", "instance": "db-1", "severity": "critical"}
	other := model.LabelSet{"alertname": "Latency", "instance": "db-2", "severity": "warning"}

	code, body := tc.do(t, http.MethodPost, "/api/v2/alerts", []map[string]any{{"labels": target}})
	require.Equal(t, http.StatusOK, code, string(body))
	assert.False(t, muted(target), "nothing inhibits it yet")

	code, body = tc.do(t, http.MethodPost, "/api/v2/alerts", []map[string]any{{"labels": source}})
	require.Equal(t, http.StatusOK, code, string(body))
	require.Eventually(t, func() bool { return muted(target) }, 5*time.Second, 10*time.Millisecond,
		"inhibited by the firing source")
	assert.False(t, muted(source), "the source itself is not inhibited")
	assert.False(t, muted(other), "equal: [instance] does not match")

	now := time.Now()
	code, body = tc.do(t, http.MethodPost, "/api/v2/silences", map[string]any{
		"matchers":  []map[string]any{{"name": "instance", "value": "db-2", "isRegex": false, "isEqual": true}},
		"startsAt":  now.Add(-time.Minute).Format(time.RFC3339),
		"endsAt":    now.Add(time.Hour).Format(time.RFC3339),
		"createdBy": "test",
		"comment":   "maintenance",
	})
	require.Equal(t, http.StatusOK, code, string(body))
	assert.True(t, muted(other), "silenced")
}
