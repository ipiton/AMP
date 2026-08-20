package config

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	metricsv2 "github.com/ipiton/AMP/pkg/metrics/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scrapeStatus(t *testing.T, gate *metricsv2.ExpositionGate) int {
	t.Helper()
	handler := gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return recorder.Code
}

func TestMetricsReloadable_Contract(t *testing.T) {
	reloadable := NewMetricsReloadable(metricsv2.NewExpositionGate(true), nil, NewRestartWarnings(), slog.Default())

	assert.Equal(t, "metrics", reloadable.Name())
	assert.Equal(t, []string{"metrics"}, reloadable.RelevantSections())
	assert.False(t, reloadable.IsCritical())
	assert.Equal(t, 20, reloadable.ReloadPriority())
}

func TestMetricsReloadable_TogglesExposition(t *testing.T) {
	gate := metricsv2.NewExpositionGate(true)
	warnings := NewRestartWarnings()
	oldCfg := &Config{Metrics: MetricsConfig{Enabled: true}}
	newCfg := &Config{Metrics: MetricsConfig{Enabled: false}}
	reloadable := NewMetricsReloadable(gate, oldCfg, warnings, slog.Default())

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))
	assert.False(t, gate.Enabled())
	assert.Equal(t, http.StatusNotFound, scrapeStatus(t, gate))
	assert.Empty(t, warnings.List(), "an enabled toggle is real, so it must not warn")

	// ...and back on.
	require.NoError(t, reloadable.Reload(context.Background(), newCfg, oldCfg))
	assert.True(t, gate.Enabled())
	assert.Equal(t, http.StatusOK, scrapeStatus(t, gate))
}

func TestMetricsReloadable_PathAndPortWarnW603AndDoNotTouchTheGate(t *testing.T) {
	gate := metricsv2.NewExpositionGate(true)
	warnings := NewRestartWarnings()
	oldCfg := &Config{Metrics: MetricsConfig{Enabled: true, Path: "/metrics", Port: 9090}}
	reloadable := NewMetricsReloadable(gate, oldCfg, warnings, slog.Default())

	newCfg := &Config{Metrics: MetricsConfig{Enabled: true, Path: "/prom", Port: 9091}}

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	assert.True(t, gate.Enabled(), "path/port must not disturb the exposition state")

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnMetricsRestartRequired, list[0].Code)
	assert.ElementsMatch(t, []string{"metrics.path", "metrics.port"}, list[0].Fields)
	// The reason must not claim a restart would help — it would not.
	assert.Contains(t, list[0].Reason, "not read by the process")
}

func TestMetricsReloadable_UnchangedSectionIsNoOp(t *testing.T) {
	// Gate closed, and the boot config says closed: no drift, no action.
	gate := metricsv2.NewExpositionGate(false)
	warnings := NewRestartWarnings()
	cfg := &Config{Metrics: MetricsConfig{Enabled: false}}
	reloadable := NewMetricsReloadable(gate, cfg, warnings, slog.Default())

	require.NoError(t, reloadable.Reload(context.Background(), cfg, cfg))

	assert.False(t, gate.Enabled(), "a no-op reload must not silently re-open the gate")
	assert.Empty(t, warnings.List())
}

func TestMetricsReloadable_NilGateWarnsInsteadOfPretending(t *testing.T) {
	warnings := NewRestartWarnings()
	oldCfg := &Config{Metrics: MetricsConfig{Enabled: true}}
	newCfg := &Config{Metrics: MetricsConfig{Enabled: false}}
	reloadable := NewMetricsReloadable(nil, oldCfg, warnings, slog.Default())

	require.NoError(t, reloadable.Reload(context.Background(), oldCfg, newCfg))

	list := warnings.List()
	require.Len(t, list, 1)
	assert.Equal(t, WarnMetricsRestartRequired, list[0].Code)
	assert.Equal(t, []string{"metrics.enabled"}, list[0].Fields)
}

func TestMetricsReloadable_NilNewConfigIsAnError(t *testing.T) {
	reloadable := NewMetricsReloadable(metricsv2.NewExpositionGate(true), nil, nil, slog.Default())
	require.Error(t, reloadable.Reload(context.Background(), &Config{}, nil))
}
