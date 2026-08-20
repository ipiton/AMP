package config

import (
	"context"
	"fmt"
	"log/slog"

	metricsv2 "github.com/ipiton/AMP/pkg/metrics/v2"
)

// ================================================================================
// MetricsReloadable (INF-A slice 1)
// ================================================================================

// metricsReloadPriority: right after the logger. Flipping an atomic bool is
// the cheapest reload in the registry and cannot fail, so doing it early keeps
// the expensive components last.
const metricsReloadPriority = 20

// MetricsReloadable hot-reloads the metrics exposition toggle.
//
// What is real: `metrics.enabled` opens/closes the /metrics endpoint through a
// metricsv2.ExpositionGate, atomically, immediately.
//
// What is NOT real, and says so (W603): `metrics.path` and `metrics.port`.
// AMP serves Prometheus metrics at a FIXED /metrics on the main HTTP server
// (internal/application/router.go); neither field is read anywhere in the
// process, so honouring them is a feature that does not exist rather than a
// value awaiting a restart. The warning says exactly that instead of implying
// a restart would help.
//
// Collection itself is not toggleable: the collectors are promauto-registered
// at package init and increment unconditionally. See ExpositionGate's doc for
// why exposition is the honest granularity, and note the consequence — closing
// the gate does not create a counter gap, it hides one.
type MetricsReloadable struct {
	gate     *metricsv2.ExpositionGate
	logger   *slog.Logger
	warnings *RestartWarnings
}

// NewMetricsReloadable wires a MetricsReloadable over the process's exposition
// gate. A nil gate is legal (a process that mounted /metrics unguarded); in
// that case a metrics.enabled change is reported as restart-required.
func NewMetricsReloadable(
	gate *metricsv2.ExpositionGate,
	warnings *RestartWarnings,
	logger *slog.Logger,
) *MetricsReloadable {
	if logger == nil {
		logger = slog.Default()
	}
	return &MetricsReloadable{gate: gate, logger: logger, warnings: warnings}
}

// Name implements Reloadable.
func (m *MetricsReloadable) Name() string { return "metrics" }

// RelevantSections implements Reloadable.
func (m *MetricsReloadable) RelevantSections() []string { return []string{"metrics"} }

// IsCritical implements Reloadable: losing metrics is a degradation.
func (m *MetricsReloadable) IsCritical() bool { return false }

// ReloadPriority implements OrderedReloadable.
func (m *MetricsReloadable) ReloadPriority() int { return metricsReloadPriority }

// Reload implements Reloadable.
func (m *MetricsReloadable) Reload(_ context.Context, oldCfg, newCfg *Config) error {
	if newCfg == nil {
		return fmt.Errorf("metrics reload: nil config")
	}

	var fields []string
	if oldCfg != nil {
		fields = changedFields("metrics", oldCfg.Metrics, newCfg.Metrics)
		if len(fields) == 0 {
			return nil
		}
	}

	// path/port: not implemented at all, in either direction.
	unsupported := make([]string, 0, 2)
	for _, field := range fields {
		if field == "metrics.path" || field == "metrics.port" {
			unsupported = append(unsupported, field)
		}
	}
	if len(unsupported) > 0 {
		warnRestartRequired(m.logger, m.warnings, RestartRequiredWarning{
			Code:      WarnMetricsRestartRequired,
			Component: m.Name(),
			Fields:    unsupported,
			Reason:    "AMP always serves Prometheus metrics at /metrics on the main HTTP server port; metrics.path and metrics.port are not read by the process, so a restart will not apply them either",
		})
	}

	enabledChanged := len(fields) == 0
	for _, field := range fields {
		if field == "metrics.enabled" {
			enabledChanged = true
		}
	}
	if !enabledChanged {
		return nil
	}

	if m.gate == nil {
		warnRestartRequired(m.logger, m.warnings, RestartRequiredWarning{
			Code:      WarnMetricsRestartRequired,
			Component: m.Name(),
			Fields:    []string{"metrics.enabled"},
			Reason:    "this process mounted /metrics without an exposition gate; restart to apply metrics.enabled",
		})
		return nil
	}

	m.gate.SetEnabled(newCfg.Metrics.Enabled)
	m.logger.Info("metrics exposition toggled from config",
		"enabled", newCfg.Metrics.Enabled,
		"note", "collection is unaffected; only the /metrics endpoint is gated",
	)
	return nil
}

// Compile-time contract checks.
var (
	_ Reloadable        = (*MetricsReloadable)(nil)
	_ OrderedReloadable = (*MetricsReloadable)(nil)
)
