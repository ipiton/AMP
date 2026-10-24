package upstreamcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
)

// The matcher parser is process-global upstream; the binary sets it the same
// way (cmd/amp-upstream).
func TestMain(m *testing.M) {
	compat.InitFromFlags(slog.New(slog.DiscardHandler), featurecontrol.NoopFlags{})
	os.Exit(m.Run())
}

const testConfig = `route:
  receiver: hook
  group_by: [alertname]
  group_wait: 50ms
  group_interval: 1h
  repeat_interval: 1h
receivers:
  - name: hook
    webhook_configs:
      - url: %s
`

type testCore struct {
	core       *Core
	srv        *httptest.Server
	configFile string
}

// newTestCore starts a core on its own registry (a second New on the same
// Registerer panics) and data directory, stopped at test end before the
// directory is removed.
func newTestCore(t *testing.T, webhookURL string) *testCore {
	t.Helper()
	dir := t.TempDir()
	configFile := filepath.Join(dir, "alertmanager.yml")
	require.NoError(t, os.WriteFile(configFile, fmt.Appendf(nil, testConfig, webhookURL), 0o600))

	core, err := New(Options{
		ConfigFile:  configFile,
		DataDir:     filepath.Join(dir, "data"),
		ExternalURL: &url.URL{Scheme: "http", Host: "am.example"},
		Logger:      slog.New(slog.DiscardHandler),
		Registerer:  prometheus.NewRegistry(),
	})
	require.NoError(t, err)
	srv := httptest.NewServer(core.Handler())
	// Handler contract: stop serving before Stop.
	t.Cleanup(func() {
		srv.Close()
		core.Stop()
	})
	return &testCore{core: core, srv: srv, configFile: configFile}
}

func (tc *testCore) do(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, tc.srv.URL+path, r)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, out
}

type gettableAlert struct {
	Labels map[string]string `json:"labels"`
	Status struct {
		State      string   `json:"state"`
		SilencedBy []string `json:"silencedBy"`
	} `json:"status"`
}

func (tc *testCore) alerts(t *testing.T) []gettableAlert {
	t.Helper()
	code, body := tc.do(t, http.MethodGet, "/api/v2/alerts", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	var out []gettableAlert
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

func webhookReceiver(t *testing.T) (*httptest.Server, <-chan map[string]any) {
	t.Helper()
	got := make(chan map[string]any, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg map[string]any
		if err := json.NewDecoder(r.Body).Decode(&msg); err == nil {
			got <- msg
		}
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestCoreEndToEnd(t *testing.T) {
	hook, got := webhookReceiver(t)
	tc := newTestCore(t, hook.URL)

	code, _ := tc.do(t, http.MethodGet, "/-/healthy", nil)
	require.Equal(t, http.StatusOK, code)

	code, body := tc.do(t, http.MethodPost, "/api/v2/alerts", []map[string]any{
		{"labels": map[string]string{"alertname": "DiskFull", "instance": "db-1"}},
	})
	require.Equal(t, http.StatusOK, code, string(body))

	alerts := tc.alerts(t)
	require.Len(t, alerts, 1)
	assert.Equal(t, "active", alerts[0].Status.State)

	code, body = tc.do(t, http.MethodGet, "/api/v2/alerts/groups", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	var groups []struct {
		Labels   map[string]string     `json:"labels"`
		Receiver struct{ Name string } `json:"receiver"`
	}
	require.NoError(t, json.Unmarshal(body, &groups))
	require.Len(t, groups, 1)
	assert.Equal(t, map[string]string{"alertname": "DiskFull"}, groups[0].Labels)
	assert.Equal(t, "hook", groups[0].Receiver.Name)

	select {
	case msg := <-got:
		assert.Equal(t, "hook", msg["receiver"])
		assert.Equal(t, "firing", msg["status"])
		assert.Equal(t, "http://am.example", msg["externalURL"])
	case <-time.After(10 * time.Second):
		t.Fatal("webhook was not notified")
	}

	now := time.Now()
	code, body = tc.do(t, http.MethodPost, "/api/v2/silences", map[string]any{
		"matchers":  []map[string]any{{"name": "instance", "value": "db-1", "isRegex": false, "isEqual": true}},
		"startsAt":  now.Add(-time.Minute),
		"endsAt":    now.Add(time.Hour),
		"createdBy": "test",
		"comment":   "maintenance",
	})
	require.Equal(t, http.StatusOK, code, string(body))
	var created struct {
		SilenceID string `json:"silenceID"`
	}
	require.NoError(t, json.Unmarshal(body, &created))
	require.NotEmpty(t, created.SilenceID)

	require.Eventually(t, func() bool {
		a := tc.alerts(t)
		return len(a) == 1 && a[0].Status.State == "suppressed" &&
			len(a[0].Status.SilencedBy) == 1 && a[0].Status.SilencedBy[0] == created.SilenceID
	}, 5*time.Second, 50*time.Millisecond)

	code, body = tc.do(t, http.MethodDelete, "/api/v2/silence/"+created.SilenceID, nil)
	require.Equal(t, http.StatusOK, code, string(body))
	require.Eventually(t, func() bool {
		a := tc.alerts(t)
		return len(a) == 1 && a[0].Status.State == "active"
	}, 5*time.Second, 50*time.Millisecond)
}

func TestCoreReloadInvalidConfigKeepsActive(t *testing.T) {
	tc := newTestCore(t, "http://hook.example/")

	require.NoError(t, os.WriteFile(tc.configFile, []byte("route: [not, a, route]\n"), 0o600))
	code, body := tc.do(t, http.MethodPost, "/-/reload", nil)
	assert.Equal(t, http.StatusInternalServerError, code, string(body))
	require.Error(t, tc.core.Reload())

	code, body = tc.do(t, http.MethodGet, "/api/v2/status", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	var status struct {
		Config struct{ Original string } `json:"config"`
	}
	require.NoError(t, json.Unmarshal(body, &status))
	// Webhook URLs are secrets in the status output; the receiver name shows
	// the previous configuration is still active.
	assert.Contains(t, status.Config.Original, "name: hook")

	require.NoError(t, os.WriteFile(tc.configFile, fmt.Appendf(nil, testConfig, "http://hook2.example/"), 0o600))
	code, body = tc.do(t, http.MethodPost, "/-/reload", nil)
	assert.Equal(t, http.StatusOK, code, string(body))
	require.NoError(t, tc.core.Reload())
}

func TestCoreStop(t *testing.T) {
	tc := newTestCore(t, "http://hook.example/")
	tc.srv.Close()

	tc.core.Stop()
	tc.core.Stop() // idempotent
	assert.ErrorIs(t, tc.core.Reload(), errStopped)

	dataDir := filepath.Join(filepath.Dir(tc.configFile), "data")
	for _, f := range []string{"nflog", "silences"} {
		assert.FileExists(t, filepath.Join(dataDir, f), "final snapshot")
	}
}

func TestNewInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "alertmanager.yml")
	require.NoError(t, os.WriteFile(configFile, []byte("route: [not, a, route]\n"), 0o600))

	_, err := New(Options{
		ConfigFile:  configFile,
		DataDir:     filepath.Join(dir, "data"),
		ExternalURL: &url.URL{Scheme: "http", Host: "am.example"},
		Logger:      slog.New(slog.DiscardHandler),
		Registerer:  prometheus.NewRegistry(),
	})
	require.ErrorContains(t, err, "error loading configuration file")
}
