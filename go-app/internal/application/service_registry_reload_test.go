package application

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	appconfig "github.com/ipiton/AMP/internal/config"
	pkglogger "github.com/ipiton/AMP/pkg/logger"
	metricsv2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ================================================================================
// Reloadable registration wiring (INF-A slice 1)
// ================================================================================

func newReloadTestRegistry(t *testing.T) *ServiceRegistry {
	t.Helper()

	registry, err := NewServiceRegistry(&appconfig.Config{
		Profile: appconfig.ProfileLite,
		Metrics: appconfig.MetricsConfig{Enabled: true},
	}, slog.Default())
	require.NoError(t, err)

	// Initialize() normally creates these; this test exercises registration in
	// isolation, without booting storage/publishing/etc.
	registry.restartWarnings = appconfig.NewRestartWarnings()
	registry.metricsGate = metricsv2.NewExpositionGate(true)
	return registry
}

func TestRegisterReloadables_RegistersAllFiveInReloadOrder(t *testing.T) {
	registry := newReloadTestRegistry(t)
	reloader := appconfig.NewConfigReloader(slog.Default())

	registry.registerReloadables(reloader)

	// Order is ReloadPriority order, not registration order: logger first so
	// later reload lines honour the new level, storage last.
	assert.Equal(t,
		[]string{"logger", "metrics", "llm", "redis", "database"},
		reloader.GetRegisteredComponents(),
	)
}

func TestRegisterReloadables_NilReloaderIsSafe(t *testing.T) {
	registry := newReloadTestRegistry(t)
	registry.registerReloadables(nil) // must not panic
}

func TestSetLogHandler_WriteOnceBeforeInitialize(t *testing.T) {
	registry := newReloadTestRegistry(t)

	handler, err := pkglogger.NewSwappableHandler(httptest.NewRecorder().Body, slog.LevelInfo, "json")
	require.NoError(t, err)

	require.NoError(t, registry.SetLogHandler(handler))

	// Second call is refused rather than silently overwriting: two owners of
	// the process logger is a bug, not a preference.
	err = registry.SetLogHandler(handler)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already set")

	// After Initialize it is too late — LoggerReloadable was already wired.
	fresh := newReloadTestRegistry(t)
	fresh.initialized = true
	err = fresh.SetLogHandler(handler)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before Initialize")
}

func TestMetricsGate_DrivesTheMetricsEndpoint(t *testing.T) {
	registry := newReloadTestRegistry(t)

	handler := registry.MetricsGate().Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	scrape := func() int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return recorder.Code
	}

	assert.Equal(t, http.StatusOK, scrape())
	registry.MetricsGate().SetEnabled(false)
	assert.Equal(t, http.StatusNotFound, scrape())
}

func TestRestartWarnings_SurfacedThroughTheRegistry(t *testing.T) {
	registry := newReloadTestRegistry(t)
	assert.Empty(t, registry.RestartWarnings())

	registry.restartWarnings.Record(appconfig.RestartRequiredWarning{
		Code:      appconfig.WarnRedisRestartRequired,
		Component: "redis",
		Fields:    []string{"redis.addr"},
		Reason:    "handles are shared",
	})

	list := registry.RestartWarnings()
	require.Len(t, list, 1)
	assert.Equal(t, appconfig.WarnRedisRestartRequired, list[0].Code)
	assert.Equal(t, []string{"redis.addr"}, list[0].Fields)
}
