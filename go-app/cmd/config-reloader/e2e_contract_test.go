package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/ipiton/AMP/internal/application/handlers"
	appconfig "github.com/ipiton/AMP/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// End-to-end contract: real coordinator -> real /health/reload -> real sidecar
// ================================================================================
// The sidecar and the application are separate binaries that agree only on a
// JSON shape and on the meaning of `attempts`. Unit tests on either side cannot
// catch a mismatch, so this drives the REAL ReloadCoordinator through the REAL
// handler with the REAL sidecar loop.
//
// It uses the SIGHUP path (signalling this test process, which reloads the
// coordinator exactly as cmd/server's SIGHUP handler does) rather than
// POST /-/reload, so the test needs a coordinator but not a fully booted
// ServiceRegistry.

// statusProvider adapts a coordinator + warning collector to what the endpoint
// needs — the same composition ServiceRegistry.ReloadStatus performs.
type statusProvider struct {
	coordinator *appconfig.ReloadCoordinator
	warnings    *appconfig.RestartWarnings
}

func (s statusProvider) ReloadStatus() appconfig.ReloadStatusSnapshot {
	return appconfig.ReloadStatusSnapshot{
		CoordinatorStatus: s.coordinator.StatusSnapshot(),
		RestartRequired:   s.warnings.List(),
	}
}

func writeE2EConfig(t *testing.T, path, logLevel string) {
	t.Helper()

	content := "app:\n  name: e2e\nserver:\n  host: localhost\n  port: 8080\n" +
		"log:\n  level: " + logLevel + "\n  format: json\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestEndToEnd_SidecarDrivesAndVerifiesARealReload(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeE2EConfig(t, configPath, "info")

	initial, err := appconfig.LoadConfig(configPath)
	require.NoError(t, err)

	warnings := appconfig.NewRestartWarnings()
	reloader := appconfig.NewConfigReloader(slog.New(slog.NewTextHandler(io.Discard, nil)))
	coordinator := appconfig.NewReloadCoordinator(
		initial, configPath,
		appconfig.NewConfigValidator(), appconfig.NewConfigComparator(), reloader,
		nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	coordinator.SetRestartWarnings(warnings)

	// The application's real endpoint.
	app := httptest.NewServer(handlers.ReloadHealthHandler(statusProvider{coordinator, warnings}))
	defer app.Close()

	// The application's real SIGHUP behaviour: reload from the file.
	sighup := make(chan os.Signal, 1)
	signal.Notify(sighup, syscall.SIGHUP)
	defer signal.Stop(sighup)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sighup:
				_, _ = coordinator.ReloadFromFile(ctx, configPath)
			}
		}
	}()

	registry := prometheus.NewRegistry()
	sidecar := newSidecarForApp(t, configPath, app, registry)
	watcher := &sidecar

	watcher.prime()

	// 1. A real config change is detected, triggered, applied and confirmed.
	writeE2EConfig(t, configPath, "debug")
	watcher.tick(ctx)

	require.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess),
		"the sidecar must confirm a real successful reload")
	assert.Equal(t, "debug", coordinator.GetCurrentConfig().Log.Level,
		"and the application must really be running the new config")

	// 2. A comment-only edit: the hash changes, the parsed config does not.
	// The application reports no_changes, which must still count as confirmed —
	// this is the case that was indistinguishable from a lost signal before
	// slice 2 gave the coordinator a no_changes status and an attempt counter.
	content, err := os.ReadFile(configPath) //nolint:gosec // test temp path
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, append([]byte("# touched\n"), content...), 0o600))

	watcher.tick(ctx)
	assert.Equal(t, float64(2), counterValue(t, registry, "amp_config_reloader_reloads_total", resultSuccess))

	// 3. An INVALID config is rejected by the application, and the sidecar
	// reports it as rejected rather than adopting the hash.
	require.NoError(t, os.WriteFile(configPath,
		[]byte("app:\n  name: e2e\nlog:\n  level: info\n  format: carrier-pigeon\n"), 0o600))

	watcher.tick(ctx)

	assert.Equal(t, float64(1), counterValue(t, registry, "amp_config_reloader_reloads_total", resultRejected),
		"an invalid config must be reported as rejected")
	assert.Equal(t, float64(0), gaugeValue(t, registry, "amp_config_reloader_last_reload_successful"))
	assert.Equal(t, "debug", coordinator.GetCurrentConfig().Log.Level,
		"and the previous config must still be live")

	// The endpoint agrees, with the reason.
	response, err := app.Client().Get(app.URL) //nolint:noctx // test client
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
}

// newSidecarForApp builds a sidecar wired to the given application server.
func newSidecarForApp(
	t *testing.T,
	configPath string,
	app *httptest.Server,
	registry *prometheus.Registry,
) reloader {
	t.Helper()

	return reloader{
		configPath: configPath,
		interval:   10 * time.Millisecond,
		// Signal THIS process: the goroutine above plays the application's
		// SIGHUP handler. --allow-self-pid exists for exactly this shape.
		notifier:      &signalNotifier{pid: os.Getpid()},
		verifier:      &verifier{url: app.URL, client: app.Client()},
		metrics:       newReloaderMetrics(registry),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		verifyTimeout: 5 * time.Second,
		verifyPoll:    5 * time.Millisecond,
		maxBackoff:    time.Minute,
		now:           time.Now,
	}
}
