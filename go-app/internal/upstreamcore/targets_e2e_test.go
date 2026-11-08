package upstreamcore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ipiton/AMP/internal/business/publishing"
	"github.com/ipiton/AMP/internal/upstreamtargets"
)

// fakeK8s serves the publishing-target Secrets of one namespace.
type fakeK8s struct {
	mu      sync.Mutex
	secrets []corev1.Secret
}

func (f *fakeK8s) set(secrets ...corev1.Secret) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets = secrets
}

func (f *fakeK8s) ListSecrets(context.Context, string, string) ([]corev1.Secret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]corev1.Secret(nil), f.secrets...), nil
}

func (f *fakeK8s) GetSecret(context.Context, string, string) (*corev1.Secret, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeK8s) Health(context.Context) error { return nil }
func (f *fakeK8s) Close() error                 { return nil }

func targetSecret(t *testing.T, name string, cfg map[string]any) corev1.Secret {
	t.Helper()
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "amp", Labels: map[string]string{"publishing-target": "true"}},
		Data:       map[string][]byte{"config": raw},
	}
}

func webhookSecret(t *testing.T, name, hookURL string) corev1.Secret {
	return targetSecret(t, name, map[string]any{
		"name": name, "type": "webhook", "format": "alertmanager", "enabled": true,
		"url":     hookURL + "/k8s-path-token",
		"headers": map[string]string{"Authorization": "Bearer e2e-bearer-token", "X-Team-Key": "e2e-header-value"},
	})
}

// recordingHook records the requests it receives, by alertname.
type recordingHook struct {
	srv *httptest.Server
	mu  sync.Mutex
	got []hookRequest
}

type hookRequest struct {
	path, authorization, teamKey, alertname string
}

func newRecordingHook(t *testing.T) *recordingHook {
	t.Helper()
	h := &recordingHook{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			CommonLabels map[string]string `json:"commonLabels"`
		}
		_ = json.NewDecoder(r.Body).Decode(&msg)
		h.mu.Lock()
		defer h.mu.Unlock()
		h.got = append(h.got, hookRequest{
			path: r.URL.Path, authorization: r.Header.Get("Authorization"),
			teamKey: r.Header.Get("X-Team-Key"), alertname: msg.CommonLabels["alertname"],
		})
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *recordingHook) received(alertname string) (hookRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.got {
		if r.alertname == alertname {
			return r, true
		}
	}
	return hookRequest{}, false
}

func (h *recordingHook) waitFor(t *testing.T, alertname string) hookRequest {
	t.Helper()
	var got hookRequest
	require.Eventually(t, func() bool {
		var ok bool
		got, ok = h.received(alertname)
		return ok
	}, 10*time.Second, 20*time.Millisecond, "no notification for %s", alertname)
	return got
}

func TestCoreK8sTargets(t *testing.T) {
	fileHook := newRecordingHook(t)
	k8sHook := newRecordingHook(t)
	k8sHook2 := newRecordingHook(t)

	kube := &fakeK8s{}
	kube.set(webhookSecret(t, "k8s-hook", k8sHook.srv.URL))
	logger := slog.New(slog.DiscardHandler)
	discovery, err := publishing.NewTargetDiscoveryManager(kube, "amp", "publishing-target=true", logger, nil)
	require.NoError(t, err)
	reg := prometheus.NewRegistry()
	source, err := upstreamtargets.New(discovery, logger, reg)
	require.NoError(t, err)
	_, err = source.Refresh(context.Background())
	require.NoError(t, err)

	dir := t.TempDir()
	configFile := filepath.Join(dir, "alertmanager.yml")
	require.NoError(t, os.WriteFile(configFile, fmt.Appendf(nil, testConfig, fileHook.srv.URL+"/file"), 0o600))
	core, err := New(Options{
		ConfigFile:   configFile,
		DataDir:      filepath.Join(dir, "data"),
		ExternalURL:  &url.URL{Scheme: "http", Host: "am.example"},
		Logger:       logger,
		Registerer:   reg,
		ExtendConfig: source.ExtendConfig,
	})
	require.NoError(t, err)
	srv := httptest.NewServer(core.Handler())
	t.Cleanup(func() {
		srv.Close()
		core.Stop()
	})
	tc := &testCore{core: core, srv: srv, configFile: configFile}

	post := func(alertname string) {
		t.Helper()
		code, body := tc.do(t, http.MethodPost, "/api/v2/alerts", []map[string]any{{"labels": map[string]string{"alertname": alertname}}})
		require.Equal(t, http.StatusOK, code, string(body))
	}

	// The K8s target is delivered next to the file's integration, with its
	// URL, Authorization and headers.
	post("First")
	fileHook.waitFor(t, "First")
	got := k8sHook.waitFor(t, "First")
	assert.Equal(t, "/k8s-path-token", got.path)
	assert.Equal(t, "Bearer e2e-bearer-token", got.authorization)
	assert.Equal(t, "e2e-header-value", got.teamKey)

	// /api/v2/status shows the applied configuration with the K8s values masked.
	code, body := tc.do(t, http.MethodGet, "/api/v2/status", nil)
	require.Equal(t, http.StatusOK, code, string(body))
	for _, secret := range []string{"k8s-path-token", "e2e-bearer-token", "e2e-header-value", k8sHook.srv.URL} {
		assert.NotContains(t, string(body), secret)
	}

	// A plain reload keeps the target.
	require.NoError(t, core.Reload())
	post("AfterReload")
	k8sHook.waitFor(t, "AfterReload")

	// A changed Secret, picked up by Refresh, moves delivery on reload; a
	// broken target next to it does not fail the reload.
	kube.set(
		webhookSecret(t, "k8s-hook", k8sHook2.srv.URL),
		targetSecret(t, "broken", map[string]any{
			"name": "broken", "type": "webhook", "format": "alertmanager", "enabled": true,
			"url": k8sHook2.srv.URL, "headers": map[string]string{"Authorization": "no-scheme"},
		}),
	)
	changed, err := source.Refresh(context.Background())
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, core.Reload())

	post("AfterChange")
	fileHook.waitFor(t, "AfterChange")
	k8sHook2.waitFor(t, "AfterChange")
	_, old := k8sHook.received("AfterChange")
	assert.False(t, old, "the previous URL is no longer notified")
}
