package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// Sidecar loop with a fake trigger target and a fake application
// ================================================================================

// fakeNotifier stands in for the real SIGHUP/HTTP trigger and lets a test
// decide whether delivery succeeds.
type fakeNotifier struct {
	mu    sync.Mutex
	calls int
	err   error

	// onNotify runs inside Notify, so a test can make the fake application
	// advance its attempt counter exactly as the real one would.
	onNotify func()
}

func (f *fakeNotifier) Notify(_ context.Context) error {
	f.mu.Lock()
	f.calls++
	err := f.err
	hook := f.onNotify
	f.mu.Unlock()

	if hook != nil {
		hook()
	}
	return err
}

func (f *fakeNotifier) Describe() string { return "fake trigger" }

func (f *fakeNotifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeApp serves /health/reload the way the real application does.
type fakeApp struct {
	attempts atomic.Int64
	healthy  atomic.Bool
	status   atomic.Value // string

	restartRequired atomic.Value // []map[string]any
	server          *httptest.Server
}

func newFakeApp(t *testing.T) *fakeApp {
	t.Helper()

	app := &fakeApp{}
	app.healthy.Store(true)
	app.status.Store("initial")

	app.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		healthy := app.healthy.Load()
		body := map[string]any{
			"healthy":     healthy,
			"status":      app.status.Load(),
			"version":     int64(1),
			"attempts":    app.attempts.Load(),
			"split_state": !healthy,
		}
		if warnings, ok := app.restartRequired.Load().([]map[string]any); ok {
			body["restart_required"] = warnings
		}

		code := http.StatusOK
		if !healthy {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(app.server.Close)

	return app
}

// processReload is what the real app does on SIGHUP: bump the attempt counter
// and record an outcome.
func (a *fakeApp) processReload(healthy bool, status string) {
	a.attempts.Add(1)
	a.healthy.Store(healthy)
	a.status.Store(status)
}

func newTestReloader(t *testing.T, path string, notify notifier, app *fakeApp) (*reloader, *prometheus.Registry) {
	t.Helper()

	registry := prometheus.NewRegistry()
	return &reloader{
		configPath:    path,
		interval:      10 * time.Millisecond,
		notifier:      notify,
		verifier:      &verifier{url: app.server.URL, client: app.server.Client()},
		metrics:       newReloaderMetrics(registry),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		verifyTimeout: 2 * time.Second,
		verifyPoll:    5 * time.Millisecond,
		maxBackoff:    time.Minute,
		now:           time.Now,
	}, registry
}

func counterValue(t *testing.T, registry *prometheus.Registry, name, result string) float64 {
	t.Helper()

	families, err := registry.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if matchesLabel(metric, "result", result) {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return -1
}

func gaugeValue(t *testing.T, registry *prometheus.Registry, name string) float64 {
	t.Helper()

	families, err := registry.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() == name && len(family.GetMetric()) > 0 {
			return family.GetMetric()[0].GetGauge().GetValue()
		}
	}
	return -1
}

// plainCounterValue reads a counter that has no labels.
func plainCounterValue(t *testing.T, registry *prometheus.Registry, name string) float64 {
	t.Helper()

	families, err := registry.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() == name && len(family.GetMetric()) > 0 {
			return family.GetMetric()[0].GetCounter().GetValue()
		}
	}
	return -1
}

func matchesLabel(metric *dto.Metric, name, value string) bool {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name && label.GetValue() == value {
			return true
		}
	}
	return false
}

// TestReloader_PrimeDoesNotTriggerAReload: the application has just read the
// same file itself, so starting the sidecar must not cause a reload.
func TestReloader_PrimeDoesNotTriggerAReload(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	notify := &fakeNotifier{}
	watcher, registry := newTestReloader(t, path, notify, app)

	watcher.prime()
	watcher.tick(context.Background())

	assert.Zero(t, notify.callCount(), "an unchanged file must never trigger a reload")
	assert.Equal(t, float64(0), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess))
	assert.Positive(t, gaugeValue(t, registry, "amp_config_reloader_config_hash"), "the hash gauge must be published")
}

// TestReloader_ChangeTriggersVerifiedReload is the happy path end to end.
func TestReloader_ChangeTriggersVerifiedReload(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	notify := &fakeNotifier{onNotify: func() { app.processReload(true, "success") }}
	watcher, registry := newTestReloader(t, path, notify, app)

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o600))

	watcher.tick(context.Background())

	assert.Equal(t, 1, notify.callCount())
	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess))
	assert.Equal(t, float64(1), gaugeValue(t, registry, "amp_config_reloader_last_reload_successful"))
	assert.Positive(t, gaugeValue(t, registry, "amp_config_reloader_last_reload_timestamp_seconds"))

	// A second tick with no further change must be silent.
	watcher.tick(context.Background())
	assert.Equal(t, 1, notify.callCount(), "the applied hash must become the new baseline")
}

// TestReloader_NoChangesOutcomeStillCounts: a comment-only edit changes the
// file hash but parses to an identical config, so the app reports no_changes.
// That is a SUCCESS — before slice 2 the app left its status untouched and this
// was indistinguishable from a lost signal.
func TestReloader_NoChangesOutcomeCountsAsSuccess(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	notify := &fakeNotifier{onNotify: func() { app.processReload(true, "no_changes") }}
	watcher, registry := newTestReloader(t, path, notify, app)

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("# just a comment\nlog:\n  level: info\n"), 0o600))

	watcher.tick(context.Background())

	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess))
	assert.Equal(t, 1, notify.callCount())
}

// TestReloader_RejectedConfigIsRetriedWithBackoff: the app refuses the config,
// so the hash must NOT become the baseline — but the retries must thin out
// instead of firing on every tick.
func TestReloader_RejectedConfigIsRetriedWithBackoff(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	notify := &fakeNotifier{onNotify: func() { app.processReload(false, "validation_failed") }}
	watcher, registry := newTestReloader(t, path, notify, app)

	// Controlled clock so the backoff is deterministic.
	now := time.Now()
	watcher.now = func() time.Time { return now }

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: nonsense\n"), 0o600))

	watcher.tick(context.Background())
	require.Equal(t, 1, notify.callCount())
	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultRejected))
	assert.Equal(t, float64(0), gaugeValue(t, registry, "amp_config_reloader_last_reload_successful"))

	// Still inside the backoff window: no second attempt.
	watcher.tick(context.Background())
	assert.Equal(t, 1, notify.callCount(), "a rejected config must not be retried on every tick")

	// Past the backoff: retried, because the file still differs from what the
	// app confirmed.
	now = now.Add(time.Minute)
	watcher.tick(context.Background())
	assert.Equal(t, 2, notify.callCount())
}

// TestReloader_NewEditResetsTheBackoff: the operator fixing their typo must be
// picked up on the next tick, not after the backoff the broken version earned.
func TestReloader_NewEditResetsTheBackoff(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)

	rejectNext := atomic.Bool{}
	rejectNext.Store(true)
	notify := &fakeNotifier{onNotify: func() {
		if rejectNext.Load() {
			app.processReload(false, "validation_failed")
			return
		}
		app.processReload(true, "success")
	}}
	watcher, _ := newTestReloader(t, path, notify, app)

	now := time.Now()
	watcher.now = func() time.Time { return now }

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: nonsense\n"), 0o600))
	watcher.tick(context.Background())
	require.Equal(t, 1, notify.callCount())

	// Operator fixes the file while the backoff is still running.
	rejectNext.Store(false)
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o600))

	watcher.tick(context.Background())
	assert.Equal(t, 2, notify.callCount(), "a new edit must be attempted immediately")
	assert.Equal(t, "success", app.status.Load())
}

// TestReloader_FailedTriggerIsCounted: the trigger itself did not deliver
// (app down, wrong URL, wrong PID).
func TestReloader_FailedTriggerIsCounted(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	notify := &fakeNotifier{err: assert.AnError}
	watcher, registry := newTestReloader(t, path, notify, app)

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o600))
	watcher.tick(context.Background())

	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultFailed))
	assert.Equal(t, float64(0), gaugeValue(t, registry, "amp_config_reloader_last_reload_successful"))
}

// TestReloader_UnconfirmedReloadIsNotSuccess: the trigger delivered but the
// application never advanced its attempt counter — a lost SIGHUP looks exactly
// like this, and it must not be reported as a success.
func TestReloader_UnconfirmedReloadIsNotSuccess(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	notify := &fakeNotifier{} // delivers, but the app does nothing
	watcher, registry := newTestReloader(t, path, notify, app)
	watcher.verifyTimeout = 50 * time.Millisecond

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o600))
	watcher.tick(context.Background())

	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultUnverified))
	assert.Equal(t, float64(0), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess))
}

// TestReloader_RestartRequiredIsSurfaced: the reload succeeded, but part of the
// operator's edit needs a restart. The sidecar must say so — nothing else will.
func TestReloader_RestartRequiredIsSurfaced(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	app.restartRequired.Store([]map[string]any{
		{"code": "W600", "component": "database", "fields": []string{"database.host"}},
	})
	notify := &fakeNotifier{onNotify: func() { app.processReload(true, "success") }}
	watcher, registry := newTestReloader(t, path, notify, app)

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("database:\n  host: db-2\n"), 0o600))
	watcher.tick(context.Background())

	// Still a success — the reload was applied — and the hash is the baseline.
	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess))
	watcher.tick(context.Background())
	assert.Equal(t, 1, notify.callCount(), "a restart-required warning must not cause a retry loop")
}

func TestReloader_MissingFileCountsAWatchError(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	notify := &fakeNotifier{}
	watcher, registry := newTestReloader(t, path, notify, app)

	watcher.prime()
	require.NoError(t, os.Remove(path))
	watcher.tick(context.Background())

	assert.Equal(t, float64(1), plainCounterValue(t, registry, "amp_config_reloader_watch_errors_total"))
	assert.Zero(t, notify.callCount(), "an unreadable file must not trigger a reload")
}

// TestReloader_RunStopsOnContextCancel guards the shutdown path.
func TestReloader_RunStopsOnContextCancel(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	watcher, _ := newTestReloader(t, path, &fakeNotifier{}, app)
	watcher.prime()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		watcher.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestRestartRequiredFields(t *testing.T) {
	health := reloadHealth{}
	health.RestartRequired = []struct {
		Code      string   `json:"code"`
		Component string   `json:"component"`
		Fields    []string `json:"fields"`
	}{
		{Code: "W600", Component: "database", Fields: []string{"database.host", "database.port"}},
		{Code: "W610", Component: "reload-coordinator"},
	}

	assert.Equal(t,
		[]string{"W600:database.host", "W600:database.port", "W610:reload-coordinator"},
		restartRequiredFields(health))
}

// TestReloader_DoesNotConfirmAgainstAnAppThatNeverReloaded is review M11: when
// the pre-trigger status read fails the baseline is -1, so any counter value
// satisfies the comparison. A freshly started application reporting
// attempts=0 / status=initial must NOT count as a confirmation — on the signal
// path Notify succeeds even when the app is not processing anything, so this is
// how a lost SIGHUP would otherwise be reported as a success.
func TestReloader_DoesNotConfirmAgainstAnAppThatNeverReloaded(t *testing.T) {
	var preReadFailed atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Fail the FIRST read (the baseline), then serve a pristine app.
		if preReadFailed.CompareAndSwap(false, true) {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"healthy": true, "status": "initial", "version": 1, "attempts": 0,
		})
	}))
	defer server.Close()

	path := writeConfig(t, "log:\n  level: info\n")
	registry := prometheus.NewRegistry()
	watcher := &reloader{
		configPath:    path,
		interval:      10 * time.Millisecond,
		notifier:      &fakeNotifier{}, // "delivers", like SIGHUP always does
		verifier:      &verifier{url: server.URL, client: server.Client()},
		metrics:       newReloaderMetrics(registry),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		verifyTimeout: 50 * time.Millisecond,
		verifyPoll:    5 * time.Millisecond,
		maxBackoff:    time.Minute,
		now:           time.Now,
	}

	watcher.prime()
	require.NoError(t, os.WriteFile(path, []byte("log:\n  level: debug\n"), 0o600))
	watcher.tick(context.Background())

	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultUnverified))
	assert.Equal(t, float64(0), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess))
}

// TestReloader_UnchangedFileDoesNotChurnMetrics is review M15: the config gauges
// only need republishing when the hash changes.
func TestReloader_UnchangedFileDoesNotChurnMetrics(t *testing.T) {
	path := writeConfig(t, "log:\n  level: info\n")
	app := newFakeApp(t)
	watcher, registry := newTestReloader(t, path, &fakeNotifier{}, app)

	watcher.prime()
	before := gaugeValue(t, registry, "amp_config_reloader_config_hash")
	require.Positive(t, before, "prime must publish the hash")

	for i := 0; i < 5; i++ {
		watcher.tick(context.Background())
	}

	// Still exactly one config_info series, still the same hash value.
	assert.Equal(t, before, gaugeValue(t, registry, "amp_config_reloader_config_hash"))
	families, err := registry.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == "amp_config_reloader_config_info" {
			assert.Len(t, family.GetMetric(), 1, "exactly one config_info series")
		}
	}
}
